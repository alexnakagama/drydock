//go:build integration

package views

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
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
