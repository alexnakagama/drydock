//go:build integration

package views

import (
	"bytes"
	"os/exec"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
)

// dockerRunExited runs `docker run --label drydock-it=1 args...` without
// --rm (unlike dockerRun: an exited container must outlive its command so
// that there is something to prune), waits for the container to exit, removes it when the test ends (if
// the test did not), and returns its ID.
func dockerRunExited(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	cmd := exec.Command("docker", append([]string{"run", "-d", "--label", "drydock-it=1"}, args...)...)
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
	if out, err := exec.Command("docker", "wait", id).CombinedOutput(); err != nil {
		t.Fatalf("docker wait %s: %v: %s", id, err, out)
	}
	return id
}

// TestIntegrationPruneStoppedContainers prunes from a drill-down of the
// alpine image on the real engine, through the root model, and checks that
// only the exited container is removed: the running one is kept.
func TestIntegrationPruneStoppedContainers(t *testing.T) {
	exited := dockerRunExited(t, "alpine:latest", "true")
	running := runTestContainer(t, "alpine:latest")

	svc, err := docker.NewSDKService("drydock-integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	e := newEnvOn(t, svc, nil, 120, 24)

	find := func(id string) (container.Summary, bool) {
		return findContainer(e.root().snap, id)
	}
	e.until("both containers listed", func() bool {
		x, ok1 := find(exited)
		r, ok2 := find(running)
		return ok1 && ok2 && x.State == container.StateExited && r.State == container.StateRunning
	})
	x, _ := find(exited)
	alpineID := x.ImageID

	// The drill-down's predicate is the images view's (same image ID),
	// narrowed to this test's two containers so that a developer's own
	// stopped alpine containers are never pruned by the test.
	ours := map[string]bool{exited: true, running: true}
	e.push(NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"alpine:latest", "containers"},
		"No containers use alpine:latest.", func(c container.Summary) bool {
			return c.ImageID == alpineID && ours[c.ID]
		}))

	e.openPrune()
	if got, want := e.pruneListed(), []string{store.ContainerName(x) + " exited"}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")

	if _, err := svc.ContainerInspect(t.Context(), exited); !cerrdefs.IsNotFound(err) {
		t.Errorf("inspect of the pruned container: %v, want not found", err)
	}
	res, err := svc.ContainerInspect(t.Context(), running)
	if err != nil {
		t.Fatalf("inspect of the running container: %v", err)
	}
	if res.Container.State == nil || !res.Container.State.Running {
		t.Errorf("running container state = %+v, want running", res.Container.State)
	}
}

// findContainer returns the container with the given ID from snap.
func findContainer(snap store.Snapshot, id string) (container.Summary, bool) {
	i := slices.IndexFunc(snap.Containers(), func(c container.Summary) bool { return c.ID == id })
	if i < 0 {
		return container.Summary{}, false
	}
	return snap.Containers()[i], true
}
