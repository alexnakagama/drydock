# Tasks

## 1. Engine access: pause and unpause

- [x] 1.1 In `internal/docker/service.go`, add `ContainerPause(ctx, id string) error` and `ContainerUnpause(ctx, id string) error` to `docker.Service` (design.md D1). Implement them in `internal/docker/sdk.go`:
  - Validate the ID with `ValidateContainerID`, like the other container methods.
  - Return the SDK's errors unwrapped.

  Verify:
  - `go build ./...` passes.
  - A new case in `internal/docker/sdk_integration_test.go` (`-tags integration`) uses a labeled `alpine:latest` container running `sleep 300`. Pausing it makes inspect report `Paused: true`. Unpausing makes it report `Running: true, Paused: false`. Pausing an exited container returns a conflict error.
- [x] 1.2 In `internal/docker/dockerfake/fake.go`, add `MethodContainerPause` and `MethodContainerUnpause`. Mirror the engine's checks:
  - Pausing a container that is not running returns a conflict error.
  - Unpausing a container that is not paused returns a conflict error.
  - Unknown IDs return not-found.
  - While disconnected, both fail like the fake's other methods.

  On success, set the state (and `Status` text) and emit `pause`/`unpause` container events. Leave any open stats stream alone. The real engine is believed to keep it open (not verified), and the view closes it itself because paused containers are not stats targets.

  Verify with `internal/docker/dockerfake/fake_test.go` cases:
  - running -> pause -> paused -> unpause -> running, with the matching events.
  - The conflict errors for exited and running containers.
  - Not-found.
  - Disconnected.

## 2. Pause and unpause in the containers view

- [ ] 2.1 In `internal/ui/keys/keys.go`, add `Pause` (`p`, help `pause/unpause`) and `Prune` (`P`, help `prune stopped`) to the containers key map, and add both to its `Bindings()` list. Verify by updating `internal/ui/keys/keys_test.go`: the containers bindings include `p` and `P`, and no key conflicts with the global or table keys.
- [ ] 2.2 In `internal/ui/views/containers.go`, handle `p` (design.md D2):
  - running: `ContainerPause` through `ui.Track`/`st.Action`.
  - paused: `ContainerUnpause` the same way.
  - any other state: `ui.Error("pause/unpause is unavailable for a <state> container (<name>)")` without calling the engine.

  Append "; press p to unpause" to the `s` refusal for a paused container.

  Verify with unit tests in `internal/ui/views/containers_test.go` using `dockerfake`:
  - Pausing `web` shows success, and the row shows a paused status after the event-driven refresh.
  - Unpausing restores the running status.
  - `p` on an exited container records no `MethodContainerPause` call and shows the unavailable message.
  - A fake injected pause error is shown in the status area, and `web` stays running.
  - `s` on a paused container shows the new hint.
- [ ] 2.3 In `KeyHelp`, relabel `p` to `pause` on a running container and to `unpause` on a paused one, disable it otherwise, and place it right after `s`. Add `Prune` (always enabled) just before the stats hint, so `t` stays the first hint dropped (spec "Stats toggle", scenario "Narrow footer"). Update the existing exact-list expectations in `keyhelp_test.go`. Verify with `internal/ui/views/keyhelp_test.go`:
  - The footer shows `p pause` on a running row and `p unpause` on a paused row.
  - It shows no `p` hint on an exited row or an empty list.
  - Help lists `p` dimmed in that case.
  - Then run `go test ./internal/ui ./internal/ui/views -update` (only those two packages define the flag) and review the golden diffs: only footer lines may change.
- [ ] 2.4 Verify stats resume after unpausing with a test in `internal/ui/views/containers_stats_test.go`. With stats on, a paused `web` shows `-` and has no stats stream open (`StatsStreams()`). After unpausing and queueing samples with `SetStats`, its CPU cell shows a value without toggling `t`.

## 3. Shared one-by-one removal

- [ ] 3.1 Extract the loop and summary of `removeVolumes` in `internal/ui/views/volumes.go` into a generic `removal[T]` helper in a new `internal/ui/views/removal.go` (design.md D5). `removeVolumes` becomes a thin wrapper with unchanged signature and messages. Verify:
  - The existing volume prune tests pass unchanged: `go test -race ./internal/ui/views/`.
  - New `removal_test.go` cases with a stub `remove` cover all-removed, already-gone, failures beyond `maxListedFailures`, an interrupted item, and not-attempted items after cancellation.

## 4. Prune stopped containers

- [ ] 4.1 In `internal/ui/views/containers.go`, add `confirmPrune` (design.md D3, D4):
  - Candidates: snapshot containers passing `v.match` whose state is `exited`, `created` or `dead`. Ignore `v.all` and the table filter.
  - Sort them by name and list them as `name  state`.
  - End the body with "Their volumes are kept."
  - Before the first snapshot, report "prune: containers are still loading". With no candidates, report "nothing to prune: no stopped containers".

  On confirm, remove the candidates through the `removal` helper under `ui.Track("pruning stopped containers", st.ActionWith("prune stopped containers", 5*time.Minute, ...))`, using `ContainerRemove(ctx, id, {Force: false, RemoveVolumes: false})`. Map not-found to "already gone".

  Verify with unit tests in `internal/ui/views/containers_test.go`, one per spec scenario of "Prune stopped containers":
  - root scope
  - created and dead containers included
  - hidden stopped containers with `a` off
  - a `/` filter ignored
  - drill-down scope from an image
  - paused and restarting containers kept
  - volumes kept: no `RemoveVolumes`, and the fake still has the volume
  - a container started after the preview reported as failed, not removed
  - already gone
  - nothing to prune
  - still loading
- [ ] 4.2 Add a golden snapshot of the prune confirmation dialog with three stopped containers in `internal/ui/views/testdata/` (using the views package's `checkGolden`, rewritten with `go test ./internal/ui/views -update`; `internal/ui/testdata/confirm_80x24.golden` belongs to the root model's harness). Verify that `go test ./internal/ui/...` passes and the golden shows the title, the listed containers and "Their volumes are kept."
- [ ] 4.3 Add an integration test in `internal/ui/views/` (`-tags integration`):
  - Create two labeled `alpine:latest` containers: one exited, one running `sleep 300`.
  - Open a drill-down for the alpine image and prune.
  - Only the exited one is removed, and the running one is kept.

  Verify with `go test -tags integration ./internal/ui/views/`.

## 5. Documentation and final checks

- [ ] 5.1 Update `README.md`:
  - Add `p` pause/unpause and `P` prune stopped to the Containers row of the actions table.
  - Add a sentence after the footer paragraph explaining that `p` reads `pause` or `unpause` depending on the selected container.
  - Note in the delete/prune line that container prune keeps volumes and only covers the containers of the current view.

  Verify that the README's key table matches `keys.Containers.Bindings()`.
- [ ] 5.2 Run the full check suite from the README's Development section and verify every command passes with no output from `gofmt -l .`:
  - `go build ./...`
  - `gofmt -l .`
  - `go vet ./...`
  - `golangci-lint run ./...` and `golangci-lint run --build-tags integration ./...`
  - `go test -race ./...`
  - `go test -tags integration ./...`
- [ ] 5.3 Run `openspec validate add-container-pause-and-prune --strict` and verify it passes.

## Workflow follow-up

- Archive the change with `/opsx:archive` once it is implemented and reviewed, which syncs the delta into `openspec/specs/container-management/spec.md`.
