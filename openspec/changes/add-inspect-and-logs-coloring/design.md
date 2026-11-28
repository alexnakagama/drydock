# Design

## Context

Both views draw through `components.Pager`, which takes the line text from a callback. The pager's contract is that lines hold no ANSI sequences. It scrolls horizontally with `ansi.Cut` and pads or truncates each line with `Fit`, and `Fit` already accepts styled text.

- **Inspect** (`views/inspect.go`): at load, the raw JSON is indented with `json.Indent`, split into lines, and each line is passed through `components.Clean`. The view is shared by all four resource kinds and does not know which kind it shows.
- **Logs** (`views/logs.go`): each line is stripped of ANSI, cleaned, and stored in a `lineRing` (`text`, an optional `lower` copy, and `width`). The ring is capped at 10,000 lines and 32 MiB. Search filters with `strings.Contains` on the lower-cased text. The view re-renders on every batch, at most every 50 ms while lines are arriving.
- **Styles**: all styles live in `components.Styles`, built from a dark or light `palette`. The root model replaces it when the terminal reports its background, so a style must be looked up at render time, not stored with the content.
- **Tests**: golden files are compared after `ansi.Strip`, so they cover the text but not the colors. Color checks compare against `Styles.X.Render(...)`, as in `table_test.go`.

## Goals / Non-Goals

**Goals:**
- One mechanism for styling a part of a line, shared by both views and safe with horizontal scrolling.
- No extra memory per stored log line. All coloring is computed for the visible lines only.
- A palette change recolors views that are already open.

