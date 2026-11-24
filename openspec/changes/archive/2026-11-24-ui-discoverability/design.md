# Design

## Context

The root `Model` (`internal/ui/model.go`) owns a stack of `View`s. It renders the header, the content, the status line and the footer in `render.go`, with `chromeLines = 3`. Resource switching goes through `runCommand`, which looks up a `ui.Resource` (by name or alias) and calls `replace`. Today the model does not remember which resource the root view came from. It keeps the latest snapshot in `m.snap`, and Bubble Tea re-renders after every `Update`.

The three list views wrap `components.Table`. The table renders every row with the same style and reverse-video for the selection. `KeyHelp()` returns static bindings. The footer joins them with the global hints and cuts the result with `components.Fit`. The help overlay skips bindings that are not `Enabled()`, and renders keys in an 8-cell column. Views match key presses against the package-level `keys.*` bindings, never against what `KeyHelp()` returns.

`key.Binding` is a value type (`keys []string`, `help`, `disabled`). `SetEnabled` and `SetHelp` write only the `help` and `disabled` fields, and `key.Matches` skips disabled bindings.

See proposal.md for motivation and specs/tui-navigation/spec.md for the required behavior.

## Goals / Non-Goals

**Goals:**
- Keep the `View` interface unchanged. Add new view capabilities as small optional interfaces that the root detects with a type assertion, so the logs and inspect views need no edits.
- Keep all colors in `styles.go`.
- Keep the help overlay fitting an 80x24 terminal.

**Non-Goals:**
- Configurable themes or key bindings.
- Mouse support, sorting, multi-select, preview panes.
- Changing what any action does. Hiding a hint never disables its key.
- Marking tab counts as stale while disconnected. They show the last snapshot, as the lists do.

## Decisions

### D1. The tab strip is its own chrome line, dropped on very short terminals

`render()` inserts `tabsView()` after the header when `m.height >= 5`. Chrome is then 4 lines. Below 5 lines the strip is left out and chrome stays 3 lines, so the header, status line and footer keep their lines on a tiny terminal. `contentSize()` uses the same rule. A helper `showTabs()` keeps the two in sync.

The strip renders `1 containers 12  2 images 34  3 volumes 5`. The active tab uses the accent style, bold. The others are muted. Each tab's short form is the resource's first alias, or its name if it has no alias. The strip uses the short forms when the full form does not fit.

The position indicator (D4) is right-aligned on this same line, and it always wins. The line is a leading space, the strip, a one-cell gap, the position and a trailing space, so the strip gets `width - width(position) - 3` cells. Anything that still does not fit is clipped with `Fit`. Only a terminal narrower than the position itself cuts the position.

*Alternative:* merge the tabs into the header next to the breadcrumb. Rejected: the header already carries the breadcrumb, the indicators and the connection state, and would overflow on narrow terminals.

### D2. The model tracks the root resource index

`Model` gains `rootIdx int`. `lookup` is refactored into `lookupIdx(cmd) (int, bool)`, which matches names and aliases, and `lookup` wraps it. `New` sets `rootIdx` from `lookupIdx(initial)`, so an alias initial works. `runCommand` and the tab keys share a `switchTo(i int) tea.Cmd` helper that sets `rootIdx` and calls `replace`. Drill-down pushes leave `rootIdx` alone, so the highlight follows the stack's root.

The tab keys do nothing when `i == rootIdx && len(m.stack) == 1`. This keeps a stray `1` or a `Tab` wrap from discarding the root view's filter, selection and mode. The command bar keeps its existing behavior (`:containers` always replaces), because typing a command is deliberate.

`ui.Resource` gains a `Count func(store.Snapshot) int` field, set in `views/resources.go`. The tab strip calls it on `m.snap`, and leaves out the count when `Count` is nil or `!m.snap.Loaded()`. `Snapshot` gains `NumContainers()`, `NumImages()` and `NumVolumes()`, which return `len` without cloning. `Containers()` and the other accessors clone their slice, and the strip renders on every spinner tick. Counts are engine totals: all containers whatever the `a` toggle, and image objects including dangling ones, not tag rows. The spec states this, and the list position gives the exact row count.

*Alternative:* the model hard-codes counts by resource name. Rejected: it leaks resource knowledge into `ui`, and test resources could not stub it.

### D3. Global keys: hidden switching bindings plus one help entry

`keys.Global` gains bindings that are used only for matching and are not listed: `Tab1`, `Tab2` and `Tab3` (`1`, `2`, `3`), `NextTab` (`tab`) and `PrevTab` (`shift+tab`). It also gains one display-only binding, `SwitchHelp`, with the help text `1-3/tab` / `switch resource`. `SwitchHelp` alone joins `keys.Global.Bindings()`, which adds one help row instead of five.

The switching keys are handled in `handleKey` beside `g.Command`, after the overlay and `InputFocused` checks, so text inputs (the list filter, the logs search, the prompt, the command bar) keep precedence for free. The digit keys map to resource index `0..2`, and keys beyond `len(resources)` do nothing. The footer does not show `SwitchHelp`, because the tab strip itself is the hint.

### D4. List position: an optional `Positioner` interface

