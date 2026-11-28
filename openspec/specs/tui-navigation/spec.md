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
Every resource list view SHALL support an incremental, case-insensitive text filter opened with `/`, matching against the row's displayed name and image/reference columns, and in a containers view also its PROJECT or SERVICE column when shown; Esc clears an active filter.

#### Scenario: Filter narrows the list
- **WHEN** the user presses `/` in the containers view and types `pos`
- **THEN** only rows whose name or image contains `pos` (case-insensitive) are listed, and the active filter is shown in the header

#### Scenario: Filter by Compose project
- **WHEN** the containers view shows the PROJECT column and the user filters by `myapp`
- **THEN** every container of project `myapp` is listed, whatever its name or image

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
The application SHALL require an explicit confirmation before any destructive action (delete, prune), showing what will be affected; only an explicit confirm key (`y`) proceeds, and any other key cancels. The confirmation dialog SHALL be drawn with a border in the error color, so it is visibly different from non-destructive dialogs such as text prompts and the help overlay.

#### Scenario: Confirm a delete
- **WHEN** the user requests deletion of container `web` and presses `y` in the confirmation dialog
- **THEN** the deletion is performed

#### Scenario: Cancel a delete
- **WHEN** the user requests deletion of container `web` and presses `n` or Esc in the confirmation dialog
- **THEN** no deletion is performed and the dialog closes

#### Scenario: Destructive dialog stands out
- **WHEN** the confirmation dialog for deleting container `web` is open
- **THEN** its border uses the error color, while a text prompt (for example the image pull prompt) uses the accent color

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

### Requirement: List columns fit their content
In resource list views, a column that can grow SHALL be no wider than its widest listed cell or its header, whichever is wider, and no narrower than its minimum width, except that any width left over once every such column fits its content SHALL go to the last column that can grow. Columns SHALL be re-measured whenever the listed rows change.

#### Scenario: Short names do not take extra width
- **WHEN** the containers view lists containers named `db`, `web` and `worker` on a 120-column terminal
- **THEN** the NAME column is only as wide as its minimum width, and the remaining width goes to the PORTS column, not to NAME or IMAGE

#### Scenario: Long value widens its column
- **WHEN** a container whose image reference is 40 cells wide is listed and the terminal has room
- **THEN** the IMAGE column is 40 cells wide and that reference is shown untruncated

#### Scenario: Filter re-measures columns
- **WHEN** a filter hides the only row with a long image reference
- **THEN** the IMAGE column shrinks to fit the rows still listed

### Requirement: Selected row marker
The selected row in a list view SHALL be shown with a highlighted background and a marker in the leftmost column, so the selection is visible even on a terminal without color. Unselected rows SHALL leave that column blank.

#### Scenario: Marker on the selected row
- **WHEN** the containers view lists `db`, `web` and `worker` with `web` selected
- **THEN** the `web` row starts with the marker `▌`, and the `db` and `worker` rows start with a blank column

#### Scenario: Marker follows the selection
- **WHEN** the user presses `j` with `web` selected and `worker` below it
- **THEN** the marker moves to the `worker` row

### Requirement: Container state glyphs
The containers view SHALL begin each STATUS cell with a glyph for the container's state: `●` for running, `◐` for paused or restarting, `○` for exited, created or dead, and a blank for any other state. The glyph SHALL be shown on the selected row too, where the row has no state color.

#### Scenario: Glyphs by state
- **WHEN** the containers view lists `web` (running), `frozen` (paused) and `worker` (exited)
- **THEN** their STATUS cells begin with `●`, `◐` and `○` respectively

#### Scenario: Glyph on the selected row
- **WHEN** the selection is on the running container `web`
- **THEN** its STATUS cell still begins with `●`

#### Scenario: Other states have no glyph
- **WHEN** the containers view lists a container in the `removing` state
- **THEN** its STATUS cell begins with a blank in place of a glyph

### Requirement: Dialogs fit their content
Confirmation and prompt dialogs SHALL be only as wide as their content, but no narrower than 40 cells and no wider than 60 cells, border included. They SHALL never be wider than the terminal. A prompt's title wider than the dialog SHALL be truncated with an ellipsis.

#### Scenario: Short confirmation
- **WHEN** the user requests deletion of container `web` on an 80-column terminal
- **THEN** the confirmation dialog is 40 cells wide, centered, not spanning the terminal

#### Scenario: Long content capped
- **WHEN** a confirmation lists an image reference 90 cells long on a 120-column terminal
- **THEN** the dialog is 60 cells wide and the reference is truncated with an ellipsis

#### Scenario: Narrow terminal
- **WHEN** the terminal is 30 columns wide
- **THEN** the dialog fits within 30 columns

### Requirement: Confirmation text is never cut off
A confirmation's title and body text wider than the dialog SHALL wrap at spaces, so the description of a destructive action and what it affects are not cut off. The title SHALL take at most two lines, with the rest cut with an ellipsis. A single word wider than the dialog SHALL be shortened in the middle with an ellipsis, keeping its start and end.

