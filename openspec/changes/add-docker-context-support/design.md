# Design

## Context

See proposal.md for motivation and the specs in this change for the required behavior. The current code shapes the approach in these ways:

- **The client is built from the environment alone.** `docker.NewSDKService` calls `client.New(client.FromEnv, ...)`. `FromEnv` reads `DOCKER_HOST`, `DOCKER_TLS_VERIFY`/`DOCKER_CERT_PATH` and `DOCKER_API_VERSION` together. The only dependency is `moby/moby/client`; there is no `docker/cli` module.
- **The endpoint travels as `Service.Host()`.** It is used to redact credentials from errors (`store.go`, `main.go`), in `ConnectErrorMessage`, and by the shell handoff in `views/exec.go`. The handoff builds the CLI's environment with `execEnviron`: it removes `DOCKER_HOST` and `DOCKER_CONTEXT` and sets `DOCKER_HOST` back to `Host()`. TLS settings are inherited from the environment unchanged, which is only right while TLS comes from the environment.
- **`cmd/drydock/main.go` injects the service.** `deps.newService` builds the service without connecting, so tests can swap in the fake. The flags are parsed before `newService` runs, so `-version` and `-h` already return before any engine setup.
- **The header's right side is only the connection state.** `headerView` renders it at full width, and shrinks it to its symbol when the gap left for the breadcrumb drops below 10 cells.

The CLI's context store, as written by `docker context create`:

```
$DOCKER_CONFIG (default ~/.docker)
|-- config.json                          {"currentContext": "colima", ...}
`-- contexts/
    |-- meta/<hex(sha256(name))>/meta.json
    |      {"Name": "colima",
    |       "Metadata": {...},
    |       "Endpoints": {"docker": {"Host": "unix:///...", "SkipTLSVerify": false}}}
    `-- tls/<hex(sha256(name))>/docker/{ca.pem, cert.pem, key.pem}
```

## Goals / Non-Goals

**Goals:**
- The `default` context behaves exactly as drydock does today, including `DOCKER_API_VERSION`.
- Resolution is a pure function of its inputs (flag, environment, files), testable with a temporary directory and no engine.
- Everything that talks to an engine (the SDK client, error redaction, the shell handoff) uses one resolved endpoint, so they cannot drift apart.

**Non-Goals:**
- A dialer for `ssh://`, or any process started to reach the engine.
- Reading context fields other than the docker endpoint (`Metadata`, Kubernetes endpoints, descriptions).
- Watching the config for changes while drydock runs.

## Decisions

### D1. A small reader in `internal/docker/dockerctx`, not `docker/cli`

`dockerctx` reads `config.json` and one context's `meta.json` and TLS directory with `encoding/json` and `os`. The format has been stable since contexts arrived in Docker 19.03.

- *Alternative: import `github.com/docker/cli/cli/context/store`.* It brings in a large dependency tree that overlaps with, and can conflict with, `moby/moby/client`, and it grows the binary, for two JSON files.

### D2. `Resolve` returns an `Endpoint`, and an error naming what failed

```go
type Inputs struct {
	Flag      string              // -context; "" means not given
	Getenv    func(string) string // also supplies HOME for ~/.docker, so tests are hermetic
	ConfigDir string              // "" means $DOCKER_CONFIG, then $HOME/.docker
}

type Endpoint struct {
	Context       string // "default" or the context's name; "" is treated as "default"
	Host          string // "" means the platform's default socket (and always "" for default)
	TLS           bool   // CertDir != "" || SkipTLSVerify
	CertDir       string // TLS directory, "" if the context has no TLS files
	CA, Cert, Key string // paths of the pem files present in CertDir, "" if absent
	SkipTLSVerify bool
	ConfigDir     string // absolute config directory the context was read from, "" for default
}

func (e Endpoint) IsDefault() bool // Context == "" || Context == "default"
func DefaultEndpoint() Endpoint    // Endpoint{Context: "default"}

func Resolve(in Inputs) (Endpoint, error)
```

Precedence follows the spec's "Active context selection" requirement. Name hashing is `hex(sha256(name))`, as the CLI does. Errors are typed so `main` and the tests can tell them apart: `ErrNotFound` (no meta directory for the name), `*FileError{Path, Err}` (unreadable or malformed file), and `ErrSSHUnsupported`. Errors about a named context are wrapped in `*ContextError{Name, Err}`, so the messages can name the context while `errors.Is`/`errors.As` still see the three types; errors from `config.json` come before any context is known and stay a bare `*FileError`. `config.json` is read only when the precedence reaches it: with `-context`, `DOCKER_CONTEXT` or `DOCKER_HOST` set, a broken config file does not matter, as with the CLI. A missing `config.json` is not an error; it means no current context. The home directory is `USERPROFILE` then `HOME` on Windows and `HOME` elsewhere, as the CLI does, then the user database; when none is found and the store is needed, `Resolve` returns a `*FileError` instead of assuming no current context. The config directory is made absolute once, so the TLS paths handed to the shell stay valid. A dangling symlink, a non-regular file, or a meta directory without `meta.json` is a `*FileError`, not a missing file, and files are opened without blocking so a FIFO cannot hang startup.

