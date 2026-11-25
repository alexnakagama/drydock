# drydock

[![CI](https://github.com/alexnakagama/drydock/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/alexnakagama/drydock/actions/workflows/ci.yml)

A terminal UI for managing a local Docker engine: containers, images and
volumes, with live updates from the engine's event stream.

drydock lists, filters and acts on containers (start, stop, restart, delete,
logs, inspect, shell), images (pull, delete, inspect, prune) and volumes
(create, delete, inspect, prune). It is pre-1.0: key bindings and behavior
may still change between minor versions.

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
VERSION=0.3.0
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

On terminals at least 5 lines tall, a tab strip below the header lists the
resources as pills with their counts in parentheses, for example
`1 containers (12)`, `2 images (34)` and `3 volumes (5)`, and fills the pill
of the one the current view belongs to. On narrow terminals the tabs use
short aliases instead, for example `1 co (12)`. Counts appear once the first
snapshot has loaded.
They are engine totals: every container whatever the `a` toggle, and every
image object, dangling ones included. On the right of the strip, list views
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

Global keys and list navigation:

| Key               | Action                                                        |
|-------------------|---------------------------------------------------------------|
| `1`/`2`/`3`       | Switch to containers/images/volumes (no-op if already there)  |
| `Tab`/`Shift+Tab` | Switch to the next/previous resource                          |
| `:`               | Command bar: `containers`, `images`, `volumes` (`co`, `im`, `vol`) |
| `/`               | Filter the list (case-insensitive); Esc clears it             |
| `j`/`k`, arrows   | Move the selection; `g`/`G` jump to the first/last row        |
| `Esc`             | Clear the filter, otherwise go back to the previous view      |
| `?`               | Help                                                          |
| `q`               | Quit (Ctrl+C quits from anywhere, even inside a text input)   |

Actions in the resource views. Delete and prune ask for confirmation first:

| View       | Keys                                                               |
|------------|--------------------------------------------------------------------|
| Containers | `s` start/stop, `r` restart, `d` delete, `l` logs, `i` inspect, `x` shell, `a` all/running, `t` stats/ports |
| Images     | `Enter` containers using it, `p` pull, `d` delete, `i` inspect, `P` prune dangling, `a` all/tagged |
| Volumes    | `Enter` containers using it, `c` create, `d` delete, `i` inspect, `P` prune unused |
| Logs       | `G` follow, `/` search, `r` reopen an ended stream                 |

Flags:

| Flag       | Description                            |
|------------|----------------------------------------|
| `-version` | Print version information and exit.    |
| `-h`       | Show usage and exit.                   |

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
