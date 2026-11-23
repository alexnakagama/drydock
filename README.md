# drydock

A terminal UI for managing a local Docker engine: containers, images and
volumes, with live updates from the engine's event stream.

> Status: early development. The project skeleton is in place; the TUI is
> being built incrementally (see `openspec/changes/bootstrap-drydock-tui`).

## Requirements

- **Go 1.27** or newer, to build from source.
- **Linux.** macOS is expected to work but is not verified. Windows is not
  supported.
- **Access to a Docker engine socket.** drydock talks to the engine directly
  through the Docker Engine Go SDK. The endpoint is resolved from
  `DOCKER_HOST` (with `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH` for TLS), or
  the default local socket `unix:///var/run/docker.sock`. Your user needs
  permission to use that socket, usually through membership in the `docker`
  group:

  ```sh
  sudo usermod -aG docker "$USER"   # then log out and back in
  ```

  Membership in the `docker` group is equivalent to root access on the
  host. For a less privileged setup, use
  [rootless Docker](https://docs.docker.com/engine/security/rootless/) and
  point `DOCKER_HOST` at its socket:

  ```sh
  export DOCKER_HOST="unix://$XDG_RUNTIME_DIR/docker.sock"
  ```
- **The `docker` CLI on `PATH`**, only for the exec-shell feature. drydock
  hands the terminal to `docker exec -it` to open a shell in a container.
  Everything else works without the CLI.

drydock does not read `docker context` settings. If you use a non-default
context, set `DOCKER_HOST` to that context's endpoint.

## Build and run

Until the TUI lands, `drydock` only supports `-version` and `-h`. Running it
without flags prints "the terminal UI is not implemented yet" and exits with
status 1.

Run from a checkout:

```sh
go run ./cmd/drydock
```

Build a binary:

```sh
go build -o drydock ./cmd/drydock
./drydock
```

Install into `$(go env GOPATH)/bin`:

```sh
go install ./cmd/drydock
```

Flags:

| Flag       | Description                            |
|------------|----------------------------------------|
| `-version` | Print version information and exit.    |
| `-h`       | Show usage and exit.                   |

## Development

```sh
go build ./...
go vet ./...
go test -race ./...
```

Integration tests run against a real engine and are opt-in, behind the
`integration` build tag. They create labeled test resources and remove them
afterwards. (They arrive with the Docker service layer; until then this
command runs only the unit tests.)

```sh
go test -tags integration ./...
```