**Non-Goals:**
- Keeping the container's own ANSI colors. They are still stripped, because they come from untrusted output.
- Recognizing log formats beyond what the spec lists (for example klog's `E1128` prefix or syslog priorities).
- Coloring the Compose project or service names in logs. That belongs to the merged-logs feature.
- Making coloring configurable or adding a toggle. `NO_COLOR` already covers opting out.

## Decisions

### 1. Spans of token kinds, resolved to styles at render time

The scanners return `[]components.Span{Start, End int; Kind components.SpanKind}` with byte offsets into the plain, cleaned line. These types are exported because the scanners live in `views` and the renderer in `components`. `SpanKind` has exported constants: `KindJSONKey`, `KindJSONString`, `KindJSONNumber`, `KindJSONPunct`, `KindStateGood`, `KindStateWarn`, `KindStateDim`, `KindLogTime`, `KindLogError`, `KindLogWarn`, `KindLogDebug` and `KindMatch`.

A small helper, `components.RenderSpans(text, spans)`, writes the text, wrapping each span in the style its kind maps to in the current `Styles`. Text outside any span is unstyled. `RenderSpans` requires spans that are sorted by `Start` and do not overlap, and it clamps any span that runs past the end of the text. Callers that combine several span sets, such as the logs view with search matches, split and merge them before calling it.

- *Why kinds, not styles:* the inspect lines are scanned once at load and kept. Storing the kind lets a later `tea.BackgroundColorMsg` recolor them without scanning again.
- *Alternative:* pre-render ANSI strings at load. This was rejected because they would keep the old palette, and the pager's widths would have to strip them again.

### 2. The pager styles first, then cuts

Add `Pager.ViewStyled(line func(i int) (string, []components.Span))`. For each visible line, the pager renders the spans and then applies the existing `ansi.Cut` and `Fit`. Both are ANSI-aware, so a span that starts before the left edge keeps its style in the visible part. `contentWidth` still comes from the plain text.

- *Alternative:* cut the plain text first and then shift the span offsets into the cut window. This avoids depending on `ansi.Cut` preserving SGR state, but it means re-implementing cell-to-byte mapping for wide graphemes, which `ansi` already handles. The risk is covered by a test (see Risks) instead.
- The pager's doc comment changes to: "lines must be plain and display-safe; styling is supplied as spans."

### 3. Inspect: a line scanner with a key stack

`json.Indent` output has at most one key and one scalar, or one bracket, per line, and strings never span lines. So a per-line scanner is exact:

- leading whitespace;
- an optional `"key"` followed by `:` (a string is a key when the next non-space character after it is `:`);
- a value: a string, a number, `true`/`false`/`null`, or `{`/`[`;
- an optional `,`, or a closing `}`/`]`.

Strings are scanned honoring `\` escapes. `Clean` only replaces control characters, so it never adds or removes quotes or backslashes.

The scanner runs once in `prettyJSON` and keeps `[]components.Span` next to each line. To find `State.Status`, it keeps a stack of open blocks:

- Every `{` or `[` that ends a line opens a block and pushes an entry. The entry is the key on that line, or `""` for the root value and for array elements.
- A `{}` or `[]` that opens and closes on the same line, for example `"State": {}`, pushes nothing.
- Every line whose first non-space character is `}` or `]` pops one entry.
- The value is a container state when the stack is exactly `["", "State"]` (the root object, then `State`) and the line's key is `Status`. This excludes `State.Health.Status` (stack `["", "State", "Health"]`) and a `State` object inside an array element (stack `["", "Mounts", "", "State"]`).

The value is unquoted with `strconv.Unquote` before it is passed to `containerTone(container.ContainerState(value))`, but the span still covers the quoted string, quotes included. The kind is `KindStateGood`, `KindStateWarn` or `KindStateDim`. `ToneDefault`, or a value that does not unquote, keeps `KindJSONString`.

- `newInspectView` gets a variadic option (`withContainerState()`) that only the containers view passes (`containers.go`; the other call sites are in `images.go`, `volumes.go` and `networks.go`), so an image or network inspection that happens to have a `State.Status` key is not affected.

### 4. Logs: scanned per visible line, at render time

`logsView.shown(i)` returns the text plus spans computed on the spot:

- **Timestamp:** an anchored regexp, `^(\[)?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?: ?(?:Z|[+-]\d{2}(?::?\d{2})?))?`. The zone may follow a space and may be hours only (`-05`), so `2026-11-28 10:15:02 +0100` and `[2026-11-28 10:15:02 +0100]` are covered. When the match starts with `[`, a closing `]` must follow immediately, and it is included in the span. Otherwise there is no timestamp span.
- **Level:** level detection looks only at the first 512 bytes of the line (cut back to a rune boundary). The first level token is almost always near the start, and unanchored regexps over a 16 KiB line cost about 1.7 ms each, which would make a screen of long lines stutter. Within that window, the leftmost of these candidates wins:
  - (a) an uppercase whole word from the level set, where word characters are ASCII letters, digits and `_` (RE2's `\b`);
  - (b) `(?i)\b(?:level|lvl)="?(\w+)` with a value in the set;
  - (c) `(?i)"level"\s*:\s*"(\w+)"` with a value in the set;
  - (d) `\[(\w+)\]` whose content, in any case, is in the set.

  Candidates (b), (c) and (d) iterate every match with `FindAllStringSubmatchIndex` and take the first whose value is in the set. For example, in `[main] [error] boom`, `[main]` is skipped and `[error]` is used. Only the value is colored: the `error` of `level=error`, not `level=`. A level token cannot overlap the timestamp, since a timestamp holds only digits and `T Z + - : . , [ ]`, so no overlap rule is needed.
- **Search matches:** all non-overlapping, case-insensitive occurrences of the needle, left to right, over the whole line. `strings.Index` is fast, so no window is needed. When the line is all ASCII, byte offsets carry over between the lowered and original text. Otherwise the line is lowered rune by rune while recording a lower-to-original offset map. Comparing only total lengths is not enough: `K` (Kelvin sign) shrinks and `İ` grows, so a line can keep its total length while its offsets shift. This costs nothing in the common case and stays correct for text like `İ`.
- **Precedence:** match spans are laid over the others. Where a match overlaps a timestamp or level span, the overlapped part takes the match style, and any part outside the match keeps its own style.

*Why at render time:* the pager draws at most a screen of lines, about 50 at typical sizes. With the timestamp regexp anchored and level detection capped at 512 bytes, a line costs tens of microseconds at most, while storing spans would add per-line memory to a ring that is budgeted by bytes. Search highlighting has to be computed at render time anyway, because it depends on the current needle.

### 5. Styles

New palette fields (dark / light, ANSI 256):

- `matchBg`: 220 / 222
- `matchFg`: 16 / 16

New `StyleSet` styles:

| Style | Look |
|---|---|
| `JSONKey` | accent |
| `JSONString` | success |
| `JSONNumber` | warn, also used for literals |
| `JSONPunct` | muted |
| `LogTime` | muted |
| `LogError` | error, bold |
| `LogWarn` | warn |
| `LogDebug` | muted |
| `SearchMatch` | matchFg on matchBg, underlined |

The state kinds reuse `RowGood`, `RowWarn` and `RowDim`, so inspect and the list agree. `LogError` is bold, so it stands out from `JSONString`-style green and from the dimmed text even on terminals that drop colors but keep attributes. With `NO_COLOR`, the ASCII color profile keeps attributes and drops colors, so the underline on `SearchMatch` is what keeps matches visible. When the output is not a terminal, or `TERM=dumb`, the profile drops all styling, the underline included, as it does for the rest of the UI.

## Risks / Trade-offs

- **[`ansi.Cut` drops or misplaces SGR state at a cut inside a span]** → A pager unit test renders lines whose spans cross the left edge, the right edge and wide characters. It checks that the stripped output equals the plain-path output, and that a cut inside a span still starts with that span's style. If it fails, switch to the cut-then-shift alternative in Decision 2; nothing else changes.
- **[False level matches]**, for example an uppercase `ERROR` inside a message → This is accepted. Only the first token is colored, only the token itself, and the default color is kept everywhere else, so a false positive costs one colored word.
- **[Very long log lines]** (up to 16 KiB) → Only visible lines are scanned, the timestamp regexp is anchored, and level scanning is limited to the first 512 bytes. A level word that appears only after byte 512 is not colored, which is accepted. A benchmark of a 16 KiB line with an active search guards against regressions.
- **[Colors clash with some terminal themes]** → The colors come from the existing palette, which is already tuned for light and dark backgrounds. The only new pair is the match highlight, which has an explicit foreground, so it never depends on the terminal's default text color.

## Migration Plan

Nothing to migrate: this adds styling only, with no new configuration or stored state. Rolling back is reverting the change, and golden files are unaffected either way.
