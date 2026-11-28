# Spec Delta

## MODIFIED Requirements

### Requirement: Container list
The containers view SHALL list containers with the columns NAME, IMAGE, STATUS, and PORTS, sorted by name unless a stats sort order is chosen (see "Stats sort cycle"), showing only running containers by default. While stats are shown (see "Stats toggle"), the PORTS column SHALL be replaced by the CPU and MEM columns. While the Compose column is shown (see "Compose column"), it SHALL follow NAME.

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
- **WHEN** stats are shown and no container carries a Compose project label
- **THEN** the columns are NAME, IMAGE, STATUS, CPU and MEM, and no PORTS column is shown

#### Scenario: Columns with Compose containers
- **WHEN** a container carries a Compose project label and stats are not shown
- **THEN** the root containers view's columns are NAME, PROJECT, IMAGE, STATUS and PORTS

### Requirement: Stats toggle
The containers view SHALL switch between showing the PORTS column and showing the CPU and MEM stats columns when the user presses `t`. Stats SHALL be off when drydock starts. While stats are shown, the header SHALL indicate `stats`. The footer hint for `t` SHALL name what pressing it would show: `stats` while stats are off and `ports` while they are on. Turning stats off SHALL set the sort order back to name.

#### Scenario: Turn stats on
- **WHEN** stats are off and the user presses `t` in a containers view
- **THEN** the PORTS column is replaced by CPU and MEM, the header indicates `stats`, the footer hint reads `t ports`, and the rows stay sorted by name

#### Scenario: Turn stats off
- **WHEN** stats are on and the user presses `t`
- **THEN** the CPU and MEM columns are replaced by PORTS, the header no longer indicates `stats`, and the footer hint reads `t stats`

#### Scenario: Turn stats off while sorted by CPU
- **WHEN** the rows are sorted by CPU and the user presses `t`
- **THEN** stats are turned off, the rows are sorted by name, the header shows no sort indicator, and the footer hint reads `o sort cpu`

#### Scenario: Off at startup
- **WHEN** drydock starts
- **THEN** the containers view shows the PORTS column and no stats are read from the engine

#### Scenario: Help lists the toggle
- **WHEN** the user opens help in a containers view
- **THEN** the `t` key is listed

#### Scenario: Narrow footer
- **WHEN** the footer is too narrow for every hint of the containers view
- **THEN** the `o` hint is dropped first and the `t` hint next, before the other view hints, and help still lists both

## ADDED Requirements

### Requirement: Stats sort cycle
The containers view SHALL change the sort order when the user presses `o`, cycling name → CPU → MEM → name. The sort order SHALL be name when drydock starts. Choosing CPU or MEM while stats are off SHALL turn stats on, as `t` would. Choosing name SHALL leave stats as they are. The footer hint for `o` SHALL name the order the next press would choose: `sort cpu`, `sort mem` or `sort name`.

#### Scenario: Sort by CPU from the default view
- **WHEN** stats are off, the rows are sorted by name, and the user presses `o`
- **THEN** stats are turned on, the rows are sorted by CPU, and the footer hints read `o sort mem` and `t ports`

#### Scenario: CPU to MEM
- **WHEN** the rows are sorted by CPU and the user presses `o`
- **THEN** the rows are sorted by MEM and the footer hint reads `o sort name`

#### Scenario: MEM back to name
- **WHEN** the rows are sorted by MEM and the user presses `o`
- **THEN** the rows are sorted by name, stats stay on, and the footer hint reads `o sort cpu`

#### Scenario: Name order at startup
- **WHEN** drydock starts
- **THEN** the containers view is sorted by name and the footer hint reads `o sort cpu`

#### Scenario: Help lists the sort key
- **WHEN** the user opens help in a containers view
- **THEN** the `o` key is listed

### Requirement: Stats sort order
While sorted by CPU or MEM, rows with a value SHALL come first, largest first. CPU SHALL compare the value shown in the CPU cell (one decimal) and MEM the memory usage. Rows with equal values SHALL be in name order. Rows whose sorted cell shows `-` SHALL come after every row with a value, in name order.

#### Scenario: Largest CPU first
- **WHEN** the rows are sorted by CPU and `api` shows `41.2%`, `worker` `38.0%` and `db` `2.1%`
- **THEN** the rows are listed as `api`, `worker`, `db`

#### Scenario: Largest memory first
- **WHEN** the rows are sorted by MEM and `db` uses 1.2 GiB, `api` 300 MiB and `web` 20 MiB
- **THEN** the rows are listed as `db`, `api`, `web`

#### Scenario: Equal shown values use name order
- **WHEN** the rows are sorted by CPU and `web` and `cache` both show `0.0%`
- **THEN** `cache` is listed before `web`

#### Scenario: Placeholders last
- **WHEN** "all" mode is active, the rows are sorted by CPU, `old` is exited, and running `web` shows `0.0%`
- **THEN** `old` is listed after `web`

#### Scenario: MEM not reported
- **WHEN** the rows are sorted by MEM and the engine reports no memory usage for running `api`
- **THEN** `api` is listed after every container that shows a MEM value

#### Scenario: Engine connection lost
- **WHEN** the rows are sorted by CPU and the engine connection is lost
- **THEN** every stats cell shows `-` and the rows are in name order until stats values return

### Requirement: Live re-sorting
While sorted by CPU or MEM, the containers view SHALL sort the rows again each time the stats refresh, without user input. The selected container SHALL stay selected when its row moves.

#### Scenario: Rows reorder as usage changes
- **WHEN** the rows are sorted by CPU, `api` is listed above `worker`, and `worker`'s CPU usage rises above `api`'s
- **THEN** `worker` is listed above `api` within 2 seconds

#### Scenario: Selection follows the container
- **WHEN** the rows are sorted by CPU, the user has selected `api`, and `api` moves from the first row to the second
- **THEN** `api` stays selected

### Requirement: Stats sort indicator
While the rows are sorted by CPU or MEM, the containers view's header SHALL indicate `sort cpu` or `sort mem`, after the `stats` indicator. While sorted by name, the header SHALL show no sort indicator.

#### Scenario: Sorted by MEM
- **WHEN** the rows are sorted by MEM
- **THEN** the header indicates `stats` and `sort mem`

#### Scenario: Sorted by name
- **WHEN** stats are on and the rows are sorted by name
- **THEN** the header indicates `stats` and no sort indicator

### Requirement: Stats sort applies to every containers view
The sort order SHALL apply to every containers view for the rest of the session, including the root containers view, a containers view reopened with the command bar, and containers views opened by drill-down from an image or a volume. It SHALL NOT be saved between runs.

#### Scenario: Drill-down keeps the sort
- **WHEN** the rows are sorted by CPU and the user opens the containers using an image with `Enter` in the images view
- **THEN** that containers view shows stats and is sorted by CPU

#### Scenario: Reopening keeps the sort
- **WHEN** the rows are sorted by MEM, the user switches to images, and then returns with `:containers`
- **THEN** the containers view is sorted by MEM

#### Scenario: Not saved between runs
- **WHEN** the user quits while sorted by CPU and starts drydock again
- **THEN** the containers view is sorted by name and stats are off
