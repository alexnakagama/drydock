# Design

## Context

See proposal.md for motivation and specs/container-management/spec.md for the required behavior. The current code shapes the approach in five ways:

- **The engine has no bulk stats endpoint.** `GET /containers/{id}/stats` serves one container. With `stream=true` it keeps the connection open and sends a sample about once a second. From the second record on, each record carries the previous sample's CPU counters in `precpu_stats`; the first record's `precpu_stats` is empty (SDK doc on `ContainerStatsOptions`). `docker stats` opens one such stream per container.
- **`store.Snapshot` is event-driven and immutable.** A new snapshot is published only after debounced engine events. Every view on the stack receives it and rebuilds its rows. Pushing stats through snapshots would wake every view and rebuild the image and volume indexes every second.
- **The store's `send` is single-goroutine.** `store.New` documents that `send` is called only from the goroutine running `Run`, in order. A second sender would break that contract.
- **Views have no "shown/hidden" signal.** `ui.View` has `Init` (pushed) and `Close` (left the stack). A view under a pushed logs view still receives broadcast messages but is not on screen, and nothing tells it so.
- **Views already own streams.** `logsView` runs a goroutine under `store.Do`, batches its output onto a channel, and receives the batches through a `wait()` command that blocks on that channel or on the view's context. `Close` cancels the context. That pattern keeps the bootstrap design's "no goroutine or stream leaks" goal, and it fits this change.

## Goals / Non-Goals

**Goals:**
- One stream per listed running container, and only while stats are visible.
- At most two UI messages per second for stats, whatever the number of containers.
- CPU and memory math and formatting as pure functions, unit-tested against `docker stats` numbers.
- The table and sampler shaped so a later sorting change can sort by CPU or MEM without redoing them.

**Non-Goals:**
- A cap on the number of concurrent streams (see Risks).
- Any change to the default column layout. In existing golden snapshots only the footer line may change (D8).

## Decisions

### D1. `docker.Service.ContainerStats` returns a decoded stream

Add `ContainerStats(ctx, id) (StatsStream, error)`, where `StatsStream` follows `PullStream`: `Samples(ctx) iter.Seq2[container.StatsResponse, error]` plus an idempotent `Close()`. The SDK wrapper validates the ID with `ValidateContainerID` (as `ContainerLogs` does), calls `client.ContainerStats` with `Stream: true`, and decodes the JSON sequence. It returns the SDK's errors unwrapped, like the other methods. The stream closes when ctx is cancelled.

- *Alternative: return the raw `io.ReadCloser`* (as `ContainerLogs` does). Logs need raw bytes for demultiplexing. Stats are a JSON sequence, and decoding them in the wrapper keeps JSON out of the store and lets `dockerfake` script samples as typed values.
- *Alternative: one-shot polling* (`stream=false`, `IncludePreviousSample`). The engine blocks about one second per call to collect the second sample, so a polling loop costs a request per container per second and still needs one goroutine per container. Streaming is what `docker stats` does and gives the same cadence for free.

### D2. A stats sampler in `store`, consumed like the logs stream

`store.Store` gains `SubscribeStats() *StatsSub`. A subscription has:

- `SetTargets(ids []string)`: the IDs of the containers the view lists whose state is `running`, whether or not a filter hides them. The sampler starts a stream for each new ID and cancels streams for IDs that left.
- `C() <-chan map[string]ContainerStats`: batches of the latest values.
- `Close()`: cancels every stream and the flush loop, and is idempotent.

