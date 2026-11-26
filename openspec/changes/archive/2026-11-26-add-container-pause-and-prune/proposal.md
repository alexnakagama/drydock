# Proposal

## Why

drydock already shows paused containers (orange, `◐`, `-` in the stats columns), but offers no way to pause or unpause one: `s` refuses a paused container and the user has to leave drydock and run `docker unpause`. The images and volumes views each have a `P` prune for cleaning up leftovers, but the containers view does not, so clearing out stopped containers means deleting them one by one. Both gaps are small and close the most obvious holes in container lifecycle management.

## What Changes

- **Pause and unpause**: in a containers view, `p` pauses the selected container when it is running and unpauses it when it is paused. There is no confirmation, because pausing is non-destructive and reversible. For any other state, `p` reports that the action is unavailable without calling the engine.
- **Footer hint**: the `p` hint names what it would do: `p pause` on a running container, `p unpause` on a paused one, omitted from the footer (and dimmed in help) otherwise. `P prune stopped` is listed just before the stats hint, which stays the first hint dropped on narrow footers.
- **Start/stop on a paused container**: `s` still refuses a paused container, and the status message now points to `p` to unpause it.
- **Prune stopped containers**: `P` previews the stopped containers (exited, created or dead) in the current view's scope and, after confirmation, removes exactly those, keeping their volumes. The scope is every container the view would list in "all" mode, whatever the `a` toggle and the `/` filter: every container in the root view, and only the containers using that image or volume in a drill-down.
- **Safe removal**: the previewed containers are removed one by one, without force. A container that started after the preview is refused by the engine and reported in the result instead of being killed.

Out of scope: kill, bulk actions on selected rows, pruning with an age filter (`until`), removing the pruned containers' anonymous volumes, and pausing or pruning from the images or volumes views.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `container-management`: the "Start and stop" requirement's paused-container scenario now points to `p`. A new "Pause and unpause" requirement covers `p` and its footer hint. A new "Prune stopped containers" requirement covers `P`: its scope, the preview, removal without force, kept volumes, and the result report.

## Impact

- `internal/docker/service.go`, `internal/docker/sdk.go`: new `ContainerPause` and `ContainerUnpause` methods on `docker.Service`, wrapping the SDK calls.
- `internal/docker/dockerfake/fake.go`: pause and unpause in the fake engine, with the engine's state checks and the matching `pause`/`unpause` events.
- `internal/ui/keys/keys.go`: `Pause` (`p`) and `Prune` (`P`) bindings in the containers key map and its help list.
- `internal/ui/views/containers.go`: `p` and `P` handling, the state-dependent footer hint, the prune preview and dialog, and the new hint in the paused `s` message.
- `internal/ui/views/volumes.go` (and a shared helper): the one-by-one removal and its result summary, today specific to volumes, made reusable for containers.
- `README.md`: the `p` and `P` keys in the containers row of the actions table.
- Tests: unit tests for pause/unpause in every state, the prune scope in root and drill-down views, the preview, partial failures, and the fake engine's new methods; an integration test against a real engine. In the existing golden snapshots, only footer lines may change.
- No new dependencies.
