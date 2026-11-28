# Tasks

## 1. Store: CPU count

- [x] 1.1 Add `CPUs int` to `store.ContainerStats` and set it in `statsFromSample` from the same online-CPU value `cpuPercent` uses (`OnlineCPUs`, else `len(PercpuUsage)`). Do this by having `cpuPercent` return the count, or with a small shared helper. Extend `internal/store/stats_test.go` with cases for `OnlineCPUs` set, the `PercpuUsage` fallback, and `CPUs == 0` whenever `HasCPU` is false. Verify with `go test ./internal/store/`.

## 2. Table: per-cell tones

- [x] 2.1 Add `ToneBad` to `components.Tone`, `RowBad` (error foreground) to `StyleSet` in both palettes, and `KindStateBad` to `SpanKind` mapped to `RowBad`. Extend `styles_test.go` and `spans_test.go` so the new kind renders in the error color on dark and light palettes. Verify with `go test ./internal/ui/components/`.
- [x] 2.2 Make `renderLine` also return each cell's byte range in the plain line (D2). Add `Row.CellTones` and render unselected rows with `RenderSpans` over those ranges, using the cell tone or else the row tone (D1). Keep the selected row on today's selection-style path. Add `table_test.go` cases for these: a toned row with one `ToneBad` cell colors only that cell; the stripped output equals the plain line; a line truncated by the table width keeps valid styling; and the selected row has no cell tone. Verify that `go test ./internal/ui/...` passes with no golden changes.

## 3. Table: meter column and bar

- [x] 3.1 Implement `components.Bar(fraction, width)` (D4) with table-driven tests for 0, 25% at width 10 (`██▌░░░░░░░`), rounding to the nearest eighth (including an exact half), exactly 100%, above 100%, negative, and widths 5 and 10. Check that every result is exactly `width` cells. Verify with `go test ./internal/ui/components/ -run Bar`.
- [x] 3.2 Add `Column.Meter`, `Row.Meter` and `Row.HasMeter`. In `View`, make `refilter` record the widest cell of each column among the visible rows (separate from `natural`), then in `View` compute `barW = min(10, w-textW-2)` and draw `Fit(text,textW)+"  "+Bar(...)` only when `barW >= 5` and `w > Column.Width` (D3). Add table tests for these: no bar when spare width < 5, no bar when the column is at its minimum width even if the text is short (`textW` 9 in a 19-cell column), a 10-cell bar when there is ample width, bars aligned across rows of different text widths, rows without `HasMeter` unchanged, alignment following the filter when the widest row is hidden, the bar taking the cell's tone, the bar shown on the selected row, and `natural` widths and filter matches unaffected. Verify with `go test ./internal/ui/components/`.

## 4. Containers view: levels, tones, meter

- [x] 4.1 Add pure `cpuLevel`, `memLevel` and `usageTone` helpers in `internal/ui/views/containers.go` (D5). Add table-driven tests for the spec scenarios: `280.0%` on 4 cores is warn, `100.0%` on 16 cores is the default tone, `92.0%` on 1 core is bad, exactly 70% and 90% are boundaries, 922 MiB of 1 GiB is bad, a level from byte values (700 of 1000 is warn, 900 of 1000 is bad), a zero limit gives no level, and usage over the limit clamps to 1. Verify with `go test ./internal/ui/views/ -run Level`.
- [x] 4.2 Mark MEM with `Meter: true` in `statsColumns` and `composeStatsColumns`. Make `setRows` fill `CellTones` for the CPU and MEM cells and `Meter`/`HasMeter` from `memLevel`, using `shownStats` so that placeholders, non-running containers and the disconnected state get no tone and no bar. Add view tests for these: a warn MEM cell in a running row, a bad CPU cell, the exited and paused rows in "all" mode without tone or bar, disconnected rows without bars, and the light palette using the light warn/error colors. Verify with `go test ./internal/ui/views/`.
- [x] 4.3 Run `go test ./internal/ui/... -update`, review `git diff internal/ui`, and confirm that no existing golden changed. This covers the "100-column terminal" scenario. Then add `containers_stats_meters_130x10.golden` and a compose variant showing aligned bars. Also add a 100-column view test with one warn and one bad cell and a short MEM text (`52KiB / 1GiB`): its raw screen holds the warn and error escape sequences, its `ansi.Strip` has no bar, and its stripped lines equal the cells' plain text laid out as before ("Stats meters keep text unchanged"). Verify that `go test ./internal/ui/...` passes without `-update`.

## 5. Documentation

- [ ] 5.1 Update the stats section of `README.md`. Add that CPU and MEM cells turn the warning color at 70% and the error color at 90% of host CPU capacity or of the memory limit. Also add that wider terminals show a memory bar after the MEM text, and that the text itself is unchanged, so `NO_COLOR` users read the same values. Verify the thresholds match `usageTone`.

## 6. Integration checks

- [ ] 6.1 Run `go vet ./...` and `go test ./...` (including the integration tests in the environment CI uses) and verify everything passes.
- [ ] 6.2 Run drydock against a real engine. Start one container with a low memory limit filling memory, for example `docker run -d --name hog -m 64m python:3-alpine python -c "b=b'x'*(48*2**20); import time; time.sleep(1e9)"` (write real bytes: `bytearray(n)` is zero pages that are not resident), and one container busy on every core (`busybox sh -c 'for i in $(seq $(nproc)); do (while :; do :; done) & done; wait'`; one busy loop stays under 70% on a multi-core host). Confirm with `docker stats --no-stream` that `hog` is at 70% or more. With stats on, check these: at 100 columns nothing moved and `hog`'s MEM is colored; at 130+ columns the bars appear aligned and `hog`'s bar is in the error or warning color; the selected row shows the bar without color; and `NO_COLOR=1 drydock` shows the same text and bars uncolored.
- [ ] 6.3 Run `openspec validate add-container-stats-meters --strict` and verify it passes.

## Workflow follow-up

- Archive the change with `/opsx:archive` after review, which syncs the delta into `openspec/specs/container-management/spec.md`.
