# Design

## Context

See proposal.md for the motivation. The containers view (`internal/ui/views/containers.go`) has two paths that write rows:

```
 SnapshotMsg   --> refresh()       filter + sort by name, ID --> v.list --> setRows()
 statsMsg      --> receiveStats()  store the batch in v.stats ------------> setRows()
 ConnectionMsg --> (conn changed, stats on) ------------------------------> setRows()
 SetActive     --> (prefs.Stats changed elsewhere) --> applyLayout() -----> setRows()
```

Today only `refresh()` orders rows, and a stats batch never changes the order. `setRows()` turns `v.list` into table rows keyed by container ID, and `Table.SetRows` keeps the selection on the same key (`refilter` → `Select`), scrolling so it stays visible. The session settings live in `views.Prefs`, a pointer shared by every containers view. A view applies a setting another view changed when it becomes active (`SetActive` compares `statsCols` with `prefs.Stats`).

`statsCells` already decides when a cell shows `-`: the container is not running, the view is not connected, there is no sample yet, or `HasCPU`/`HasMem` is false.

## Goals / Non-Goals

**Goals:**
- One place that decides row order, used by every path that writes rows, so the order can never fall out of step with the cells shown.
- Order rules that are exactly what the cells show: a row sorts as `-` exactly when its cell shows `-`.

**Non-Goals:**
- A generic sortable-column mechanism in `components.Table`. The order is a view concern, and other views keep sorting by name.
- Reducing reorder churn beyond comparing the shown CPU value (no hysteresis, and no freezing the order while the cursor moves).

## Decisions

### The sort order is a session preference that implies stats

`Prefs` gains `Sort sortOrder` (`sortName`, the zero value, `sortCPU`, `sortMem`). One invariant: `Sort != sortName` implies `Stats`. The only places that change either field are:

- `o` (`cycleSort`): it moves to the next order, and when the new order is not name and stats are off, it sets `prefs.Stats = true` and runs the existing stats path (`setColumns`, `syncStats`), so the subscription starts as it would for `t`.
- `t` (`toggleStats`): when it turns stats off, it also sets `Sort = sortName`.

Alternative considered: separate settings, where the sort is remembered while stats are off and comes back when they return. The user rejected it during exploration because it adds a hidden state that the header cannot show well.

### Sort inside `setRows`, not in `refresh`

`setRows()` sorts `v.list` before it builds the rows, using `slices.SortFunc` with a comparator for the order currently in effect. `refresh()` keeps its name/ID sort, which is the name order and also a deterministic starting point. Since every path in the Context diagram ends in `setRows()`, a stats batch, a connection change, a snapshot and a layout switch all re-sort without separate hooks.

The effective order is `prefs.Sort` when `v.statsCols` is set, and name otherwise. That way a view whose layout has not caught up yet (inactive while another view changed prefs) never sorts on columns it does not show.

Alternative considered: sort only in `receiveStats`. That would miss connection changes (every cell goes to `-`, so the order must fall back to name) and snapshots (a new container must land in its sorted place at once, not up to a second later).

Cost: sorting a few hundred rows once a second is negligible next to building and rendering them.

### Comparator

```
 key(c) = (hasValue, value)       hasValue: what statsCells would show for c
 CPU value = tenths parsed from formatCPU's number   (exactly what the cell shows)
 MEM value = MemUsage                      (bytes)

 compare(a, b):
   hasValue  : true before false
   value     : larger first          (only when both have a value)
   name      : ascending
   ID        : ascending             (names are unique, but this keeps it total)
```

A helper shared with `statsCells` decides `hasValue`, so the placeholder test exists in one place (running, connected, sample present, `HasCPU`/`HasMem`). Taking the value from the same one-decimal formatting as the cell (for example `strconv.FormatFloat(p, 'f', 1, 64)` parsed back to tenths) rather than `math.Round(p*10)` keeps the order identical to the cell for halves such as `0.25`, which `FormatFloat` shows as `0.2` but `math.Round` would turn into 3 tenths. Comparing those tenths makes `0.04%` and `0.0%` equal, as the user sees them, so idle containers stay in name order instead of swapping on noise. MEM compares raw bytes: memory moves slowly, and rounding to a display unit would make values that look different (`1.2GiB` and `1.3GiB`) compare as equal only by accident of the unit.

The comparator only needs the container summary and the stats map, so it can be a plain function that is unit-tested without a view.

### Applying a sort changed in another view

`containersView` records the order it last applied (`sortApplied`). `SetActive(true)` already calls `applyLayout()` when `statsCols != prefs.Stats`. It also calls `setRows()` when the layout matches but `sortApplied != prefs.Sort` (for example, a drill-down cycled CPU → MEM and the user pressed Esc). `setRows()` updates `sortApplied`.

### Header, footer and help

- `Indicators()` appends `sort cpu` or `sort mem` after `stats`, and before the `/filter` indicator, while the effective order is not name.
- `keys.Containers` gains `Sort: key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "sort cpu"))`. `KeyHelp()` copies it, relabels it with the next order (`sort mem`, `sort name`), and appends it after `stats`. The footer drops hints from the end, so `o` goes first and `t` next, which is what the narrow-footer scenario requires. The help overlay lists every binding, so `o` appears there without further work.

## Risks / Trade-offs

- [Rows move under the cursor once a second] → The selection follows the container (by row key), so an action always hits the container the user selected. Comparing shown CPU values keeps idle containers still. Freezing the order during navigation is left out of v1 on purpose.
- [Two busy containers with close CPU values swap often] → Accepted. This is what `docker stats`-style views do. Hysteresis can be added later without changing the spec's ordering rules for distinct shown values.
- [`o` turning stats on starts a stats subscription] → It uses the same path as `t`, so the "read only while shown" requirement and its tests still cover it.
- [Golden snapshots of the containers view change] → Only the footer line can change (the `o sort cpu` hint shows when it fits). Regenerate them and review the diff to confirm nothing outside the footer moved.
- [The order jumps once when the connection comes back] → While disconnected, every cell is `-`, so the rows are in name order, and the stats sort comes back with the first batch. This matches the spec's "Engine connection lost" scenario.
