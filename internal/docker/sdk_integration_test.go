//go:build integration

package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
)

// These tests run against the engine configured by the environment
// (DOCKER_HOST or the default socket): go test -tags integration ./...
// They only create volumes named "drydock-it-*" and remove them afterwards.

func newIntegrationService(t *testing.T) *SDKService {
	t.Helper()
	svc, err := NewSDKService("drydock-integration-test")
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
	svc, err := NewSDKService("drydock-integration-test")
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
