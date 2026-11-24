# Design

## Context

See proposal.md for motivation. Relevant current state, verified locally on 2026-11-24:

- `gofmt -l .`, `go vet ./...`, `go vet -tags integration ./...`, `go test -race ./...` and `go test -tags integration ./...` (Docker 29.10) are all clean.
- `GOOS={linux,darwin,windows} GOARCH={amd64,arm64} go build ./cmd/drydock` all succeed, so no build constraints block the targets.
- `go.mod` requires Go 1.27.0. There is no `.github/`, no tag, no lint config, and no release tooling.
- `cmd/drydock/main.go` reads the version from `debug.ReadBuildInfo()`. Since Go 1.24, `go build` in a clean checkout at an exact tag stamps `Main.Version` with that tag (for example `v0.1.0`). Builds from untagged commits get a pseudo-version, and a dirty tree adds `+dirty`. `main_test.go` already covers `-version`.
- Integration tests (`//go:build integration`) need a reachable engine, the `docker` CLI, and the `alpine:latest` and `debian:stable-slim` images (pulled if missing). They create labeled resources and clean them up.
- The README states Linux is supported, macOS is expected to work but is unverified, and Windows is unsupported.
- `cmd/drydock/drydock` is tracked. `.gitignore` has `/drydock`, which matches only the repo root.

## Goals / Non-Goals

**Goals:**
- Every push to `master` and every PR gets a pass/fail signal covering formatting, vet, lint, race-enabled unit tests, integration tests and cross-compilation.
- `git tag v0.1.0 && git push origin v0.1.0` produces a published GitHub Release with no manual steps, and only if the same checks pass for the tagged commit.
- The released binary reports the tag from `drydock -version`.

