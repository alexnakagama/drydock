# Design

## Context

See proposal.md for motivation and specs/tui-navigation/spec.md for the behavior. Current state that shapes the approach:

- **Styles:** every style lives in one global `components.Styles` (`internal/ui/components/styles.go`), built once at package init from five fixed ANSI-256 colors. Views and components read it at render time, so swapping the value restyles them on the next frame. The exception is text inputs: `components.NewInput` (`text.go`) copies `textinput.DefaultDarkStyles()` and `Styles.InputPrompt` into each input when it is created, so inputs that already exist keep their old styles.
- **Screen layout:** `Model.render` (`internal/ui/render.go`) stacks the header, tab strip, content, status line and footer. `chromeLines()` decides how many lines the content area gets, and the tab strip is dropped below `minTabsHeight`.
- **Column widths:** `Table.widths()` gives each column its `Width` as a minimum, then splits the leftover space evenly across `Flex` columns. It doesn't look at what's in the cells.
- **Selection:** the selected row is rendered with `Reverse(true)` on the untoned line.
- **Dialog width:** `Prompt` and `Confirm` size themselves with `dialogInner(maxWidth, preferred)`: a preferred width of 60 or 70, shrunk only to fit the terminal. The help overlay already sizes itself to its content.
- **Empty lists:** the empty-list text is drawn left-aligned on the first body line. The views already write hint text such as "press a to show all".
- **Golden tests:** goldens strip ANSI codes, so they capture text and layout only, not color. Both golden harnesses take `-update`. The `internal/ui/testdata` goldens render a real `components.Table` (through `listView` in `model_test.go`), so table changes affect them too. Every golden contains the tab line.
- **Tests:** the tests in `model_test.go` and `views/root_test.go` that check tab-strip text compare against `st.TabActive.Render("1 containers 12")`, and `TestTabsShowCounts` checks the spacing between tabs. Dialog tests live in `components/table_test.go`; there is no `prompt_test.go`.

## Goals / Non-Goals

**Goals:**
- Keep every change inside the existing style and render seams: `styles.go`, `render.go`, `table.go`, `prompt.go` and the containers row builder.
- Keep the layout monochrome-safe: with color stripped (e.g. `NO_COLOR`), selection and state must still be readable from text alone (marker and glyphs).

**Non-Goals:**
- User-configurable themes, or a theme file.
- An ASCII fallback for glyphs.
- Any change to the layout model (side panels, split views).
- Restyling the logs and inspect pagers beyond what they inherit from the palette.

## Decisions

### 1. Palette: two built style sets, swapped on the terminal's background report

`styles.go` gets `NewStyles(dark bool) StyleSet`. `Styles` starts as `NewStyles(true)`. `Model.Init` sends `tea.RequestBackgroundColor`. On `tea.BackgroundColorMsg`, `Update` sets `components.Styles = NewStyles(msg.IsDark())`.

- **Why this is safe:** bubbletea runs `Update` and `View` on one goroutine, so swapping the global needs no locking.
- **Alternative rejected: `lipgloss.LightDark` resolved at init via a direct terminal query.** That reads stdin outside bubbletea's input reader and can steal or block key input.
- **Alternative rejected: per-style `LightDark` wrappers.** They would need the background known when each style is built, which comes back to the same plumbing.

The light palette uses darker shades of the same hues: blue, green, red, orange, and a grey around 240 instead of 245. Only foreground and background values change between palettes, never text, which is what the spec requires.

**Text inputs:** `text.go` gets `StyleInput(in *textinput.Model)`. It applies `textinput.DefaultStyles(dark)` (bubbles v2) with the current `Styles.InputPrompt` and the static cursor. `NewInput` calls it, and so does every owner of an input when it receives `tea.BackgroundColorMsg`: `Table.Update` for the filter, `Model.Update` for the command bar and any open prompt, and the logs view for its search input. `Model.broadcast` already forwards unhandled messages to every view on the stack, so views see the message without new plumbing. `StyleSet` records which palette it was built for (`Dark bool`), so `StyleInput` doesn't need a separate argument.

### 2. Header bar and one rule line

