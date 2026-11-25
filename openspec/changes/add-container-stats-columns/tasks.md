# Tasks

## 1. Engine access: stats streams

- [x] 1.1 In `internal/docker/service.go`, add `ContainerStats(ctx, id string) (StatsStream, error)` to `docker.Service`. Add a `StatsStream` interface shaped like `PullStream`, closed when ctx is cancelled (design.md D1):
  - `Samples(ctx) iter.Seq2[container.StatsResponse, error]`
  - an idempotent `Close() error`

  Implement it in `internal/docker/sdk.go`:
  - Validate the ID with `ValidateContainerID`, as `ContainerLogs` does.
  - Call `client.ContainerStats(..., ContainerStatsOptions{Stream: true})` and decode with a `json.Decoder` loop.
  - Return the SDK's errors unwrapped, like the other methods.

  Verify:
  - `go build ./...` passes.
  - A new case in `internal/docker/sdk_integration_test.go` (`-tags integration`): start an `alpine:latest` container running `sleep 300`, labeled like the other integration resources. Within 3 s it yields at least two samples. The first has zero `PreCPUStats.SystemUsage` and the second non-zero. Cancelling ctx ends the iteration.
- [x] 1.2 In `internal/docker/dockerfake/fake.go`, add:
  - `MethodContainerStats`.
  - `SetStats(containerID string, samples ...container.StatsResponse)`, which queues samples for the container's stream.
  - `StatsStreams() int`, which reports the stats streams currently open.

  The fake returns an error for unknown or non-running containers and while disconnected, like its other methods. Extend `Disconnect` to end open stats streams with an error; today it fails only event subscriptions. End a container's stats stream when the fake stops or removes the container, as the real engine does.

  Verify with `internal/docker/dockerfake/fake_test.go` cases:
  - Queued samples are yielded in order.
  - `StatsStreams()` goes up when a stream opens, and back to 0 after `Close` and after ctx cancel.
  - `Disconnect` ends open streams with an error.
  - Stopping the container ends its stream.

## 2. Store: stats math and sampler

- [ ] 2.1 In a new `internal/store/stats.go`, add `ContainerStats{CPUPercent float64; HasCPU bool; MemUsage, MemLimit uint64; HasMem bool}` and the pure functions from design.md D6:
  - `cpuPercent(container.StatsResponse) (float64, bool)`
  - `memUsage(container.StatsResponse) (usage, limit uint64, ok bool)`

  Verify with table-driven tests in `internal/store/stats_test.go`:
  - CPU: 1 core of 4 gives 100.0; 2 cores of 8 gives 200.0; idle gives 0.0.
  - CPU, no value: a first stream record (`precpu_stats.system_cpu_usage` 0, with non-zero current counters); `sysDelta` 0; a negative `cpuDelta`.
  - CPU: `online_cpus` 0 falls back to `len(percpu_usage)`.
  - Memory: cgroup v2 `inactive_file` and cgroup v1 `total_inactive_file` are each subtracted, with 900 MiB raw usage and 400 MiB inactive giving 500 MiB. An inactive value at or above usage is not subtracted. The limit is passed through unchanged, including a host-sized limit. Zero usage with empty stats gives no value.
- [ ] 2.2 Add `Store.SubscribeStats() *StatsSub` with `SetTargets(ids []string)`, `C() <-chan map[string]ContainerStats` and an idempotent `Close()`, as in design.md D2:
  - The subscription's context derives from `Run`'s context.
  - One goroutine per target runs under `Store.Do`.
  - A 500 ms flush loop puts a copy of the map on the channel only when it changed, replacing any batch not yet taken.
  - An ended stream is restarted while its ID is still targeted: 500 ms after `ErrNotConnected`, otherwise with a delay doubling from 1 s to 5 s. The sleep wakes on a wake channel that `SetTargets` signals, and `SetTargets` resets the delays.

  Add `type Option`, a variadic `opts ...Option` on `store.New`, and `WithStatsTimings(flush, notConnected, base, max time.Duration)`, all stored in `config`. Existing callers compile unchanged.

  Verify with `internal/store/stats_test.go` cases using `dockerfake` and short timings:
  - Targets `a,b` open 2 streams, and a batch carries both.
  - `SetTargets(a)` closes `b`'s stream and drops `b` from the next batch.
  - No batch is sent when nothing changed.
  - A stream ended by `FailNext` is reopened.
  - With the real `Run`: after `Disconnect`, `Reconnect`, and a `SetTargets` triggered by the next snapshot, the streams reopen and a batch arrives within 3 s with default restart timings scaled down by the same factor as the test's other timings.
  - A sampler asleep in a 5 s backoff restarts immediately on `SetTargets`.
  - `Close` brings `StatsStreams()` to 0, and later calls are no-ops.
  - Cancelling `Run`'s context also brings `StatsStreams()` to 0.
  - `go test -race ./internal/store/...` passes.
