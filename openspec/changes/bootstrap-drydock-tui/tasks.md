# Tasks

## 1. Project setup

- [x] 1.1 Initialize the Go module (`go mod init`), create the package layout from design D3 (`cmd/drydock`, `internal/docker`, `internal/store`, `internal/ui`, `internal/ui/views`, `internal/ui/components`, `internal/ui/keys`), and verify `go build ./...` succeeds
- [x] 1.2 Add dependencies `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`, and `github.com/moby/moby/client` (pinned v0.6.x), and verify `go mod tidy` and `go build ./...` succeed
- [x] 1.3 Add a README section with build/run instructions and runtime requirements (engine socket access, `docker` CLI for exec), and verify `go run ./cmd/drydock` as documented starts the program

## 2. Docker service layer

- [x] 2.1 Define the `docker.Service` interface (ping, list/inspect for containers/images/volumes, container start/stop/restart/remove, logs, image pull/remove/prune, volume create/inspect/remove, exec create/start/inspect for shell probing, events), and verify it compiles with both implementations below
- [x] 2.2 Implement the SDK-backed service using `client.New(client.FromEnv)`, and verify an `integration`-tagged test lists containers against the local engine and that `cli.ClientVersion()` equals the engine's API version after `Ping` (`go test -tags integration ./internal/docker/...`)
- [x] 2.3 Implement an in-memory fake service with scriptable events and errors, and verify its own unit tests pass
- [x] 2.4 Implement startup connection check in `main` (5s ping before entering the TUI, error mapping for refused/not-found and permission denied, exit status 1), and verify by running with `DOCKER_HOST=unix:///nonexistent.sock` that the documented message is printed and the exit code is 1

## 3. Store and live updates

- [x] 3.1 Implement the store's initial load of containers (all), images, and volumes into immutable snapshots delivered as `tea.Msg`, and verify with a fake-backed unit test
- [x] 3.2 Compute derived indexes (`imageID -> containers`, `volumeName -> containers`) per snapshot, and verify unit tests for IN USE counts including stopped containers and multi-tag images
- [x] 3.3 Subscribe to engine events with per-kind 150ms debounce and re-list of the affected kind, and verify unit tests that a burst of events yields one re-list and that container events refresh usage counts
- [x] 3.4 Implement disconnected state, capped backoff reconnect (1s/2s/4s/max 10s), full reload, and resubscribe on reconnect, and verify a fake-backed test that drops the event stream, then recovers and emits fresh snapshots
- [x] 3.5 Gate actions on connection state, and verify a unit test that an action while disconnected returns a "not connected" error without calling the service

## 4. Application shell and navigation

- [x] 4.1 Implement the root model with full-screen layout (header with breadcrumb and connection indicator, content, footer key hints, status line) and resize handling, and verify a golden-file test of `View()` at 100x30 plus a unit test that a `WindowSizeMsg` preserves selection and filter
- [x] 4.2 Implement the view stack (push, pop on Esc never below root, filter-clears-first rule, replace on `:command`), and verify unit tests for each scenario in the `tui-navigation` "Drill-down view stack" requirement
- [x] 4.3 Implement the command bar with `containers/images/volumes` and aliases `co/im/vol`, unknown-command error, and Esc cancel, and verify unit tests for each command-bar scenario
- [x] 4.4 Implement a shared table component with j/k/arrows/g/G navigation, selection preserved by a per-view row key (container ID, image ID+reference, volume name) across refreshes, nearest-row fallback, empty-state message, and `/` case-insensitive filter, and verify unit tests for selection-survives-refresh and filter scenarios
- [x] 4.5 Implement overlays: help (`?`), confirm dialog (only `y` confirms), and single-line text prompt, with key routing order Ctrl+C -> overlay -> focused view input -> global -> view, and verify unit tests that non-`y` keys cancel the confirm dialog, help opens/closes with `?`/Esc, Ctrl+C quits with an overlay open, and typing `q`, `:` and `?` into an active filter appends them to the filter text
- [x] 4.6 Implement status feedback (transient success, error with engine text, in-progress spinner) and quit on `q`/Ctrl+C restoring the terminal, and verify unit tests for status messages and a manual run confirming the terminal is restored after `q`

## 5. Containers

