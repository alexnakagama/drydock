## ADDED Requirements

### Requirement: Shell uses drydock's engine
When opening a shell with `x`, the application SHALL point the `docker` CLI at the same engine drydock is connected to, with the same TLS settings, whatever context or `DOCKER_HOST` the CLI would otherwise use. No context selection from the environment SHALL be passed to the CLI.

#### Scenario: Shell under a named context
- **WHEN** drydock runs with context `colima` and the user presses `x` on running container `web`
- **THEN** the shell opens in `web` on the engine of `colima`, even if `DOCKER_CONTEXT` or the CLI config names another context

#### Scenario: Shell under a TLS context
- **WHEN** drydock runs with a context whose TCP endpoint uses TLS with stored certificates, and the user presses `x` on running container `web`
- **THEN** the `docker` CLI connects to that endpoint with the context's certificates and verification setting, and the shell opens in `web`

#### Scenario: Shell under DOCKER_HOST
- **WHEN** drydock runs with the `default` context, `DOCKER_HOST` set to a TCP endpoint and `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH` set
- **THEN** the `docker` CLI connects to that endpoint with the same TLS settings, and the shell opens
