# Proposal

## Why

With stats on, the containers view shows each container's CPU and memory, but the rows stay sorted by name, so finding the container using the most CPU or memory means scanning every row. Sorting by CPU or MEM puts the biggest consumers at the top. The stats-columns change left this out on purpose and planned it as a separate change.

## What Changes

- **Sort cycle key**: in a containers view, `o` cycles the row order through name → CPU → MEM → name. The footer hint names the order the next press would pick (`o sort cpu`, `o sort mem`, `o sort name`).
- **Sorting turns stats on**: choosing the CPU or MEM order while stats are off turns stats on. Turning stats off with `t` brings back the name order. Cycling back to name leaves stats on.
- **Order rules**: CPU and MEM sort descending (largest first). CPU sorts on the value shown, rounded to one decimal, and MEM on memory usage. Ties fall back to name order. Rows whose cell shows `-` (not running, no sample yet, value not reported, or engine disconnected) always come last, in name order. There is no reverse order.
- **Live re-sorting**: while sorted by CPU or MEM, the rows re-sort each time the stats refresh (about once per second). The selected container stays selected as it moves.
- **Header indicator**: while sorted by CPU or MEM, the header shows `sort cpu` or `sort mem` after `stats`. The name order shows no indicator.
- **Session-wide setting**: like stats, the sort order applies to every containers view (the root view, a view reopened with the command bar, and drill-downs) for the rest of the session, and is not saved between runs.
- **Footer and help**: the help overlay lists `o`. When the footer is too narrow, the `o` hint is dropped first, before `t`.

Out of scope: a reverse (ascending) order, sorting by other columns (image, status, ports), sorting in the images, volumes, networks or projects views, sorting by memory as a percentage of the limit, keeping rows still while the user navigates, and saving the sort between runs.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `container-management`: "Container list" changes from always sorting by name to sorting by name unless a stats order is chosen. "Stats toggle" changes so that turning stats off brings back the name order, and its narrow-footer scenario now has `o` dropped before `t`. New requirements cover the `o` sort cycle (including turning stats on), the CPU and MEM order rules and placeholder placement, re-sorting on refresh while keeping the selection, the `sort cpu`/`sort mem` header indicator, and the session-wide sort setting.

## Impact

- `internal/ui/keys/keys.go`: a `Sort` binding (`o`) in the containers key map and its help list.
- `internal/ui/views/resources.go`: a sort order field in `Prefs`, shared by every containers view.
- `internal/ui/views/containers.go`: the sort comparator (name, CPU, MEM with placeholders last), sorting again when a stats batch arrives, `o` handling that also turns stats on, resetting to name when `t` turns stats off, the `sort cpu`/`sort mem` indicator, and the `o` footer hint as the view's last hint.
- `README.md`: the `o` key in the key table and a sentence on sorting in the stats section.
- Tests: unit tests for the comparator (rounding ties, placeholders last, disconnected), the `o`/`t` interactions, re-sorting on a stats batch while the selection is kept, and the shared setting across drill-downs; key-help expectations gain `o`. Existing golden snapshots of the containers view may change in their footer line only, because the `o sort cpu` hint now shows when it fits.
- No new dependencies, and no changes to the store or the engine layer: the view already has the stats values it sorts on.
