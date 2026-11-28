# Proposal

## Why

With stats on, the CPU and MEM cells are the only plain numbers left in the containers view. They take the row's color like every other cell, so spotting a container that is close to its memory limit or using most of the host's CPU means reading every number. Coloring high usage and drawing a memory bar make the busy containers stand out at a glance, using the palette and coloring that the styling and logs-coloring changes already added.

## What Changes

- **Usage colors**: a CPU or MEM cell turns the warning color when its usage reaches 70% and the error color when it reaches 90%. Below 70% the cell keeps the row's color. CPU usage is measured against the host's capacity, which is the number of online CPUs × 100%. So on a 4-core host, `280.0%` is 70%. MEM usage is the memory usage in bytes divided by the memory limit in bytes (the values behind the cell's text, not the rounded text).
- **Memory bar**: when the MEM column is wider than its minimum width and has spare width after its text, each MEM cell with a usage level also shows a bar of the usage as a fraction of the limit, after its text, for example `512.3MiB / 2GiB  ██▌░░░░░░░`. The bar is 5 to 10 cells wide, starts at the same column on every row, and has the cell's color. The bar only uses width that would otherwise be blank. On a 100-column terminal every column is at its minimum width, so there is no bar for any MEM text, however short, and the containers view looks exactly as it does today. On wider terminals, the bars appear.
- **Text stays the same**: the CPU and MEM text, its format and the column widths do not change. With color stripped (for example `NO_COLOR`), every cell reads the same as today, plus the bar on wide terminals.
- **Selected row**: like the rest of the selected row, its stats cells are drawn with the selection highlight alone. The bar is still drawn there.
- **Placeholders**: a `-` cell has no color and no bar.

Out of scope: a CPU bar; colors or bars based on a container's own CPU limit (`--cpus`), which the container list does not report; configurable thresholds; history or sparklines; shortening the MEM text; and meters in any view other than containers.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `container-management`: new requirements for coloring CPU and MEM cells by usage, with their thresholds, and for the memory bar: when it is shown, its width and alignment, how it is drawn, and how it behaves on the selected row, on placeholder cells and without color. The existing CPU, MEM, placeholder, sort and 100-column requirements are unchanged.

## Impact

- `internal/store/stats.go`: `ContainerStats` gains the number of online CPUs, which is already read to compute the CPU percentage, so the view can measure CPU against host capacity.
- `internal/ui/components/table.go`: rows can give a single cell its own tone, instead of the row's tone. A column can carry a meter that the table draws in that column's spare width, only when the column is wider than its minimum width. The table also keeps the widest cell of that column among the listed rows. Line rendering colors each cell on its own, but the plain text is still built the same way.
- `internal/ui/components/styles.go` and `spans.go`: an error tone for cells, which uses the existing error color in both palettes.
- `internal/ui/views/containers.go`: usage fractions and their tones for the CPU and MEM cells, and the MEM meter for running containers that have a value.
- `README.md`: a sentence on usage colors and the memory bar in the stats section.
- Tests: unit tests for the thresholds (boundaries, multi-core, zero limit), bar drawing (eighths, clamping, alignment, minimum width), per-cell tones on toned and selected rows, and stripped output matching the uncolored text. Add a new golden snapshot at a width that shows bars. Existing 100-column goldens should not change.
- No new dependencies and no engine-layer changes.
