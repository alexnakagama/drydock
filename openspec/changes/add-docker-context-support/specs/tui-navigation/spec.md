## ADDED Requirements

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