- [ ] 2.3 Add `BenchmarkStatsSampler200` in `internal/store/stats_test.go`: 200 fake targets, each yielding a sample per flush interval, reporting allocations per flush. Verify it runs with `go test -run '^$' -bench StatsSampler ./internal/store/`, and record the result in the change's PR description (design.md Risks).

## 3. Table: switching column sets

- [ ] 3.1 In `internal/ui/components/table.go`, add `SetColumns(cols []Column)` as in design.md D5:
  - It replaces the columns and clears the column-dependent row data (`all`, `cellWidth`, `lowered`, `visible`, `natural`).
  - It keeps the filter text, the selected key and the scroll offset.
  - Callers then call `SetRows` with rows of the new width.

  Verify with `table_test.go` cases:
  - 4 → 5 columns and 5 → 4 columns, each with rows set before: no panic.
  - After `SetRows`, the header shows the new titles and the same key stays selected.
  - An applied filter stays applied and still matches on the filter columns of the new set.
  - Every line is exactly the table width.
  - `View()` between `SetColumns` and `SetRows` renders without panicking.

## 4. Root model: show/hide signal

- [ ] 4.1 In `internal/ui/view.go`, add the optional `Activatable` interface (design.md D3). In `internal/ui/model.go`, call `SetActive` as follows, and tag every returned command like other view commands:
  - `SetActive(true)` on the root from `Model.Init`, after its `Init`.
  - `SetActive(false)` on the old top before `push`, and `SetActive(true)` on the pushed view after its `Init`.
  - `SetActive(true)` on the new root after `replace`.
  - Change `pop()` to return a `tea.Cmd` carrying `SetActive(true)` for the revealed view, and return it from the Esc branch of `handleKey`.

  Overlays do not change activity.

  Verify with `model_test.go` cases using a recording test view:
  - `Init` activates the root view.
  - Pushing deactivates the root and activates the pushed view.
  - Esc pops the pushed view and reactivates the root.
  - Switching tabs activates the new root, and the old root gets `Close` and no later `SetActive`.
  - Opening help or a confirmation sends no `SetActive`.

## 5. Containers view: toggle, columns, live cells

- [ ] 5.1 Add the toggle and the session-wide setting (design.md D4, D8):
  - Add `Stats` (`t`, help `stats`) to `keys.Containers` in `internal/ui/keys/keys.go`, and include it in `ContainersMap.Bindings()`.
  - Add `views.Prefs{Stats bool}`, created once in `views.Resources` and passed to `NewContainers`, `NewImages`, `NewVolumes`, and on to `NewFilteredContainers`.
  - Update the constructors' callers and the test harnesses.

  Verify:
  - `keys_test.go`: no key clash in the containers map.
  - A `views` test: a zero `Prefs` means stats off, so a fresh `Resources` starts with stats off, as after a restart.
  - `go test ./internal/ui/...` passes after updating the `keyhelp_test.go` expectations to include `t` last.
- [ ] 5.2 In `internal/ui/views/containers.go`, add the stats layout and cells (design.md D5, D6, D7, D8):
  - Add `statsColumns`, and `formatCPU` and `formatMem` (`units.BytesSize(float64(...))`).
  - Render CPU and MEM cells from the latest batch for this view, with `-` for containers whose state is not `running`, for missing values, and while the view's last known connection state (initialized from `st.State()`, updated from `ConnectionMsg`) is not connected. Discard batches that arrive while not connected.
  - On `t`, flip `Prefs.Stats`, call `SetColumns`, and rebuild rows.
  - Add the `stats` header indicator, and put the `t` hint last in `KeyHelp`, labeled `stats` or `ports`.

  Verify with `containers_test.go` cases:
  - Formatting: `formatMem(512.3 MiB, 2 GiB)` gives `512.3MiB / 2GiB`, and 500 MiB gives `500MiB`.
  - `t` swaps PORTS for CPU and MEM and back. The indicator and the footer label follow the toggle.
  - A `statsMsg` for another view ID is ignored.
  - In all mode, a stopped container and a paused container show `-` in both cells.
  - A sample without memory shows `-` in MEM and a value in CPU.
  - A disconnected `ConnectionMsg` turns every stats cell into `-`, and a batch received after it still leaves `-`.
  - A view created while the store is disconnected shows `-`.
  - The selected container stays selected across a batch.
  - Help lists `t`.