#### Scenario: Long sentence wraps
- **WHEN** the confirmation for deleting volume `cache-data` says `Volume cache-data (local driver) and all its data will be removed.` on an 80-column terminal
- **THEN** the dialog is 60 cells wide and the sentence is shown in full, wrapped across lines

#### Scenario: Similar long names stay distinguishable
- **WHEN** the confirmations for deleting two volumes whose names are wider than the dialog and differ only in their last characters are shown
- **THEN** each dialog shows the end of its volume's name

### Requirement: Centered empty-list message
When a resource list view has no rows to list, it SHALL show its empty-list message, including any hint such as how to show all containers, centered in the list area below the column headers.

#### Scenario: No running containers
- **WHEN** the containers view lists only running containers, none are running, and 2 are stopped
- **THEN** the message `No running containers (2 stopped; press a to show all).` is shown centered horizontally and vertically in the list area

#### Scenario: No filter matches
- **WHEN** a filter matches no rows
- **THEN** the no-match message is shown centered in the list area

### Requirement: Rule below the tab strip
On terminals at least 12 lines tall, the application SHALL draw a horizontal rule on the line below the resource tab strip, taking one line from the content area. On shorter terminals the rule SHALL NOT be drawn, and the content area SHALL keep the height it had without it.

#### Scenario: Tall terminal
- **WHEN** the terminal is 30 lines tall
- **THEN** a rule is drawn on the third line, below the tab strip, and the content area is one line shorter than it would be without the rule

#### Scenario: Short terminal
- **WHEN** the terminal is 11 lines tall
- **THEN** no rule is drawn and the content area starts directly below the tab strip

### Requirement: Colors adapt to the terminal background
The application SHALL choose its colors for the terminal's background: a palette for dark backgrounds or a palette for light backgrounds. Until the terminal reports its background, or if it never does, the dark palette SHALL be used. Changing the palette SHALL not change any text shown.

#### Scenario: Light terminal
- **WHEN** the terminal reports a light background color
- **THEN** the light palette is used for every styled element, including text inputs that were created before the background was reported (list filter, command bar), and dimmed rows remain readable against the background

#### Scenario: Terminal does not report its background
- **WHEN** the terminal does not answer the background color query
- **THEN** the dark palette is used

### Requirement: Active context in the header
When the active Docker context is not `default`, the header SHALL show `ctx:<name>` immediately before the connection state, so the engine that actions apply to is always visible. With the `default` context the header SHALL be unchanged. When the header is too narrow for both, the context indicator SHALL be dropped before the connection state is shortened to its symbol.

#### Scenario: Named context
- **WHEN** drydock runs with context `colima` on an 100-column terminal
- **THEN** the right side of the header reads `ctx:colima` followed by the connection state

#### Scenario: Default context
- **WHEN** drydock runs with the `default` context
- **THEN** the header shows no context indicator

#### Scenario: Narrow terminal
- **WHEN** drydock runs with context `colima` on a terminal too narrow for the breadcrumb, the context indicator and the full connection state
- **THEN** the context indicator is omitted and the full connection state is kept while it fits; only then is the connection state shortened to its symbol

#### Scenario: Long or unusual context name
- **WHEN** the active context's name is wider than 20 terminal cells or contains terminal control characters
- **THEN** the indicator shows the name truncated with an ellipsis and with control characters replaced, never altering the terminal

### Requirement: Colored inspection JSON
Every inspect view SHALL color its JSON by token type: object keys in the accent color, string values in the success color, numbers and the literals `true`, `false` and `null` in the warning color, and the punctuation `{ } [ ] : ,` dimmed. Coloring SHALL not change the text shown or the result of horizontal scrolling.

#### Scenario: Key and value colors differ
- **WHEN** an inspect view shows the line `  "Image": "nginx:1.27",`
- **THEN** `"Image"` is rendered in the accent color, `"nginx:1.27"` in the success color, and `:` and `,` dimmed

#### Scenario: Numbers and literals
- **WHEN** an inspect view shows the lines `"RestartCount": 0,`, `"Running": true,` and `"Args": null,`
- **THEN** `0`, `true` and `null` are rendered in the warning color

#### Scenario: Quotes and colons inside strings
- **WHEN** an inspect view shows a string value that contains `\"`, `:`, `{` or `,`
- **THEN** the whole string, through its closing quote, is rendered in the string color and nothing inside it is treated as punctuation or as a key

#### Scenario: Horizontal scrolling keeps colors aligned
- **WHEN** the user scrolls an inspect view right so that a line starts in the middle of a string value
- **THEN** the visible part of the string keeps the string color, and the visible text is the same as without coloring

#### Scenario: Every inspect view is colored
- **WHEN** the user opens the inspect view of a container, an image, a volume or a network
- **THEN** its JSON is colored by token type

### Requirement: Container state in inspection
In a container's inspect view, the string value of the `Status` key directly inside the top-level `State` object SHALL be rendered in the color the container's row takes in the containers list for that state, instead of the string color. Any other `Status` key SHALL use the string color.

