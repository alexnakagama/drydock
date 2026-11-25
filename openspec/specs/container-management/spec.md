# container-management Specification

## Purpose

Defines how users view and operate on Docker containers in drydock: listing, lifecycle actions, deletion, logs, inspection, and opening an interactive shell inside a container.

## Requirements

### Requirement: Container list
The containers view SHALL list containers with the columns NAME, IMAGE, STATUS, and PORTS, sorted by name, showing only running containers by default. While stats are shown (see "Stats toggle"), the PORTS column SHALL be replaced by the CPU and MEM columns.

#### Scenario: Default list shows running containers
- **WHEN** the engine has running containers `web` and `db` and a stopped container `old`
- **THEN** the containers view lists `db` and `web` and does not list `old`

#### Scenario: Ports column
- **WHEN** container `web` publishes host port 8080 to container port 80/tcp
- **THEN** its PORTS cell shows `8080->80/tcp`

#### Scenario: Ports column deduplicates address families
- **WHEN** `web` publishes 8080->80/tcp on both IPv4 and IPv6
- **THEN** its PORTS cell shows `8080->80/tcp` once

#### Scenario: Exposed but unpublished port
- **WHEN** container `db` exposes port 5432/tcp without publishing it
- **THEN** its PORTS cell shows `5432/tcp`

#### Scenario: No containers
- **WHEN** there are no containers to show
- **THEN** the view shows an empty-state message instead of an empty table

#### Scenario: Columns while stats are shown
- **WHEN** stats are shown
- **THEN** the columns are NAME, IMAGE, STATUS, CPU and MEM, and no PORTS column is shown

### Requirement: Toggle stopped containers
The containers view SHALL toggle between running-only and all containers when the user presses `a`, and indicate the current mode in the header.

#### Scenario: Show all
- **WHEN** the user presses `a` in the running-only mode
- **THEN** stopped containers such as `old` are listed with their exited status, and the header indicates "all"

#### Scenario: Drill-down views start in all mode
- **WHEN** a containers view is opened by drill-down from an image or a volume
- **THEN** it starts in "all" mode, and `a` toggles it as usual

### Requirement: Start and stop
The containers view SHALL, on `s`, stop the selected container when it is running and start it when it is `created` or `exited`, and reflect the new state in the list when the action completes. For any other state (such as paused or restarting) it SHALL report that the action is unavailable.

#### Scenario: Stop a running container
- **WHEN** `web` is running and the user presses `s`
- **THEN** drydock stops `web`, and on completion the status area reports success; in running-only mode `web` leaves the list, and in "all" mode its row shows an exited status

#### Scenario: Start a stopped container
- **WHEN** "all" mode is active, `old` is stopped, and the user presses `s` on it
- **THEN** drydock starts `old` and its row shows a running status

#### Scenario: Paused container
- **WHEN** the selected container is paused and the user presses `s`
- **THEN** no action is sent to the engine and the status area states that start/stop is unavailable for a paused container

### Requirement: Restart
The containers view SHALL restart the selected container when the user presses `r`.

#### Scenario: Restart a container
- **WHEN** the user presses `r` on `web`
- **THEN** drydock restarts `web` and reports success in the status area

### Requirement: Delete container
The containers view SHALL delete the selected container after confirmation when the user presses `d`; for a running, paused, or restarting container the confirmation SHALL name that state and state it will be force-removed.

#### Scenario: Delete a stopped container
- **WHEN** the user presses `d` on stopped container `old` and confirms
- **THEN** `old` is removed and disappears from the list

#### Scenario: Delete a running container
- **WHEN** the user presses `d` on running container `web`
- **THEN** the confirmation states that `web` is running and will be force-removed, and on confirm the container is stopped and removed

#### Scenario: Volumes are not removed
- **WHEN** a container with named volumes is deleted
- **THEN** its named volumes are left in place

### Requirement: Container logs
The containers view SHALL open a logs view for the selected container when the user presses `l`, showing recent output (stdout and stderr) and following new output live until the view is closed.

#### Scenario: Follow logs
- **WHEN** the user opens logs for `web` and `web` writes a new line
- **THEN** the new line appears at the bottom of the logs view without user action

#### Scenario: Initial backlog is bounded
- **WHEN** the user opens logs for a container with a very long history
- **THEN** only the most recent lines (at most 1000) are loaded initially

