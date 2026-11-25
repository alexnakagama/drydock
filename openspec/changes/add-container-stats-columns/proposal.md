# Proposal

## Why

drydock shows what state each container is in, but not what it is using. To find the container that is eating CPU or memory, users leave drydock and run `docker stats` in another terminal. A glanceable CPU and memory readout in the containers view keeps that check inside drydock, without changing the default view for users who do not want it.

## What Changes

- **Stats toggle**: in a containers view, `t` switches the PORTS column out for two live columns, CPU and MEM, and back again. Stats are off by default.
- **CPU column**: the container's CPU usage computed the way `docker stats` computes it: summed across cores, so a container using two full cores shows `200.0%`.
- **MEM column**: the container's memory usage and limit, for example `512.3MiB / 2GiB`, with page cache excluded the way `docker stats` excludes it, so the numbers match.
- **Live updates**: values refresh about once a second while stats are shown. Containers that are not running, containers with no sample yet, and containers the engine reports no data for show `-`. While drydock is disconnected from the engine, every stats cell shows `-` until the connection is back.
- **Session-wide toggle**: the stats setting applies to every containers view, including drill-downs from an image or a volume, for the rest of the session. It is not saved between runs.
- **Header and footer**: the header indicates `stats` while stats are on, and the footer hint for `t` names what pressing it would show (`t stats` or `t ports`).
- **No sampling while hidden**: drydock reads stats from the engine only while a containers view with stats on is the current view, so stats cost nothing when they are off or when another view is shown.

Out of scope: sorting by CPU or memory (planned as a separate change), saving the toggle between runs, per-container history or sparklines, and network or block I/O columns.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `container-management`: the "Container list" requirement changes so that PORTS is shown only while stats are off. A new "Live resource stats" requirement covers the `t` toggle, the CPU and MEM columns and their formulas, placeholders, the session-wide setting, the header indicator, and sampling only while stats are shown.

## Impact

- `internal/docker/service.go`, `internal/docker/sdk.go`: a new `ContainerStats` streaming method on `docker.Service`, wrapping the SDK's `ContainerStats` with `Stream: true`.
- `internal/docker/dockerfake/fake.go`: a scripted stats stream per container for tests.
- `internal/store`: a stats sampler that runs one stream per listed running container while it has a subscriber, reconciles its streams with each snapshot, computes CPU% and memory usage, and hands batched samples to the subscribing view. A store option lets tests shorten its timings.
- `internal/ui/view.go`, `internal/ui/model.go`: an optional interface that tells a view when it becomes, or stops being, the view on screen.
- `internal/ui/components/table.go`: switching a table's column set while keeping its rows, filter and selection.
- `internal/ui/keys/keys.go`: a `Stats` binding (`t`) in the containers key map and its help list.
- `internal/ui/views/containers.go`, `images.go`, `volumes.go`, `resources.go`: the stats column layout, `t` handling, the `stats` indicator, the footer label, starting and stopping the subscription when the view is shown or hidden, and the session-wide setting passed to every containers view, drill-downs included.
- `README.md`: the `t` key and a short description of the stats columns.
- Tests: new unit tests for the CPU and memory formulas and formatting, the sampler lifecycle (no goroutine or stream leaks) and the view; new golden snapshots for the stats layout. In the existing golden snapshots, only footer lines may change, because the `t stats` hint is shown while stats are off. Key-help expectations gain `t`.
- No new dependencies: the stats types come from `github.com/moby/moby/api`, which is already a dependency.