- **Header bar:** the header line is drawn on a bar background across the full width. Lipgloss resets all attributes at the end of each styled span, so every style used inside the header (`Breadcrumb`, `Crumb`, `Indicator`, connection states) is built with the bar background as well. Today `headerView` returns `Fit(" "+left, gap) + right + " "`, where the leading and trailing spaces and `Fit`'s padding are unstyled. Those become spans in a `HeaderBar` style, which is the bar background alone. Doing it with a single outer style would leave holes after each inner span.
- **Rule line:** a single muted `─` rule goes under the tab strip. It is drawn only when the terminal is at least `minRuleHeight` (12) lines tall, and `chromeLines()` counts it. Terminals shorter than 12 lines (the 100x8 and 100x10 goldens) keep all their list rows. Goldens of 12 or more lines lose one content row to the rule. For example, `images_all_100x12` drops from 7 to 6 visible rows, which is expected.
- **Overlays may cover the rule:** dialogs are centred over the rule and the content area together. The rule is decoration only, and without this the help overlay (20 lines for the containers view) would lose its bottom border at 80x24, where the content area is now 19 lines.
- **No rule above the footer:** the status line already sits between the list and the footer and does that job.

### 3. Tab pills

Each tab is padded to ` 1 containers (4) `:

- **Active tab:** filled with the accent background and a contrasting foreground, bold.
- **Inactive tabs:** muted foreground, no fill.

Both use the same padding, so switching tabs never moves the labels. Pills are joined by a single space instead of today's two, because the padding already separates them. The short-alias fallback uses the same format. `tabLabels` builds the text, and the pill style adds the padding. The width checks in `tabsView` measure the rendered strings, so they need no change.

Pills are wider than today's labels. In the short form, ` 1 co (12) `, ` 2 im (34) ` and ` 3 vol (5) ` plus two joins come to 36 cells, against 26 before. So tests that pin a narrow width must be recomputed. `TestTabsNarrowUseAliases` at 40 columns only has 34 cells for tabs once its `1/3` position is placed, and moves to a width where the short form fits and the full form doesn't.

### 4. Selection gutter

The table reserves a 2-cell gutter on every line, header included, so columns stay aligned:

- **Selected row:** the gutter shows `▌` in the accent color, followed by a space. The rest of the line uses a new `TableSelected` style: a subtle accent-tinted background with the default foreground, bold. Reverse video goes away.
- **Unselected rows:** the gutter is two blanks.

As today, the selected line is built from untoned cells, which keeps the "selection highlight alone" rule. The gutter's space cell is inside the `TableSelected` span, so the bar starts right after the marker with no hole.

`t.width` keeps meaning the full table width: `View`, the padding lines and the filter input still fill it, and every line is still exactly `t.width` cells. Only `widths()` changes: it lays out columns in `t.width - gutterWidth`. `renderLine(cells, ws)` keeps its signature and returns the cells line only, without the gutter. `View` prepends the gutter: the marker plus space on the selected row, two blanks on every other line including the header. That keeps `TestTableRowTones`, which calls `widths()` and `renderLine()`, compiling. Its expected selected line becomes the marker plus `TableSelected` applied to the space and the plain line.

### 5. State glyphs in the containers view

The containers row builder prefixes the STATUS cell with `containerGlyph(state)` and a space. `containerGlyph` sits next to `containerTone` and uses the same state grouping, so the color and the glyph can't disagree. STATUS's minimum width goes from 22 to 24 to make room. The glyph is part of the cell text, so it survives selection and color stripping. The filter only matches NAME and IMAGE, so the glyph never affects filtering.

### 6. Content-fitted widths

`Table` caches each column's natural width: `max(minWidth, header width, widest visible cell)`, measured with `ansi.StringWidth` on cleaned cells. It is recomputed in `refilter` (which `SetRows` and filter changes already go through). `widths()` then:

1. Gives every column its minimum width, as today.
2. If there is spare width, grows flex columns toward their natural width. It uses water-filling: it adds one cell at a time round-robin to columns still below their natural width, done in O(columns) steps by sorting each column's growth need.
3. Gives any remainder to the last flex column.
4. When even the minimums don't fit, behaves as today: the line is fitted and cut.

Alternatives considered:
- **Proportional growth by natural width:** this lets one very long cell starve the others.
- **Measuring all rows instead of visible rows:** this keeps widths stable while filtering, but the spec asks for re-measuring.

