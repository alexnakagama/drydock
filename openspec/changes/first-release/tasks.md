# Tasks

## 1. Repository hygiene

- [x] 1.1 Untrack the committed binary with `git rm --cached cmd/drydock/drydock` and add `/cmd/drydock/drydock` to `.gitignore` (not a bare `drydock` pattern). Verify: `git ls-files | grep -c 'cmd/drydock/drydock$'` prints 0, `go build -o cmd/drydock/drydock ./cmd/drydock && git status --porcelain` shows nothing, and `git check-ignore cmd/drydock/main.go` reports the source file is not ignored

## 2. Linting

- [ ] 2.1 Add `.golangci.yml` (v2 format, `standard` linters plus the `gofmt` formatter) as described in design.md §3. Verify: `golangci-lint config verify` succeeds
- [ ] 2.2 Run `golangci-lint run ./...` and `golangci-lint run --build-tags integration ./...` locally. Fix every finding, or exclude it narrowly with a comment giving the reason. Verify: both commands exit 0 and `go test -race ./...` still passes

## 3. CI workflow

- [ ] 3.1 Add `.github/workflows/ci.yml` with `push` (master), `pull_request` and `workflow_call` triggers, `contents: read` permissions and per-ref concurrency, and the `lint`, `test` (ubuntu + macOS, `-race`) and `integration` (ubuntu, pre-pulled `alpine:latest`/`debian:stable-slim`, timeout) jobs from design.md §2, all using `setup-go` with `go-version-file: go.mod`. Verify: `actionlint` (or the GitHub workflow editor) reports no errors
- [ ] 3.2 Add the `build` job running `goreleaser check` and `goreleaser build --snapshot --clean` (needs task 4.1). Verify: the same commands succeed locally and produce 4 binaries under `dist/`

## 4. Release configuration

- [ ] 4.1 Add `.goreleaser.yaml` with the builds, archives, checksum, changelog and release settings from design.md §4 (linux/darwin × amd64/arm64, `CGO_ENABLED=0`, `-trimpath`, `-s -w`, no `-X` injection). Verify: `goreleaser check` passes and `goreleaser release --snapshot --clean` produces 4 `tar.gz` archives plus `checksums.txt`, and the extracted linux/amd64 binary runs `-version` and prints a `drydock v…` line
- [ ] 4.2 Add `.github/workflows/release.yml`, triggered on `v*` tags, that calls `ci.yml` as a reusable workflow and then runs a `release` job (`needs` CI, `contents: write`, checkout with `fetch-depth: 0`, `goreleaser-action` with a pinned version and `release --clean`, `GITHUB_TOKEN`). Verify: `actionlint` reports no errors

## 5. Documentation

- [ ] 5.1 Update `README.md`: add a CI status badge, an "Install" section (download from GitHub Releases, verify with `checksums.txt`, the macOS quarantine note, plus `go install github.com/alexnakagama/drydock/cmd/drydock@latest`), refresh the stale status and "placeholder views" wording, and add a short "Releasing" section (`git tag vX.Y.Z && git push origin vX.Y.Z`). Under Development, list `golangci-lint run`. Verify: every command in the new sections runs as written, except the release push

## 6. End-to-end verification

- [ ] 6.1 Push the branch, open a PR, and confirm all CI jobs (lint, test ubuntu/macOS, integration, build) are green. Verify: the PR checks page shows every job passing
- [ ] 6.2 After merge, tag `v0.1.0` on `master` and push the tag. Confirm the release workflow publishes a GitHub Release with 4 archives, `checksums.txt` and a changelog. Verify: download the linux/amd64 archive, `sha256sum -c` against `checksums.txt` succeeds, and `./drydock -version` prints `drydock v0.1.0`

## Workflow follow-up

- Archive the change with `/opsx:archive` once v0.1.0 is published and verified.
