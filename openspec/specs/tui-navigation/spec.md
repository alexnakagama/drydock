# tui-navigation Specification

## Purpose

Defines the drydock application shell: how the user moves between full-screen resource views, issues commands, filters lists, gets help, confirms destructive actions, and sees feedback about what is happening.

## Requirements

### Requirement: Full-screen single-view layout
The application SHALL occupy the full terminal and display exactly one resource view at a time, with a header showing the current breadcrumb, a resource tab strip below the header, a main content area, and a footer showing the key hints relevant to the active view.

#### Scenario: Application starts on the containers view
- **WHEN** the user launches drydock and the Docker engine is reachable
- **THEN** the containers view is displayed full-screen with the breadcrumb `drydock > containers` in the header, the `containers` tab highlighted in the tab strip, and its key hints in the footer

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
The application SHALL show a help overlay listing the global keys and every key of the active view when the user presses `?`, and close it on `?` or Esc. Keys whose action does not apply to the current selection SHALL still be listed, visibly dimmed.

#### Scenario: Open help
- **WHEN** the user presses `?` in the images view
- **THEN** an overlay lists the global keybindings and the images view keybindings

#### Scenario: Close help
- **WHEN** the help overlay is open and the user presses `?` or Esc
- **THEN** the overlay closes and the images view is unchanged

#### Scenario: Inapplicable key is dimmed, not hidden
- **WHEN** a stopped container is selected and the user presses `?`
- **THEN** the overlay lists `x shell` dimmed alongside the other container keys

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

### Requirement: Resource tab strip
The application SHALL show a tab strip listing `containers`, `images` and `volumes` in that order, each with its 1-based number and the count of that resource in the engine, highlighting the resource the current view stack belongs to. Counts are engine totals independent of a view's mode toggle: every container, running or stopped, and every image object including dangling ones. Counts SHALL be omitted until the first engine data has loaded.

#### Scenario: Counts shown
- **WHEN** the engine has 12 containers (running or stopped), 34 images and 5 volumes
- **THEN** the tab strip shows `containers 12`, `images 34` and `volumes 5` with their numbers `1`, `2`, `3`

#### Scenario: Count independent of the running-only mode
- **WHEN** the engine has 12 containers of which 5 are running, and the containers view lists only running containers
- **THEN** the tab strip shows `containers 12` and the list position reports 5 listed rows

#### Scenario: Highlight follows the root of a drill-down
- **WHEN** the user drills from the images view into the containers using image `nginx:1.27`
- **THEN** the `images` tab remains highlighted

#### Scenario: Counts update live
- **WHEN** a new volume is created in the engine
- **THEN** the volumes count in the tab strip increases by one without user action

#### Scenario: Narrow terminal
- **WHEN** the terminal is too narrow for the full tab labels
- **THEN** the tab strip shows each resource's first short alias (`co`, `im`, `vol`), or its full name if it has none, with their numbers and counts instead

#### Scenario: Position wins over tabs when space is short
- **WHEN** the terminal is too narrow for both the tab strip and the list position indicator
- **THEN** the position indicator is shown in full and the tab strip is clipped

#### Scenario: Very short terminal
- **WHEN** the terminal is fewer than 5 lines tall
- **THEN** the tab strip is not shown, and the header, status line and footer keep the lines they had without it

### Requirement: Switching resources with tab keys
The application SHALL switch to the resource numbered `1`, `2` or `3` when that key is pressed, and to the next or previous resource (wrapping) on `Tab` or `Shift+Tab`, replacing the view stack as the command bar does. When the target is the active resource and its root view is the only view on the stack, the key SHALL do nothing. These keys SHALL be ignored while a text input is focused or an overlay is open.

#### Scenario: Jump by number
- **WHEN** the user presses `2` in the containers view
- **THEN** the images view is displayed and the breadcrumb reads `drydock > images`

#### Scenario: Cycle with Tab
- **WHEN** the volumes view is active and the user presses `Tab`
- **THEN** the containers view is displayed

#### Scenario: Cycle backwards
- **WHEN** the containers view is active and the user presses `Shift+Tab`
- **THEN** the volumes view is displayed

#### Scenario: Switch from a drill-down
- **WHEN** the user is in `drydock > containers > web > logs` and presses `3`
- **THEN** the logs view is closed and the volumes view becomes the only view on the stack

#### Scenario: Pressing the active tab's number at the root
- **WHEN** the containers view is the only view on the stack, with filter `web` and all-mode on, and the user presses `1`
- **THEN** nothing changes: the filter, selection and mode are kept

#### Scenario: Pressing the active tab's number from a drill-down
- **WHEN** the user is in `drydock > containers > web > logs` and presses `1`
- **THEN** the stack is replaced by a fresh containers root view