A context whose docker endpoint `Host` is empty means the platform's default socket, as with the CLI. A context with `SkipTLSVerify: true` and no TLS files still uses TLS, without verification, as `docker context create --docker host=tcp://h:2376,skip-tls-verify=true` expects; hence the separate `TLS` field. A certificate without a key, or a key without a certificate, is a `*FileError` naming the file that is present. Absent pem files are otherwise skipped: a CA alone means server verification without a client certificate. `-context ""` is the same as not passing the flag.

### D3. Build the SDK client from the endpoint

`NewSDKService(userAgent string, ep dockerctx.Endpoint)`:

The branch is on `ep.IsDefault()`, so a zero `Endpoint{}` keeps today's behavior.

- **default context:** `client.FromEnv`, as today. This keeps `DOCKER_HOST`, the TLS environment variables and `DOCKER_API_VERSION` exactly as they behave now.
- **named context:** never `FromEnv`, which would apply `DOCKER_HOST` and `DOCKER_TLS_VERIFY`/`DOCKER_CERT_PATH` from the environment to a context that has its own settings. The options, in this order (order matters, because `WithHost` configures whatever transport is current, and `WithHTTPClient` replaces it):
  1. when `ep.TLS`: `client.WithHTTPClient(&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}, CheckRedirect: client.CheckRedirect})`, where `cfg` has `MinVersion: tls.VersionTLS12`, the CA pool from `ep.CA` (system roots if absent), the key pair from `ep.Cert`/`ep.Key` when present, and `InsecureSkipVerify: ep.SkipTLSVerify`. `client.Opt` takes an unexported config type, so a custom option is not possible, and `WithTLSClientConfig` has no skip-verify parameter.
  2. `client.WithHost(ep.Host)` only when `ep.Host != ""`. `WithHost("")` fails to parse; without it the client uses its built-in default socket, and `DOCKER_HOST` is not read.
  3. `client.WithAPIVersionFromEnv()`, so `DOCKER_API_VERSION` still pins the version. (`WithVersionFromEnv` is deprecated in v0.6.3 and would fail staticcheck.)
  4. `client.WithUserAgent(...)`.

  The replacement HTTP client loses the SDK's default idle-connection limits. drydock opens few connections, so this is accepted.

The service keeps the endpoint and exposes it: `Service.Endpoint() dockerctx.Endpoint`, next to `Host()`. `Host()` keeps returning `cli.DaemonHost()`, so the resolved default socket is still named in errors.

### D4. `main` resolves before building the service

`run` parses flags (adding `-context`), returns for `-version`/`-h`, then calls `dockerctx.Resolve`, then `deps.newService(ep)`. A resolution error is printed through a new `docker.ContextErrorMessage(err)` and exits with status 1 without pinging. `deps` gains a `resolve` function so the tests can cover failures without touching the real `~/.docker`. The usage text mentions `-context`, `DOCKER_CONTEXT` and the CLI config.

### D5. Context in error messages

`ConnectErrorMessage` takes the endpoint and adds `context "colima" (...)` when the context is not `default`. The permission hint becomes "add your user to the docker group, or point DOCKER_HOST or a Docker context at a socket you can access". The ssh error reads: `context "box" uses an ssh:// endpoint, which drydock does not support yet; forward the remote socket (for example with ssh -L) and set DOCKER_HOST to it`. Context names and paths go through `SanitizeText`, since they come from files.

### D6. The shell handoff gets the whole endpoint

`execProbeMsg` carries the service's `Endpoint()` and `Host()` instead of the host alone. For the default context, the handoff is unchanged. For a named context, the CLI is pointed at the same context store entry drydock read: `--context=<name>` is placed before `exec`, and `DOCKER_CONFIG` is set to the config directory drydock resolved (`Endpoint.ConfigDir`), with the variables that could interfere removed:

| drydock's endpoint | Environment | Global flags before `exec` |
|---|---|---|
| default context | `DOCKER_CONTEXT` removed, `DOCKER_HOST=<Host()>`, inherited TLS variables passed through (as today) | none |
| named context (any TLS setting) | `DOCKER_CONTEXT`, `DOCKER_HOST`, `DOCKER_TLS`, `DOCKER_TLS_VERIFY`, `DOCKER_CERT_PATH` removed; `DOCKER_CONFIG=<ConfigDir>` | `--context=<name>` |

