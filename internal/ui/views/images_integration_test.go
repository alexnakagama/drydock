//go:build integration

package views

import (
	"context"
	"strings"
	"testing"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
)

// TestPullIntegration pulls alpine:latest anonymously from Docker Hub
// through the store, as the images view does, and checks that progress was
// reported and the pull succeeded. It needs network access to Docker Hub.
func TestPullIntegration(t *testing.T) {
	st := connectedStore(t)
	var reports []string
	res := st.ActionWith("pull alpine:latest", pullTimeout, func(ctx context.Context, svc docker.Service) (string, error) {
		return pullImage(ctx, svc, "alpine:latest", func(s string) { reports = append(reports, s) })
	})().(store.ActionResultMsg)
	if res.Err != nil {
		t.Fatalf("pull failed: %s", res.Reason)
	}
	t.Logf("detail %q, progress %v", res.Detail, reports)
	if res.Detail == "" && len(reports) == 0 {
		t.Error("a fresh pull reported no progress")
	}
	for _, r := range reports {
		if !strings.HasSuffix(r, "layers") {
			t.Errorf("unexpected progress text %q", r)
		}
	}

	// A reference that does not exist fails with the registry's message.
	res = st.ActionWith("pull", pullTimeout, func(ctx context.Context, svc docker.Service) (string, error) {
		return pullImage(ctx, svc, "drydock-integration/does-not-exist:never", func(string) {})
	})().(store.ActionResultMsg)
	if res.Err == nil || res.Reason == "" {
		t.Errorf("pull of a missing repository = %+v, want the registry's error", res)
	}
	t.Logf("missing repository: %s", res.Reason)
}
