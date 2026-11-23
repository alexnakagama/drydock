# Spec Delta

## Purpose

Defines how users view and operate on Docker volumes in drydock: listing with usage information, creation, deletion, inspection, pruning unused volumes, and navigating to the containers that mount a volume.

## ADDED Requirements

### Requirement: Volume list
The volumes view SHALL list volumes with the columns NAME, DRIVER, MOUNTPOINT, and IN USE (number of containers, running or stopped, that mount the volume), sorted by name.

#### Scenario: Volume in use
- **WHEN** volume `pgdata` is mounted by container `db`
- **THEN** its row shows IN USE as 1

#### Scenario: Anonymous volume
- **WHEN** an anonymous volume exists
- **THEN** it is listed by its generated name like any other volume

### Requirement: Create volume
The volumes view SHALL prompt for a volume name when the user presses `c` and create a volume with the default local driver; an empty name SHALL create a volume with an engine-generated name.

#### Scenario: Create a named volume
- **WHEN** the user presses `c`, enters `cache-data`, and presses Enter
- **THEN** volume `cache-data` is created and appears in the list

#### Scenario: Name already exists
- **WHEN** the user creates a volume with the name of an existing volume
- **THEN** no create request is sent, the existing volume is unchanged, and the status area states that the volume already exists

#### Scenario: Cancel the prompt
- **WHEN** the create prompt is open and the user presses Esc
- **THEN** no volume is created

### Requirement: Delete volume
The volumes view SHALL delete the selected volume after confirmation when the user presses `d`. If the volume is in use, the confirmation SHALL name the containers mounting it and warn that removal will fail, without forcing removal.

#### Scenario: Delete unused volume
- **WHEN** the user presses `d` on unused volume `cache-data` and confirms
- **THEN** the volume is removed and disappears from the list

#### Scenario: Delete in-use volume
- **WHEN** the user presses `d` on `pgdata`, which is mounted by `db`
- **THEN** the confirmation lists `db` as using it; if confirmed, the engine's refusal is shown as an error and the volume remains

### Requirement: Volume inspect
The volumes view SHALL open a scrollable inspect view showing the volume's full engine inspection data as pretty-printed JSON when the user presses `i`.

#### Scenario: Inspect a volume
- **WHEN** the user presses `i` on `pgdata`
- **THEN** the inspect view shows its inspection JSON with breadcrumb `drydock > volumes > pgdata > inspect`

### Requirement: Prune unused volumes
The volumes view SHALL, when the user presses `P`, show a confirmation listing every volume not mounted by any container (including named volumes), and on confirm remove exactly those volumes and report how many were removed.

#### Scenario: Prune unused volumes
- **WHEN** volumes `cache-data` and `tmp1` are not mounted by any container and the user presses `P`
- **THEN** the confirmation lists `cache-data` and `tmp1`; on confirm both are removed and the status area reports 2 volumes removed

#### Scenario: In-use volumes are kept
- **WHEN** the user prunes and `pgdata` is mounted by stopped container `db`
- **THEN** `pgdata` is neither listed in the confirmation nor removed

#### Scenario: Nothing to prune
- **WHEN** every volume is in use and the user presses `P`
- **THEN** no confirmation is shown and the status area states there is nothing to prune

### Requirement: Navigate to containers using a volume
The volumes view SHALL, when the user presses Enter on a volume, push a containers view filtered to the containers (running and stopped) that mount that volume.

#### Scenario: Drill into users of a volume
- **WHEN** the user presses Enter on `pgdata`
- **THEN** a containers view listing `db` is shown with breadcrumb `drydock > volumes > pgdata > containers`, and Esc returns to the volumes view