### 7. Dialog sizing

`dialogInner(maxWidth, preferred)` becomes `dialogWidth(content, maxWidth int) int`. It clamps `content + dialogChrome` to [40, 60] and then to `maxWidth`. It returns the inner width.

- **`Confirm`:** measures the widest of its cleaned title, the body entries that can be shown (the first `maxConfirmLines`) and the hint. Body entries wrap at spaces to the dialog width, so a sentence describing what a destructive action does is never cut off by the 60-cell cap; the title wraps too, over at most two lines. A single word wider than the dialog, such as a long image reference or a generated volume name, is shortened in the middle with `…` (`TruncateMiddle`), keeping both ends, so names that differ only at the end can be told apart before confirming. All shown text is `Clean`ed once, before it is measured and wrapped; the prompt's placeholder is cleaned in `NewPrompt` as well. `maxConfirmLines` now bounds wrapped lines, and its `… and N more` line counts the entries left out or shown only in part.
- **`Prompt`:** measures its title, hint and the input's prompt plus placeholder. Typed text scrolls inside the input. Previously the input's width was set only on the copy rendered in `View`, so `Update` scrolled against a zero width and long text wrapped. `Prompt.SetMaxWidth` sizes the input, and the root model calls it when the prompt opens and on every resize.

A new `ConfirmDialog` style is `Dialog` with an error-colored border, and only `Confirm` uses it. The help overlay keeps `Dialog` and its current content sizing.

### 8. Centered empty message

When there are no visible rows, `Table.View` uses `lipgloss.Place` to center the `Empty`-styled message (truncated to the table width) in the body area below the header row. The body area's size doesn't change, so `padLines` and the line count stay the same.

### 9. Tests

- **New unit tests:**
  - `widths()`: content fit, leftover to the last flex column, cut when even the minimums don't fit.
  - Dialog width clamps, in `components/table_test.go` next to the existing dialog tests.
  - `containerGlyph` and `containerTone` agree.
  - Palette swap on a `tea.BackgroundColorMsg`, including inputs created before it. These tests restore `components.Styles` with `t.Cleanup`, because it is a package global.
  - Rule-line threshold in `chromeLines()`.
- **Existing tests to update:**
  - Tab assertions move from `"1 containers 12"` to the pill form.
  - `TestTabsShowCounts` gets the new prefix spacing.
  - `TestTabsNarrowUseAliases` gets a recomputed width.
  - In `table_test.go`, the row-prefix check (`HasPrefix(lines[9], "l ")`) and the `TableSelected.Render(plain(i))` expectations change for the gutter.
  - Stale comments are updated: `Column.Flex` and "plain reverse video" in `table.go`.
- **Header bar test:** checks that the header line has no unstyled run, without relying on the absence of resets. Split the raw line on `\x1b[m`; every non-empty segment must start with an SGR sequence that sets the bar background.
- **Goldens:** regenerated with `-update` in both `internal/ui/testdata` and `internal/ui/views/testdata` at the end of each group that changes them, and only after each diff has been read and matches the spec.

## Risks / Trade-offs

- **[Column widths shift as live events add or remove rows, so columns can jitter]** → Widths only change when the widest visible cell changes, and the minimum widths stop collapse. If it proves distracting, the cache can be made to only grow while a view stays open. That's an implementation-level tweak, with no spec change.
- **[`●◐○▌` are East-Asian-ambiguous width, so CJK-locale terminals set to "ambiguous = wide" will misalign rows]** → Accepted. drydock already uses `●`/`✗` and rounded borders, and the user chose Unicode-only.
- **[Header background is lost when color is stripped (`NO_COLOR`, dumb terminals)]** → The header text is unchanged and still readable. Only decoration is lost.
- **[Terminals that never answer the background query]** → The dark palette is the default, the same as today's effective behavior.
- **[Golden churn hides a real regression]** → Regenerate one golden at a time, read each diff, and keep layout-only diffs separate from text diffs.
- **[Gutter and glyph take 4 cells of width, and narrow terminals truncate sooner]** → Flex columns absorb this first. At 80 columns the containers view still fits NAME, IMAGE and STATUS at their minimums.
