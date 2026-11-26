package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

// fakeDeps returns deps backed by f, resolving every run to the default
// context. If f is nil, creating the service fails the test, for cases that
// must not reach the engine.
func fakeDeps(t *testing.T, f docker.Service) deps {
	t.Helper()
	return deps{
		resolve: func(string) (dockerctx.Endpoint, error) { return dockerctx.DefaultEndpoint(), nil },
		newService: func(dockerctx.Endpoint) (docker.Service, error) {
			if f == nil {
				t.Error("service created unexpectedly")
				return nil, errors.New("unexpected")
			}
			return f, nil
		},
		pingTimeout: 200 * time.Millisecond,
		// The TUI reads a single "q" (quit) instead of the terminal.
		programOptions: tuiOptions(strings.NewReader("q")),
	}
}

// tuiOptions replaces the program's terminal with input and a discarded
// output of a fixed size.
func tuiOptions(input io.Reader) []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithInput(input),
		tea.WithOutput(io.Discard),
		tea.WithWindowSize(100, 30),
		tea.WithEnvironment([]string{"TERM=xterm-256color"}),
	}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		svc        func() docker.Service // nil: the engine must not be contacted
		wantCode   int
		wantStdout string // substring; "" means stdout must be empty
		wantStderr string // substring; "" means stderr must be empty
	}{
		{name: "version", args: []string{"-version"}, wantCode: exitOK, wantStdout: "drydock "},
		{name: "version double dash", args: []string{"--version"}, wantCode: exitOK, wantStdout: "drydock "},
		{name: "help", args: []string{"-h"}, wantCode: exitOK, wantStderr: "Usage: drydock"},
		{name: "help long", args: []string{"-help"}, wantCode: exitOK, wantStderr: "Usage: drydock"},
		{name: "unknown flag", args: []string{"-nope"}, wantCode: exitUsage, wantStderr: "flag provided but not defined"},
		{name: "positional argument", args: []string{"extra"}, wantCode: exitUsage, wantStderr: `unexpected argument "extra"`},
		{name: "version with positional argument", args: []string{"-version", "extra"}, wantCode: exitUsage, wantStderr: `unexpected argument "extra"`},
		{
			name:     "engine reachable",
			svc:      func() docker.Service { return dockerfake.New() },
			wantCode: exitOK,
		},
		{
			name: "engine not running",
			svc: func() docker.Service {
				f := dockerfake.New()
				f.SetHost("unix:///nonexistent.sock")
				f.SetError(dockerfake.MethodPing, errors.New(
					"failed to connect to the docker API at unix:///nonexistent.sock; check if the path is correct "+
						"and if the daemon is running: dial unix /nonexistent.sock: connect: no such file or directory"))
				return f
			},
			wantCode:   exitError,
			wantStderr: "cannot connect to the Docker engine at unix:///nonexistent.sock: the engine may not be running",
		},
		{
			name: "connection refused",
			svc: func() docker.Service {
				f := dockerfake.New()
				f.SetHost("tcp://127.0.0.1:2375")
				f.SetError(dockerfake.MethodPing, syscall.ECONNREFUSED)
				return f
			},
			wantCode:   exitError,
			wantStderr: "at tcp://127.0.0.1:2375: the engine may not be running",
		},
		{
			name: "permission denied",
			svc: func() docker.Service {
				f := dockerfake.New()
				f.SetError(dockerfake.MethodPing, errors.New(
					"permission denied while trying to connect to the docker API at unix:///var/run/docker.sock"))
				return f
			},
			wantCode:   exitError,
			wantStderr: "permission denied for the Docker engine at unix:///var/run/docker.sock",
		},
		{
			name:       "ping timeout",
			svc:        func() docker.Service { return hangingPing{dockerfake.New()} },
			wantCode:   exitError,
			wantStderr: "did not respond within 200ms",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var svc docker.Service
			if tt.svc != nil {
				svc = tt.svc()
			}
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), tt.args, &stdout, &stderr, fakeDeps(t, svc))
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d (stderr: %q)", code, tt.wantCode, stderr.String())
			}
			checkOutput(t, "stdout", stdout.String(), tt.wantStdout)
			checkOutput(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

func TestRunServiceCreationFails(t *testing.T) {
	t.Parallel()
	d := deps{
		resolve: func(string) (dockerctx.Endpoint, error) { return dockerctx.DefaultEndpoint(), nil },
		newService: func(dockerctx.Endpoint) (docker.Service, error) {
			return nil, errors.New("unable to parse docker host `bogus`")
		},
		pingTimeout: time.Second,
	}
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), nil, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stderr", stderr.String(), "unable to parse docker host")
}