#### Scenario: Running container
- **WHEN** the user inspects a running container
- **THEN** the value `"running"` of `State.Status` is rendered in the success color

#### Scenario: Exited container
- **WHEN** the user inspects an exited container
- **THEN** the value `"exited"` of `State.Status` is dimmed

#### Scenario: Other Status keys
- **WHEN** a container's inspection holds `State.Health.Status` with the value `"healthy"`
- **THEN** that value uses the string color

### Requirement: Dimmed log timestamps
The logs view SHALL dim a timestamp at the very start of a log line: an ISO 8601 date and time (date, `T` or a space, then `HH:MM:SS`, with optional fractional seconds and zone), optionally enclosed in square brackets. Text elsewhere on the line SHALL not be treated as a timestamp.

#### Scenario: RFC 3339 timestamp
- **WHEN** a log line reads `2026-11-28T10:15:02.123Z GET /health 200`
- **THEN** `2026-11-28T10:15:02.123Z` is dimmed and the rest of the line uses the default color

#### Scenario: Bracketed timestamp with a space
- **WHEN** a log line reads `[2026-11-28 10:15:02] started`
- **THEN** `[2026-11-28 10:15:02]` is dimmed

#### Scenario: Zone after a space
- **WHEN** a log line reads `[2026-11-28 10:15:02 +0100] started`
- **THEN** `[2026-11-28 10:15:02 +0100]` is dimmed

#### Scenario: Timestamp not at the start
- **WHEN** a log line reads `request at 2026-11-28T10:15:02Z failed`
- **THEN** no part of the line is dimmed as a timestamp

### Requirement: Colored log levels
The logs view SHALL color the first log-level token within the first 512 bytes of each line: `ERROR`, `ERR`, `FATAL`, `PANIC`, `CRIT` and `CRITICAL` in the error color, `WARN` and `WARNING` in the warning color, `DEBUG` and `TRACE` dimmed. `INFO` and every other word SHALL keep the default color. Only the token itself SHALL be colored, not the whole line.

#### Scenario: Uppercase level word
- **WHEN** a log line reads `2026-11-28T10:15:02Z ERROR connection refused`
- **THEN** `ERROR` is rendered in the error color and `connection refused` uses the default color

#### Scenario: Only the first level token
- **WHEN** a log line reads `WARN retrying after ERROR`
- **THEN** `WARN` is rendered in the warning color and `ERROR` uses the default color

#### Scenario: Info keeps the default color
- **WHEN** a log line reads `INFO server listening on :8080`
- **THEN** no part of the line is colored as a level

### Requirement: Log level recognition
An uppercase level word SHALL be recognized as a whole word, not as part of a longer word made of ASCII letters, digits and `_`. A level word in any other case SHALL be recognized only as the value of `level=` or `lvl=`, as a JSON `"level"` value, or alone inside square brackets.

#### Scenario: Part of a longer word
- **WHEN** a log line reads `ERRORS_TOTAL=3`
- **THEN** no part of the line is colored as a level

#### Scenario: Lowercase in prose
- **WHEN** a log line reads `finished with no error`
- **THEN** no part of the line is colored as a level

#### Scenario: Structured lowercase levels
- **WHEN** log lines read `level=error msg="boom"`, `{"level":"warn","msg":"slow"}` and `[debug] cache miss`
- **THEN** `error` is rendered in the error color, `warn` in the warning color, and `debug` dimmed

#### Scenario: Bracketed tag before the level
- **WHEN** a log line reads `[main] [error] boom`
- **THEN** `error` is rendered in the error color and `[main]` uses the default color

### Requirement: Highlighted log search matches
While a log search is active, the logs view SHALL highlight every occurrence of the search text on the shown lines, case-insensitively, with a highlight background and an underline, so matches are visible without color. A match's highlight SHALL take precedence over its timestamp or level color.

#### Scenario: Every occurrence highlighted
- **WHEN** the user searches for `err` and a shown line reads `ERR: retry after err`
- **THEN** both `ERR` and `err` are highlighted

#### Scenario: Search clears the highlight
- **WHEN** the user clears the log search with Esc
- **THEN** no text in the logs view is highlighted as a match

#### Scenario: Highlight without color
- **WHEN** `NO_COLOR` is set, or the terminal supports text attributes but not color, and a log search is active
- **THEN** each match is still underlined

### Requirement: Content coloring keeps text unchanged
Coloring in the inspect and logs views SHALL only add styling. The characters shown, line widths, truncation, horizontal scrolling and what search matches SHALL be identical to the uncolored output, on light and dark palettes and on terminals without color.

#### Scenario: Plain text identical
- **WHEN** the inspect or logs view is rendered and its styling is stripped
- **THEN** the result is identical to the same view rendered without content coloring

#### Scenario: Light palette
- **WHEN** the terminal reports a light background
- **THEN** inspect and log coloring uses the light palette's colors, and every colored token remains readable against the background
