## MODIFIED Requirements

### Requirement: Engine endpoint resolution
The application SHALL connect to the engine of the active Docker context, negotiating a compatible API version with the engine. The `default` context SHALL use `DOCKER_HOST` (with `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`) when set, and otherwise the platform's default local socket. Any other context SHALL use the docker endpoint, TLS material and TLS verification setting stored for it in the CLI's context store.

#### Scenario: Default socket
- **WHEN** `DOCKER_HOST` and `DOCKER_CONTEXT` are unset, no `-context` flag is given, the CLI config sets no current context, and the engine listens on the default local socket
- **THEN** drydock connects to it and displays the containers view

#### Scenario: DOCKER_HOST override
- **WHEN** `DOCKER_HOST` is set to `unix:///run/user/1000/docker.sock` and neither `DOCKER_CONTEXT` nor `-context` is given
- **THEN** drydock connects to that socket instead of the default, even if the CLI config sets a current context

#### Scenario: Context with TLS
- **WHEN** the active context has a TCP endpoint and stored CA, certificate and key files
- **THEN** drydock connects over TLS with those files, verifying the engine's certificate unless the context is set to skip verification

#### Scenario: Engine API version older than the client
- **WHEN** the engine supports an older API version than the client library's default
- **THEN** drydock negotiates down to the engine's version and operates normally

### Requirement: Startup failure when the engine is unreachable
The application SHALL verify the engine is reachable at startup and, if it is not, exit with a non-zero status and a human-readable message naming the endpoint it tried, the active context when it is not `default`, and the underlying error, without leaving the terminal in an altered state.

#### Scenario: Daemon not running
- **WHEN** drydock starts and nothing is listening on the resolved endpoint
- **THEN** it prints an error naming the endpoint and suggesting the engine may not be running, and exits with a non-zero status

#### Scenario: Permission denied on socket
- **WHEN** drydock starts and the user lacks permission to access the socket
- **THEN** it prints an error stating that permission was denied for the endpoint, suggesting the docker group, `DOCKER_HOST` or a Docker context that points at an accessible socket, and exits with a non-zero status

#### Scenario: Unreachable engine of a named context
- **WHEN** the active context is `colima` and nothing listens on its endpoint
- **THEN** the error names both the context `colima` and its endpoint

## ADDED Requirements

### Requirement: Active context selection
The application SHALL select the active Docker context with the same precedence as the `docker` CLI: the `-context` flag; the `DOCKER_CONTEXT` environment variable; `default` when `DOCKER_HOST` is set; the `currentContext` of the CLI config file in `$DOCKER_CONFIG` (default `~/.docker`); otherwise `default`. The context is selected once, at startup.

#### Scenario: Current context from the CLI config
- **WHEN** the CLI config's current context is `colima`, whose docker endpoint is `unix:///Users/me/.colima/default/docker.sock`, and no flag or environment variable selects a context or host
- **THEN** drydock connects to `unix:///Users/me/.colima/default/docker.sock`

#### Scenario: DOCKER_CONTEXT selects a context
- **WHEN** `DOCKER_CONTEXT` is `remote` and the CLI config's current context is `colima`
- **THEN** drydock connects to the endpoint of `remote`

#### Scenario: DOCKER_CONTEXT takes precedence over DOCKER_HOST
- **WHEN** `DOCKER_CONTEXT` is `colima` and `DOCKER_HOST` is also set
- **THEN** drydock connects to the endpoint of `colima`, as the `docker` CLI does

#### Scenario: Flag takes precedence over the environment
- **WHEN** drydock is started with `-context remote` while `DOCKER_CONTEXT` is `colima`
- **THEN** drydock connects to the endpoint of `remote`

#### Scenario: Explicit default context
- **WHEN** drydock is started with `-context default` and `DOCKER_HOST` is set
- **THEN** drydock connects to `DOCKER_HOST`, ignoring the CLI config's current context

#### Scenario: Custom config directory
- **WHEN** `DOCKER_CONFIG` is `/tmp/dcfg` and its config file's current context is `colima`
- **THEN** drydock reads the config file and context store under `/tmp/dcfg`, not under `~/.docker`

### Requirement: Context resolution failures
When the active context cannot be used, the application SHALL exit with status 1 before connecting and print a message naming the context and the reason, and SHALL NOT fall back to another context or endpoint. This covers an unknown context, an unreadable or malformed CLI config or context file (naming the file), and a context whose docker endpoint uses `ssh://`, which is not supported.

#### Scenario: Unknown context
- **WHEN** drydock is started with `-context nope` and no context named `nope` exists
- **THEN** it prints that context `nope` was not found, suggests `docker context ls`, and exits with status 1

#### Scenario: Malformed CLI config
- **WHEN** no flag or environment variable selects a context or host and the CLI config file is not valid JSON
- **THEN** drydock prints an error naming the config file and the parse error, and exits with status 1 without connecting to the default socket

#### Scenario: Malformed context metadata
- **WHEN** the active context's stored metadata cannot be read or parsed
- **THEN** drydock prints an error naming the context and the file, and exits with status 1

#### Scenario: ssh endpoint
- **WHEN** the active context's docker endpoint is `ssh://me@build-box`
- **THEN** drydock prints that context's ssh endpoints are not supported yet, suggests forwarding the remote socket and setting `DOCKER_HOST`, and exits with status 1

### Requirement: Informational flags do not read the context store
The `-version` and `-h` flags SHALL work without reading the CLI config file or the context store, so they succeed even when those files are broken.

#### Scenario: Version with a broken config
- **WHEN** the CLI config file is malformed and drydock is started with `-version`
- **THEN** it prints its version and exits with status 0
