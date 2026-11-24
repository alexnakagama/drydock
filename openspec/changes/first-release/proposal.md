# Proposal

## Why

drydock is feature-complete for its first scope (containers, images, volumes, logs, exec) and its full test suite passes locally, but nothing guards that state: there is no CI, no tag has ever been cut, and the only way to get the tool is to build it from source with Go 1.27. The first release (v0.1.0) needs an automated quality gate on every change and a repeatable way to publish prebuilt binaries, so people without a Go toolchain can install it.

## What Changes

- Add a **CI workflow** (GitHub Actions) that runs on every push to `master` and every pull request:
  - formatting (`gofmt`), `go vet` (with and without the `integration` tag) and `golangci-lint`;
  - unit tests with the race detector on Linux and macOS;
  - the opt-in `integration` suite on Linux against the runner's real Docker engine;
  - a cross-compile check for every released platform.
- Add a **release workflow** triggered by pushing a `v*` tag that re-runs the checks and uses **GoReleaser** to publish a GitHub Release with per-platform archives (linux and darwin, amd64 and arm64), a SHA-256 checksums file and a changelog generated from commits.
- Add a `.goreleaser.yaml` and a `.golangci.yml` to the repository.
- **Remove the committed binary** `cmd/drydock/drydock` (11 MB) from version control and fix `.gitignore` so built binaries anywhere in the tree stay untracked.
- Update the **README**: install instructions from GitHub Releases, a CI badge, a short release procedure, and drop the stale "early development / placeholder views" wording.
- Versioning keeps using the Go toolchain's VCS build info (`drydock -version` already prints it); the release build must run from a clean checkout of the tag so the version reads `v0.1.0` and not `+dirty`.

Out of scope: Homebrew tap, Linux packages (deb/rpm), container images, signing, Windows builds (Windows is documented as unsupported).

## Capabilities

### New Capabilities

None. This change adds build, test and release tooling around the application; it does not change any user-observable behavior of drydock itself.

### Modified Capabilities

None. The change sets `skip_specs: true`.

## Impact

- New files: `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `.goreleaser.yaml`, `.golangci.yml`.
- Modified: `.gitignore`, `README.md`. Possibly small code fixes if `golangci-lint` reports issues not covered by `go vet`.
- Removed from git: `cmd/drydock/drydock`.
- GitHub repository: Actions must be enabled and the workflow token needs `contents: write` for the release job (uses the built-in `GITHUB_TOKEN`; no extra secrets).
- External dependencies at CI time: Docker Hub (`alpine:latest`, `debian:stable-slim` pulled by the integration tests), GoReleaser and golangci-lint GitHub Actions.
