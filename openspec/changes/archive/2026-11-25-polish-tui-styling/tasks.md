# Tasks

## 1. Adaptive palette

- [x] 1.1 In `internal/ui/components/styles.go`, replace the fixed palette with `NewStyles(dark bool) StyleSet` (dark and light color values, with a `Dark bool` field recording which) and initialize `Styles = NewStyles(true)`. Add a `ConfirmDialog` style (the `Dialog` style with an error-colored border) and the new `HeaderBar`, tab-pill, gutter-marker, selection-bar and rule styles from design.md decisions 2-4, unused for now. Verify with `go build ./...` and `go test ./internal/ui/...`, which must pass unchanged.
- [x] 1.2 In `internal/ui/components/text.go`, add `StyleInput(in *textinput.Model)`. It applies `textinput.DefaultStyles(Styles.Dark)` with `Styles.InputPrompt` and the static cursor. `NewInput` calls it instead of setting the styles inline. Verify with `go test ./internal/ui/...`, which must pass unchanged.
- [x] 1.3 In `internal/ui/model.go`, request the terminal background color in `Init`. On `tea.BackgroundColorMsg`, set `components.Styles = NewStyles(msg.IsDark())` and restyle the command bar and any open prompt. Forward the message to views through `broadcast`; `Table.Update` and the logs view's search input then call `StyleInput`. Verify with a new `model_test.go` test, which restores `components.Styles` with `t.Cleanup`: before any message the dark set is in use; after a light `BackgroundColorMsg` the light set is in use, and the list filter and command bar, both created before the message, render their prompt in the light `InputPrompt` style; and the rendered text with ANSI stripped is identical before and after.

## 2. Table: content-fitted columns, selection gutter, centered empty message

- [x] 2.1 In `internal/ui/components/table.go`, cache each column's natural width in `refilter` and rewrite `widths()` as described in design.md decision 6, laying columns out in `t.width - gutterWidth`: minimums first, then flex columns grow toward their natural widths, then the remainder goes to the last flex column. Update the `Column.Flex` doc comment. Verify with new `table_test.go` cases: short names keep NAME at its minimum while the remainder goes to the last flex column; a 40-cell value widens its column; filtering re-measures; when even the minimums don't fit, the line is cut as before.
- [x] 2.2 Add the 2-cell gutter in `View`, keeping `t.width` and the `renderLine` signature as described in design.md decision 4. The selected row gets `▌` in the accent color, then the `TableSelected` background bar over the gutter space and the untoned line. Every other line, the header included, gets two blanks. Remove reverse video and update the "plain reverse video" comment. Update the existing tests that change: the `HasPrefix(lines[9], "l ")` check, and the `TableSelected.Render(plain(i))` expectations in `TestTableRowTones` and the test after it. Verify with `table_test.go` cases: every line is exactly the table width (`TestTableViewSizeAndSanitizing` unchanged); only the selected line starts with `▌`; the marker moves on `j`; the selected line carries no tone style; columns stay aligned with the header.
- [x] 2.3 Center the empty or no-match message in the body area with `lipgloss.Place`, truncated to the table width. Verify with `table_test.go` cases for an empty list and a filter with no matches, checking the message's row and column offsets.
- [x] 2.4 Regenerate the goldens in both `internal/ui/testdata` and `internal/ui/views/testdata` (`go test ./internal/ui/... -update`), one file at a time. Read each diff: only the gutter, column widths and the empty-message position may change. Then confirm `go test ./internal/ui/...` passes without `-update`.

## 3. Container state glyphs

- [x] 3.1 In `internal/ui/views/containers.go`, add `containerGlyph(state)` next to `containerTone`, using the same state grouping. Prefix the STATUS cell with the glyph and a space, and raise STATUS's minimum width from 22 to 24. Verify with `containers_test.go` cases: running → `●`, paused/restarting → `◐`, exited/created/dead → `○`, removing → blank; every state's glyph agrees with `containerTone`; the selected running row still shows `●`.
- [x] 3.2 Regenerate the `containers_*` goldens, read the diffs (only the STATUS cells and widths may change), and confirm `go test ./internal/ui/...` passes.