// failingWriter fails every write, like stdout redirected to a full disk or
// a closed pipe.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, syscall.ENOSPC }

func TestRunVersionWriteFails(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := run(t.Context(), []string{"-version"}, failingWriter{}, &stderr, fakeDeps(t, nil)); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stderr", stderr.String(), "drydock: write version: no space left on device")
}

func TestRunInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout, stderr bytes.Buffer
	d := fakeDeps(t, hangingPing{dockerfake.New()})
	d.pingTimeout = time.Minute
	if code := run(ctx, nil, &stdout, &stderr, d); code != exitInterrupted {
		t.Errorf("exit code = %d, want %d", code, exitInterrupted)
	}
	checkOutput(t, "stderr", stderr.String(), "interrupted")
}

func TestRunTUIInterrupted(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	// The input never delivers a key, so only cancellation (a signal in
	// main) can end the program.
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	d := fakeDeps(t, dockerfake.New())
	d.programOptions = tuiOptions(pr)
	time.AfterFunc(200*time.Millisecond, cancel)

	done := make(chan int, 1)
	var stdout, stderr bytes.Buffer
	go func() { done <- run(ctx, nil, &stdout, &stderr, d) }()
	select {
	case code := <-done:
		if code != exitInterrupted {
			t.Errorf("exit code = %d, want %d (stderr %q)", code, exitInterrupted, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}
}

func TestRunClosesService(t *testing.T) {
	t.Parallel()
	c := &closeRecorder{Service: dockerfake.New()}
	var stdout, stderr bytes.Buffer
	run(t.Context(), nil, &stdout, &stderr, fakeDeps(t, c))
	if !c.closed {
		t.Error("service was not closed")
	}
}

// storeResolver resolves contexts the way drydock does, but against the
// config directory dir and an environment holding only HOME=dir, so the
// tests never read the real ~/.docker or the process environment.
func storeResolver(dir string) func(string) (dockerctx.Endpoint, error) {
	getenv := func(k string) string {
		if k == "HOME" {
			return dir
		}
		return ""
	}
	return func(flag string) (dockerctx.Endpoint, error) {
		return dockerctx.Resolve(dockerctx.Inputs{Flag: flag, Getenv: getenv, ConfigDir: dir})
	}
}

// writeContext stores a context named name with docker endpoint host in the
// context store under dir, as `docker context create` does.
func writeContext(t *testing.T, dir, name, host string) {
	t.Helper()
	sum := sha256.Sum256([]byte(name))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]))
	if err := os.MkdirAll(meta, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"Name":` + strconv.Quote(name) + `,"Metadata":{},"Endpoints":{"docker":{"Host":` +
		strconv.Quote(host) + `,"SkipTLSVerify":false}}}`
	if err := os.WriteFile(filepath.Join(meta, "meta.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunContextNotFound(t *testing.T) {
	t.Parallel()
	d := fakeDeps(t, nil) // the service must not be created
	d.resolve = storeResolver(t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-context", "nope"}, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stdout", stdout.String(), "")
	checkOutput(t, "stderr", stderr.String(),
		`drydock: Docker context "nope" was not found; run "docker context ls" to list the available contexts`)
}

func TestRunContextSSH(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeContext(t, dir, "box", "ssh://me@build-box")
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(dir)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-context", "box"}, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stderr", stderr.String(),
		`context "box" uses an ssh:// endpoint, which drydock does not support yet; `+
			"forward the remote socket (for example with ssh -L) and set DOCKER_HOST to it")
}

func TestRunContextMalformedConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(dir)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), nil, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stderr", stderr.String(), "cannot read the Docker CLI config "+cfg)
}

func TestRunContextErrorSanitized(t *testing.T) {
	t.Parallel()
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(t.TempDir())
	var stdout, stderr bytes.Buffer
	run(t.Context(), []string{"-context", "evil\x1b]0;pwned\x07"}, &stdout, &stderr, d)
	if strings.ContainsAny(stderr.String(), "\x1b\x07") {
		t.Errorf("stderr contains control characters: %q", stderr.String())
	}
	checkOutput(t, "stderr", stderr.String(), "was not found")
}