#### Scenario: Digits typed into a filter
- **WHEN** the user presses `/` in the images view and types `2`
- **THEN** the filter text is `2` and the view does not change

#### Scenario: Digits typed into log search
- **WHEN** the user is searching in a logs view and types `3`
- **THEN** the search text contains `3` and the view does not change

#### Scenario: Help lists the switching keys compactly
- **WHEN** the user presses `?`
- **THEN** the overlay lists the resource-switching keys as a single entry

### Requirement: State-colored rows
Resource list views SHALL color each row by its resource's state so state is visible without reading the status column: running containers in a success color; paused and restarting containers in a warning color; exited, created and dead containers, dangling images and volumes not used by any container dimmed. Other rows SHALL use the default color. The selected row SHALL be rendered with the selection highlight alone, without its state color.

#### Scenario: Running and exited containers differ
- **WHEN** the containers view lists all containers, with `web` running and `worker` exited
- **THEN** the `web` row is rendered in the success color and the `worker` row is dimmed

#### Scenario: Dangling image dimmed
- **WHEN** the images view lists all images including a dangling `<none>:<none>` image
- **THEN** the dangling row is dimmed and tagged images use the default color

#### Scenario: Unused volume dimmed
- **WHEN** the volumes view lists volume `cache` that no container mounts
- **THEN** the `cache` row is dimmed

#### Scenario: Selected row stays distinct
- **WHEN** the selection is on a dimmed row
- **THEN** the row is rendered with the selection highlight, distinguishable from unselected rows

#### Scenario: Other container states use the default color
- **WHEN** the containers view lists a container in the `removing` state
- **THEN** its row uses the default color

### Requirement: Footer hints fit the terminal width
The footer SHALL never cut a key hint in the middle. When the active view's hints and the global hints (`: command`, `? help`, `q quit`) all fit, it SHALL show them all. Otherwise it SHALL show only the view's hints in priority order, as many as fit, followed by `? more` in place of the global hints.

#### Scenario: Wide terminal
- **WHEN** the terminal is wide enough for every hint of the containers view
- **THEN** the footer shows all of them followed by the global `: command`, `? help` and `q quit` hints

#### Scenario: Narrow terminal
- **WHEN** the terminal is 60 columns wide in the containers view
- **THEN** the footer shows the highest-priority container hints that fit, ends with `? more`, shows neither `: command`, `? help` nor `q quit`, and no hint is cut off

#### Scenario: Non-list views fit too
- **WHEN** the terminal is 60 columns wide in a logs view
- **THEN** the footer ends with `? more` and no hint is cut off

### Requirement: Footer hides inapplicable actions
The footer SHALL omit hints for actions that do not apply to the selected row, and SHALL label toggle actions by what they would do. For containers, `s` SHALL read `stop` for a running container, `start` for a created or exited one, and be omitted in any other state. Pressing an omitted key SHALL behave as before.

#### Scenario: Stopped container
- **WHEN** an exited container is selected
- **THEN** the footer omits `x shell` and shows `s start`

#### Scenario: Running container
- **WHEN** a running container is selected
- **THEN** the footer shows `x shell` and `s stop`

#### Scenario: Paused container
- **WHEN** a paused container is selected
- **THEN** the footer omits both `s` and `x shell`

#### Scenario: Unused image
- **WHEN** an image used by no container is selected
- **THEN** the footer omits `enter containers`

#### Scenario: Empty list
- **WHEN** the list shows no rows
- **THEN** the footer omits every hint that acts on a selected row

#### Scenario: Hidden key still works
- **WHEN** an exited container is selected and the user presses `x`
- **THEN** the status area shows the existing error that the container must be running

### Requirement: List position indicator
Every resource list view SHALL show the selected row's position and the number of rows listed, as `<selected>/<listed>`, with `<selected>` 0 when no row is listed. Whenever a filter is applied it SHALL also show the number of rows before filtering, as `<selected>/<listed> of <total>`.

#### Scenario: Unfiltered list
- **WHEN** the containers view lists 12 rows and the third is selected
- **THEN** the position reads `3/12`

#### Scenario: Filtered list
- **WHEN** a filter narrows 12 rows to 5 and the first is selected
- **THEN** the position reads `1/5 of 12`

#### Scenario: Filter matches nothing
- **WHEN** a filter matches none of 12 rows
- **THEN** the position reads `0/0 of 12`

#### Scenario: Filter matches every row
- **WHEN** a filter matches all 12 rows and the first is selected
- **THEN** the position reads `1/12 of 12`

#### Scenario: Empty list without filter
- **WHEN** the list has no rows and no filter is applied
- **THEN** the position reads `0/0`

#### Scenario: Position follows the selection
- **WHEN** the user presses `j` with row 3 of 12 selected
- **THEN** the position reads `4/12`
