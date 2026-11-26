# Tasks

## 1. Context resolution (`internal/docker/dockerctx`)

- [x] 1.1 Create `internal/docker/dockerctx` with `doc.go`, the `Inputs` and `Endpoint` types (with `IsDefault` and `DefaultEndpoint`), and the error types `ErrNotFound`, `*FileError{Path, Err}` and `ErrSSHUnsupported` (design.md D2). Verify that `go build ./...` and `go vet ./...` pass.
- [x] 1.2 Implement the precedence in `Resolve`: `Flag`, then `DOCKER_CONTEXT`, then `default` when `DOCKER_HOST` is set, then `currentContext` from `config.json`, then `default`. The config directory is `Inputs.ConfigDir`, then `$DOCKER_CONFIG`, then `$HOME/.docker` with `HOME` read through `Inputs.Getenv`. `-context ""` counts as not given. Read `config.json` only when the precedence reaches it; a missing file means no current context. Verify with table tests in `dockerctx_test.go`, one per scenario of the "Active context selection" requirement, using `t.TempDir()` config directories and a fake `Getenv`, plus "DOCKER_HOST set and config.json malformed → default, no error" and "`HOME` from `Getenv` points at a temp dir → its `.docker` is read, never the real home directory".
- [x] 1.3 Implement reading a named context: find `contexts/meta/<hex(sha256(name))>/meta.json`, take `Endpoints.docker.Host` and `SkipTLSVerify`, set `CertDir` to `contexts/tls/<hash>/docker` when at least one of `ca.pem`, `cert.pem` or `key.pem` exists there, fill `CA`/`Cert`/`Key` with the files present, and set `TLS` to `CertDir != "" || SkipTLSVerify` (design.md D2). An empty `Host` means the default socket. A certificate without a key, or a key without a certificate, is a `*FileError` naming the file present. Before writing the code, run `docker context create t --docker host=tcp://h:2376,skip-tls-verify=true` against a temp `DOCKER_CONFIG` and confirm the layout it writes (no TLS directory, `SkipTLSVerify: true`); record the result in design.md's risks. Verify with tests for:
  - a `unix://` context (`TLS` false)
  - a `tcp://` context with a full TLS directory
  - a context with only `ca.pem`
  - `SkipTLSVerify: true` with a TLS directory, and with none (`TLS` true, `CertDir` empty)
  - `cert.pem` without `key.pem`, and the reverse (`*FileError`)
  - an empty `Host`
  - a hash computed for a name with spaces and non-ASCII characters
- [x] 1.4 Implement the failure cases from the "Context resolution failures" requirement:
  - an unknown name returns `ErrNotFound`
  - malformed or unreadable `config.json` or `meta.json`, or `meta.json` without `Endpoints.docker`, returns `*FileError` with the path
  - an `ssh://` host returns `ErrSSHUnsupported`

  Verify with a test per case using `errors.Is`/`errors.As`. Also check that `-context default` never reads the store: an unreadable config directory still resolves.

## 2. Building the client from the endpoint

- [x] 2.1 Change `docker.NewSDKService` to take a `dockerctx.Endpoint`, branching on `ep.IsDefault()` (design.md D3). The default context uses `client.FromEnv` as today. A named context uses, in this order: `WithHTTPClient` with the TLS transport when `ep.TLS`, `WithHost` only when `ep.Host != ""`, `WithAPIVersionFromEnv` (not the deprecated `WithVersionFromEnv`), and `WithUserAgent`. Add `Endpoint()` to `docker.Service` and implement it in `sdk.go` and in `dockerfake` (settable by tests, default `DefaultEndpoint()`). Update the package doc comment. Update every caller in the same task: the `NewSDKService(...)` calls in `internal/docker/sdk_integration_test.go`, `internal/ui/views/{exec,logs,stats,containers_prune}_integration_test.go` and `internal/store/store_integration_test.go`, which pass `dockerctx.DefaultEndpoint()` to keep their environment-only meaning. Verify:
  - `go build ./...` and `go vet -tags integration ./...` pass.
  - Unit tests in `internal/docker/docker_test.go` check that a named `unix://` endpoint gives `Host()` equal to it, even with `DOCKER_HOST` and `DOCKER_TLS_VERIFY` set in the test environment.
  - A named endpoint with an empty `Host` gives the platform default socket as `Host()`, even with `DOCKER_HOST` set.
  - The zero `Endpoint{}` behaves as the default context.
  - Present but unreadable or invalid pem files return an error naming the file; absent files are skipped.
  - TLS takes effect, tested against `httptest.NewTLSServer` serving a ping response: with the server's CA and `SkipTLSVerify: false`, the ping succeeds; with a different CA, it fails with a certificate error; with no CA and `SkipTLSVerify: true`, it succeeds; with a server requiring client certificates, the ping succeeds only when `Cert`/`Key` are set.
- [x] 2.2 Add `docker.ContextErrorMessage(err)` for the three resolution errors, and make `ConnectErrorMessage` name the context when it is not `default`, with the new permission hint (design.md D5). Sanitize context names and paths. Verify with tests in `internal/docker/docker_test.go` for:
  - each message's wording
  - a context name containing an escape sequence, which comes out sanitized
  - the default context, whose messages stay unchanged byte for byte against the current expected strings

