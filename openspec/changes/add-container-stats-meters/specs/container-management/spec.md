# Spec Delta

## ADDED Requirements

### Requirement: Stats usage colors
While stats are shown, a CPU or MEM cell SHALL be drawn in the warning color when its usage level (see "CPU usage level" and "Memory usage level") is at least 70%, and in the error color when it is at least 90%. Below 70%, or when the cell has no usage level, it SHALL keep its row's color. Only that cell changes color; the rest of the row keeps the row's color. A cell showing `-` SHALL have no usage color.

#### Scenario: Normal usage keeps the row color
- **WHEN** running `web` has a memory usage level of 25%
- **THEN** its MEM cell is drawn in the same color as the rest of its row

#### Scenario: Warning threshold
- **WHEN** running `api` has a memory usage level of exactly 70%
- **THEN** its MEM cell is drawn in the warning color and its other cells keep the row's color

#### Scenario: Error threshold
- **WHEN** running `db` has a CPU usage level of 95%
- **THEN** its CPU cell is drawn in the error color

#### Scenario: Placeholder cells
- **WHEN** stats are on, "all" mode is active, and `old` is exited
- **THEN** its CPU and MEM cells show `-` with no usage color

#### Scenario: Selected row
- **WHEN** the selection is on `db`, whose CPU usage level is 95%
- **THEN** the `db` row is drawn with the selection highlight alone, like any selected row, and its CPU cell text is unchanged

### Requirement: CPU usage level
A CPU cell's usage level SHALL be the value it shows divided by the host's capacity: the number of online CPUs the engine reports for the container's latest sample, times 100%. A CPU cell showing `-` SHALL have no usage level.

#### Scenario: Several cores on a multi-core host
- **WHEN** a container's CPU cell shows `280.0%` on a 4-core host
- **THEN** its CPU usage level is 70% and the cell is drawn in the warning color

#### Scenario: One full core on a large host
- **WHEN** a container's CPU cell shows `100.0%` on a 16-core host
- **THEN** its CPU usage level is 6.25% and the cell keeps the row's color

#### Scenario: Single-core host
- **WHEN** a container's CPU cell shows `92.0%` on a 1-core host
- **THEN** the cell is drawn in the error color

### Requirement: Memory usage level
A MEM cell's usage level SHALL be the memory usage divided by the memory limit, both as reported in bytes, not as rounded in the cell's text. When the reported limit is zero, or the cell shows `-`, the cell SHALL have no usage level. A usage above the limit SHALL count as a level of 100%.

#### Scenario: Usage against the limit
- **WHEN** a container's MEM cell shows `922MiB / 1GiB` (922 MiB of 1024 MiB)
- **THEN** its memory usage level is about 90.04% and the cell is drawn in the error color

#### Scenario: Zero limit reported
- **WHEN** the engine reports a memory usage but a limit of zero for a running container
- **THEN** its MEM cell shows its usual text with no usage color and no memory bar

### Requirement: Memory usage bar
While stats are shown, each MEM cell with a usage level SHALL also show a bar of that level, 2 cells after the widest listed MEM text and at most 10 cells wide. The bar SHALL use only width the column leaves blank, so column widths do not change. It SHALL be shown only when the MEM column is wider than its minimum width and at least 5 cells remain for it.

#### Scenario: 100-column terminal
- **WHEN** the terminal is 100 columns wide and stats are shown, even if every listed MEM text is short (for example `52KiB / 1GiB`)
- **THEN** no memory bar is shown and the containers view looks exactly as it did before memory bars existed

#### Scenario: Wide terminal
- **WHEN** the MEM column is wider than its minimum width and has 12 or more cells after its widest text
- **THEN** each MEM cell with a usage level shows a 10-cell bar, 2 cells after the end of the widest MEM text

#### Scenario: Bars start at the same column
- **WHEN** bars are shown and `db` shows `999.9MiB / 2GiB` while `redis` shows `8.1MiB / 1GiB`
- **THEN** both bars start at the same column

#### Scenario: Narrow spare width
- **WHEN** the MEM column is wider than its minimum width but has only 4 cells left after its widest text and the 2-cell gap
- **THEN** no bar is shown on any row

#### Scenario: No bar without a usage level
- **WHEN** bars are shown and a listed container's MEM cell shows `-` or has a zero limit
- **THEN** that row shows no bar, and the other rows' bars are unaffected

#### Scenario: Filtering changes the alignment
- **WHEN** bars are shown and a filter hides the row with the widest MEM text
- **THEN** the bars of the remaining rows start 2 cells after the widest MEM text among the rows still listed

### Requirement: Memory bar drawing
A memory bar of width W SHALL show the usage level as a filled part followed by an empty part. The filled part uses full blocks (`█`) and at most one partial block (`▏▎▍▌▋▊▉`) for the last eighth-cell, rounded to the nearest eighth (a half rounds up). The empty part uses `░`. A level of 0 SHALL draw only `░`, and a level of 100% or more only `█`. The bar SHALL be drawn in its cell's color, including the warning and error usage colors.

#### Scenario: Quarter usage
- **WHEN** a 10-cell bar is drawn for a memory usage level of 25%
- **THEN** it reads `██▌░░░░░░░`

#### Scenario: Full usage
- **WHEN** a 10-cell bar is drawn for a container using more memory than its limit
- **THEN** it reads `██████████`

#### Scenario: Bar on a warning cell
- **WHEN** a bar is drawn for a MEM cell with a usage level of 80%
- **THEN** the bar is drawn in the warning color, like the cell's text

#### Scenario: Bar on the selected row
- **WHEN** the selection is on a row with a memory bar
- **THEN** the bar is still drawn, with the selection highlight alone

### Requirement: Stats meters keep text unchanged
Usage colors SHALL only add styling. The CPU and MEM text, the column widths, truncation and the filter SHALL be the same as without usage colors and memory bars, on light and dark palettes and on terminals without color. The memory bar SHALL be the only text the meters add, and the list filter SHALL NOT match it.

#### Scenario: Styling stripped
- **WHEN** the containers view is rendered with stats on and its styling is stripped
- **THEN** the result is identical to the same view rendered without usage colors, apart from any memory bars

#### Scenario: Without color
- **WHEN** the terminal shows no color (for example with `NO_COLOR` set) and bars are shown
- **THEN** the CPU and MEM text is the same as with color, and each bar shows its level with its `█`, partial and `░` glyphs

#### Scenario: Light palette
- **WHEN** the terminal reports a light background
- **THEN** a MEM cell at 80% is drawn in the light palette's warning color and a CPU cell at 95% in its error color

#### Scenario: Filter ignores bars
- **WHEN** bars are shown and the user filters with `█`
- **THEN** no row matches the filter
