# Spec Delta

## MODIFIED Requirements

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

## ADDED Requirements

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
