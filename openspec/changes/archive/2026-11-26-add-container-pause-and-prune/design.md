# Design

## Context

See proposal.md for motivation and specs/container-management/spec.md for the required behavior. The current code shapes the approach in four ways:

- **Pause and unpause already flow through the store.** `store.eventKind` maps every container event except a short ignore list (attach, resize, exec_*, ...) to a containers refresh, so `pause` and `unpause` events already update the list. Paused rows already have their color, glyph and `-` stats cells, and the stats targets are recomputed from each snapshot (`statsTargets` lists `running` containers only), so unpausing resumes stats with no extra work.
- **There are two prune styles.** The images view previews dangling images, then calls the engine's `ImagePruneDangling`. The engine decides what to remove when it runs, which can differ from the preview. The volumes view previews unused volumes, then removes exactly those one by one without force (`removeVolumes`), with a summary that counts removed, already gone, failed, interrupted and not attempted items.
- **A drill-down containers view has a fixed predicate.** `containersView.match` restricts a view opened from an image or a volume, and `nil` means every container. The `a` toggle and the `/` filter apply on top of it.
- **The confirm dialog bounds its body.** `components.Confirm` shows at most `maxConfirmLines` body lines and summarizes the rest, so a long prune preview needs no new UI.

## Goals / Non-Goals

**Goals:**
- What the prune dialog lists is exactly what gets removed, and nothing running is ever killed.
- The one-by-one removal and its summary are shared by volumes and containers, not copied.

**Non-Goals:**
- Using the engine's `ContainersPrune` endpoint.
- Any change to the store or to event handling.

## Decisions

### D1. `ContainerPause` and `ContainerUnpause` on `docker.Service`

Add `ContainerPause(ctx, id) error` and `ContainerUnpause(ctx, id) error`. The SDK wrapper validates the ID with `ValidateContainerID`, like the other container methods, and returns the SDK's errors unwrapped. `dockerfake` implements the engine's state checks: pausing a container that is not running, or unpausing one that is not paused, returns a conflict error with the engine's wording. On success it sets the state and emits `pause` or `unpause`.

### D2. `p` is state-dependent and unconfirmed, like `s`

`p` maps to a `pause` method that switches on `c.State`: running pauses, paused unpauses, anything else returns `ui.Error("pause/unpause is unavailable for a <state> container (<name>)")` without calling the engine. Each action goes through `ui.Track` and `st.Action`, like `restart`. `KeyHelp` relabels the binding `pause` or `unpause`, or disables it. `p` goes right after `s` in the hint order, so it is dropped late on narrow terminals. `P` is always enabled and goes just before `t`: last among the view's actions, as in the images and volumes views, but ahead of `t`, which the spec's "Stats toggle" requirement requires to be the first hint dropped. The `s` refusal for a paused container appends "; press p to unpause".

- *Alternative: let `s` stop a paused container.* The engine accepts it, but SIGTERM cannot reach a frozen process, so the stop is expected to wait out the full timeout (10 s by default) before the engine kills the container (not verified against a real engine). That would look like a hang, so `s` keeps refusing and points to `p`.

### D3. Prune scope: the view's predicate, ignoring `a` and `/`

Candidates are the snapshot's containers that pass `v.match` (all of them when `match` is nil) and whose state is `exited`, `created` or `dead`. Neither the `a` toggle nor the table filter narrows the scope. With `a` off the view lists no stopped containers, so honoring it would make `P` a no-op, and the filter is a transient search, not a scope (the images and volumes prunes ignore it too). The dialog lists every candidate, so a hidden container is never removed unseen. Candidates are sorted by name and listed as `name  state`. The body ends with "Their volumes are kept."

- *Alternative: engine-wide in every view.* Inside a drill-down from an image, "prune" naturally means that image's stopped containers. Removing unrelated containers from there would surprise the user.

### D4. Remove exactly the previewed containers, without force

On confirm, the previewed containers are removed by ID with `ContainerRemove(ctx, id, {Force: false, RemoveVolumes: false})`. IDs are unique and never reused, so the identity check `removeVolume` needs for volume names is unnecessary here. A container that started after the preview is refused by the engine (conflict) and counted as failed. A not-found error counts as already gone.

- *Alternative: the engine's `ContainersPrune`.* It decides at run time, could remove containers stopped after the preview, and offers no per-container outcome. It also cannot express a drill-down scope except through label filters, which do not match image or volume usage.

### D5. A generic one-by-one remover shared with volumes

Extract the loop and summary out of `removeVolumes` into a helper in `internal/ui/views`:

```go
type removal[T any] struct {
	name   func(T) string                              // shown in failure lists
	remove func(ctx context.Context, item T) error     // one item
	gone   error                                       // sentinel for "already gone"
	noun   [2]string                                   // "volume", "volumes"
}

func (r removal[T]) run(ctx context.Context, items []T) (string, error)
```

`removeVolumes` becomes a thin wrapper that keeps its current messages, so the existing volume tests pass unchanged. Containers use the same helper with a `remove` that maps `cerrdefs.IsNotFound` to `errContainerGone`. The summary wording ("2 containers removed, 1 already gone", "1 removed, 1 failed (old): ...") follows volumes. The prune runs under `st.ActionWith` with a 5-minute timeout, like the volume prune.

- *Alternative: copy `removeVolumes` for containers.* It is about 70 lines of subtle counting and cancellation logic. Two copies would drift.

## Risks / Trade-offs

- [Cgroup v1 rootless engines and some runtimes cannot pause] → The engine's error is shown through the normal `ErrorText` path (spec scenario "Engine refuses to pause"). No capability probing.
- [A container with a restart policy can restart between the preview and the removal] → Removal without force is refused, and the container is reported as failed, never killed.
- [Pruning leaves anonymous volumes behind] → Consistent with `d`, and the dialog says volumes are kept. The volumes view's `P` cleans them up.
- [The `D5` refactor touches the tested volume prune] → `removeVolumes` keeps its signature and messages, so the existing volume tests guard the refactor. Do it as a separate task before adding container prune.