#### Scenario: Scrolling pauses auto-follow
- **WHEN** the user scrolls up in the logs view
- **THEN** the view stops auto-scrolling until the user returns to the bottom (with `G`)

#### Scenario: Search within logs
- **WHEN** the user presses `/` in the logs view and types `error`
- **THEN** only log lines containing `error` (case-insensitive) are shown

#### Scenario: Esc clears log search first
- **WHEN** a log search is active and the user presses Esc
- **THEN** the search is cleared and the logs view stays open, still streaming

#### Scenario: Closing logs stops streaming
- **WHEN** no log search is active and the user presses Esc in the logs view
- **THEN** drydock stops reading the container's log stream and returns to the containers view

### Requirement: Container inspect
The containers view SHALL open a scrollable inspect view showing the container's full engine inspection data as pretty-printed JSON when the user presses `i`.

#### Scenario: Inspect a container
- **WHEN** the user presses `i` on `web`
- **THEN** the inspect view shows `web`'s inspection JSON, scrollable, with breadcrumb `drydock > containers > web > inspect`

### Requirement: Interactive shell
The containers view SHALL, when the user presses `x` on a running container, suspend the TUI and give the terminal to an interactive shell inside the container (trying `/bin/bash`, then `/bin/sh`), and restore the TUI in the same view when the shell exits.

#### Scenario: Shell into a container
- **WHEN** the user presses `x` on running container `web`
- **THEN** the TUI is suspended and the user is in an interactive shell inside `web`; on `exit`, the containers view is restored with `web` selected

#### Scenario: Container is not running
- **WHEN** the user presses `x` on a stopped container
- **THEN** no shell is started and the status area states the container must be running

#### Scenario: No shell available
- **WHEN** the container has neither `/bin/bash` nor `/bin/sh`
- **THEN** no shell is started, the TUI is not suspended, and the status area reports that no shell could be started

### Requirement: Stats toggle
The containers view SHALL switch between showing the PORTS column and showing the CPU and MEM stats columns when the user presses `t`. Stats SHALL be off when drydock starts. While stats are shown, the header SHALL indicate `stats`. The footer hint for `t` SHALL name what pressing it would show: `stats` while stats are off and `ports` while they are on.

#### Scenario: Turn stats on
- **WHEN** stats are off and the user presses `t` in a containers view
- **THEN** the PORTS column is replaced by CPU and MEM, the header indicates `stats`, and the footer hint reads `t ports`

#### Scenario: Turn stats off
- **WHEN** stats are on and the user presses `t`
- **THEN** the CPU and MEM columns are replaced by PORTS, the header no longer indicates `stats`, and the footer hint reads `t stats`

#### Scenario: Off at startup
- **WHEN** drydock starts
- **THEN** the containers view shows the PORTS column and no stats are read from the engine

#### Scenario: Help lists the toggle
- **WHEN** the user opens help in a containers view
- **THEN** the `t` key is listed

#### Scenario: Narrow footer
- **WHEN** the footer is too narrow for every hint of the containers view
- **THEN** the `t` hint is dropped before the other view hints, and help still lists it

### Requirement: Stats setting applies to every containers view
The stats setting SHALL apply to every containers view for the rest of the session, including the root containers view, a containers view reopened with the command bar, and containers views opened by drill-down from an image or a volume. It SHALL NOT be saved between runs.

#### Scenario: Drill-down keeps stats on
- **WHEN** stats are on and the user opens the containers using an image with `Enter` in the images view
- **THEN** that containers view shows CPU and MEM

#### Scenario: Reopening keeps stats on
- **WHEN** stats are on, the user switches to images, and then returns with `:containers`
- **THEN** the containers view shows CPU and MEM

#### Scenario: Not saved between runs
- **WHEN** the user quits with stats on and starts drydock again
- **THEN** stats are off

### Requirement: CPU column
The CPU cell SHALL show the container's CPU usage over the latest sampling interval as a percentage with one decimal, computed the way `docker stats` computes it: the change in the container's CPU time divided by the change in the host's CPU time, multiplied by the number of online CPUs and by 100. Usage across several cores SHALL add up, so values above `100.0%` are possible.

#### Scenario: One full core
- **WHEN** a container uses one full core on a 4-core host during the sampling interval
- **THEN** its CPU cell shows `100.0%`

