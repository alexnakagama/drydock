# Proposal

## Why

Most containers on a typical local engine are started by Docker Compose, but
drydock lists them as a flat set of unrelated containers. There is no way to
see which containers form one stack, or to stop, start or restart a stack
without acting on each container in turn. Compose already labels every
container it creates with its project, and those labels reach drydock's
store today, unused.

## What Changes

- Add a fourth resource, **projects**: a tab (`4`, `:projects`, alias `pr`)
  that is always shown, listing one row per distinct
  `com.docker.compose.project` label across all containers, sorted by name,
  with the columns PROJECT, RUNNING (running/total), STATE (`running`,
  `partial` or `exited`, colored and with a glyph like containers) and
  WORKING DIR (from `com.docker.compose.project.working_dir`, `$HOME` shown
  as `~`). With no project it shows "No Compose projects.". A project
  disappears when its last container is removed.
- `Enter` on a project opens a drill-down containers view of that project's
  containers, where the existing scoped `P` prune applies to that project.
- Project actions, applied to every container of the project in parallel,
  without confirmation, reporting per-container failures:
  - `s` reads `stop` when any service container is running, and stops the
    running ones; otherwise, when one is created or exited, it reads `start`
    and starts those. Paused and restarting containers are never touched,
    as with the single-container `s`.
  - `r` restarts every service container of the project that is not
    paused, whatever its other state.
  - Containers created by `docker compose run` (`com.docker.compose.oneoff=True`)
    are listed in the drill-down but excluded from the RUNNING count, the
    STATE and the project actions.
  - The README states that start only starts existing containers: it does
    not create services the way `docker compose up` does.
- Containers views gain a column while at least one container in the engine
  (the whole snapshot, not only the listed rows) has a project label:
  PROJECT in the root containers view and the image and volume drill-downs,
  and SERVICE (`com.docker.compose.service`) in a project drill-down.
  Containers without the label show `-`. The `/` filter also matches this
  column. Column minimum widths are rebalanced so both the ports and the
  stats layouts still fit a 100-column terminal; with no Compose container
  the layouts are unchanged.
- The tab strip count for projects is the number of distinct project labels;
  the other counts stay engine totals.

Out of scope: `compose up`/`down` or anything that needs the Compose files or
CLI; dependency-ordered actions (`com.docker.compose.depends_on`); deleting a
whole project; inspecting a project; Swarm stacks.

## Capabilities

### New Capabilities
- `compose-projects`: the projects view: how projects are derived from
  container labels, its rows, columns, states and empty state, the drill-down
  into a project's containers, and the project-level start, stop and restart
  actions and their footer labels.

### Modified Capabilities
- `tui-navigation`: the tab strip, number keys, `Tab` cycling and the command
  bar gain a fourth resource, `projects`, whose count is derived from labels
  rather than an engine total; the filter matches the PROJECT/SERVICE column.
- `container-management`: the containers view's column list gains the
  conditional PROJECT column (SERVICE in a project drill-down), in both the
  ports and the stats layouts.

## Impact

- `internal/ui/views`: new projects view; `Resources()` gains a fourth entry;
  containers view column sets and their minimum widths; footer and help hints.
- `internal/store`: project grouping derived from the snapshot's containers
  (a project index and count, like the existing image and volume user
  indexes).
- `internal/ui`: tab strip and number-key switching for four resources,
  including the short `pr` label on narrow terminals.
- Every golden file that shows the tab strip (its line gains
  `4 projects (N)`), tests that hard-code three resources or `1-3/tab`, and
  new golden files for the projects view and the Compose column layouts;
  `dockerfake` needs containers with Compose labels and a per-container
  failure hook.
- README: keys table, tab description and the containers column description.
- No new dependencies and no new engine API calls: start, stop and restart
  reuse the existing container operations.