The CLI then loads the context's host, TLS files and skip-verify setting from the same files drydock read, so every TLS case drydock supports, including a CA-only directory, a skip-verify context without files, and a client certificate verified against the system roots, works the same way for the shell. `--context` overrides the CLI config's `currentContext`. The `--flag=value` form keeps a name from being read as another flag. A named endpoint without an absolute `ConfigDir` is refused with a status-line error instead of guessing the store.

What the "same engine" guarantee now rests on: drydock and the CLI read the same `meta.json` and TLS directory. If the context is changed with `docker context update` while drydock runs, the shell follows the new settings; drydock reads the store once, at startup (see Non-Goals).

- *Rejected during implementation (task 4.2): the CLI's global flags (`-H`, `--tls`, `--tlsverify`, `--tlscacert`, `--tlscert`, `--tlskey`).* This was the original D6. Checked with CLI 29.10.1: in any TLS mode without `--tlscacert`, the CLI opens `ca.pem` in the config directory (or `DOCKER_CERT_PATH`) and fails with `unable to resolve docker endpoint: open .../ca.pem: no such file or directory` when it is absent, so skip-verify contexts without files and contexts verifying against the system roots could not open a shell. When such a `ca.pem` exists, the CLI silently uses it, and the default `cert.pem`/`key.pem` are picked up the same way, which could present unrelated client credentials to the context's engine. Non-TLS `-H` worked.
- *Alternative: environment variables only (`DOCKER_CERT_PATH`, `DOCKER_TLS_VERIFY`, `DOCKER_TLS`).* Cannot express a partial TLS directory, and depends on `DOCKER_TLS` support.

### D7. Header indicator

`ui.New` gains variadic options, `ui.New(resources, initial, opts ...Option)`, so existing callers compile unchanged, and an option `ui.WithContext(name)`; `main` passes `ep.Context` and the model shows nothing for `default`. `headerView` builds the right side as `ctx:<name>` + space + connection state, with the name cleaned and truncated to 20 terminal cells. Shrinking happens in steps while the breadcrumb's gap is under 10 cells: first drop the context, then shorten the connection state to its symbol, then the existing last resort. The indicator uses the `Indicator` style already used for filter indicators.

- *Alternative: put the context in the breadcrumb (`drydock@colima > containers`).* The breadcrumb is cut first on narrow terminals, which is exactly when the indicator matters.

## Risks / Trade-offs

- [The context store format changes in a future Docker release] → Unknown JSON fields are ignored, and a missing `Endpoints.docker` gives a `FileError` naming the file instead of a silent fallback. An integration test runs `docker context create` and resolves the result, so CI notices format drift.
- [The `--context` handoff behaves differently on an old `docker` CLI] → Task 4.2 checks it locally and against the CLI version CI prints with `docker version`, and an integration test opens a shell through a named context created with `docker context create`, with `DOCKER_CONTEXT`, `DOCKER_HOST`, `DOCKER_CONFIG` and the store's `currentContext` all pointing elsewhere.
  *Checked locally with docker CLI 29.10.1 (2026-11-26):* the integration test passes, and fails if either `--context` or `DOCKER_CONFIG` is left out of the handoff. Skip-verify-without-files, cert/key-only and CA-only contexts created with `docker context create` all pass CLI initialization through `--context`. CI's CLI (29.4.2, from the "Docker engine info" step) is checked by the same integration test in CI.
- [A user relied on drydock ignoring their current context, for example with an unused stale `currentContext`] → After this change, drydock fails at startup naming the context, so the cause is obvious. The README's Requirements section and the changelog (generated from commit messages) call out the behavior change, and `-context default` restores the old behavior.
- [Partial TLS directories (only `ca.pem`)] → Load what exists in both the SDK and the shell handoff (D6 flags). A lone certificate or key is rejected with a `FileError`.
- [The behavior of a context with `SkipTLSVerify` and no files] → Based on the CLI's context loader, which was not checked against its source while writing this design. Task 1.3 confirms it with `docker context create ... skip-tls-verify=true` before implementing.
  *Checked with docker CLI 29.10.1 (2026-11-26), against a temporary `DOCKER_CONFIG`:* `docker context create ts --docker host=tcp://h:2376,skip-tls-verify=true` writes `{"Name":"ts","Metadata":{},"Endpoints":{"docker":{"Host":"tcp://h:2376","SkipTLSVerify":true}}}` and no `contexts/tls/<hash>` directory, as assumed. Also seen: `host=` (empty) leaves `Host` out of `meta.json`; `ca=` alone writes only `ca.pem`; `cert=` without `key=` is accepted by the CLI and writes only `cert.pem`, which drydock rejects with a `FileError` as D2 specifies; the CLI rejects names shorter than two characters (`^[a-zA-Z0-9][a-zA-Z0-9_.+-]+$`), but `dockerctx` hashes any name it is given.

## Migration Plan

None beyond documenting the behavior change: no stored state changes. Users who set `DOCKER_HOST` keep their current behavior unless they also set `DOCKER_CONTEXT`.