- [x] 5.1 Implement the containers view (NAME, IMAGE, STATUS, PORTS formatted as `8080->80/tcp` deduplicated across host IPs and unpublished ports shown as `5432/tcp`, sorted by name, running-only default, `a` toggle with header indicator), and verify golden-file and unit tests for both modes and empty state
- [x] 5.2 Implement start/stop toggle (`s`, start only from `created`/`exited`, unavailable for other non-running states) and restart (`r`) with status reporting, and verify unit tests with the fake service for success, paused-container, and engine-error cases
- [x] 5.3 Implement delete (`d`) with confirm text naming the state and force-removal for running/paused/restarting containers, `Force` only in those states and `RemoveVolumes: false`, and verify unit tests asserting the options passed to the fake
- [x] 5.4 Implement the shared inspect view (pretty-printed JSON in a scrollable viewport, breadcrumb `... > <name> > inspect`) and wire `i` for containers, and verify a golden-file test
- [x] 5.5 Implement the logs view (`l`): tail 1000, follow, stdcopy demux for non-TTY containers, 50ms/200-line batching, 10,000-line ring buffer, auto-follow paused on scroll-up and resumed with `G`, `/` search with Esc clearing the search before closing, context cancelled on pop, and verify unit tests for batching, ring-buffer cap, follow pause, Esc ordering, and that popping cancels the stream
- [x] 5.6 Implement exec shell (`x`): probe `/bin/bash` then `/bin/sh` via SDK exec, then `tea.ExecProcess` running `docker exec -it <id> <probed shell>` with `DOCKER_HOST=<resolved host>` and `DOCKER_CONTEXT` removed from the env, with not-running guard and missing-CLI and no-shell errors (no suspend when no shell). Verify with unit tests for the probe order and the built `exec.Cmd` env, and manually against a running `alpine` (sh only) and `debian` (bash) container that the TUI suspends and restores with the selection intact
- [x] 5.7 Implement the filtered containers view used for drill-downs (fixed predicate, custom breadcrumb, starts in "all" mode), and verify a unit test that it lists only matching running and stopped containers

## 6. Images

- [x] 6.1 Implement the images view (REPOSITORY:TAG one row per tag, short ID, human size, relative age, IN USE; dangling hidden by default with `a` toggle), and verify golden-file and unit tests for multi-tag and dangling cases
- [x] 6.2 Implement delete (`d`) by reference without force and with `PruneChildren: true`, with confirm listing in-use containers only when the reference is the last tag (otherwise stating only the tag is removed), and verify unit tests for unused, shared-tag (only that tag removed), untag-in-use-with-other-tags, and in-use-refused cases
- [x] 6.3 Wire `i` to the inspect view for images, and verify the breadcrumb `drydock > images > <ref> > inspect` in a unit test
- [x] 6.4 Implement pull (`p`): prompt, default `:latest` tag, anonymous pull, async progress decoding to an in-progress status, success/error reporting, Esc cancel, and verify unit tests with a fake progress stream plus a manual pull of `alpine:latest`
- [x] 6.5 Implement prune dangling (`P`): preview of dangling images with IN USE = 0 and their total size from snapshot, nothing-to-prune message, `ImagePrune` with `dangling=true` on confirm, reclaimed-space report, and verify unit tests for both branches and that a dangling image used by a stopped container is excluded
- [x] 6.6 Wire Enter to push the filtered containers view for the image, and verify a unit test for breadcrumb `drydock > images > <ref> > containers` and Esc return

## 7. Volumes

- [ ] 7.1 Implement the volumes view (NAME, DRIVER, MOUNTPOINT, IN USE, sorted by name), and verify a golden-file test including an anonymous volume
- [ ] 7.2 Implement create (`c`) with name prompt, empty name for engine-generated, client-side existence check against the snapshot (falling back to `VolumeInspect`) that rejects an existing name before calling `VolumeCreate` (the engine treats create as idempotent), Esc cancel, and verify unit tests for each scenario
- [ ] 7.3 Implement delete (`d`) without force, with confirm listing mounting containers, and verify unit tests for unused and in-use-refused cases
- [ ] 7.4 Wire `i` to the inspect view for volumes, and verify the breadcrumb in a unit test
- [ ] 7.5 Implement prune unused (`P`): preview of all unmounted volumes (named and anonymous) from the snapshot, nothing-to-prune message, per-volume `VolumeRemove` without force, aggregated "N removed, M failed" report, and verify unit tests that in-use volumes are excluded and partial failures are reported
- [ ] 7.6 Wire Enter to push the filtered containers view for the volume, and verify a unit test for breadcrumb `drydock > volumes > <name> > containers` and Esc return

## 8. Integration checks

- [ ] 8.1 Add `integration`-tagged end-to-end tests against a real engine that create labeled resources, assert they appear in store snapshots within 2 seconds of external changes, and clean up, and verify `go test -tags integration ./...` passes locally
- [ ] 8.2 Manually verify engine restart recovery: run drydock, `sudo systemctl restart docker`, confirm the disconnected indicator appears and clears with lists reloaded
- [ ] 8.3 Run `go vet ./...` and `go test -race ./...`, and verify both pass with no data races reported
