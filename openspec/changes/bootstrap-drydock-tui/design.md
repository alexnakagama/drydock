# Design

## Context

Greenfield repository: no Go code exists yet. The development machine runs Go 1.27 and Docker Engine 29. Motivation and scope are in proposal.md; required behavior is in the five capability specs under `specs/`.

Three things shape the approach:
- **Long-lived streams.** The event stream, log follow, and pull progress are long-running and asynchronous, and the UI must stay responsive while they run.
- **Shared, cross-referenced data.** Several views need the same data: "in use" counts for images and volumes come from the container list.
- **Terminal handover.** The exec-shell feature has to hand the real terminal to another process and then take it back.

## Goals / Non-Goals

**Goals:**
- A small, layered architecture where adding a resource view (networks, stacks) means adding one view plus one store slice, without touching the shell.
- The Docker access layer sits behind an interface, so views and the store can be unit-tested without a daemon.
- No goroutine or stream leaks when views are opened and closed repeatedly.

**Non-Goals:**
- Configurable keybindings or themes (keys are hard-coded for v1).
- Windows support (Linux is the target; macOS should work but is not verified).
- A plugin or extension mechanism.

## Decisions

### D1. TUI framework: Bubble Tea v2 (`charm.land/bubbletea/v2`) with Bubbles v2 and Lip Gloss v2
Bubble Tea's message loop (Model/Update/View) fits a UI fed by asynchronous streams: every engine event, log line, and action result becomes a `tea.Msg`. Bubbles provides the table, text input, viewport, and spinner components; Lip Gloss handles layout and styling. `tea.ExecProcess` covers the terminal handover for exec (D7).
- *Alternative: tview.* It has richer ready-made widgets, but it is callback-driven and uses its own goroutine model, so streaming updates need `QueueUpdateDraw` plumbing throughout. It also has no built-in equivalent of ExecProcess.
- *Alternative: Bubble Tea v1 (`github.com/charmbracelet/bubbletea`).* It is stable, but it is the previous major version. Starting a new project on v1 would mean a migration later.

### D2. Docker access: Engine Go SDK (`github.com/moby/moby/client`, v0.6.x) behind a `docker.Service` interface
`client.New(client.FromEnv)` honors `DOCKER_HOST`, `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`, and the client negotiates the API version by default. All engine calls go through a narrow interface owned by drydock, roughly: list containers/images/volumes, the container lifecycle calls, logs, inspect, image pull/remove, volume create/remove, events, and ping. The production implementation wraps the SDK. Tests use a fake.
- *Alternative: the legacy `github.com/docker/docker/client` import path.* Since Engine v29 the SDK is published as separate `moby/moby/client` and `moby/moby/api` modules, and the legacy path is frozen.
- *Alternative: shelling out to the `docker` CLI and parsing its output.* This is fragile, slower, and loses typed data. The one place it is used is exec (D7).
- *Registry credentials are out of scope.* The SDK does not read `~/.docker/config.json` or credential helpers, and supporting them would pull in `github.com/docker/cli`. Pulls are anonymous in v1 (see proposal, out of scope).
- *Note:* the SDK module is pre-v1, so its API may change between minor versions. The version is pinned in `go.mod`, and the wrapper keeps any breakage contained.

### D3. Package layout
```
cmd/drydock/main.go        entry: resolve client, ping, start program
internal/docker/           Service interface, SDK implementation, fake
internal/store/            in-memory state + event-driven refresh
internal/ui/               root model: view stack, command bar, status, help, confirm
internal/ui/views/         containers, images, volumes, logs, inspect
internal/ui/components/    shared table wrapper, prompt, confirm dialog
internal/ui/keys/          keymaps (global + per view)
```
`internal/` keeps the packages private. Dependencies only point downward: `ui -> store -> docker`, and views never import `docker` directly, except through store-provided command constructors for actions.