**Non-Goals:**
- Windows binaries or Windows CI.
- Homebrew, deb/rpm, container images, artifact signing or SBOMs.
- Coverage reporting or thresholds.
- Running integration tests on macOS (GitHub's macOS runners have no Docker).

## Decisions

### 1. Two workflows, with CI reusable by release

`ci.yml` triggers on `push` (branch `master`), `pull_request` and `workflow_call`. `release.yml` triggers on `push` of tags matching `v*`. It calls `ci.yml` as a reusable workflow, and its GoReleaser job `needs:` that call. A tag on a broken commit therefore never publishes.

- *Alternative:* duplicate the checks in `release.yml`. Rejected because the copies drift.
- *Alternative:* have release depend on the push's CI run with `workflow_run`. Rejected because it's harder to reason about, and tag pushes don't run the branch-filtered CI anyway.

### 2. CI job layout

```
 lint (ubuntu)            gofmt -l, go vet (+ -tags integration), golangci-lint
 test (ubuntu, macos)     go test -race ./...
 integration (ubuntu)     pre-pull images, go test -tags integration ./...
 build (ubuntu)           goreleaser build --snapshot (all release targets)
```

- The jobs run in parallel. Splitting them shows at a glance which kind of failure happened.
- Go comes from `actions/setup-go` with `go-version-file: go.mod`, so CI follows the module's Go version without a second source of truth.
- `test` runs on macOS as well as Linux, which closes part of the "macOS unverified" gap without needing Docker.
- `integration` pre-pulls `alpine:latest` and `debian:stable-slim` in a separate step. A Docker Hub hiccup then shows up as a clear pull failure instead of a test failure. The job gets a `timeout-minutes` cap.
- The `build` job runs `goreleaser build --snapshot --clean`, which cross-compiles exactly the release matrix from `.goreleaser.yaml`. That way the release config is also validated on every PR (`goreleaser check` runs too).
- `concurrency` keyed by workflow and ref, with `cancel-in-progress` for PRs, so superseded runs are cancelled.
- The default token permission is `contents: read`. Only the release job gets `contents: write`.

### 3. golangci-lint with a small, explicit config

`.golangci.yml` uses the v2 config format: the `standard` linter set (errcheck, govet, ineffassign, staticcheck, unused) plus `gofmt` as a formatter. It runs through `golangci/golangci-lint-action` with a pinned linter version, so a new release can't break CI unannounced.

- *Alternative:* a large opinionated linter set. Rejected for a first release, since it would mostly produce style churn. Linters can be added later.
- golangci-lint has never run on this code, so the first run may report findings. Those get fixed (or narrowly excluded with a reason) as part of this change.

### 4. GoReleaser configuration

- `builds`: `main: ./cmd/drydock`, `binary: drydock`, `env: [CGO_ENABLED=0]`, `goos: [linux, darwin]`, `goarch: [amd64, arm64]`, `flags: [-trimpath]`, `ldflags: [-s -w]`. Setting `ldflags` explicitly replaces GoReleaser's default `-X main.version=...`. This project has no such variable, so the default would be dead configuration.
- Versioning: no ldflags injection. The Go toolchain stamps the tag from VCS info, because GoReleaser builds in the clean tag checkout and `dist/` is gitignored. `-trimpath` doesn't affect VCS stamping.
- `archives`: `tar.gz`, named `drydock_{{ .Version }}_{{ .Os }}_{{ .Arch }}`, containing the binary, `README.md` and `LICENSE` if present.
- `checksum`: `checksums.txt` (SHA-256).
- `changelog`: generated by GitHub from commits since the previous tag, sorted ascending, with `Merge pull request` and `openspec`-only commits filtered out.
- `release`: published (not a draft) to `alexnakagama/drydock`. `prerelease: auto`, so tags like `v0.2.0-rc.1` are marked pre-release.
- The release job checks out with `fetch-depth: 0` (GoReleaser needs the tag history for the changelog) and runs `goreleaser/goreleaser-action` with a pinned major version and `args: release --clean`.

### 5. Untracking the binary

`git rm --cached cmd/drydock/drydock`, then add `/cmd/drydock/drydock` next to the existing `/drydock` in `.gitignore`.

- A bare `drydock` pattern is **not** used: it would also match the `cmd/drydock/` source directory and ignore it.
- History is not rewritten. The 11 MB blob stays in past commits, which is acceptable for a small repo and avoids a force-push.

### 6. Action versions

Actions are referenced by major version tag (`actions/checkout@v7`, `actions/setup-go@v7`, `golangci/golangci-lint-action@v9`, `goreleaser/goreleaser-action@v7`, the current majors as of 2026-11-24), and the GoReleaser and golangci-lint tool versions are pinned. Pinning by commit SHA was considered. It isn't worth it yet for a personal project with only the built-in `GITHUB_TOKEN`. Revisit if third-party secrets are added (for example a Homebrew tap token).

## Risks / Trade-offs

- [Integration tests are flaky on shared runners (timing, Docker Hub rate limits)] → Pre-pull the images in their own step and cap the job time. If flakiness shows up, the integration job can be made non-required for PRs without touching the release gate.
- [The first golangci-lint run reports many findings] → Fix them in this change. Exclude only with a comment explaining why. Don't weaken the linter set to get green.
- [The release build stamps `+dirty` or a pseudo-version instead of the tag] → GoReleaser refuses to release from a dirty tree, and `dist/` is ignored. Verify after the first release by running the downloaded binary's `-version`.
- [A tag is pushed to a commit that doesn't pass CI] → Release `needs` the reusable CI. The run fails before publishing, and the tag can be deleted and re-pushed after a fix.
- [Darwin binaries are unsigned, so Gatekeeper may quarantine them] → Document `xattr -d com.apple.quarantine drydock` in the README. Signing and notarization are out of scope.

## Migration Plan

1. Land the change on `master` through a PR and check that CI is green on the PR itself.
2. Tag `v0.1.0` on the merge commit and push the tag. Watch the release workflow.
3. Download one Linux archive, verify it against `checksums.txt`, and run `drydock -version`. Expect `drydock v0.1.0`.
4. Rollback: delete the GitHub Release and the tag (`git push --delete origin v0.1.0`), fix, and re-tag. Nobody depends on the release yet.