- [ ] 5.3 Implement `SetActive` on the containers view (design.md D3):
  - Hold a `StatsSub` only while active and `Prefs.Stats` is on.
  - Receive batches with a `wait()` command like `logsView`.
  - Call `SetTargets` with the IDs of listed containers whose state is `running`, before filtering, after each applied snapshot and on toggle.
  - Close the subscription on deactivation, on `t` off and in `Close`. Ignore `SetActive` after `Close`.

  Verify with `containers_test.go` cases against `dockerfake` with short timings (`store.WithStatsTimings`):
  - Stats off: `StatsStreams()` is 0.
  - `t` on: one stream per running listed container, and none for a paused one.
  - A drill-down listing 2 of 12 running containers opens 2 streams.
  - A filter hiding a running container keeps its stream.
  - `t` off, deactivation and `Close` each bring the count back to 0.
  - A container started via a snapshot gains a stream and shows values in a later batch. A stopped one loses its stream.
  - A batch with changed values updates the cells without user input.
- [ ] 5.4 Add golden snapshots in `internal/ui/views/testdata`:
  - `containers_stats_100x10.golden`: values present, one stopped container in all mode.
  - `containers_stats_pending_100x10.golden`: before the first sample.

  Regenerate the existing goldens affected by the `t` hint.

  Verify:
  - Run `go test ./internal/ui/... -update` once, read each new file, and confirm that no CPU or MEM cell is cut at 100 columns.
  - In every changed existing golden, check with `git diff` that only the footer line changed.
  - `go test ./internal/ui/...` passes without `-update`.
- [ ] 5.5 In `internal/ui/views/root_test.go`, cover the session-wide setting end to end on the root model, using short stats timings. Verify with `go test -race ./internal/ui/...`:
  - Stats are off at startup and no stream is open.
  - With `t` on, `Enter` on an image opens a drill-down that shows CPU and MEM.
  - Switching to images closes all stats streams, and switching back reopens them.
  - `:containers` reopens with stats on.
  - Pushing logs closes the streams, and `Esc` reopens them.

## 6. Documentation and integration

- [ ] 6.1 In `README.md`:
  - Add `t` stats/ports to the Containers row of the actions table.
  - Add a short paragraph under "Using drydock": stats are off by default; they match `docker stats` (CPU summed across cores, memory excluding inactive page cache); `-` means no value is available, including for paused containers; they are read only while shown; and the `t` hint is the first footer hint dropped on narrow terminals.

  Verify by reading the rendered section and checking that every key in the table matches `keys.Containers`.
- [ ] 6.2 Add `internal/ui/views/stats_integration_test.go` (`-tags integration`). It starts an `alpine:latest` container running a CPU-busy loop, labeled like the other integration resources, and removes it afterwards. With stats on against the real engine, it checks within 5 s that:
  - the container's CPU cell shows a value above `0.0%`
  - its MEM cell matches `^[0-9.]+[KMGT]?i?B / [0-9.]+[KMGT]?i?B$`

  Verify with `go test -tags integration ./internal/ui/views/ -run Stats`.
- [ ] 6.3 Run the full check list from the README's Development section and confirm everything passes:
  - `go build ./...`
  - `gofmt -l .`
  - `go vet ./...`
  - `golangci-lint run --build-tags integration ./...`
  - `go test -race ./...`
  - `go test -tags integration ./...`

  Then validate the change with `openspec validate add-container-stats-columns --strict`.

## Workflow follow-up

- Archive the change with `/opsx:archive` after review and merge. Archiving syncs the `container-management` delta into the main spec.
- Propose the follow-up sorting change (sort by CPU or MEM), building on `Table.SetColumns` and the stats map.
