//go:build integration

package docker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// These tests run against the engine configured by the environment
// (DOCKER_HOST or the default socket): go test -tags integration ./...
// They only create volumes named "drydock-it-*" and containers labelled
// drydock-it=1 (with the docker CLI, from alpine:latest, which must be present
// or pullable), and remove them afterwards.

func newIntegrationService(t *testing.T) *SDKService {
	t.Helper()
	svc, err := NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatalf("NewSDKService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := svc.Ping(ctx); err != nil {
		t.Fatalf("Ping %s: %v", svc.Host(), err)
	}
	return svc
}

func testVolumeName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "drydock-it-" + hex.EncodeToString(b)
}

func TestIntegrationPingNegotiatesVersion(t *testing.T) {
	svc, err := NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ping, err := svc.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping %s: %v", svc.Host(), err)
	}
	want := ping.APIVersion
	if versions.GreaterThan(want, client.MaxAPIVersion) {
		want = client.MaxAPIVersion // a newer engine still serves older clients
	}
	if got := svc.APIVersion(); got != want {
		t.Errorf("client API version = %s, want %s (engine %s)", got, want, ping.APIVersion)
	}
}

func TestIntegrationListContainers(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	all, err := svc.ContainerList(ctx, ContainerListOptions{All: true})
	if err != nil {
		t.Fatalf("ContainerList(all): %v", err)
	}
	runningOnly, err := svc.ContainerList(ctx, ContainerListOptions{})
	if err != nil {
		t.Fatalf("ContainerList: %v", err)
	}
	if len(runningOnly) > len(all) {
		t.Errorf("running-only list (%d) is longer than the full list (%d)", len(runningOnly), len(all))
	}
	if _, err := svc.ImageList(ctx); err != nil {
		t.Errorf("ImageList: %v", err)
	}
}