## 4. Content-sized dialogs

- [x] 4.1 In `internal/ui/components/prompt.go`, replace `dialogInner` with `dialogWidth(content, maxWidth)`, which clamps the total width to [40, 60] and then to the terminal width. `Confirm` measures its title, body lines and hint; `Prompt` measures its title, hint and placeholder. `Confirm` renders with `ConfirmDialog`, while `Prompt` and the help overlay keep `Dialog`. Verify with new cases in `internal/ui/components/table_test.go`, next to `TestDialogViewsFitWidth`: a short confirmation is 40 cells wide; a 90-cell body line caps the dialog at 60 cells and is shown truncated with `…`; on a 30-column terminal the dialog fits in 30 cells; `Confirm`'s border uses the error color and `Prompt`'s the accent color. `TestDialogViewsFitWidth` must still pass.
- [x] 4.2 Regenerate `internal/ui/testdata/confirm_80x24.golden`, check that the dialog is now 40 cells wide and centered, and confirm `go test ./internal/ui/...` passes.

## 5. Screen chrome: header bar, tab pills, rule

- [x] 5.1 In `internal/ui/render.go`, render the header line on the bar background across the full width. Every inner span (breadcrumb, crumb, indicators, connection state) uses the bar background. The leading and trailing spaces and the gap padding that `Fit` produces become `HeaderBar` spans instead of plain spaces. Verify with a `model_test.go` test: the header line with ANSI stripped is unchanged; and when the raw line is split on `\x1b[m`, every non-empty segment starts with an SGR sequence that sets the bar background.
- [x] 5.2 Change `tabLabels` to put counts in parentheses (`1 containers (12)`, short form `1 co (12)`), with no parentheses before the first snapshot. Render active and inactive tabs as equal-padded pills joined by a single space (design.md decision 3). Update the tests: the tab assertions in `internal/ui/model_test.go` and `internal/ui/views/root_test.go`; the prefix spacing in `TestTabsShowCounts`; and the width in `TestTabsNarrowUseAliases`, recomputed so the short form fits and the full form doesn't, along with its stale `47` cell comment. Add a not-yet-loaded case. Regenerate all 10 goldens (every one contains the tab line) after reading each diff. Verify with `go test ./internal/ui/...` without `-update`.
- [x] 5.3 Add the muted `─` rule under the tab strip, drawn only when the terminal is at least `minRuleHeight` (12) lines tall, and count it in `chromeLines()`. Verify with `model_test.go` cases: at 30 lines the rule is on the third line and the content area is one line shorter; at 11 lines there is no rule and the content height is unchanged.
- [x] 5.4 Regenerate the goldens 12 or more lines tall: `root_100x30`, `confirm_80x24`, `images_all_100x12`, `images_tagged_100x12`, `logs_80x12` and `inspect_container_80x14`. Read each diff: a rule line, and one fewer content row. Then confirm `go test ./internal/ui/...` passes without `-update`.
- [x] 5.5 Update the "Using drydock" section of `README.md`: the tab-strip example becomes `1 containers (12)`, `2 images (34)`, `3 volumes (5)` (pills, so exact spacing is not shown); the selected row is described as highlighted and marked with `▌` instead of reverse video; container STATUS glyphs are mentioned. Verify by reading the section against the spec scenarios.

## 6. Integration check

- [x] 6.1 Run `go test ./...`, `go vet ./...` and `golangci-lint run`; all must pass.
- [x] 6.2 Run `openspec validate polish-tui-styling --strict`; it must pass.
- [x] 6.3 Launch drydock against a local engine on a dark and a light terminal theme, and with `NO_COLOR=1`. Check the selection marker, glyphs, tab pills, header bar, the delete confirmation's red border, the filter input's colors on the light theme, and the centered empty message (toggle `a` with nothing running). In the `NO_COLOR` run, the selection and states must be readable from the marker and glyphs alone.

## Workflow follow-up

- Archive the change with `/opsx:archive` once implementation is reviewed and merged.