## 3. Startup wiring (`cmd/drydock`)

- [x] 3.1 In `cmd/drydock/main.go`, add the `-context` flag and a `deps.resolve` function, call `Resolve` after the `-version`/`-h` returns, print `ContextErrorMessage` and exit 1 on error, and pass the endpoint to `deps.newService`, which becomes `func(dockerctx.Endpoint) (docker.Service, error)` (design.md D4). Update the usage text and the `newService` stubs in `cmd/drydock/main_test.go`. Verify with tests in `cmd/drydock/main_test.go` for:
  - `-context nope` exits 1 with the not-found message and never calls `newService`
  - `-version` with a resolver that would fail still exits 0, and the resolver is not called
  - an ssh context exits 1 with the ssh message
  - an unreachable named context's error names the context
- [x] 3.2 Update `README.md`:
  - **Requirements:** replace "drydock does not read `docker context` settings" with the precedence and the ssh limitation.
  - **Flags table:** add `-context`.
  - **Behavior change:** a note in the Requirements section that a `currentContext` set in the CLI config is now honored, and that `-context default` restores the previous behavior. The changelog is generated from commits, so the implementing commit message states this change too.

  Verify that every precedence step and error case in the README matches the spec scenarios.

## 4. Shell handoff

- [x] 4.1 In `internal/ui/views/exec.go`, make `execProbeMsg` carry the service's `Endpoint()` and `Host()`, and make `execCommand`/`execEnviron` build the CLI's environment and global flags from the table in design.md D6: unchanged for the default context; for a named context, remove `DOCKER_CONTEXT`, `DOCKER_HOST`, `DOCKER_TLS`, `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH`, set `DOCKER_CONFIG` to `Endpoint.ConfigDir` (added to `dockerctx.Endpoint` and filled by `Resolve`), and put `--context=<name>` before `exec` (revised from the `-H`/`--tls*` flags after task 4.2's CLI check; see design.md D6). Update the doc comments and the `execCommand` calls in `internal/ui/views/exec_test.go`. Verify with table tests in `exec_test.go`, one per row of the D6 table plus a CA-only named context, each with a stray `DOCKER_CONTEXT`, `DOCKER_TLS_VERIFY` and `DOCKER_CERT_PATH` in the input environment, asserting both the environment and the argument list (container ID and shell still last).
- [ ] 4.2 Check that the D6 flag handoff works with the `docker` CLI: locally, and against the CLI version CI prints in its "Docker engine info" step (`docker version`). In `exec_integration_test.go` (`-tags integration`), add a case that opens a shell through a named, non-TLS context pointing at the CI socket, while `DOCKER_CONTEXT` names a nonexistent context (and `DOCKER_HOST`, `DOCKER_CONFIG` and the store's `currentContext` point elsewhere). CI gets no other new step. Verify that the integration test passes and that design.md records the CLI versions checked.

## 5. Header indicator

- [ ] 5.1 Make `ui.New` take variadic options (`ui.New(resources, initial, opts ...Option)`) so existing callers such as `internal/ui/views/harness_test.go` compile unchanged, add the `ui.WithContext(name)` option, and render `ctx:<name>` before the connection state in `headerView`, cleaned and truncated to 20 terminal cells. When the gap is under 10 cells, drop the context first, then shorten the connection state (design.md D7). Pass `ep.Context` from `runTUI`. Verify with tests in `internal/ui/model_test.go`:
  - `colima` at 100 columns shows `ctx:colima` and `● connected`
  - `default` shows no indicator, and the existing `root_100x30.golden` is unchanged
  - at a width where only one fits, the context is dropped and the full state is kept
  - at a narrower width, only the state symbol remains
  - a 40-character name with an escape sequence comes out truncated and cleaned, and a name of wide (CJK) runes is truncated by cell width
- [ ] 5.2 Document the header indicator in the README's "Using drydock" section. Verify that the README text matches the "Active context in the header" scenarios.

## 6. Integration

- [ ] 6.1 Add an integration test (`-tags integration`) in `internal/docker/sdk_integration_test.go`. It creates a temporary `DOCKER_CONFIG` and runs `docker context create drydock-it --docker host=<the CI engine's socket>` against it, then sets `currentContext` to `drydock-it`. It checks that `Resolve` plus `NewSDKService` pings the engine, and that the store layout `docker` wrote matches what `dockerctx` reads. Verify that `go test -count=1 -tags integration ./internal/docker/...` passes locally and in CI.
- [ ] 6.2 Run the full suite: `gofmt -l .`, `go vet ./...`, `go vet -tags integration ./...`, `golangci-lint run ./...`, `golangci-lint run --build-tags integration ./...` and `go test -race ./...`. Verify that all pass with no new lint exclusions.

## Workflow follow-up

- Archive the change once it is merged and its review requirements are met.
- Before releasing, check that the generated changelog mentions the behavior change.
