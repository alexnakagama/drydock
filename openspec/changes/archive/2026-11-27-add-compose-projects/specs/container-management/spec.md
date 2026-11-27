# Spec Delta

## MODIFIED Requirements

### Requirement: Container list
The containers view SHALL list containers with the columns NAME, IMAGE, STATUS, and PORTS, sorted by name, showing only running containers by default. While stats are shown (see "Stats toggle"), the PORTS column SHALL be replaced by the CPU and MEM columns. While the Compose column is shown (see "Compose column"), it SHALL follow NAME.

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

## ADDED Requirements

### Requirement: Compose column
While at least one container in the engine carries a non-empty `com.docker.compose.project` label, every containers view SHALL show a Compose column: SERVICE, showing the `com.docker.compose.service` label, in a project's containers drill-down, and PROJECT, showing the project label, in every other containers view. A container without the label SHALL show `-`. Whether the column is shown SHALL NOT depend on the listed rows, the `a` mode or the filter.

#### Scenario: No Compose container
- **WHEN** no container in the engine carries a Compose project label
- **THEN** no containers view shows a PROJECT or SERVICE column

#### Scenario: Container outside any project
- **WHEN** the PROJECT column is shown and container `redis` has no project label
- **THEN** its PROJECT cell shows `-`

#### Scenario: Column stays when the Compose containers are hidden
- **WHEN** the only Compose container is exited and the root containers view lists running containers only
- **THEN** the PROJECT column is still shown

#### Scenario: Column appears with the first Compose container
- **WHEN** the containers view shows no PROJECT column and `docker compose up` creates the first Compose container
- **THEN** the PROJECT column appears without user input

#### Scenario: Service column in a project drill-down
- **WHEN** the user opens the containers of project `myapp`, whose container `myapp-web-1` is labeled with service `web`
- **THEN** the view shows a SERVICE column with `web` for `myapp-web-1` and no PROJECT column

#### Scenario: Image drill-down shows the project
- **WHEN** the PROJECT column is shown and the user opens the containers using image `nginx:1.27`
- **THEN** that drill-down shows the PROJECT column

### Requirement: Containers view fits 100 columns
On a terminal 100 columns wide, every column of a containers view SHALL be shown, none cut off at the right edge, in both the ports and the stats layouts, with or without the Compose column.

#### Scenario: Compose column with stats on a 100-column terminal
- **WHEN** the terminal is 100 columns wide, the PROJECT column is shown and stats are shown
- **THEN** the NAME, PROJECT, IMAGE, STATUS, CPU and MEM headers are all shown in full

#### Scenario: Compose column with ports on a 100-column terminal
- **WHEN** the terminal is 100 columns wide, the PROJECT column is shown and stats are not shown
- **THEN** the NAME, PROJECT, IMAGE, STATUS and PORTS headers are all shown in full
