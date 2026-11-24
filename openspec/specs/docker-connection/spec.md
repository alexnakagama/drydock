# docker-connection Specification

## Purpose

Defines how drydock connects to the Docker engine, how it behaves when the engine is unavailable, and how it keeps the displayed resource state current without manual refresh.

## Requirements

### Requirement: Engine endpoint resolution
The application SHALL connect to the Docker engine using the `DOCKER_HOST` environment variable when set (along with `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`), and otherwise the platform's default local socket, negotiating a compatible API version with the engine.

#### Scenario: Default socket
- **WHEN** `DOCKER_HOST` is unset and the engine listens on the default local socket
- **THEN** drydock connects to it and displays the containers view

#### Scenario: DOCKER_HOST override
- **WHEN** `DOCKER_HOST` is set to `unix:///run/user/1000/docker.sock`
- **THEN** drydock connects to that socket instead of the default

#### Scenario: Engine API version older than the client
- **WHEN** the engine supports an older API version than the client library's default
- **THEN** drydock negotiates down to the engine's version and operates normally

### Requirement: Startup failure when the engine is unreachable
The application SHALL verify the engine is reachable at startup and, if it is not, exit with a non-zero status and a human-readable message naming the endpoint it tried and the underlying error, without leaving the terminal in an altered state.

#### Scenario: Daemon not running
- **WHEN** drydock starts and nothing is listening on the resolved endpoint
- **THEN** it prints an error naming the endpoint and suggesting the engine may not be running, and exits with a non-zero status

#### Scenario: Permission denied on socket
- **WHEN** drydock starts and the user lacks permission to access the socket
- **THEN** it prints an error stating that permission was denied for the endpoint, and exits with a non-zero status

### Requirement: Live updates from engine events
The application SHALL subscribe to the engine's event stream for container, image, and volume events and update the affected resource lists automatically, so changes made outside drydock (for example via the `docker` CLI) appear without user action.

#### Scenario: Container started externally
- **WHEN** a container is started with the `docker` CLI while drydock shows the containers view
- **THEN** the container's row appears or updates to a running status within 2 seconds, without user input

#### Scenario: Volume removed externally
- **WHEN** a volume is removed with the `docker` CLI while drydock shows the volumes view
- **THEN** the volume's row disappears within 2 seconds

### Requirement: Recovery from lost engine connection
The application SHALL detect when the event stream or engine connection is lost while running, indicate a disconnected state in the header, retry the connection periodically, and on reconnect reload all resource lists and resume live updates.

#### Scenario: Engine restarts while drydock is open
- **WHEN** the Docker engine is restarted while drydock is running
- **THEN** the header shows a disconnected indicator while the engine is down, and once the engine is back the indicator clears and the lists reflect current engine state

#### Scenario: Actions while disconnected
- **WHEN** the user triggers an action while disconnected
- **THEN** the status area shows an error stating the engine is not connected, and no action is attempted
