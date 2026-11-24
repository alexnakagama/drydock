# Spec Delta

## MODIFIED Requirements

### Requirement: Resource tab strip
The application SHALL show a tab strip listing `containers`, `images` and `volumes` in that order, each with its 1-based number and the count of that resource in the engine, highlighting the resource the current view stack belongs to. The count SHALL follow the name in parentheses, so it cannot be mistaken for the number. Counts are engine totals independent of a view's mode toggle: every container, running or stopped, and every image object including dangling ones. Counts SHALL be omitted until the first engine data has loaded.

#### Scenario: Counts shown
- **WHEN** the engine has 12 containers (running or stopped), 34 images and 5 volumes
- **THEN** the tab strip shows `1 containers (12)`, `2 images (34)` and `3 volumes (5)`

#### Scenario: Counts not yet loaded
- **WHEN** drydock has started and the first engine data has not loaded
- **THEN** the tab strip shows `1 containers`, `2 images` and `3 volumes`, with no parentheses

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
- **THEN** the tab strip shows each resource's first short alias (`co`, `im`, `vol`), or its full name if it has none, with their numbers and parenthesized counts instead, for example `1 co (12)`

#### Scenario: Position wins over tabs when space is short
- **WHEN** the terminal is too narrow for both the tab strip and the list position indicator
- **THEN** the position indicator is shown in full and the tab strip is clipped

#### Scenario: Very short terminal
- **WHEN** the terminal is fewer than 5 lines tall
- **THEN** the tab strip is not shown, and the header, status line and footer keep the lines they had without it

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

## ADDED Requirements

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
Confirmation and prompt dialogs SHALL be only as wide as their content, but no narrower than 40 cells and no wider than 60 cells, border included. They SHALL never be wider than the terminal. Content wider than the dialog SHALL be truncated as before.

#### Scenario: Short confirmation
- **WHEN** the user requests deletion of container `web` on an 80-column terminal
- **THEN** the confirmation dialog is 40 cells wide, centered, not spanning the terminal

#### Scenario: Long content capped
- **WHEN** a confirmation lists an image reference 90 cells long on a 120-column terminal
- **THEN** the dialog is 60 cells wide and the reference is truncated with an ellipsis

#### Scenario: Narrow terminal
- **WHEN** the terminal is 30 columns wide
- **THEN** the dialog fits within 30 columns

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
