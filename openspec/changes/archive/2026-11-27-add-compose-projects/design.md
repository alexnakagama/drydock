# Design

## Context

See proposal.md for motivation and the specs for the required behavior.

The relevant current structure:

- `store.Snapshot` holds every container (`container.Summary`, which already
  carries `Labels`), and derives the `imageUsers` and `volumeUsers` indexes
  once per snapshot in `buildIndexes`. Views read the snapshot only on the UI
  goroutine.
- `views.Resources()` returns the tab resources as `ui.Resource` values
  (name, aliases, constructor, `Count`). The root model builds the tab strip,
  command bar and switching from that list. The number keys are a
  fixed-size array in `internal/ui/keys` (`tabKeys`, `Tab1`..`Tab3`).
- Drill-downs use `NewFilteredContainers(match)`. The scoped prune
  (`pruneCandidates`) already honors `match`, so a project drill-down gets
  a project-scoped `P` without extra work.
- Single-container actions go through `Store.Action`/`ActionWith`, which
  bound the work by a timeout (2 minutes by default) and report an
  `ActionResultMsg`. Bulk removals use `removal[T]`, which runs
  sequentially and summarizes partial failures.
- Containers views have two fixed column sets (`containerColumns`,
  `statsColumns`), each sized to fit exactly 100 columns, and switch between
  them when the stats setting changes.

## Goals / Non-Goals

**Goals:**
- Compute projects once per snapshot, so the tab count (called on every
  render) and the projects view stay cheap.
- Reuse the containers view for the project drill-down instead of a new
  list type.
- Keep the layout unchanged for engines without Compose containers.

**Non-Goals:**
- A general labels column, or grouping by any label other than the Compose
  project.
- Reading Compose files or calling the `docker compose` CLI.
- Ordering actions by `com.docker.compose.depends_on`.

## Decisions

### Projects are a snapshot index

A new `buildProjects`, called next to `buildIndexes` when a snapshot is
built, computes the project index: project name to its containers, sorted
like the other indexes, plus a sorted list of project names. Keeping it
separate leaves `buildIndexes` and its existing call sites unchanged.
`Snapshot` exposes `Projects()` (a summary per project: name, service and
one-off containers, working dir) and `NumProjects()` for the tab `Count`.
Service/one-off classification, the RUNNING count and the STATE are
computed here, not in the view, so the drill-down, the tab and the view
agree, and they are unit-testable without the UI.

The label names (`com.docker.compose.project`, `.service`, `.oneoff`,
`.project.working_dir`) are constants in `internal/store`, next to the
code that reads them.

*Alternative:* derive projects in the projects view from
`snap.Containers()`. Rejected: the tab `Count` would rebuild the grouping on
every render, and the containers view also needs "is there any Compose
container" to decide on its column.

### The projects view follows the images and volumes views

A new `projectsView` in `internal/ui/views` uses `components.Table` like
the other list views, implements `ui.Positioner`, and is added as the
fourth entry of `Resources()` (name `projects`, alias `pr`, `Count:
store.Snapshot.NumProjects`). Row tones map running/partial/exited to the
existing success/warning/dim tones. Enter pushes
`NewFilteredContainers` with a predicate on the project label and crumbs
`{name, "containers"}`, as the images view does for an image.

The WORKING DIR cell shows `~` for a path equal to `os.UserHomeDir()`,
and replaces the home directory with `~` only for a path that starts with
the home directory followed by `/`, so `/home/meow` is not shortened for
the home `/home/me`. A home of `/` or an empty home is never replaced. The
home directory is read once when the view is created; if it cannot be read,
paths are shown unchanged.

### Containers views pick their Compose column per snapshot

A containers view gets a column mode: `project` for the root and the
image and volume drill-downs, `service` for a project drill-down
(`NewFilteredContainers` gains an option for it). On every snapshot, the
view checks whether the snapshot has any project (`NumProjects() > 0`)
and chooses among four column sets: ports or stats, each with or without
the Compose column. It rebuilds the table only when the chosen set changes,
the way the stats toggle already switches layouts.

Minimum widths with the Compose column, each fitting 100 columns with the
2-cell gutter and 2-cell gaps:

