# Proposal

## Why

drydock works well, but it looks plain. The header, tab strip, table and footer are all unstyled text, so it's hard to see where one ends and the next begins. In the tab strip, each hotkey number runs into its count. Selected rows use harsh reverse video. Flex columns split the leftover width evenly, whatever they contain. Dialogs stretch to almost the full terminal width, even for a three-line message. This change polishes the look without changing the layout model or any key behavior.

## What Changes

- **Section chrome**: the header gets a background bar, and on terminals tall enough for it, a thin rule separates the tab strip from the list. The status line already separates the list from the footer.
- **Tab strip**: each tab is shown as a pill with its count set apart from its hotkey number, for example `1 containers (4)`, so the two numbers no longer run together. The active tab is filled with the accent color.
- **Selection**: the selected row gets an accent background bar and a gutter marker (`▌`) instead of reverse video. It still never shows its state color.
- **State glyphs**: container rows show a state glyph in the STATUS column (`●` running, `◐` paused/restarting, `○` exited/created/dead). This keeps state readable on the selected row, which has no state color. The glyphs are Unicode only, with no ASCII fallback, as the existing `●` connection indicator and rounded dialog borders already are.
- **Column sizing**: flex columns shrink to their widest cell (never narrower than their header or minimum width). Width left over goes to the last flex column, instead of being split evenly.
- **Dialogs**: confirmation and prompt dialogs fit their content, within a bounded width range, instead of spanning the terminal. Confirmation dialogs, which are always destructive, get an error-colored border.
- **Empty state**: an empty list shows a centered message, with a hint when one applies (for example, how to show all containers when only running ones are listed).
- **Adaptive palette**: colors adapt to light and dark terminal backgrounds, instead of fixed ANSI-256 values.

No key bindings, view-stack behavior or data shown change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tui-navigation`: the "Resource tab strip" requirement changes how the count is displayed next to the resource name. The "Confirmation for destructive actions" requirement adds a distinct destructive style. New requirements cover content-fitted list columns, the selected-row marker, container state glyphs (shown on the selected row too), content-sized dialogs, confirmation text that wraps instead of being cut off, the centered empty-list message, the rule below the tab strip, and the adaptive palette. "State-colored rows" is unchanged.

## Impact

- `internal/ui/components/styles.go`: adaptive palette; new header-bar, tab-pill, selection-bar and destructive-dialog styles.
- `internal/ui/render.go`: header bar, tab pills, section rules, empty-state rendering.
- `internal/ui/components/table.go`: content-aware `widths()`, gutter marker, selection style.
- `internal/ui/components/prompt.go`: content-sized `Prompt`/`Confirm` dialogs.
- `internal/ui/components/text.go`: text inputs (list filter, command bar, prompt, log search) are restyled when the palette changes.
- `internal/ui/views/containers.go`: state glyph in the STATUS cell.
- All 10 golden snapshots under `internal/ui/testdata/` and `internal/ui/views/testdata/` will be regenerated: every one contains the tab line, and those at least 12 lines tall also gain the rule line. Tests that assert on tab-strip text, selected-row rendering or column widths need updating.
- No new dependencies. Lipgloss v2 already provides adaptive colors.