The subscription's context derives from the store's action base (`Run`'s context), so streams also end when the program shuts down. Each stream goroutine runs under `store.Do`, decodes samples, computes a `store.ContainerStats{CPUPercent, MemUsage, MemLimit, HasCPU, HasMem}`, and writes it to a map under a mutex. A flush loop ticks every 500 ms. When the map changed since the last flush, it puts a copy on the channel, replacing any batch not yet taken, so the view always gets the latest and never a backlog. With samples arriving about every second, the worst-case latency is about 1.5 s, which is within the spec's 2 s.

The view receives batches through a `wait()` command, exactly as `logsView` does, and wraps each one in a `statsMsg{view, stats}` tagged with its own view ID. `send` is not used, so the store's single-goroutine contract stands.

**Restarts.** A stream that ends while its ID is still targeted is restarted after a delay. The delay is 500 ms when the cause is `ErrNotConnected`, and doubles from 1 s up to 5 s for other errors. The sleep selects on the delay, the subscription's context, and a per-subscription wake channel. `SetTargets` signals the wake channel and resets the delays, so a sampler asleep for an outage restarts right away.

After a reconnect, `session` publishes the fresh snapshot just before it marks the store connected. A `SetTargets` call triggered by that snapshot can therefore meet `ErrNotConnected` and retry 500 ms later. That keeps recovery well inside the spec's 3 s without reordering `session`.

**Timings.** The flush interval and restart delays live in the store's `config`. Tests outside the package shorten them through an exported option: `store.New(svc, send, opts ...Option)` with `WithStatsTimings(flush, notConnected, base, max time.Duration)`. Existing callers are unaffected.

- *Alternative: the view owns the goroutines directly* (like `logsView`). Image and volume drill-downs would then each carry the sampler code. Putting it in `store` keeps engine access, `Do` and backoff in one package, and the view only decides when sampling runs.
- *Alternative: deliver through the store's `send`.* That would need changing `send`'s documented contract to concurrent use. `tea.Program.Send` allows it, but test doubles may not, and the logs pattern already solves delivery.
- *Alternative: the store samples all running containers whenever stats are on, independent of views.* This is simpler to wire, but it keeps streams open while the user is on images or logs. The spec forbids that.

### D3. Show/hide signal: an optional `ui.Activatable` interface

Add to `internal/ui`:

```go
// Activatable is implemented by views that need to know whether they are
// on screen (the top of the stack).
type Activatable interface {
    SetActive(active bool) tea.Cmd
}
```

The root model calls:

- `SetActive(true)` on the root view from `Model.Init`, after its `Init`.
- `SetActive(false)` on the old top before a push covers it, and `SetActive(true)` on the pushed view after its `Init`.
- `SetActive(true)` on the new root after `replace`. The old views are only `Close`d.
- `SetActive(true)` on the view revealed by `pop`. `pop` changes to return a `tea.Cmd`, and the Esc branch in `handleKey` returns it.

`Close` still means "gone for good" and implies inactive. The containers view ignores `SetActive` after `Close`, as a defensive guard. Overlays (help, confirmation, prompt, command bar) do not change activity, because the list stays on screen behind them.

The containers view holds a subscription only while `active && Prefs.Stats`. It opens one when both become true and closes it when either becomes false. A `statsMsg` that arrives for a closed subscription is dropped, because its `wait()` returns nil once the view's subscription context is cancelled.

- *Alternative: check `fromTop` inside the view.* Views do not know the stack, and keeping them unaware of it is a design goal.
- *Alternative: add `SetActive` to `ui.View`.* That forces a no-op on every view and every test double. An optional interface detected with a type assertion matches how `Positioner` is already done.

### D4. Session-wide toggle: a shared `*views.Prefs`

`views.Resources` creates one `prefs := &Prefs{}` and passes it to `NewContainers`. The images and volumes views receive it too, and pass it on to `NewFilteredContainers`. `Prefs.Stats` is a plain bool. It is touched only on the UI goroutine, so it needs no locking. Its zero value is "stats off", which is what makes stats off at startup and unsaved between runs.

- *Alternative: a field on `store.Store`.* The store is engine state, and a display preference does not belong there.
- *Alternative: a package-level variable.* It would leak between tests, which build many views in one process.

### D5. Two column sets and `Table.SetColumns`

`containerColumns` stays as it is: `NAME(20,flex) IMAGE(24,flex) STATUS(24) PORTS(24,flex)`, which is 92 cells plus 3 gaps of 2, or 98. `statsColumns` is `NAME(20,flex) IMAGE(20,flex) STATUS(24) CPU(7) MEM(19,flex)`, which is 90 cells plus 4 gaps, also 98. Both fit a 100-column terminal after the 2-cell gutter. MEM is the last flex column, so it receives the width left over. Values wider than 19 cells, such as `1023.9MiB / 15.52GiB`, fit whenever the terminal has the room, and are cut like any other cell when it does not.

Add `Table.SetColumns(cols)`. It replaces the columns and clears the row data that depends on them (`all`, `cellWidth`, `lowered`, `visible`, `natural`), so it never reads per-row data sized for the old column count. It keeps the filter text, the selected key and the scroll offset, and the caller must call `SetRows` with rows of the new width before the next render. `SetRows` already restores the selection by `Row.Key` and re-applies the filter. Toggling calls `SetColumns` and then rebuilds rows from the snapshot plus the latest stats.

Rows keep `Key = container ID`, so `SetRows` also preserves the selection when a stats refresh rebuilds rows (spec: "Selection kept").

- *Alternative: two `Table` instances.* Filter text and selection would then need syncing between them.
- *Alternative: `SetColumns` recomputes widths from the retained rows.* The retained rows have the old cell count, so their widths are wrong, and `refilter` would index past the new columns. Clearing them is simpler and safe.

### D6. Formulas and formatting, matching `docker stats`

Pure functions in `store`, with table-driven tests:

- **CPU%**: if `precpu_stats.system_cpu_usage == 0` (the first record of a stream), there is no value. Otherwise `cpuDelta = cpu.total_usage - precpu.total_usage`, `sysDelta = cpu.system_cpu_usage - precpu.system_cpu_usage`, and `online = cpu.online_cpus`, falling back to `len(cpu.percpu_usage)`. When `sysDelta > 0 && cpuDelta >= 0` the value is `cpuDelta / sysDelta * online * 100`; otherwise there is no value. The explicit first-record guard is where drydock deliberately differs from the CLI, which shows a bogus cumulative figure for that record. The spec asks for `-` instead.
- **Memory**: `usage = memory_stats.usage`. Subtract `stats["total_inactive_file"]` when that key is present (cgroup v1), or else `stats["inactive_file"]` (cgroup v2), each only when it is less than `usage`. This matches the current CLI. If `usage` is 0 and `stats` is empty, there is no value. The limit is `memory_stats.limit` as reported. With no limit set, the engine reports host memory there, as `docker stats` shows.
- **Formatting** (`formatCPU`, `formatMem` in `views`): CPU is `%.1f%%`. MEM is `units.BytesSize(float64(usage)) + " / " + units.BytesSize(float64(limit))`, using `github.com/docker/go-units`, which the images view already imports. `BytesSize` takes a `float64` and produces `512.3MiB / 2GiB`, which is exactly the CLI's output.

### D7. Disconnected display

The containers view keeps the last connection state it received (`store.ConnectionMsg`), starting from `st.State()` when the view is created. While that state is not connected, it renders every stats cell as `-` and discards incoming batches. Streams may still deliver for a moment after the state flips, because `Store.Do` checks the state only when a call starts, and the event stream can fail while the engine still serves stats. Discarding is what keeps the cells blank. The view keeps its subscription, so recovery needs only the next snapshot (D2).

### D8. The `t` hint goes last

`KeyHelp` appends `t` after `a`, and `ContainersMap.Bindings()` includes `Stats` so help lists it. The footer drops view hints from the end until they fit, so at the 100-column golden width `t` is the first to go and the existing hints keep their place. The empty-list golden shows all global hints, and the `keyhelp_test.go` expectations list every binding, so those change. The rule for existing goldens is: only footer lines may change, and each diff is read before it is accepted.

## Risks / Trade-offs

- [Many running containers means many streams: 100 containers is 100 HTTP connections and about 100 JSON decodes per second] → This is what `docker stats` does. The cost exists only while stats are visible, and UI messages stay at no more than 2 per second. Measure with the fake at 200 targets in a benchmark, and revisit with a cap only if it shows a problem.
- [Goroutine or stream leaks across toggles, tab switches and pushes] → Every stream runs under the subscription's context, which derives from `Run`'s. Tests assert that the fake's open stats streams return to 0 after `t` off, after switching tabs, after pushing logs, and after quitting, mirroring the existing `Subscribers()` checks.
- [Stats from cgroup v1 hosts or rootless setups can be partial] → Missing values render `-` per field (spec "Memory not reported"). The formulas are tested with v1 and v2 sample fixtures.
- [Rows rebuilt up to twice a second could cost CPU on huge lists] → Rebuilding is already what a snapshot does. Only the current view rebuilds, and only when it has a new batch.
- [Adding `Activatable` touches the root model's stack code, and `pop` gains a return value] → The change is additive for views. Views without it behave as before. Model tests cover init, push, pop and replace transitions.
- [A footer hint hidden at 100 columns makes `t` less discoverable] → Help always lists it, and wider terminals show it. Moving it earlier would push existing hints out instead.

## Migration Plan

No migration is needed. Stats are off by default, the default column layout is unchanged, and nothing is persisted. Existing golden snapshots change only in footer lines (D8). Rollback is a revert.