### D4. Store: snapshot state, refreshed per kind by events
```
  docker events ----> store.Run (goroutine)
                         |  debounce 150ms per kind
                         v
                  List(kind) from engine
                         |
                         v
          tea.Msg{Snapshot} --> root model --> active view re-renders
```
- On startup the store lists all three kinds (containers with `All: true`, so "in use" counts include stopped containers).
- On each `container`/`image`/`volume` event it schedules a re-list of that kind only. Bursts are debounced, so `docker compose up` doesn't trigger N re-lists. Container events also recompute the image and volume usage counts, since those come from the container data.
- Snapshots are immutable values sent to the UI as messages. The UI never shares mutable state with the store goroutine, so no locks are needed in the views.
- Derived indexes are computed once per snapshot: `imageID -> []container` and `volumeName -> []container`. They drive the IN USE columns, the drill-down filters, and the delete warnings.
- *Alternative: polling every N seconds.* It is simpler, but it adds latency, sends wasted calls when nothing changes, and can't meet the 2-second update requirement cheaply.
- *Alternative: applying event deltas without re-listing.* Re-listing the affected kind is simpler and self-correcting. Local list calls take milliseconds, so applying deltas isn't worth the complexity.

### D5. Connection lifecycle
- At startup, `main` runs `Ping` with a 5-second timeout **before** entering the alternate screen. On failure it prints the endpoint and the error to stderr and exits with status 1. It maps `ECONNREFUSED`/not-found to "engine may not be running" and `EACCES` to "permission denied".
- At runtime, an error on the event stream sends the store into a `disconnected` state, which the UI shows in the header. The store then retries `Ping` with capped backoff (1s, 2s, 4s, max 10s). On success it re-lists everything and resubscribes to events. Actions check the connection state first and fail immediately with "not connected".

### D6. Navigation: root model owns a view stack
The root model holds `[]View`, where each `View` is a Bubble Tea model plus a `Breadcrumb()` and `KeyHelp()`. The root model also renders the header, footer, status line and overlays (command bar, help, confirm, prompt), and it routes keys:
1. Ctrl+C is always handled first and quits, even with an overlay open.
2. If an overlay is open, the overlay gets the key.
3. If the top view reports `InputFocused()` (its filter or search input is active), the view gets the key. This way `q`, `:` and `?` typed into a filter are text, not commands.
4. Otherwise, global keys (`:`, `?`, `q`) are handled.
5. Anything else goes to the top view.

Esc is handled by the top view first: it clears the filter or search if one is active. If the view doesn't consume it, the root pops the stack, but never below the root view. `:resource` replaces the stack with a new root view. Drill-down views (containers filtered by image or volume) reuse the containers view with a fixed predicate, their own breadcrumb, and "all" mode on by default.

Selection is preserved across refreshes by a per-view row key: container ID, image `ID + reference` (several rows can share one image ID), and volume name.

### D7. Exec shell via `tea.ExecProcess` running the `docker` CLI
Pressing `x` first probes for a shell with a non-interactive SDK exec (`ExecCreate` / `ExecStart` / `ExecInspect`): it tries `/bin/bash -c true`, then `/bin/sh -c true`. If neither starts, the status area reports "no shell available" and the TUI is never suspended. Otherwise drydock builds `exec.Command("docker", "exec", "-it", id, <probed shell>)` and hands it to `tea.ExecProcess`. Bubble Tea releases the terminal, the CLI takes care of TTY setup and resize, and when the process exits the program restores the UI and receives a completion message. The shell's exit code is not interpreted, because it only reflects the user's last command.

The child process environment sets `DOCKER_HOST` to the endpoint drydock resolved (`cli.DaemonHost()`) and removes `DOCKER_CONTEXT`. `client.FromEnv` does not read the CLI's current context but the CLI does, so without this an active `docker context` would make exec target a different engine than drydock.
- *Alternative: an SDK hijacked exec connection with raw-mode terminal handling.* It needs no CLI, but it means reimplementing TTY raw mode, resize propagation (SIGWINCH) and stream copying, which is significant and error-prone code for a feature the CLI already gets right.
- *Trade-off:* the exec feature needs `docker` on PATH (as noted in the proposal). If it is missing, the status area reports that clearly.

