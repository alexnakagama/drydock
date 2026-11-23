# Spec Delta

## Purpose

Defines the drydock application shell: how the user moves between full-screen resource views, issues commands, filters lists, gets help, confirms destructive actions, and sees feedback about what is happening.

## ADDED Requirements

### Requirement: Full-screen single-view layout
The application SHALL occupy the full terminal and display exactly one resource view at a time, with a header showing the current breadcrumb, a main content area, and a footer showing the key hints relevant to the active view.

#### Scenario: Application starts on the containers view
- **WHEN** the user launches drydock and the Docker engine is reachable
- **THEN** the containers view is displayed full-screen with the breadcrumb `drydock > containers` in the header and its key hints in the footer

#### Scenario: Terminal resize
- **WHEN** the terminal window is resized
- **THEN** the layout re-renders to the new dimensions without losing the current view, selection, or filter

### Requirement: Command bar for switching resources
The application SHALL open a command bar when the user presses `:`, accept a resource command (`containers`, `images`, `volumes`, and the short aliases `co`, `im`, `vol`), and on Enter replace the view stack with the requested resource view.

#### Scenario: Switch to images
- **WHEN** the user types `:images` and presses Enter
- **THEN** the images view is displayed and the breadcrumb reads `drydock > images`

#### Scenario: Alias is accepted
- **WHEN** the user types `:vol` and presses Enter
- **THEN** the volumes view is displayed

#### Scenario: Unknown command
- **WHEN** the user types `:foo` and presses Enter
- **THEN** the current view remains displayed and an error message stating that `foo` is an unknown command is shown in the status area

#### Scenario: Cancel the command bar
- **WHEN** the command bar is open and the user presses Esc
- **THEN** the command bar closes and the current view is unchanged

### Requirement: Drill-down view stack
The application SHALL maintain a stack of views: opening a detail or related view (logs, inspect, related containers) pushes onto the stack, and pressing Esc pops back to the previous view, restoring its selection and filter. When the active view has a filter applied, Esc clears the filter first instead of popping.

#### Scenario: Drill into logs and back
- **WHEN** the user opens the logs of container `web` from the containers view and then presses Esc
- **THEN** the containers view is displayed again with `web` still selected

#### Scenario: Breadcrumb reflects depth
- **WHEN** the user drills into the logs of container `web`
- **THEN** the breadcrumb reads `drydock > containers > web > logs`

#### Scenario: Esc at the root view
- **WHEN** the stack holds only a single root view and the user presses Esc with no filter active
- **THEN** nothing happens and the application does not exit

### Requirement: List filtering
Every resource list view SHALL support an incremental, case-insensitive text filter opened with `/`, matching against the row's displayed name and image/reference columns; Esc clears an active filter.

#### Scenario: Filter narrows the list
- **WHEN** the user presses `/` in the containers view and types `pos`
- **THEN** only rows whose name or image contains `pos` (case-insensitive) are listed, and the active filter is shown in the header

#### Scenario: Clear the filter
- **WHEN** a filter is active and the user presses Esc
- **THEN** the filter is cleared and all rows are listed again

### Requirement: Keyboard navigation in lists
Every list view SHALL let the user move the selection with the arrow keys and `j`/`k`, jump to the first and last row with `g`/`G`, and keep the selection on the same resource when the list is refreshed.

#### Scenario: Selection survives refresh
- **WHEN** container `db` is selected and a new container is created that sorts above it
- **THEN** after the list refreshes the selection remains on `db`

#### Scenario: Selected resource disappears
- **WHEN** the selected resource is removed from the engine
- **THEN** the selection moves to the nearest remaining row, or the view shows an empty-state message if no rows remain

### Requirement: Help overlay
The application SHALL show a help overlay listing the global keys and the keys of the active view when the user presses `?`, and close it on `?` or Esc.

#### Scenario: Open help
- **WHEN** the user presses `?` in the images view
- **THEN** an overlay lists the global keybindings and the images view keybindings

#### Scenario: Close help
- **WHEN** the help overlay is open and the user presses `?` or Esc
- **THEN** the overlay closes and the images view is unchanged

### Requirement: Confirmation for destructive actions
The application SHALL require an explicit confirmation before any destructive action (delete, prune), showing what will be affected; only an explicit confirm key (`y`) proceeds, and any other key cancels.

#### Scenario: Confirm a delete
- **WHEN** the user requests deletion of container `web` and presses `y` in the confirmation dialog
- **THEN** the deletion is performed

#### Scenario: Cancel a delete
- **WHEN** the user requests deletion of container `web` and presses `n` or Esc in the confirmation dialog
- **THEN** no deletion is performed and the dialog closes

### Requirement: Status and error feedback
The application SHALL report the outcome of every user-triggered action in a status area: a transient success message on completion, and an error message containing the engine's error text on failure, without crashing or leaving the current view.

#### Scenario: Action fails
- **WHEN** the user stops a container and the engine returns an error
- **THEN** the status area shows an error including the engine's message, and the view stays usable

#### Scenario: Long-running action in progress
- **WHEN** an action takes longer than an instant (for example, pulling an image)
- **THEN** the status area shows an in-progress indicator until the action completes or fails, and the UI remains responsive

### Requirement: Quit
The application SHALL exit cleanly and restore the terminal when the user presses `q` (outside text input) or Ctrl+C.

#### Scenario: Quit from any view
- **WHEN** the user presses `q` while no text input is focused
- **THEN** the application exits with status 0 and the terminal is restored to its previous state

#### Scenario: Ctrl+C inside an overlay
- **WHEN** a confirmation dialog, prompt, or the command bar is open and the user presses Ctrl+C
- **THEN** the application exits with status 0 and the terminal is restored

### Requirement: Text input takes precedence over global keys
While a text input is focused (command bar, prompt, list filter, or log search), the application SHALL treat every printable key, including `q`, `:`, and `?`, as text for that input rather than as a global command.

#### Scenario: Typing global keys into a filter
- **WHEN** the user presses `/` in the images view and types `quay?`
- **THEN** the filter text is `quay?`, the application does not quit, and the help overlay does not open
