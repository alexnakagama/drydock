# Spec Delta

## Purpose

Defines how drydock groups containers into Docker Compose projects, using the labels Compose puts on the containers it creates, and how users list those projects, drill into their containers and start, stop or restart a project as one unit.

## ADDED Requirements

### Requirement: Projects derived from container labels
drydock SHALL derive Compose projects from the engine's containers, running or stopped: one project per distinct non-empty value of the `com.docker.compose.project` label. A container without that label, or with an empty value, SHALL belong to no project. A project SHALL exist exactly as long as at least one container carries its name, and SHALL NOT be stored or remembered by drydock.

#### Scenario: Containers grouped by project
- **WHEN** the engine has containers `myapp-web-1` and `myapp-db-1` labeled with project `myapp`, `blog-web-1` labeled with project `blog`, and `redis` with no project label
- **THEN** drydock knows exactly two projects, `blog` and `myapp`, and `redis` belongs to neither

#### Scenario: Project disappears with its last container
- **WHEN** the only container labeled with project `blog` is removed
- **THEN** the project `blog` no longer exists

#### Scenario: Stopped containers keep their project
- **WHEN** every container of project `myapp` is exited
- **THEN** the project `myapp` still exists

### Requirement: One-off containers
A container labeled `com.docker.compose.oneoff=True`, as created by `docker compose run`, SHALL belong to its project, but SHALL NOT be a service container: it SHALL be left out of the project's RUNNING count and STATE, and project-level actions SHALL NOT act on it. Every other container of a project SHALL be a service container.

#### Scenario: One-off container is not counted
- **WHEN** project `myapp` has running service containers `web` and `db` and a running one-off container from `docker compose run web sh`
- **THEN** the projects view shows `2/2` running for `myapp`

#### Scenario: One-off container is not stopped
- **WHEN** the user stops project `myapp`, which has a running one-off container
- **THEN** the one-off container keeps running

### Requirement: Projects list
The projects view SHALL list every project, sorted by name, with the columns PROJECT (the project name), RUNNING, STATE and WORKING DIR. It SHALL update without user action when containers are created, removed or change state.

#### Scenario: Projects listed by name
- **WHEN** the engine has projects `myapp` and `blog`
- **THEN** the projects view lists `blog` then `myapp`

#### Scenario: Live update
- **WHEN** the projects view is shown and `docker compose up` creates the containers of a new project `shop`
- **THEN** a row for `shop` appears without user input

### Requirement: Running count
A project's RUNNING cell SHALL show the number of its service containers in the `running` state and the number of its service containers, as `<running>/<total>`. Paused and restarting containers SHALL NOT count as running.

#### Scenario: Partly running project
- **WHEN** project `myapp` has service containers `web` and `db` running and `worker` exited
- **THEN** its RUNNING cell shows `2/3`

#### Scenario: Paused container is not running
- **WHEN** project `myapp` has service containers `web` running and `db` paused
- **THEN** its RUNNING cell shows `1/2`

#### Scenario: Only one-off containers
- **WHEN** every container of project `tools` is a one-off container
- **THEN** its RUNNING cell shows `0/0`

### Requirement: Project state
A project's STATE SHALL be `exited` when it has no service container or every service container is exited, created or dead; otherwise `running` when every service container is running; and `partial` in every other case. The STATE cell SHALL begin with `●` for running, `◐` for partial and `○` for exited, followed by a space and the state name.

#### Scenario: Every service running
- **WHEN** every service container of `myapp` is running
- **THEN** its STATE cell reads `● running`

#### Scenario: Mixed states
- **WHEN** `myapp` has one running and one exited service container
- **THEN** its STATE cell reads `◐ partial`

#### Scenario: Every service paused
- **WHEN** every service container of `myapp` is paused
- **THEN** its STATE cell reads `◐ partial`

#### Scenario: Nothing running
- **WHEN** every service container of `blog` is exited or created
- **THEN** its STATE cell reads `○ exited`

#### Scenario: No service container
- **WHEN** every container of project `tools` is a one-off container
- **THEN** its STATE cell reads `○ exited`

### Requirement: Project rows colored by state
The projects view SHALL color each row by the project's state: running projects in the success color, partial projects in the warning color and exited projects dimmed. The selected row SHALL be rendered with the selection highlight alone, without its state color.

#### Scenario: Exited project dimmed
- **WHEN** the projects view lists `blog`, whose state is exited, and `myapp`, whose state is running
- **THEN** the `blog` row is dimmed and the `myapp` row is rendered in the success color

### Requirement: Working directory
A project's WORKING DIR cell SHALL show the `com.docker.compose.project.working_dir` label of its containers, shown as `~` when it equals the user's home directory and with the home directory replaced by `~` when it lies inside it; any other path is shown unchanged. When its containers record different directories, the cell SHALL show the one recorded by the first container in name order. When none records one, the cell SHALL show `-`.

#### Scenario: Directory under home
- **WHEN** the containers of `myapp` record the working directory `/home/me/src/myapp` and the user's home directory is `/home/me`
- **THEN** the WORKING DIR cell shows `~/src/myapp`

#### Scenario: Sibling of the home directory
- **WHEN** the user's home directory is `/home/me` and the containers of `myapp` record `/home/meow/app`
- **THEN** the WORKING DIR cell shows `/home/meow/app`

#### Scenario: No directory recorded
- **WHEN** no container of `myapp` carries the working directory label
- **THEN** the WORKING DIR cell shows `-`

