//go:build integration

package views

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// These tests run against the engine configured by the environment and use
// the docker CLI to create containers labelled drydock-it=1, which they
// remove afterwards. They need the alpine:latest and debian:stable-slim
// images to be present or pullable.

func runTestContainer(t *testing.T, img string) string {
	t.Helper()
	return dockerRun(t, img, "sleep", "300")
}

// dockerRun runs `docker run -d --rm --label drydock-it=1 args...`, removes
// the container when the test ends, and returns its ID. Only stdout carries
// the ID: when the image has to be pulled first, docker reports the pull on
// stderr, which is kept for the failure message.
func dockerRun(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	cmd := exec.Command("docker", append([]string{"run", "-d", "--rm", "--label", "drydock-it=1"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker run %s: %v: %s", strings.Join(args, " "), err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := docker.ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

func TestIntegrationProbeShell(t *testing.T) {
	svc, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	for img, want := range map[string]string{
		"alpine:latest":      "/bin/sh", // no bash
		"debian:stable-slim": "/bin/bash",
	} {
		t.Run(img, func(t *testing.T) {
			id := runTestContainer(t, img)
			ctx, cancel := context.WithTimeout(t.Context(), probeTimeout)
			defer cancel()
			start := time.Now()
			got, err := probeShell(ctx, svc, id, probePoll)
			if err != nil || got != want {
				t.Fatalf("probeShell = %q, %v; want %q", got, err, want)
			}
			t.Logf("probed %s in %v", got, time.Since(start))
		})
	}
}

func TestIntegrationProbeNoShell(t *testing.T) {
	svc, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	id := runTestContainer(t, "alpine:latest")
	// Remove both shells (busybox provides sh as a link).
	if out, err := exec.Command("docker", "exec", id, "rm", "-f", "/bin/sh", "/bin/bash").CombinedOutput(); err != nil {
		t.Fatalf("remove shells: %v: %s", err, out)
	}
	ctx, cancel := context.WithTimeout(t.Context(), probeTimeout)
	defer cancel()
	if got, err := probeShell(ctx, svc, id, probePoll); err != errNoShell {
		t.Fatalf("probeShell = %q, %v; want errNoShell", got, err)
	}
}

// lockedBuffer is a bytes.Buffer safe for one writer and one reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestIntegrationShellThroughNamedContext opens an interactive shell with
// the command drydock builds for a named, non-TLS context created by
// `docker context create` and pointing at the engine's local socket. The
// environment the CLI would otherwise follow is wrong in every way:
// DOCKER_CONTEXT names a context that does not exist, DOCKER_HOST is
// unreachable, DOCKER_CONFIG names another config directory, and the
// context store's own config.json selects a missing current context. The
// shell opens only if the handoff points the CLI at drydock's store and
// context.
func TestIntegrationShellThroughNamedContext(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker CLI not found")
	}
	def, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = def.Close() })
	host := def.Host()
	if !strings.HasPrefix(host, "unix://") {
		t.Skipf("engine at %s is not a local socket", host)
	}

	cfgDir := t.TempDir()
	create := exec.Command(dockerPath, "context", "create", "drydock-it", "--docker", "host="+host)
	create.Env = append(os.Environ(), "DOCKER_CONFIG="+cfgDir)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("docker context create: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"currentContext": "drydock-it-missing"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ep, err := dockerctx.Resolve(dockerctx.Inputs{Flag: "drydock-it", ConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	svc, err := docker.NewSDKService("drydock-integration-test", ep)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	id := runTestContainer(t, "alpine:latest")
	ctx, cancel := context.WithTimeout(t.Context(), probeTimeout)
	defer cancel()
	shell, err := probeShell(ctx, svc, id, probePoll)
	if err != nil {
		t.Fatalf("probeShell: %v", err)
	}

	environ := append(os.Environ(),
		"DOCKER_CONTEXT=drydock-it-missing",
		"DOCKER_HOST=tcp://127.0.0.1:1",
		"DOCKER_CONFIG="+t.TempDir(),
	)
	cmd, err := execCommand(dockerPath, svc.Endpoint(), svc.Host(), id, shell, environ)
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	master, slave := openPTY(t)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %q: %v", cmd.Args, err)
	}
	_ = slave.Close() // the CLI holds its own copy; reads then end with EIO
	var out lockedBuffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&out, master)
	}()
	// The expansion proves the shell ran the line: the terminal echoes the
	// line itself, not its result.
	if _, err := io.WriteString(master, "echo drydock-$((40+2)); exit\n"); err != nil {
		t.Fatalf("write to the shell: %v", err)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		werr := <-waited
		t.Fatalf("shell did not exit (killed: %v); args %q, output %q", werr, cmd.Args, out.String())
	}
	_ = master.Close()
	<-copied
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("wait: %v", err)
	}
	if err != nil || !strings.Contains(out.String(), "drydock-42") {
		t.Fatalf("shell through %q: %v; output %q", cmd.Args, err, out.String())
	}
}
