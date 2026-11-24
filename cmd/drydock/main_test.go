package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

// fakeDeps returns deps backed by f. If f is nil, creating the service
// fails the test, for cases that must not reach the engine.
func fakeDeps(t *testing.T, f docker.Service) deps {
	t.Helper()
	return deps{
		newService: func() (docker.Service, error) {
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
		newService:  func() (docker.Service, error) { return nil, errors.New("unable to parse docker host `bogus`") },
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
