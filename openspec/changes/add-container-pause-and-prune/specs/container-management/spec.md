# Spec Delta

## MODIFIED Requirements

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
- **THEN** no action is sent to the engine and the status area states that start/stop is unavailable for a paused container and that `p` unpauses it

## ADDED Requirements

### Requirement: Pause and unpause
The containers view SHALL, on `p`, pause the selected container when it is running and unpause it when it is paused, without asking for confirmation, and reflect the new state in the list when the action completes. For any other state it SHALL report that the action is unavailable without calling the engine.

#### Scenario: Pause a running container
- **WHEN** `web` is running and the user presses `p`
- **THEN** drydock pauses `web`, the status area reports success, and its row shows a paused status

#### Scenario: Unpause a paused container
- **WHEN** `web` is paused and the user presses `p`
- **THEN** drydock unpauses `web`, the status area reports success, and its row shows a running status

#### Scenario: Stopped container
- **WHEN** "all" mode is active, `old` is exited, and the user presses `p` on it
- **THEN** no action is sent to the engine and the status area states that pause/unpause is unavailable for an exited container

#### Scenario: Engine refuses to pause
- **WHEN** the engine cannot pause `web` (for example, it lacks cgroup freezer support) and the user presses `p`
- **THEN** the status area shows the engine's error and `web` stays running

#### Scenario: Stats after unpausing
- **WHEN** stats are on, `web` is paused, and the user unpauses it
- **THEN** its CPU and MEM cells show values again without the user toggling stats

### Requirement: Pause footer hint
The footer hint for `p` SHALL name what pressing it would do: `pause` on a running container and `unpause` on a paused one. In any other state, or with no container selected, the hint SHALL be omitted from the footer and listed dimmed in help.

#### Scenario: Running container selected
- **WHEN** the selected container is running
- **THEN** the footer shows `p pause`

#### Scenario: Paused container selected
- **WHEN** the selected container is paused
- **THEN** the footer shows `p unpause`

#### Scenario: Exited container selected
- **WHEN** the selected container is exited
- **THEN** the footer shows no `p` hint, and help lists `p` dimmed

### Requirement: Prune stopped containers
The containers view SHALL, when the user presses `P`, show a confirmation listing every stopped container (state `exited`, `created` or `dead`) in the view's scope, and on confirm remove exactly those containers without force, keeping their volumes, and report how many were removed.

#### Scenario: Prune stopped containers
- **WHEN** the root containers view is shown, `old` and `tmp` are exited, `web` is running, and the user presses `P`
- **THEN** the confirmation lists `old` and `tmp` and states that their volumes are kept; on confirm both are removed, `web` is kept, and the status area reports 2 containers removed

#### Scenario: Created and dead containers are included
- **WHEN** `new` is created, `broken` is dead, `web` is running, and the user presses `P`
- **THEN** the confirmation lists `new` and `broken` with their states and not `web`, and on confirm both are removed

#### Scenario: Hidden stopped containers are included
- **WHEN** running-only mode is active, `old` is exited, and the user presses `P`
- **THEN** the confirmation lists `old` even though the list does not show it

#### Scenario: Filter does not narrow the scope
- **WHEN** a `/` filter matching only `web` is applied, `old` is exited, and the user presses `P`
- **THEN** the confirmation lists `old`

#### Scenario: Drill-down scope
- **WHEN** the user opened the containers using image `nginx` by drill-down, exited container `old` uses `nginx`, exited container `other` does not, and the user presses `P`
- **THEN** the confirmation lists `old` and not `other`, and on confirm only `old` is removed

#### Scenario: Paused and restarting containers are kept
- **WHEN** `web` is paused, `worker` is restarting, and the user prunes
- **THEN** neither `web` nor `worker` is listed in the confirmation or removed

#### Scenario: Volumes are kept
- **WHEN** a pruned container mounts named volume `pgdata`
- **THEN** `pgdata` is left in place after the prune

#### Scenario: Container started after the preview
- **WHEN** the confirmation lists `old` and `old` is started before the user confirms
- **THEN** on confirm `old` is not removed, and the status area reports that its removal failed, naming `old`

#### Scenario: Container already gone
- **WHEN** the confirmation lists `old` and `old` is removed with the `docker` CLI before the user confirms
- **THEN** on confirm the status area reports it as already gone, not as a failure

#### Scenario: Nothing to prune
- **WHEN** no container in the view's scope is stopped and the user presses `P`
- **THEN** no confirmation is shown and the status area states there is nothing to prune

#### Scenario: Containers still loading
- **WHEN** the first snapshot has not loaded and the user presses `P`
- **THEN** no confirmation is shown and the status area states that containers are still loading