func TestIntegrationVolumeLifecycleAndEvents(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	name := testVolumeName(t)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		if err := svc.VolumeRemove(cctx, name, VolumeRemoveOptions{}); err != nil && !IsNotFound(err) {
			t.Errorf("cleanup: remove volume %s: %v", name, err)
		}
	})

	evctx, evcancel := context.WithCancel(ctx)
	defer evcancel()
	es := svc.Events(evctx)

	v, err := svc.VolumeCreate(ctx, name)
	if err != nil {
		t.Fatalf("VolumeCreate: %v", err)
	}
	if v.Name != name || v.Driver != localVolumeDriver {
		t.Errorf("created volume = %+v", v)
	}
	waitForEvent(t, es, events.VolumeEventType, events.ActionCreate, name)

	insp, err := svc.VolumeInspect(ctx, name)
	if err != nil {
		t.Fatalf("VolumeInspect: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(insp.Raw, &raw); err != nil || raw["Name"] != name {
		t.Errorf("raw inspect = %s (%v)", insp.Raw, err)
	}

	vols, err := svc.VolumeList(ctx)
	if err != nil {
		t.Fatalf("VolumeList: %v", err)
	}
	found := false
	for _, vol := range vols {
		found = found || vol.Name == name
	}
	if !found {
		t.Errorf("VolumeList does not include %s", name)
	}

	if err := svc.VolumeRemove(ctx, name, VolumeRemoveOptions{}); err != nil {
		t.Fatalf("VolumeRemove: %v", err)
	}
	waitForEvent(t, es, events.VolumeEventType, events.ActionDestroy, name)
	if _, err := svc.VolumeInspect(ctx, name); !IsNotFound(err) {
		t.Errorf("inspect removed volume: err = %v, want not found", err)
	}

	evcancel()
	select {
	case err := <-es.Err:
		if err == nil {
			t.Error("event stream ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Error("event stream did not end after cancel")
	}
}

func TestIntegrationRejectsInvalidIdentifiers(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := svc.VolumeRemove(ctx, "../containers/x", VolumeRemoveOptions{}); !IsInvalidArgument(err) {
		t.Errorf("VolumeRemove with traversal: err = %v, want invalid argument", err)
	}
	if _, err := svc.ImageRemove(ctx, "../../containers/x", ImageRemoveOptions{}); !IsInvalidArgument(err) {
		t.Errorf("ImageRemove with traversal: err = %v, want invalid argument", err)
	}
	if st, err := svc.ContainerStats(ctx, "../images/x"); !IsInvalidArgument(err) {
		if st != nil {
			_ = st.Close()
		}
		t.Errorf("ContainerStats with traversal: err = %v, want invalid argument", err)
	}
	if err := svc.ContainerPause(ctx, "../images/x"); !IsInvalidArgument(err) {
		t.Errorf("ContainerPause with traversal: err = %v, want invalid argument", err)
	}
	if err := svc.ContainerUnpause(ctx, "../images/x"); !IsInvalidArgument(err) {
		t.Errorf("ContainerUnpause with traversal: err = %v, want invalid argument", err)
	}
}

// runITContainer starts a labelled alpine:latest container running cmd and
// removes it when the test ends. Only stdout carries the container ID: when
// the image has to be pulled first, docker reports the pull on stderr.
func runITContainer(t *testing.T, cmd ...string) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	args := append([]string{"run", "-d", "--rm", "--label", "drydock-it=1", "alpine:latest"}, cmd...)
	c := exec.Command("docker", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// runExitedITContainer creates a labelled alpine:latest container that has
// run `true` and exited, and removes it when the test ends.
func runExitedITContainer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	c := exec.Command("docker", "run", "-d", "--label", "drydock-it=1", "alpine:latest", "true")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	if out, err := exec.Command("docker", "wait", id).CombinedOutput(); err != nil {
		t.Fatalf("docker wait: %v: %s", err, out)
	}
	return id
}

func TestIntegrationContainerPauseUnpause(t *testing.T) {
	svc := newIntegrationService(t)
	id := runITContainer(t, "sleep", "300")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	inspectState := func(when string) *container.State {
		t.Helper()
		insp, err := svc.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatalf("%s: ContainerInspect: %v", when, err)
		}
		if insp.Container.State == nil {
			t.Fatalf("%s: inspect has no State", when)
		}
		return insp.Container.State
	}

	if err := svc.ContainerPause(ctx, id); err != nil {
		t.Fatalf("ContainerPause: %v", err)
	}
	if st := inspectState("after pause"); !st.Paused || !st.Running {
		t.Errorf("after pause: Running = %v, Paused = %v, want true, true (status %q)", st.Running, st.Paused, st.Status)
	}
	if err := svc.ContainerPause(ctx, id); !IsConflict(err) {
		t.Errorf("pause a paused container: err = %v, want conflict", err)
	}

	if err := svc.ContainerUnpause(ctx, id); err != nil {
		t.Fatalf("ContainerUnpause: %v", err)
	}
	if st := inspectState("after unpause"); !st.Running || st.Paused {
		t.Errorf("after unpause: Running = %v, Paused = %v, want true, false", st.Running, st.Paused)
	}
	// The engine answers this refusal with a 500, not a 409.
	if err := svc.ContainerUnpause(ctx, id); !cerrdefs.IsInternal(err) {
		t.Errorf("unpause a running container: err = %v, want internal", err)
	}

	exitedID := runExitedITContainer(t)
	if err := svc.ContainerPause(ctx, exitedID); !IsConflict(err) {
		t.Errorf("pause an exited container: err = %v, want conflict", err)
	}
}

func TestIntegrationContainerStats(t *testing.T) {
	svc := newIntegrationService(t)
	id := runITContainer(t, "sleep", "300")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	st, err := svc.ContainerStats(ctx, id)
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	defer func() { _ = st.Close() }()

	type result struct {
		samples []container.StatsResponse
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		for sample, err := range st.Samples(ctx) {
			if err != nil {
				r.err = err
				break
			}
			r.samples = append(r.samples, sample)
			if len(r.samples) == 2 {
				cancel()
			}
		}
		done <- r
	}()

	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stats iteration did not end after ctx was cancelled")
	}
	if len(r.samples) < 2 {
		t.Fatalf("got %d samples within 3s (err %v), want at least 2", len(r.samples), r.err)
	}
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("iteration ended with %v, want context.Canceled", r.err)
	}
	if got := r.samples[0].PreCPUStats.SystemUsage; got != 0 {
		t.Errorf("first sample PreCPUStats.SystemUsage = %d, want 0", got)
	}
	if r.samples[1].PreCPUStats.SystemUsage == 0 {
		t.Error("second sample PreCPUStats.SystemUsage = 0, want non-zero")
	}
}

func waitForEvent(t *testing.T, es EventStream, typ events.Type, action events.Action, actorID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-es.Messages:
			if m.Type == typ && m.Action == action && m.Actor.ID == actorID {
				return
			}
		case err := <-es.Err:
			t.Fatalf("event stream ended while waiting for %s/%s %s: %v", typ, action, actorID, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s/%s %s", typ, action, actorID)
		}
	}
}
