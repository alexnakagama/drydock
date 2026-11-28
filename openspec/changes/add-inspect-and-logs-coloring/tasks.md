# Tasks

## 1. Styles and span rendering

- [x] 1.1 Add `matchBg`/`matchFg` to both palettes and the `JSONKey`, `JSONString`, `JSONNumber`, `JSONPunct`, `LogTime`, `LogError`, `LogWarn`, `LogDebug` and `SearchMatch` styles to `StyleSet` in `internal/ui/components/styles.go`, as listed in design.md Decision 5. Verify with a test in `components` that every new style renders a sample differently from the plain text, in both `NewStyles(true)` and `NewStyles(false)`, and that `SearchMatch` is underlined.
- [x] 1.2 Add the exported `SpanKind` type with its `Kind…` constants, the `Span{Start, End int; Kind SpanKind}` type and `RenderSpans(text, spans)` to `internal/ui/components` (design.md Decision 1). Kinds map to styles through the current `Styles` at call time, and the state kinds use `RowGood`/`RowWarn`/`RowDim`. Document that spans must be sorted and must not overlap. Verify with table tests: no spans returns the text unchanged; adjacent spans and gaps render each part with its own style; spans past the end of the text are clamped; and `ansi.Strip(RenderSpans(t, s)) == t` for every case.
- [x] 1.3 Add `Pager.ViewStyled(line func(i int) (string, []Span))` (Decision 2): render the spans, then cut and fit, with `contentWidth` still taken from plain text. Update the pager's doc comment. Verify with `pager_test.go` cases for spans crossing the left edge, the right edge, and a wide character at a cut. The stripped output must equal `View` with plain lines, and a line cut inside a span must start with that span's style. If this fails, switch to the cut-then-shift approach described in the design.

## 2. Inspect coloring

- [x] 2.1 Implement the per-line JSON scanner with the block stack (Decision 3) in `internal/ui/views/inspect.go`, or in a new `inspect_spans.go`. Verify with table tests covering keys, strings with `\"`, `\\`, `:`, `{` and `,` inside, negative and exponent numbers, `true`/`false`/`null`, empty `{}` and `[]` on one line, closing brackets with and without `,`, and a cleaned line containing U+FFFD. Also cover the stack: `State.Status` at the top level is a state; `State.Health.Status`, a `State.Status` inside an array element, and a document with `"State": {}` followed by a top-level `Status` key are not. Each test asserts both the span kinds and that the spans cover exactly the token bytes.
- [x] 2.2 Keep the spans next to each line in `prettyJSON` and render through the styled pager path. Add the variadic `withContainerState()` option, passed only at the containers call site (`containers.go`), which unquotes the value and maps `State.Status` through `containerTone`. Correct the `inspectView` doc comment, which lists only containers, images and volumes, to include networks. Verify with view tests for spec scenarios "Key and value colors differ", "Running container", "Exited container" and "Other Status keys", plus one showing an image inspection's `State.Status` uses the string color. The tests must assert on the raw `e.m.View().Content`, not `e.screen()`, which strips styling: for example `strings.Contains(raw, components.Styles.RowGood.Render(`"running"`))`, with the quotes inside the styled text.
- [x] 2.3 Verify that a palette change recolors an open inspect view: deliver `tea.BackgroundColorMsg` with a light color and check that the raw rendered key uses the light palette's `JSONKey`. Like the existing palette tests in `views/root_test.go`, the test must save `components.Styles` and restore it in `t.Cleanup`, and must not run in parallel. Also check `inspect_container_80x14.golden` and `inspect_network_80x14.golden` still pass unchanged, without `-update`.

## 3. Logs coloring

- [ ] 3.1 Implement timestamp detection (Decision 4) as a function returning at most one span. Verify with table tests for spec scenarios "RFC 3339 timestamp", "Bracketed timestamp with a space" and "Timestamp not at the start", plus spec "Zone after a space", a comma fraction, a `+01:00`, a `+0100` and a `-05` zone, `2026-11-28 10:15:02 +0100 msg` (zone included), a `[` with no closing `]` (no span), and a date with no time (no span).
- [ ] 3.2 Implement level detection over the first 512 bytes of the line, cut to a rune boundary: the leftmost of the uppercase whole-word, `level=`/`lvl=`, JSON `"level"` and `[word]` candidates. Each structured candidate iterates all its matches and takes the first in the level set, and only the value is colored. Verify with table tests for every scenario under "Colored log levels" and "Log level recognition", plus `lvl="WARN"`, `[ERROR]`, `ERR:`, `INFO` (no span), and an `ERROR` that starts after byte 512 (no span).
- [ ] 3.3 Implement case-insensitive match spans, with the rune-by-rune offset map used only when lowering changes byte lengths. Verify with tests for spec "Every occurrence highlighted", non-overlapping repeats (`aaa` searched in `aaaa`), a needle with leading spaces (trimmed as `setSearch` does), a line containing `İ` where the highlighted bytes are the original-text bytes of the match, and a line mixing a shrinking and a growing rune (`K` then `İİ`, Kelvin sign first) followed by the needle, where the highlight lands on the needle's original bytes.
- [ ] 3.4 Combine the three span sets with match precedence, splitting timestamp and level spans around matches so the result is sorted and non-overlapping, and render them through the styled pager path in `logsView`, computed only for visible lines. Verify with view tests that assert on the raw `e.m.View().Content`:
  - a line with a timestamp, a level and a match renders each with its style, and a match over the level takes the match style;
  - spec "Search clears the highlight": after Esc, no `SearchMatch` styling is left;
  - `logs_80x12.golden` passes unchanged.
- [ ] 3.5 Add `BenchmarkLogsShown` for a 16 KiB line of prose with no level word and an active search, and verify it runs under 100 µs per line (`go test -bench LogsShown ./internal/ui/views/`).

## 4. Documentation

- [ ] 4.1 Update the colors paragraph in `README.md`: inspect views color JSON keys, strings, numbers and punctuation, and a container's `State.Status` matches its row color; logs dim a leading timestamp, color the first level word (with the recognized forms), and highlight and underline search matches; the text shown never changes. Check every described form against the scanner tests.

## 5. Integration checks

- [ ] 5.1 Run `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...`, and verify all pass with no golden-file changes (`git status internal/ui/**/testdata` is clean).
- [ ] 5.2 Run drydock against a real engine:
  - inspect a running and an exited container, an image, a volume and a network, scrolling horizontally across a long string;
  - open logs of a container that prints mixed formats (for example `docker run -d --name lv busybox sh -c 'while :; do echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) ERROR boom"; echo "level=warn msg=slow"; echo "[debug] tick"; echo "no error here"; sleep 1; done'`) and search for `err`.

  Confirm the colors match the spec on a dark and a light terminal theme, and that with `NO_COLOR=1` search matches are still underlined.
- [ ] 5.3 Run `openspec validate add-inspect-and-logs-coloring --strict` and verify it passes.

## Workflow follow-up

- Archive the change with `/opsx:archive` after review, which syncs the delta into `openspec/specs/tui-navigation/spec.md`.
