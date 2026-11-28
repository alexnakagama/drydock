# Spec Delta

## MODIFIED Requirements

### Requirement: Command bar for switching resources
The application SHALL open a command bar when the user presses `:`, accept a resource command (`containers`, `images`, `volumes`, `projects`, `networks`, and the short aliases `co`, `im`, `vol`, `pr`, `net`), and on Enter replace the view stack with the requested resource view.

#### Scenario: Switch to images
- **WHEN** the user types `:images` and presses Enter
- **THEN** the images view is displayed and the breadcrumb reads `drydock > images`

#### Scenario: Alias is accepted
- **WHEN** the user types `:vol` and presses Enter
- **THEN** the volumes view is displayed

#### Scenario: Switch to projects
- **WHEN** the user types `:pr` and presses Enter
- **THEN** the projects view is displayed and the breadcrumb reads `drydock > projects`

#### Scenario: Switch to networks
- **WHEN** the user types `:net` and presses Enter
- **THEN** the networks view is displayed and the breadcrumb reads `drydock > networks`

#### Scenario: Unknown command
- **WHEN** the user types `:foo` and presses Enter
- **THEN** the current view remains displayed and an error message stating that `foo` is an unknown command is shown in the status area

#### Scenario: Cancel the command bar
- **WHEN** the command bar is open and the user presses Esc
- **THEN** the command bar closes and the current view is unchanged

### Requirement: Resource tab strip
The application SHALL show a tab strip listing `containers`, `images`, `volumes`, `projects` and `networks` in that order, each with its 1-based number and its count, highlighting the resource the current view stack belongs to. The count SHALL follow the name in parentheses, so it cannot be mistaken for the number. Container, image, volume and network counts are engine totals independent of a view's mode toggle: every container, running or stopped, and every image object including dangling ones. The projects count is the number of Compose projects drydock derives from the containers' labels. The projects tab SHALL be shown even when that count is 0. Counts SHALL be omitted until the first engine data has loaded.

#### Scenario: Counts shown
- **WHEN** the engine has 12 containers (running or stopped) belonging to 3 Compose projects, 34 images, 5 volumes and 6 networks
- **THEN** the tab strip shows `1 containers (12)`, `2 images (34)`, `3 volumes (5)`, `4 projects (3)` and `5 networks (6)`

#### Scenario: No Compose project
- **WHEN** no container carries a Compose project label
- **THEN** the tab strip shows `4 projects (0)`

#### Scenario: Counts not yet loaded
- **WHEN** drydock has started and the first engine data has not loaded
- **THEN** the tab strip shows `1 containers`, `2 images`, `3 volumes`, `4 projects` and `5 networks`, with no parentheses

#### Scenario: Count independent of the running-only mode
- **WHEN** the engine has 12 containers of which 5 are running, and the containers view lists only running containers
- **THEN** the tab strip shows `containers (12)` and the list position reports 5 listed rows

#### Scenario: Highlight follows the root of a drill-down
- **WHEN** the user drills from the images view into the containers using image `nginx:1.27`
- **THEN** the `images` tab remains highlighted

#### Scenario: Counts update live
- **WHEN** a new volume is created in the engine
- **THEN** the volumes count in the tab strip increases by one without user action

#### Scenario: Narrow terminal
- **WHEN** the terminal is too narrow for the full tab labels
- **THEN** the tab strip shows each resource's first short alias (`co`, `im`, `vol`, `pr`, `net`), or its full name if it has none, with their numbers and parenthesized counts instead, for example `1 co (12)`

#### Scenario: Position wins over tabs when space is short
- **WHEN** the terminal is too narrow for both the tab strip and the list position indicator
- **THEN** the position indicator is shown in full and the tab strip is clipped

#### Scenario: Very short terminal
- **WHEN** the terminal is fewer than 5 lines tall
- **THEN** the tab strip is not shown, and the header, status line and footer keep the lines they had without it

### Requirement: Switching resources with tab keys
The application SHALL switch to the resource numbered `1`, `2`, `3`, `4` or `5` when that key is pressed, and to the next or previous resource (wrapping) on `Tab` or `Shift+Tab`, replacing the view stack as the command bar does. When the target is the active resource and its root view is the only view on the stack, the key SHALL do nothing. These keys SHALL be ignored while a text input is focused or an overlay is open.

#### Scenario: Jump by number
- **WHEN** the user presses `2` in the containers view
- **THEN** the images view is displayed and the breadcrumb reads `drydock > images`

#### Scenario: Jump to projects
- **WHEN** the user presses `4` in the containers view
- **THEN** the projects view is displayed and the breadcrumb reads `drydock > projects`

#### Scenario: Jump to networks
- **WHEN** the user presses `5` in the containers view
- **THEN** the networks view is displayed and the breadcrumb reads `drydock > networks`

#### Scenario: Cycle with Tab
- **WHEN** the volumes view is active and the user presses `Tab`
- **THEN** the projects view is displayed

#### Scenario: Cycle from projects to networks
- **WHEN** the projects view is active and the user presses `Tab`
- **THEN** the networks view is displayed

#### Scenario: Tab wraps around
- **WHEN** the networks view is active and the user presses `Tab`
- **THEN** the containers view is displayed

#### Scenario: Cycle backwards
- **WHEN** the containers view is active and the user presses `Shift+Tab`
- **THEN** the networks view is displayed

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
Resource list views SHALL color each row by its resource's state so state is visible without reading the status column: running containers in a success color; paused and restarting containers in a warning color; exited, created and dead containers, dangling images, and volumes and networks not used by any container dimmed. Other rows SHALL use the default color. The selected row SHALL be rendered with the selection highlight alone, without its state color.

#### Scenario: Running and exited containers differ
- **WHEN** the containers view lists all containers, with `web` running and `worker` exited
- **THEN** the `web` row is rendered in the success color and the `worker` row is dimmed

#### Scenario: Dangling image dimmed
- **WHEN** the images view lists all images including a dangling `<none>:<none>` image
- **THEN** the dangling row is dimmed and tagged images use the default color

#### Scenario: Unused volume dimmed
- **WHEN** the volumes view lists volume `cache` that no container mounts
- **THEN** the `cache` row is dimmed

#### Scenario: Unused network dimmed
- **WHEN** the networks view lists network `old-net`, to which no container is attached
- **THEN** the `old-net` row is dimmed

#### Scenario: Selected row stays distinct
- **WHEN** the selection is on a dimmed row
- **THEN** the row is rendered with the selection highlight, distinguishable from unselected rows

#### Scenario: Other container states use the default color
- **WHEN** the containers view lists a container in the `removing` state
- **THEN** its row uses the default color