```go
// in package ui
type Positioner interface {
    Position() (selected, listed, total int, filtered bool)
}
```

`components.Table` gains `Position()`:
- `selected` is `cursor+1`, or 0 when no row is listed.
- `listed` is `len(visible)`.
- `total` is `len(all)`.
- `filtered` is `filter != ""`.

The three list views implement `Positioner` by delegating to it. `render.go` asserts `m.top().(Positioner)` and formats `3/12`, adding ` of N` exactly when `filtered` is set (so `0/0 of 12` and `1/12 of 12` both happen). Rows hidden by a mode (running-only, tagged-only) are not in `all`. The mode already shows in the header indicators.

*Alternative:* put the position in `Indicators()`. Rejected: every view would duplicate the formatting, and the header would get crowded.

### D5. Row tones

`components.Row` gains a `Tone` field (an enum: `ToneDefault`, `ToneGood`, `ToneWarn`, `ToneDim`). `StyleSet` gains `RowGood`, `RowWarn` and `RowDim`, using the existing palette (`colorSuccess`, `colorWarn`, `colorMuted`). `Table.View` styles each unselected row by its tone. The selected row is rendered from the untoned line with `TableSelected` (plain reverse) alone, the same as today. Reversing a toned line would turn the tone color into the background (for example a grey block for a dimmed row), which makes the selection look different on every row.

Views set `Tone` in `refresh()`:
- **Containers:** `running` gives `Good`. `paused` and `restarting` give `Warn`. `exited`, `created` and `dead` give `Dim`. Any other state (for example `removing`) gives `Default`.
- **Images:** dangling gives `Dim`.
- **Volumes:** `snap.VolumeUseCount(name) == 0` gives `Dim`. This is the same accessor the `IN USE` column already uses.

*Alternative:* add a state glyph column. Rejected for now because it changes column layout and widths. Color is additive, and the STATUS and IN USE columns already carry the information without color, which covers NO_COLOR terminals.

### D6. Context-aware key help

`View.KeyHelp()` keeps its signature. List views build it per call from copies of their bindings. Because `key.Binding` is a value type, this never touches the package-level `keys.*` values that `handleKey` matches against.

- Order is priority:
  - Containers: `l logs`, `s start|stop`, `x shell`, `i inspect`, `r restart`, `d delete`, `a all/running`, `/ filter`.
  - Images: `enter containers`, `i inspect`, `p pull`, `d delete`, `/ filter`, `a all/tagged`, `P prune`.
  - Volumes: `enter containers`, `i inspect`, `c create`, `d delete`, `/ filter`, `P prune`.
- Inapplicable bindings are disabled copies (`SetEnabled(false)`):
  - When nothing is selected: every row action. That leaves only `a`, `/`, `p`, `c` and `P`.
  - Containers: `x` unless the container is `running`. `s` reads `stop` when `running`, `start` when `created` or `exited`, and is disabled in any other state. This mirrors what `startStop` accepts.
  - Images and volumes: `enter` when the use count is 0.

Logs and inspect keep their existing `KeyHelp()`. Footer fitting (D7) applies to them too, so the order of their existing bindings becomes their priority.

### D7. Footer fitting

`footerView` builds whole hint segments, skipping disabled bindings, and measures the styled text with `ansi.StringWidth`.
- If every view hint plus `: command`, `? help` and `q quit` fits in `m.width-1`, it shows them all.
- Otherwise it drops all three global hints, adds view hints in order while they fit in the remaining width less the width of `? more`, and appends `? more`.

The input-focused and help-overlay footers are short and unchanged.

### D8. Help overlay: disabled keys dimmed, wider key column

`helpView`'s `section` renders a disabled binding with `FooterDesc` (muted) for both the key and the description, instead of skipping it. Global bindings are never disabled. The key column widens from 8 to 10 cells, so labels such as `1-3/tab` and `shift+tab`-length keys are not cut off.

The size budget at 80x24 with chrome 4 is 20 content lines. The help overlay holds:
- 1 global title and 6 global rows (5 existing plus `SwitchHelp`)
- 1 blank line, 1 view title and 8 container rows
- 1 blank line and 1 close hint
- 2 border lines

That is 21 lines, one over the budget. Fix: drop the blank line before the close hint (`render.go:205`), which makes the total 20. A test pins it (task 3.5). If a view grows more keys later, the test fails, prompting a two-column layout.

## Risks / Trade-offs

- [`1`/`2`/`3`/`Tab` pressed by accident in a drill-down drop the stack] → This matches the command bar's existing semantics. At the root the key does nothing (D2), so no work is lost there. Text inputs take precedence, so typing is safe.
- [One less content line] → Only on terminals of 5 or more lines. Shorter terminals keep the old chrome (D1).
- [Colors can be hard to tell apart on some themes] → The palette is unchanged (already chosen for light and dark backgrounds), and dimming uses the existing muted grey. The state is also in the STATUS and IN USE text.
- [The help overlay is at its size budget at 80x24] → Pinned by a test (task 3.5).
- [Existing render tests assume 3 chrome lines and the old footer text] → They are updated by the tasks that change them.

## Migration Plan

None. This is a UI-only change with no persisted state or configuration. Rollback is a revert.