### Requirement: Projects filter
The `/` filter of the projects view SHALL match the PROJECT and WORKING DIR cells, case-insensitively, as displayed.

#### Scenario: Filter by directory
- **WHEN** project `api` has the working directory `~/work/shop` and the user filters the projects view by `shop`
- **THEN** `api` is listed

### Requirement: Empty projects view
When the engine has no Compose project, the projects view SHALL show the empty-list message `No Compose projects.`.

#### Scenario: No project
- **WHEN** no container carries a Compose project label and the user opens the projects view
- **THEN** the view shows `No Compose projects.`

### Requirement: Project containers drill-down
The projects view SHALL, on Enter, open a containers view listing every container of the selected project, one-off containers included, in "all" mode, with the breadcrumb `drydock > projects > <project> > containers`. Every key of the containers view SHALL work there, and its `P` prune SHALL be limited to the project's stopped containers.

#### Scenario: Open a project's containers
- **WHEN** the user selects `myapp` and presses Enter
- **THEN** a containers view lists the containers of `myapp`, running or stopped, and the breadcrumb reads `drydock > projects > myapp > containers`

#### Scenario: Prune within a project
- **WHEN** the user presses `P` in the containers drill-down of `myapp`
- **THEN** the confirmation lists only the stopped containers of `myapp`

#### Scenario: Back to the projects view
- **WHEN** the user presses Esc in the containers drill-down of `myapp`
- **THEN** the projects view is shown again with `myapp` selected

### Requirement: Stop a project
The projects view SHALL, on `s`, when at least one service container of the selected project is running, stop every running service container of that project, all at once rather than one after another, without asking for confirmation. Paused and restarting containers SHALL be left as they are.

#### Scenario: Stop a running project
- **WHEN** `myapp` has service containers `web` and `db` running and the user presses `s` on it
- **THEN** drydock stops `web` and `db`, and when both have stopped the row shows `0/2` and `○ exited`

#### Scenario: Stop a partly running project
- **WHEN** `myapp` has `web` running and `worker` exited and the user presses `s`
- **THEN** drydock stops `web` and sends nothing for `worker`

#### Scenario: Paused container left paused
- **WHEN** `myapp` has `web` running and `db` paused and the user presses `s`
- **THEN** drydock stops `web` and `db` stays paused

### Requirement: Start a project
The projects view SHALL, on `s`, when no service container of the selected project is running and at least one is created or exited, start every created or exited service container of that project, all at once, without asking for confirmation. It SHALL NOT create containers: services whose containers do not exist SHALL stay absent.

#### Scenario: Start a stopped project
- **WHEN** every service container of `blog` is exited and the user presses `s` on it
- **THEN** drydock starts them, and when they are running the row shows `● running`

#### Scenario: Removed service is not recreated
- **WHEN** the `db` container of `blog` was removed, the remaining `web` container is exited, and the user presses `s`
- **THEN** drydock starts `web` only, and no `db` container is created

### Requirement: Start and stop unavailable
When the selected project has no running service container and no created or exited one, such as when every service container is paused, or when no project is selected, `s` SHALL send nothing to the engine and the status area SHALL state that start/stop is unavailable for that project.

#### Scenario: Every service paused
- **WHEN** every service container of `myapp` is paused and the user presses `s`
- **THEN** no action is sent to the engine and the status area states that start/stop is unavailable

### Requirement: Restart a project
The projects view SHALL, on `r`, restart every service container of the selected project that is not paused, whatever its other state, all at once, without asking for confirmation. Paused containers SHALL be left paused. When the project has no service container that is not paused, `r` SHALL send nothing to the engine and the status area SHALL state that restart is unavailable for that project.

#### Scenario: Restart a project
- **WHEN** `myapp` has `web` running and `worker` exited and the user presses `r`
- **THEN** drydock restarts `web` and `worker`, and on completion both are running

#### Scenario: Paused container not restarted
- **WHEN** `myapp` has `web` running and `db` paused and the user presses `r`
- **THEN** drydock restarts `web` and `db` stays paused

#### Scenario: Nothing to restart
- **WHEN** every service container of `myapp` is paused and the user presses `r`
- **THEN** no action is sent to the engine and the status area states that restart is unavailable

### Requirement: Project action outcome
A project action SHALL be attempted on every targeted container even when some fail. On completion the status area SHALL report success naming the project and the number of containers acted on. If any container failed, it SHALL instead report an error stating how many succeeded and failed, naming up to three failed containers and including the first failure's engine error text.

#### Scenario: Every container stopped
- **WHEN** the user stops `myapp` and its 3 running service containers stop
- **THEN** the status area reports success, naming `myapp` and 3 containers

#### Scenario: One container fails
- **WHEN** the user stops `myapp` and the engine refuses to stop `myapp-db-1` while `myapp-web-1` stops
- **THEN** `myapp-web-1` is stopped, and the status area reports an error stating 1 stopped and 1 failed, naming `myapp-db-1` and including the engine's error text

### Requirement: Project footer hints
The projects view footer SHALL label `s` by what pressing it would do: `stop` when a service container of the selected project is running, `start` when none is running and one is created or exited, and omitted otherwise. `r restart` SHALL be omitted for a project with no service container that is not paused, and `enter containers`, `s` and `r` SHALL be omitted when no project is selected.

#### Scenario: Running project
- **WHEN** a project with a running service container is selected
- **THEN** the footer shows `enter containers`, `s stop` and `r restart`

#### Scenario: Stopped project
- **WHEN** a project whose service containers are all exited is selected
- **THEN** the footer shows `s start`
