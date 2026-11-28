# Spec Delta

## ADDED Requirements

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
