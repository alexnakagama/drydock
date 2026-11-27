# drydock

[![CI](https://github.com/alexnakagama/drydock/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/alexnakagama/drydock/actions/workflows/ci.yml)

A terminal UI for managing a local Docker engine: containers, images,
volumes and Compose projects, with live updates from the engine's event
stream.

drydock lists, filters and acts on containers (start, stop, pause, restart,
delete, logs, inspect, shell, prune), images (pull, delete, inspect, prune),
volumes (create, delete, inspect, prune) and Compose projects (start, stop,
restart). It is pre-1.0: key bindings and behavior may still change between
minor versions.

## Install

### Prebuilt binaries

Each [GitHub Release](https://github.com/alexnakagama/drydock/releases)
publishes a `tar.gz` archive per platform (Linux and macOS, `amd64` and
`arm64`) and a `checksums.txt` file with their SHA-256 sums. The binaries are
built with cgo disabled and need no libraries beyond the operating system.

Download an archive and the checksums file, verify the archive, and install
the binary. Set `VERSION` to the release you want (without the leading `v`),
and `OS`/`ARCH` to your platform:

```sh
VERSION=0.7.0
OS=linux      # linux or darwin
ARCH=amd64    # amd64 or arm64
archive="drydock_${VERSION}_${OS}_${ARCH}.tar.gz"
base="https://github.com/alexnakagama/drydock/releases/download/v${VERSION}"

# Each step runs only if the previous one succeeded: a failed download or a
# checksum mismatch stops before anything is installed.
# On macOS, replace `sha256sum -c -` with `shasum -a 256 -c -`.
curl -fsSLO "$base/$archive" &&
  curl -fsSLO "$base/checksums.txt" &&
  awk -v f="$archive" '$2 == f' checksums.txt | sha256sum -c - &&
  tar -xzf "$archive" drydock &&
  sudo install -m 0755 drydock /usr/local/bin/drydock &&
  drydock -version
```

If the checksum check fails, do not install the binary. The checksums detect a corrupted or tampered download, but they are
published next to the archives, so they do not prove who built the release.
Release binaries are not signed.

**macOS:** the binaries are not signed or notarized. If you downloaded the
archive with a browser, Gatekeeper quarantines the binary and refuses to run
it. After verifying the checksum and installing, remove the quarantine
attribute from the installed binary:

```sh
sudo xattr -d com.apple.quarantine /usr/local/bin/drydock
```

Downloads made with `curl` are not quarantined and need no extra step (the
command then reports that the attribute does not exist, which is harmless).

### With Go

With Go 1.27 or newer:

```sh
go install github.com/alexnakagama/drydock/cmd/drydock@latest
```

This installs into `$(go env GOPATH)/bin`, which must be on your `PATH`.

## Requirements

- **Linux or macOS.** Linux is the primary platform. On macOS, the unit
  tests run in CI, but drydock is not tested against a real engine there.
  Windows is not supported.
- **Access to a Docker engine socket.** drydock talks to the engine directly
  through the Docker Engine Go SDK, using the endpoint of the active Docker
  context (see [Docker contexts](#docker-contexts) below). With the `default`
  context, the endpoint is `DOCKER_HOST` (with `DOCKER_TLS_VERIFY` and
  `DOCKER_CERT_PATH` for TLS), or the default local socket
  `unix:///var/run/docker.sock`. Your user needs permission to use that
  socket, usually through membership in the `docker` group:

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

### Docker contexts

drydock selects the active Docker context once, at startup, with the same
precedence as the `docker` CLI:

1. the `-context <name>` flag;
2. the `DOCKER_CONTEXT` environment variable;
3. the `default` context, when `DOCKER_HOST` is set;
4. the `currentContext` in the CLI config file, `$DOCKER_CONFIG/config.json`
   (`~/.docker/config.json` when `DOCKER_CONFIG` is unset);
5. otherwise, the `default` context.

The `default` context uses `DOCKER_HOST`, `DOCKER_TLS_VERIFY` and
`DOCKER_CERT_PATH`, or the default local socket. Any other context uses the
docker endpoint, TLS certificates and TLS verification setting stored for it
by `docker context create`, under `$DOCKER_CONFIG/contexts`.

drydock exits with status 1, before connecting and without falling back to
another engine, when:

- the selected context does not exist (run `docker context ls` to list them);
- the CLI config file (read only when no flag, `DOCKER_CONTEXT` or
  `DOCKER_HOST` selects a context) or the context's stored files cannot be
  read or are malformed (the message names the file);
- the context's docker endpoint uses `ssh://`, which is not supported yet.
  Forward the remote socket instead (for example with `ssh -L`) and point
  `DOCKER_HOST` at the local end.

`-version` and `-h` never read the CLI config or the context store, so they
work even when those files are broken.

> **Behavior change:** earlier versions of drydock ignored `docker context`
> settings and used only `DOCKER_HOST` or the default socket. A
> `currentContext` set in the CLI config (for example by `docker context use`)
> is now honored. To get the previous behavior, run `drydock -context default`.

## Build and run

Running `drydock` first checks that the engine is reachable (waiting at most
5 seconds). If it cannot be reached, drydock prints the endpoint it tried and
why (engine not running, permission denied, or timeout) and exits with status
1. Otherwise it opens the full-screen terminal UI on the containers view.
`q` or Ctrl+C quits with status 0 and restores the terminal.

Building from source needs **Go 1.27** or newer. Run from a checkout:

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

## Using drydock

The header shows where you are on the left, for example
`drydock > containers`, and the connection state on the right
(`● connected`). When the active Docker context is not `default`, the
connection state is preceded by `ctx:<name>`, for example
`ctx:colima ● connected`, so you always see which engine your actions
apply to. With the `default` context there is no indicator. Names wider
than 20 terminal cells are cut with `…`, and control characters in them are
replaced, so a name can never alter the terminal. When fewer than 10
columns would be left for the breadcrumb, the context indicator is dropped
first, keeping the full connection state; only if the breadcrumb is still
that short is the state shortened to its symbol (`●`).

On terminals at least 5 lines tall, a tab strip below the header lists the
resources as pills with their counts in parentheses, for example `1
containers (12)`, `2 images (34)`, `3 volumes (5)` and `4 projects (3)`, and
fills the pill of the one the current view belongs to. On narrow terminals
the tabs use short aliases instead, for example `1 co (12)` or `4 pr (3)`.
Counts appear once the first snapshot has loaded. The container, image and
volume counts are engine totals: every container whatever the `a` toggle, and
every image object, dangling ones included. The projects count is the number
of distinct Compose project names among the containers' labels; the projects
tab is shown even when it is `(0)`. On the right of the strip, list views
show the selected row and how many rows are listed: `3/12`, or `3/5 of 12`
while a filter is applied. The number after `of` counts the rows of the
current view, so with `a` off it can be lower than the tab count. On
terminals at least 12 lines tall, a thin rule separates the tab strip from
the list.

Rows are colored by state. Running containers are green. Paused and
restarting containers are orange. Exited, created and dead containers,
dangling images and unused volumes are dimmed. The containers view also
starts each STATUS cell with a state glyph: `●` running, `◐` paused or
restarting, `○` exited, created or dead. The selected row is highlighted and
marked with `▌` in its leftmost column, without its state color; its glyph
still shows its state. Colors follow the terminal's light or dark
background, and the marker and glyphs keep the selection and states readable
without color (for example with `NO_COLOR` set).

The footer shows only the keys that apply to the selected row, most
important first. For example, `x shell` appears only on a running container,
and `s` reads `stop` on a running container, `start` on an exited or created
one, and is hidden otherwise. When the hints do not fit the terminal width,
the footer leaves out `: command` and `q quit`, replaces `? help` with
`? more`, and drops view hints from the end until the rest fits. `?` always
lists every key of the current view, with the ones that do not apply right
now dimmed.

In a containers view, `p` reads `pause` on a running container and `unpause`
on a paused one, and is hidden for any other state or when nothing is
selected.

Switching resources replaces the whole view stack, closing any drill-down
(for example the containers using an image). `Tab`/`Shift+Tab` wrap around.
Pressing a resource's number while already at its top level does nothing, so
its filter and selection are kept; `:name` always reopens it fresh. The
switching keys are ignored while typing in a filter, a prompt or the command
bar.

In a containers view, `t` replaces the PORTS column with live CPU and MEM
columns, and pressing it again brings PORTS back. Stats are off when drydock
starts, and the setting then applies to every containers view, drill-downs
included, until you quit; it is not saved. The values match `docker stats`:
CPU is summed across cores, so a container using two full cores shows
`200.0%`, and MEM is usage and limit with the inactive page cache excluded,
for example `512.3MiB / 2GiB`. Values refresh about once a second. A `-`
means there is no value to show: the container is not running (paused
containers included), its first sample has not arrived yet, the engine does
not report that value, or drydock is disconnected from the engine. Stats are
read from the engine only while a containers view with stats on is on
screen, and only for the running containers of that view, including those a
`/` filter hides. On narrow terminals, the `t` hint is the first footer
hint dropped; `?` still lists it.

The projects view (`4` or `:projects`) lists one row per Compose project,
derived from the `com.docker.compose.project` label Docker Compose puts on
every container it creates, sorted by name. drydock does not read Compose
files or run the `docker compose` CLI. The columns are PROJECT, RUNNING
(running/total service containers), STATE (`running` when every service
container is running, `exited` when every one is exited, created or dead,
`partial` otherwise, for example with one container paused) and WORKING
DIR, with your home directory shown as `~`. A project disappears once its
last container is removed. Containers created by
`docker compose run` (one-off containers) are listed in the project's
drill-down but are not counted in RUNNING or STATE and are never acted on.

`Enter` opens the project's containers, where `P` prunes only that project's
stopped containers. `s` stops the project's running service containers or,
when none is running, starts its created and exited ones; `r` restarts every
service container that is not paused. Paused containers are never touched;
press `p` on the container to unpause it first. The containers are acted on
in parallel, without confirmation; when some fail, the status line names them
and shows the first error. Start only starts containers that already exist:
unlike `docker compose up`, it does not create services whose containers were
removed, and it does not follow `depends_on` ordering.

While any container in the engine belongs to a Compose project, every
containers view shows a column after NAME: PROJECT in the root containers
view and in the image and volume drill-downs, and SERVICE in a project's
drill-down. Containers without the label show `-`. The column is shown
whatever the `a` toggle and the filter, and `/` also matches it. With no
Compose container the column is not shown.

Global keys and list navigation:

| Key               | Action                                                        |
|-------------------|---------------------------------------------------------------|
| `1`-`4`           | Switch to containers/images/volumes/projects (no-op if already there) |
| `Tab`/`Shift+Tab` | Switch to the next/previous resource                          |
| `:`               | Command bar: `containers`, `images`, `volumes`, `projects` (`co`, `im`, `vol`, `pr`) |
| `/`               | Filter the list (case-insensitive); Esc clears it             |
| `j`/`k`, arrows   | Move the selection; `g`/`G` jump to the first/last row        |
| `Esc`             | Clear the filter, otherwise go back to the previous view      |
| `?`               | Help                                                          |
| `q`               | Quit (Ctrl+C quits from anywhere, even inside a text input)   |

Actions in the resource views. Delete and prune ask for confirmation first.
Container prune removes only the stopped (exited, created or dead) containers
in the view's scope: every container in the root view, or only the containers
using that image or volume, or belonging to that project, in a drill-down,
including ones hidden by `a` or a `/` filter. Running, paused and restarting
containers are never removed, and the pruned containers' volumes are kept:

| View       | Keys                                                               |
|------------|--------------------------------------------------------------------|
| Containers | `s` start/stop, `p` pause/unpause, `r` restart, `d` delete, `l` logs, `i` inspect, `x` shell, `a` all/running, `P` prune stopped, `t` stats/ports |
| Images     | `Enter` containers using it, `p` pull, `d` delete, `i` inspect, `P` prune dangling, `a` all/tagged |
| Volumes    | `Enter` containers using it, `c` create, `d` delete, `i` inspect, `P` prune unused |
| Projects   | `Enter` containers, `s` start/stop, `r` restart                    |
| Logs       | `G` follow, `/` search, `r` reopen an ended stream                 |

Flags:

| Flag              | Description                                              |
|-------------------|----------------------------------------------------------|
| `-context <name>` | Use the named Docker context (see [Docker contexts](#docker-contexts)). |
| `-version`        | Print version information and exit.                      |
| `-h`              | Show usage and exit.                                     |

## Development

```sh
go build ./...
gofmt -l .
go vet ./...
golangci-lint run ./...
go test -race ./...
```

Linting uses [golangci-lint](https://golangci-lint.run/) v2 with the
configuration in `.golangci.yml`. CI pins the version (see
`GOLANGCI_LINT_VERSION` in `.github/workflows/ci.yml`); use the same one
locally to get the same results. To also lint the integration tests:

```sh
golangci-lint run --build-tags integration ./...
```

Integration tests run against a real engine and are opt-in, behind the
`integration` build tag. They create labeled test resources and remove them
afterwards.

```sh
go test -tags integration ./...
```

CI (`.github/workflows/ci.yml`) runs all of the above on every push to
`master` and every pull request: lint, unit tests with `-race` on Linux and
macOS, the integration tests on Linux, and a cross-compile of every release
target with GoReleaser.

## Releasing

Releases are cut from `master` by pushing a semver tag:

```sh
git switch master && git pull --ff-only
git tag vX.Y.Z && git push origin vX.Y.Z
```

The tag push starts `.github/workflows/release.yml`. It runs the full CI
suite on the tagged commit, refuses tags whose commit is not on `master`,
checks that the binary reports exactly `drydock vX.Y.Z`, and then publishes a
GitHub Release with GoReleaser (configuration in `.goreleaser.yaml`): the
four archives, `checksums.txt` and a changelog generated from the commits
since the previous tag. Tags with a pre-release suffix, such as
`v0.2.0-rc.1`, are marked as pre-releases.

If the workflow fails before the `goreleaser release` step, nothing is
published. Delete the tag
(`git tag -d vX.Y.Z && git push --delete origin vX.Y.Z`), fix the problem on
`master`, and tag again.

If it fails during or after that step, a GitHub Release may already be public.
Delete the release and its tag
(`gh release delete vX.Y.Z --cleanup-tag --yes && git tag -d vX.Y.Z`), fix the
problem, and prefer releasing the next patch version over reusing the tag:
users or the Go module proxy may already have fetched it.

To check the release configuration locally without publishing anything:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

## License

MIT. See [LICENSE](LICENSE).
