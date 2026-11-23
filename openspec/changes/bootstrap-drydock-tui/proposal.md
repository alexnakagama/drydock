# Proposal

## Why

drydock is a personal, self-built terminal UI for managing a local Docker engine, written in Go. Existing tools (lazydocker, dry) cover similar ground, but the goal here is to own the tool end to end: its UX, its keymap, and its architecture. This first change establishes the application shell and the three core resources (containers, images, volumes) so later features (Compose stacks, stats, remote contexts) have a solid base to grow on.

## What Changes

- New Go module and `drydock` binary that connects to the Docker engine via the official Go SDK, honoring `DOCKER_HOST` / the default socket.
- Full-screen, drill-down navigation (k9s-style): one resource view at a time, a `:command` bar to switch resources, a breadcrumb header, `Esc` to go back, `/` to filter, `?` for help.
- Live-updating resource lists driven by the Docker events stream, backed by a shared in-memory store, so list views never call the engine directly (logs, inspect and pull use service calls owned by their view).
- **Containers view**: list (with toggle to include stopped containers), start/stop, restart, delete with confirmation, follow logs, inspect, and exec into a shell by suspending the TUI and handing over the terminal.
- **Images view**: list with "in use" indication, delete with confirmation, inspect, pull by reference (simple progress feedback), prune dangling images with a preview of what will be removed, and drill-down to the containers using an image.
- **Volumes view**: list with "in use" indication, create, delete with confirmation, inspect, prune unused volumes with a preview, and drill-down to the containers mounting a volume.
- Destructive actions always require explicit confirmation; in-use resources are flagged before deletion is attempted.

Out of scope for this change: Docker Compose (stack grouping, up/down), networks, live CPU/memory stats, volume disk-usage sizes, remote hosts/context switching, per-layer pull progress bars, and private-registry authentication (pulls are anonymous).

## Capabilities

### New Capabilities
- `tui-navigation`: Application shell behavior: drill-down view stack, command bar for switching resources, breadcrumb, filtering, help overlay, global keybindings, confirmation dialogs, and error/status reporting.
- `docker-connection`: Connecting to the Docker engine, handling unavailability, and keeping resource state live via the events stream.
- `container-management`: Listing and acting on containers: lifecycle actions, deletion, logs, inspect, and interactive exec.
- `image-management`: Listing and acting on images: deletion, inspect, pull, prune of dangling images, and usage cross-reference to containers.
- `volume-management`: Listing and acting on volumes: creation, deletion, inspect, prune of unused volumes, and usage cross-reference to containers.

### Modified Capabilities
<!-- None: this is the first change in the project. -->

## Impact

- New repository code: Go module, `cmd/drydock` entrypoint, and internal packages for the TUI and the Docker service layer.
- New dependencies: Bubble Tea, Bubbles, and Lip Gloss (Charm) for the TUI; the Docker Engine Go SDK for engine access.
- Runtime requirement: access to a Docker engine socket (user in the `docker` group or equivalent); `docker` CLI on PATH for the exec-shell feature.
- No existing specs or code are affected.
