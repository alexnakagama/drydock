# Tasks

## 1. Foundations (table tones and position, snapshot counts)

- [x] 1.1 Add `Tone` (`ToneDefault`, `ToneGood`, `ToneWarn`, `ToneDim`) to `components.Row`, and `RowGood`/`RowWarn`/`RowDim` to `StyleSet` in `styles.go` (D5). Verify with `go build ./...`.
- [x] 1.2 Render unselected rows with their tone's style in `Table.View`. Render the selected row from the untoned line with `TableSelected` only. Verify with `table_test.go` cases that compare against `components.Styles.*.Render(...)` output: an unselected dimmed row equals `RowDim.Render(line)`, and the same row when selected equals `TableSelected.Render(line)`.
- [x] 1.3 Add `Table.Position() (selected, listed, total int, filtered bool)` (D4). Verify with `table_test.go` cases:
  - unfiltered `3,12,12,false`
  - filtered `1,5,12,true`
  - filter matching nothing `0,0,12,true`
  - filter matching all rows `1,12,12,true`
  - empty without a filter `0,0,0,false`
  - after moving the selection
- [x] 1.4 Add `NumContainers()`, `NumImages()` and `NumVolumes()` to `store.Snapshot`. Verify with a `snapshot` test that they equal the `len` of the cloning accessors, and are 0 on the zero `Snapshot`.

## 2. Views: tones, position, context-aware key help

- [x] 2.1 Set row tones in `refresh()` for each view. Verify with view tests that assert each row's `Tone` field for every listed state, including `removing` giving the default tone.
  - Containers: `running` is good; `paused` and `restarting` are warn; `exited`, `created` and `dead` are dim; any other state is default.
  - Images: dangling is dim.
  - Volumes: `VolumeUseCount == 0` is dim.
- [x] 2.2 Add the `ui.Positioner` interface in `internal/ui/view.go`, and implement it on the three list views by delegating to the table. Verify with compile-time assertions (`var _ ui.Positioner = (*containersView)(nil)`, etc.) and `go build ./...`.
- [x] 2.3 Rewrite `KeyHelp()` in the containers, images and volumes views to return copies of the bindings in priority order, with inapplicable ones disabled and `s` labeled per state (D6). Verify with view tests for:
  - a running container: `x` enabled, `s stop`
  - an exited container: `x` disabled, `s start`
  - a created container: `s start`
  - a paused container: `s` and `x` disabled
  - an unused image: `enter` disabled
  - an unused volume: `enter` disabled
  - an empty list: only the non-row actions enabled
- [x] 2.4 Add regression tests:
  - After `KeyHelp()` has been called with an exited container selected, pressing `x` still shows the "must be running" error.
  - Pressing `s` on a paused container still shows the "start/stop is unavailable" error.
  - The package-level `keys.Containers` bindings are still enabled.
  
  Verify with `go test ./internal/ui/views/`.

## 3. Root model: tab strip, tab keys, footer, help, position

- [ ] 3.1 Add `Count func(store.Snapshot) int` to `ui.Resource`, and set it in `views/resources.go` using the 1.4 helpers. Verify with `go build ./...`.
- [ ] 3.2 Add the matching-only bindings `Tab1/Tab2/Tab3/NextTab/PrevTab` and the display-only `SwitchHelp` (`1-3/tab`, `switch resource`) to `keys.Global`, with only `SwitchHelp` in `Bindings()` (D3). Refactor `lookup` into `lookupIdx`. Add `rootIdx`, set in `New`, and a `switchTo(i)` helper that `runCommand` and the new keys share; the tab keys are a no-op on the active root (D2). Verify with `model_test.go` cases:
  - `New` with the alias `im` initial sets the images tab active
  - `2` switches to images
  - `Tab` from volumes wraps to containers
  - `Shift+Tab` from containers goes to volumes
  - a digit from a drill-down replaces the stack
  - `1` on a containers root with a filter applied keeps the filter
  - `:containers` on the containers root still replaces it
  - `/` followed by `2` types into the filter
  - the switching keys are ignored while the help overlay or a prompt is open
  - in the logs view, typing `3` during a search goes into the search text
- [ ] 3.3 Render `tabsView()` below the header when `m.height >= 5`, with `contentSize()` following the same `showTabs()` rule (D1). The strip shows numbered tabs with counts (omitted before the first load), highlights `rootIdx`, and falls back to the first alias or the name. The `Positioner` text is right-aligned, takes priority, and reads `3/12`, `1/5 of 12` or `0/0` (D4). Verify with `model_test.go` render assertions that compare against the `Styles.*.Render` output:
  - counts, and the count independent of the running-only mode
  - the highlight after a drill-down
  - the narrow alias fallback
  - the position fully visible when tabs are clipped
  - the position text variants
  - at height 4, no strip, with header, status and footer all present
  
  Update existing tests that assumed 3 chrome lines.
- [ ] 3.4 Rewrite `footerView` to fit whole hint segments: show everything when it fits; otherwise show only view hints followed by `? more`, without the `:`, `?` or `q` hints (D7). Verify with `model_test.go` cases:
  - a wide containers view shows every hint plus `: command  ? help  q quit`
  - a 60-column containers view ends with `? more`, has no `q quit`, and no `…`
  - a 60-column logs view ends with `? more`
- [ ] 3.5 In the help overlay, render disabled bindings dimmed, widen the key column to 10, and drop the blank line before the close hint (D8). Verify with `model_test.go` cases:
  - with a stopped container selected, `?` lists `x` rendered with the muted style
  - `1-3/tab` appears in full
  - at 80x24 in the containers view, the overlay's bottom border and `? or esc to close` are both visible

## 4. Docs and integration

- [ ] 4.1 Update `README.md`:
  - Add `1-3`/`Tab`/`Shift+Tab` (switch resource) to the global keys table.
  - Mention the tab strip and the position indicator.
  - Note that the footer shows only applicable keys, and that `?` lists them all.
  
  Verify by checking the tables against `keys.go`.
- [ ] 4.2 Run `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...`, and verify all pass.
- [ ] 4.3 Launch `go run ./cmd/drydock` against a local engine at 80x24 and at 140 columns. Verify by eye:
  - tab switching, and `1` at the root being a no-op
  - counts updating when a container is created
  - row colors, and the selection on a dimmed row
  - the footer `? more`
  - the position after filtering
  - the help overlay fitting the screen

## Workflow follow-up

- Archive the change with `/opsx:archive` after review and merge.
