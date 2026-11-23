# Spec Delta

## Purpose

Defines how users view and operate on Docker containers in drydock: listing, lifecycle actions, deletion, logs, inspection, and opening an interactive shell inside a container.

## ADDED Requirements

### Requirement: Container list
The containers view SHALL list containers with the columns NAME, IMAGE, STATUS, and PORTS, sorted by name, showing only running containers by default.

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