#### Scenario: Several cores
- **WHEN** a container uses two full cores on an 8-core host during the sampling interval
- **THEN** its CPU cell shows `200.0%`

#### Scenario: Idle container
- **WHEN** a running container uses no CPU during the sampling interval
- **THEN** its CPU cell shows `0.0%`

### Requirement: MEM column
The MEM cell SHALL show the container's memory usage and memory limit, separated by ` / `, in binary units (B, KiB, MiB, GiB, TiB). Usage SHALL exclude the inactive page cache the way `docker stats` excludes it: the `inactive_file` statistic on cgroup v2 hosts, and `total_inactive_file` on cgroup v1 hosts, each subtracted only when it is smaller than the raw usage.

#### Scenario: Usage and limit
- **WHEN** a container uses 512.3 MiB after excluding inactive page cache and its limit is 2 GiB
- **THEN** its MEM cell shows `512.3MiB / 2GiB`

#### Scenario: Page cache excluded
- **WHEN** a container's raw memory usage is 900 MiB, 400 MiB of which is inactive page cache
- **THEN** its MEM cell shows a usage of `500MiB`

#### Scenario: No limit set
- **WHEN** a container has no memory limit
- **THEN** its MEM cell shows the host memory the engine reports as the limit, as `docker stats` does

### Requirement: Stats placeholders
A stats cell SHALL show `-` when there is no value to show: for a container whose state is not `running` (paused and restarting included), for a running container before its first sample with a CPU interval arrives, and for a value the engine does not report (for example memory on a host without the memory controller). While drydock is disconnected from the engine, every stats cell SHALL show `-`; once reconnected, values SHALL resume without user action.

#### Scenario: Stopped container in all mode
- **WHEN** stats are on, "all" mode is active, and `old` is exited
- **THEN** its CPU and MEM cells show `-`

#### Scenario: Paused container
- **WHEN** stats are on and a listed container is paused
- **THEN** its CPU and MEM cells show `-` and no stats are read for it

#### Scenario: First second after turning stats on
- **WHEN** the user turns stats on
- **THEN** CPU cells show `-` until the first sample with a CPU interval arrives, and then show values without user input

#### Scenario: Memory not reported
- **WHEN** the engine reports no memory usage for a running container
- **THEN** its MEM cell shows `-` and its CPU cell still shows a value

#### Scenario: Engine connection lost
- **WHEN** stats are on and the engine connection is lost
- **THEN** every CPU and MEM cell shows `-`, and once the connection is back the cells show current values again within 3 seconds

### Requirement: Live stats updates
While stats are shown, the CPU and MEM cells SHALL refresh about once per second without user input. A container that starts SHALL get values, and a container that stops SHALL show `-`, without the user toggling stats again. Refreshing SHALL keep the selected container selected.

#### Scenario: Values refresh
- **WHEN** stats are on and a container's CPU usage changes
- **THEN** its CPU cell shows the new value within 2 seconds

#### Scenario: Container started while stats are shown
- **WHEN** stats are on and a container is started with the `docker` CLI
- **THEN** its row appears and its CPU and MEM cells show values within 3 seconds

#### Scenario: Selection kept
- **WHEN** stats are on and the user has selected `web`
- **THEN** `web` stays selected as the stats refresh

### Requirement: Stats are read only while shown
drydock SHALL read stats from the engine only while a containers view with stats on is the current view, and only for the running containers that view lists, whether or not a filter hides them. When stats are turned off, or another view is shown in front of or in place of the containers view, drydock SHALL stop reading stats; when the containers view is shown again with stats on, reading SHALL resume.

#### Scenario: Stats off
- **WHEN** stats are off
- **THEN** drydock reads no stats from the engine

#### Scenario: Another resource shown
- **WHEN** stats are on and the user switches to the images view
- **THEN** drydock stops reading stats, and resumes when the user switches back to containers

#### Scenario: Logs opened on top
- **WHEN** stats are on and the user opens the logs of a container
- **THEN** drydock stops reading stats while the logs view is shown, and resumes when the user goes back

#### Scenario: Only listed containers
- **WHEN** stats are on in a drill-down view listing the 2 containers that use an image, and 10 other containers are running
- **THEN** drydock reads stats for those 2 containers only
