# Design

## Context

- `components.Table.View` builds each row as one plain line (`renderLine`: each cell `Clean`ed and `Fit` to its column width, then the line `Fit` to the table width). It then wraps the whole line in the row's tone style (`toneStyle`), or in the selection style for the selected row. A cell cannot be colored on its own today.
- `components.RenderSpans(text, spans)` already colors parts of a plain line by `SpanKind`. It resolves kinds to styles at render time, so a palette swap recolors text that is already shown. The tone kinds `KindStateGood/Warn/Dim` map to `RowGood/Warn/Dim`.
- `store.cpuPercent` reads `OnlineCPUs` (or falls back to `len(PercpuUsage)`) but does not return it. `ContainerStats` holds only `CPUPercent`, `MemUsage` and `MemLimit`.
- In the 100-column stats layouts, every column is at its minimum width and the line takes 98 cells, so `Table.widths` has no spare cells. MEM is the last flex column, so on wider terminals it gets all the leftover width. MEM's minimum width (19, or 15 with the Compose column) is larger than a short MEM text (`52KiB / 1GiB` is 12 cells), so even at 100 columns there can be blank cells after the text. The meter must therefore not use "blank cells after the text" alone.
- `natural` for a column is the widest of its title, its minimum width and its cells, so it cannot give the widest cell text by itself.
- The golden snapshots are `ansi.Strip`ped screens. So styling changes do not show in them, and new text such as a bar does.

## Goals / Non-Goals

**Goals:**
- Color one cell inside a toned row, using the existing span renderer instead of a second styling path.
- Draw the meter only in width that `Table.widths` already gives the column. Column widths, natural widths and filtering stay exactly as they are, so no existing golden changes.
- Keep the threshold and bar math pure and in `views`. The table only knows "this cell has a tone" and "this column has a fraction to draw".

**Non-Goals:**
- A general multi-meter or per-cell-span API in the table. One meter column per table and one tone per cell are enough here.
- Coloring the selected row's cells. The tui-navigation "State-colored rows" rule (selection highlight alone) stays as it is.
- Using `Fit`'s ellipsis on styled text. Truncation stays on plain text.

## Decisions

### D1. Per-cell tones in `components.Row`

`Row` gains `CellTones []Tone`, indexed like `Cells`. A missing entry or `ToneDefault` means "use the row's tone". A new `ToneBad` maps to a new `RowBad` style (`Foreground(p.error)`) and a new `KindStateBad`.

`View` renders an unselected row as `RenderSpans(plainLine, spans)`. It emits one span per cell, over that cell's byte range in the plain line, using the cell's tone kind or else the row's tone kind. The gaps between columns, the padding and any truncation ellipsis take the row's tone kind, and adjacent spans of the same kind are merged. So a row without cell tones is one span over the whole line, which renders byte for byte like today's `Style.Render(line)` (pinned by `TestRenderSpansMatchesStyleRender`). The selected row keeps today's path: the plain line in the selection style.

- *Why spans over styling each cell with `lipgloss.Render` and joining:* `RenderSpans` guarantees `ansi.Strip(out) == text`, builds each kind's escape sequence once per call, and is already tested. The plain line, and so the truncation, is built exactly as today.
- *Alternative: nest the cell style inside the row style.* lipgloss closes with a full reset, so the row's color would be lost after the first colored cell.
- *Why the tone type rather than raw `SpanKind`:* rows already speak in `Tone`, and the bad/warn split maps directly to the palette's error/warn colors.

### D2. Byte ranges come from building the plain line

`renderLine` records the byte offset where each cell starts and ends as it writes the line. It returns the line and those ranges. The final `Fit` to the table width can only cut the end of the line. If it does, `RenderSpans` clamps any span past the end. A trailing `…` may then fall inside the last span, which is harmless.

### D3. Meter column, drawn in spare width