### D8. Streams owned by views, cancelled on pop
The logs view gets its own `context.Context`, created on push and cancelled when the view is popped or replaced. A goroutine reads the log stream (using `github.com/moby/moby/api/pkg/stdcopy` to demultiplex when there's no TTY) and sends batched `LogLinesMsg`s, flushed every 50ms or 200 lines, so a chatty container doesn't flood the update loop. The view keeps a ring buffer capped at 10,000 lines, and the initial request uses `Tail: "1000"`. Pull progress follows the same pattern: the pull runs in a `tea.Cmd` that decodes the JSON progress stream, sends periodic status messages, and finishes with a done or error message.

### D9. Destructive actions are previewed from the snapshot and executed explicitly
- **Delete:** the confirm dialog text comes from the current snapshot. For example, the in-use containers named in it come from the derived indexes (D4).
- **Image delete** removes the selected reference with `PruneChildren: true` (matching `docker rmi`) and no force. When other tags share the image ID, the engine only untags, which succeeds even if the image is in use. So the confirmation warns "removal will fail" only when the selected reference is the image's last tag and the image is in use.
- **Image prune:** the engine's dangling prune skips images used by any container, including stopped ones. So the preview lists only dangling images with IN USE = 0 (from the D4 index) and their total size. On confirm, drydock calls `ImagePrune` with `dangling=true` and reports the space reclaimed from the prune report.
- **Volume prune:** since API 1.42, the engine's volume prune removes only *anonymous* volumes unless the `all=true` filter is passed. To make what's removed match the preview exactly (including named volumes), and avoid racing with containers created in the meantime, drydock deletes the previewed volumes one by one with `VolumeRemove` (no force). Failures are collected and reported, for example "2 removed, 1 failed: pgdata in use".
- **Container delete** uses `Force: true` when the container state is `running`, `paused` or `restarting` (the engine refuses to remove any of these without force), and the confirmation names that state. It always uses `RemoveVolumes: false`.
- **Volume create** (not destructive, but checked the same way): the engine treats create as idempotent and returns the existing volume when the name is taken. So drydock checks the snapshot first, falls back to `VolumeInspect`, and reports "volume <name> already exists" without calling `VolumeCreate`.

### D10. Testing strategy
- **Unit tests** for the store (event handling, debounce, derived indexes) and for each view's `Update`, using the fake `docker.Service`. View rendering is checked with golden-file snapshots of `View()` output at a fixed size.
- **Integration tests**, behind the `integration` build tag, run against a real engine: they create and destroy labeled test containers, images and volumes. These tests are opt-in.

## Risks / Trade-offs

- **[Pre-v1 SDK module may change API]** → Pin the version, keep every SDK call inside `internal/docker`, and run the integration tests before upgrading.
- **[Event storms (CI, compose) cause excessive re-lists]** → Per-kind debounce (D4). If that turns out not to be enough, the debounce window can be raised without changing behavior.
- **[Log flood freezes the UI]** → Batching plus a ring buffer cap (D8).
- **[Exec relies on the external `docker` CLI]** → An explicit error message when it is missing, and a documented runtime requirement. An SDK-based exec can replace it later without changing the spec.
- **[Snapshot staleness in confirm previews]** → The engine is still the authority. Removals don't use force (except for running containers, as confirmed), so a resource that became in-use in the meantime fails safely and the error is reported.
- **[Terminal left in a bad state on a crash]** → Bubble Tea restores the terminal on panic in `Program.Run`. drydock also avoids its own panics in goroutines by turning errors into messages.

## Migration Plan

Not applicable: this is the first release, with no prior state or users. Rolling back means not installing the binary.

## Open Questions

- Should the binary support a `--version` flag and embed build info in v1? This is cosmetic and can be decided during implementation without affecting the specs.