```
 ports:  NAME 18 | PROJECT 12 | IMAGE 18 | STATUS 24 | PORTS 18       -> 2 + 90 + 8  = 100
 stats:  NAME 18 | PROJECT 12 | IMAGE 12 | STATUS 24 | CPU 7 | MEM 15 -> 2 + 88 + 10 = 100
```

NAME, IMAGE, PORTS and MEM stay flex columns, and PROJECT/SERVICE is
flex too, so wide terminals are unaffected. The Compose column is a
filter column, like NAME and IMAGE. Without Compose containers the current
column sets are used unchanged, so the table part of the existing golden
files stays the same; only their tab strip line changes, because of the
fourth tab.

*Alternative:* show the column whenever a listed row has a project.
Rejected during exploration: the `a` toggle and the filter would make the
layout jump.

### Project actions run in parallel with a bound

A project action builds its target list on the UI goroutine from the
view's snapshot (running service containers for stop, created or exited
ones for start, service containers that are not paused for restart) and
runs one `Store.ActionWith` whose function acts on the targets
concurrently, at most 8 at a time, using a `sync.WaitGroup` and a
semaphore channel.

A stop or restart waits up to each container's own stop timeout, which
Compose users often raise with `stop_grace_period` (30 seconds to several
minutes). The default 2-minute action timeout could then cut a project of
more than 8 such containers off halfway, so project actions use their own
`projectActionTimeout` of 10 minutes, like `containerPruneTimeout` does for
prunes. Containers not yet attempted when it expires are reported as not
attempted.

A small helper next to `removal[T]` collects the outcome: the count that
succeeded, the failed container names (the first three listed) and the
first error in container name order, so the message does not depend on
goroutine scheduling. On full success the detail reads, for example,
`stopped 3 containers of myapp`. Context cancellation is reported like
`removal[T]` reports it: containers not yet started are counted as not
attempted.

*Alternative:* reuse `removal[T]` as is. Rejected: it is sequential and
its wording ("removed", "already gone") is specific to removal.
*Alternative:* `errgroup`. Rejected: it is only an indirect dependency
today, and errgroup's cancel-on-first-error is the opposite of the
required "attempt every container".

### Target rules leave paused containers alone

Single-container `s` refuses paused containers because a paused container
cannot act on SIGTERM, so a stop waits out its whole stop timeout before
the engine kills it (see `startStop` in `containers.go`). The project
actions keep that rule: stop targets only running service containers,
start only created or exited ones, and restart every service container
that is not paused. Paused containers are never touched, so one of them
cannot hold a slot of the parallel runner for its whole stop timeout. This
is narrower than the exploration's first sketch, which also stopped and
restarted paused containers.

### The help overlay shows the footer's labels

The footer and the help overlay both read the view's `KeyHelp()`, so the
help shows `s` with the same `stop`/`start` label as the footer. The note
that start only starts existing containers goes in the README instead of
the help, so no new help interface is needed.

### Switching keys scale to four resources

`tabKeys` becomes `{"1", "2", "3", "4"}` with a `Tab4` binding,
`switchKeys` includes `tabKeys[3]`, and `TabKeys()` returns four bindings.
The help entry reads `1-4/tab`. The
root model already maps tab keys to `resources[i]` by index, so the model
needs no structural change.

## Risks / Trade-offs

- [The column switch moves the layout when the first Compose container
  appears or the last one goes] → It only happens on a Compose up or down,
  which the user just ran. The view keeps its selection and filter across
  the switch, as it does for the stats toggle.
- [Narrower NAME and IMAGE minimums cut long names sooner on exactly 100
  columns] → Only when the Compose column is shown; flex columns still
  grow on wider terminals.
- [Parallel stop leaves dependent services briefly running against a
  stopped database] → Accepted for v1; dependency ordering is a non-goal
  and can come as its own change.
- [Projects with the same name from two directories merge into one row]
  → Compose itself treats the project name as unique per engine, so this
  only happens if the user forced it; WORKING DIR shows the first
  directory recorded.
- [A container with a long `stop_grace_period` holds a runner slot for
  that long] → The 10-minute project timeout covers several such waves;
  anything cut off is reported as not attempted rather than silently
  skipped.
- [Testing which container fails needs the fake to fail by container]
  → `dockerfake` gains a per-container failure hook, since its per-method
  `FailNext` cannot choose among parallel calls.
