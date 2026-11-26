# Proposal

## Why

drydock resolves the engine endpoint only from `DOCKER_HOST` and the default local socket, and ignores `docker context`. On Docker Desktop, Colima, OrbStack, Podman or any setup with a non-default context, the `docker` CLI and drydock reach different engines (or drydock reaches none), and today the README asks users to copy the context's endpoint into `DOCKER_HOST` by hand. That is the first friction most non-Linux users hit.

## What Changes

- **Context resolution**: drydock picks the engine the same way the `docker` CLI does: the `-context` flag, then `DOCKER_CONTEXT`, then `DOCKER_HOST` (which means the `default` context), then `currentContext` in the CLI config file, then `default`. The `default` context keeps today's behavior: `DOCKER_HOST`, `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`, or the platform socket.
- **Context store**: a named context is read from the CLI's context store under `$DOCKER_CONFIG` (default `~/.docker`): its docker endpoint host, its TLS material and its skip-verify setting.
- **New flag**: `-context <name>` selects a context for this run. There is no `-H` flag.
- **Clear failures**: an unknown context, an unreadable or malformed config or context file, and a context with an `ssh://` endpoint each make drydock exit with status 1 and a message naming the context (and the file, where one is at fault). drydock never falls back silently to another engine. `-version` and `-h` do not read the context store.
- **Shell handoff**: `x` hands the `docker` CLI the same endpoint and TLS settings drydock uses, so the shell opens on the same engine for TLS contexts too.
- **Header**: when the active context is not `default`, the header shows `ctx:<name>` next to the connection state, so it is always visible which engine actions apply to.
- **Messages**: startup errors name the context together with the endpoint, and the permission-denied hint mentions contexts as well as `DOCKER_HOST`.

Out of scope: `ssh://` endpoints (rejected with a hint, left for a later change), switching contexts while drydock runs, a `-H`/`-host` flag, Kubernetes or other non-docker endpoints of a context, and writing to the context store or config file.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `docker-connection`: "Engine endpoint resolution" now resolves the active context with the CLI's precedence and reads named contexts from the context store. "Startup failure when the engine is unreachable" names the context in its message. New requirements: "Active context selection" (the precedence order), "Context resolution failures" (unknown contexts, unreadable files, `ssh://` endpoints) and "Informational flags do not read the context store".
- `container-management`: adds a "Shell uses drydock's engine" requirement: the `docker` CLI opened by `x` is pointed at the same endpoint and TLS settings drydock uses. The existing "Interactive shell" requirement is unchanged.
- `tui-navigation`: a new "Active context in the header" requirement.

## Impact

- New package `internal/docker/dockerctx`: reads the CLI config and context store and resolves the active context into a name, host and TLS settings. No network access.
- `internal/docker/sdk.go`: `NewSDKService` takes the resolved endpoint instead of relying on `client.FromEnv` alone.
- `internal/docker/service.go`, `internal/docker/dockerfake/fake.go`: the service reports its resolved endpoint (`Endpoint()`) next to `Host()`.
- `internal/docker/errors.go`: messages that name the context; the permission hint.
- `cmd/drydock/main.go`: the `-context` flag, resolution before connecting, usage text.
- `internal/ui/views/exec.go`: for a named context, the shell handoff passes the endpoint and TLS settings as the CLI's `-H` and `--tls*` flags; the default context's handoff is unchanged.
- `internal/ui/render.go`: the context indicator in the header.
- `README.md`: Requirements section (contexts are now honored), the `-context` flag, and the header indicator.
- Tests: unit tests for resolution precedence, store parsing and failures against temporary config directories, the shell environment, and the header at several widths; an integration test that resolves a context pointing at the CI engine's socket.
- No new dependencies.