// TestRunInformationalFlagsSkipResolve checks that -version and -h never
// read the context store, so they work with a broken CLI config.
func TestRunInformationalFlagsSkipResolve(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-version"}, {"-h"}, {"-context", "nope", "-version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Parallel()
			d := fakeDeps(t, nil)
			d.resolve = func(string) (dockerctx.Endpoint, error) {
				t.Error("resolve called")
				return dockerctx.Endpoint{}, &dockerctx.FileError{Path: "/broken/config.json", Err: errors.New("bad")}
			}
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), args, &stdout, &stderr, d); code != exitOK {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, exitOK, stderr.String())
			}
		})
	}
}

// TestRunNamedContextUnreachable checks that the resolved endpoint reaches
// the service, and that the connection error names the context.
func TestRunNamedContextUnreachable(t *testing.T) {
	t.Parallel()
	const host = "unix:///nonexistent/colima.sock"
	dir := t.TempDir()
	writeContext(t, dir, "colima", host)
	var got dockerctx.Endpoint
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(dir)
	d.newService = func(ep dockerctx.Endpoint) (docker.Service, error) {
		got = ep
		f := dockerfake.New()
		f.SetEndpoint(ep)
		f.SetHost(ep.Host)
		f.SetError(dockerfake.MethodPing, syscall.ECONNREFUSED)
		return f, nil
	}
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-context", "colima"}, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if got.Context != "colima" || got.Host != host {
		t.Errorf("newService got %+v, want context colima at %s", got, host)
	}
	checkOutput(t, "stderr", stderr.String(),
		`cannot connect to the Docker engine for context "colima" (`+host+`): the engine may not be running`)
}

// TestRunPassesFlagToResolve checks that the -context value, and "" when
// the flag is absent, is what resolve receives.
func TestRunPassesFlagToResolve(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"-context", "remote"}, "remote"},
		{[]string{"-context="}, ""},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			t.Parallel()
			var got []string
			d := fakeDeps(t, dockerfake.New())
			d.resolve = func(flag string) (dockerctx.Endpoint, error) {
				got = append(got, flag)
				return dockerctx.DefaultEndpoint(), nil
			}
			var stdout, stderr bytes.Buffer
			if code := run(t.Context(), tt.args, &stdout, &stderr, d); code != exitOK {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, exitOK, stderr.String())
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("resolve called with %q, want [%q]", got, tt.want)
			}
		})
	}
}

// TestRunEmptyContextFlagFallsThrough checks that -context "" is treated
// as not given: the current context from the CLI config is used.
func TestRunEmptyContextFlagFallsThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeContext(t, dir, "colima", "unix:///nonexistent/colima.sock")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"currentContext":"colima"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var got dockerctx.Endpoint
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(dir)
	d.newService = func(ep dockerctx.Endpoint) (docker.Service, error) {
		got = ep
		return nil, errors.New("stop here")
	}
	var stdout, stderr bytes.Buffer
	run(t.Context(), []string{"-context="}, &stdout, &stderr, d)
	if got.Context != "colima" {
		t.Errorf("newService got context %q, want colima", got.Context)
	}
}

func TestRunContextMalformedMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeContext(t, dir, "broken", "unix:///x.sock")
	sum := sha256.Sum256([]byte("broken"))
	meta := filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json")
	if err := os.WriteFile(meta, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := fakeDeps(t, nil)
	d.resolve = storeResolver(dir)
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), []string{"-context", "broken"}, &stdout, &stderr, d); code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	checkOutput(t, "stderr", stderr.String(), `cannot read Docker context "broken": `+meta)
}

func TestUserAgent(t *testing.T) {
	t.Parallel()
	if ua := userAgent(); !strings.HasPrefix(ua, "drydock") || strings.ContainsAny(ua, "() ") {
		t.Errorf("userAgent() = %q", ua)
	}
}

// hangingPing is a service whose Ping blocks until its context ends, like
// an engine that accepts connections but never answers.
type hangingPing struct{ *dockerfake.Service }

func (h hangingPing) Ping(ctx context.Context) (docker.PingResult, error) {
	<-ctx.Done()
	return docker.PingResult{}, ctx.Err()
}

type closeRecorder struct {
	docker.Service
	closed bool
}

func (c *closeRecorder) Close() error {
	c.closed = true
	return c.Service.Close()
}

// checkOutput asserts that got contains want, or that got is empty when want
// is empty.
func checkOutput(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("unexpected %s: %q", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

func TestSignalExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sig  os.Signal
		want int
	}{
		{os.Interrupt, 130},
		{syscall.SIGTERM, 143},
		{nil, exitInterrupted},
	}
	for _, tt := range tests {
		if got := signalExitCode(tt.sig); got != tt.want {
			t.Errorf("signalExitCode(%v) = %d, want %d", tt.sig, got, tt.want)
		}
	}
}