`Column` gains `Meter bool`. `Row` gains `Meter float64` and `HasMeter bool`. In `View`, the table computes once per frame for the meter column:

- `textW` = the widest `cellWidth` of that column among the visible rows. `refilter` already walks the visible rows to compute `natural`, so it also records this per column (for example `t.textMax`, separate from `natural`, which includes the title and the minimum width). The alignment then follows the filter.
- `barW = min(10, ws[col] - textW - 2)`. No bar is drawn if `barW < 5` or if `ws[col] <= Column.Width`. The second condition keeps every 100-column layout bar-free whatever the MEM text, since there every column is at its minimum width.

For each row with `HasMeter`, the cell becomes `Fit(text, textW) + "  " + bar(fraction, barW)`, and is then `Fit` to `ws[col]` as usual. Rows without a meter keep their plain cell. The bar is inside the cell's byte range, so it gets the cell's tone (D1) for free.

- *Why the table and not the view:* only the table knows the rendered column width. The view builds rows before widths exist, and putting a bar in the cell text would raise the column's natural width, which would change widths and the 100-column layout.
- *Why the bar is excluded from `natural` and the filter:* `SetRows` measures and lowercases `Cells` only. The bar is added at render time, so the widths and filter rules in "Stats meters keep text unchanged" hold by construction.

### D4. Bar glyphs

`components.Bar(fraction float64, width int) string` (pure, exported for tests) clamps the fraction to [0, 1] and computes `eighths = round(fraction * width * 8)` (half rounds up, `math.Round`). It draws `eighths/8` full blocks, one partial block from `▏▎▍▌▋▊▉` when `eighths%8 > 0`, and pads with `░` to `width`. All glyphs are 1 cell wide, like `▌` and `●` already used in the UI.

### D5. Usage levels and thresholds in `views`

- `store.ContainerStats` gains `CPUs int`, set from the same `online` value `cpuPercent` uses. `HasCPU` already implies `CPUs > 0`.
- `cpuLevel(s) = shownCPU(s.CPUPercent) / (100 * s.CPUs)`. It uses the rounded value shown in the cell, as the sort does, so what the user reads decides the color.
- `memLevel(s) = min(float64(MemUsage) / float64(MemLimit), 1)`, in floating point and from the byte values, not the rounded text. It reports no level when `MemLimit == 0`.
- `usageTone(level)`: `ToneBad` at ≥ 0.9, `ToneWarn` at ≥ 0.7, else `ToneDefault` (which means the row's tone).
- `setRows` fills `CellTones` for the CPU and MEM cell indexes, and sets `Meter`/`HasMeter` from `memLevel`. `statsColumns` and `composeStatsColumns` mark MEM with `Meter: true`.
- *Alternative: threshold CPU against one core (100%).* Most containers on a multi-core dev machine would then turn red when merely busy. Host capacity answers "is this container saturating the machine", which suits a local engine. A container's own `--cpus` limit would be better still, but `container.Summary` does not carry it (see Risks).

## Risks / Trade-offs

- [A single-threaded container that is CPU-bound on a large host stays the row's color (100% on 16 cores is 6%)] → This is documented in the spec scenario. A per-container CPU limit needs an inspect per container and is out of scope.
- [No bar at exactly 100 columns, which is the width the snapshots use] → This was chosen to keep the current layout. A new golden at a wider width (for example 130×10) covers the bar, and the 100-column goldens prove nothing moved.
- [Bars shift when a filter hides the widest MEM text] → This is intended and specified: they align on the listed rows. It is cheap, because `cellWidth` is already cached.
- [An extra per-row slice (`CellTones`) in every table] → Only the containers view sets them, and a nil slice costs nothing.
- [Running rows are green, and the warn/error colors must stand out against them] → Warn (214/130) and error (203/160) already differ from success (42/28) in both palettes. Normal usage uses no third color.

## Migration Plan

None. This is display-only, and there is no stored state or setting.
