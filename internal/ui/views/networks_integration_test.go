//go:build integration

package views

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/network"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// itRunLabel labels the networks of one run of TestIntegrationNetworks, so
// that scopedNetworks shows the store that run's networks only.
const itRunLabel = "drydock-it-run"

// scopedNetworks is the real engine with its network list narrowed to the
// networks labeled itRunLabel=run. The networks view, and so its prune,
// then never sees a developer's own networks, built-in ones included.
// Removal is narrowed the same way, as a second guard: an ID that was not
// listed is refused without a request.
type scopedNetworks struct {
	docker.Service
	run string

	mu     sync.Mutex
	listed map[string]bool
}

func (s *scopedNetworks) NetworkList(ctx context.Context) ([]network.Summary, error) {
	all, err := s.Service.NetworkList(ctx)
	if err != nil {
		return nil, err
	}
	ours := slices.DeleteFunc(all, func(n network.Summary) bool { return n.Labels[itRunLabel] != s.run })
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range ours {
		s.listed[n.ID] = true
	}
	return ours, nil
}

func (s *scopedNetworks) NetworkRemove(ctx context.Context, id string) error {
	s.mu.Lock()
	ok := s.listed[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("test refused to remove network %s: not created by this test", id)
	}
	return s.Service.NetworkRemove(ctx, id)
}

// dockerNetworkCreate creates a bridge network called name, labeled
// drydock-it=1 and itRunLabel=run, removes it when the test ends if the
// test did not, and returns its ID.
func dockerNetworkCreate(t *testing.T, run, name string) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	cmd := exec.Command("docker", "network", "create", "--driver", "bridge",
		"--label", "drydock-it=1", "--label", itRunLabel+"="+run, name)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker network create %s: %v: %s", name, err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := docker.ValidateNetworkID(id); err != nil {
		t.Fatalf("docker network create printed %q, want a network ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", id).Run() })
	return id
}

// networkExists reports whether the engine has a network with the given
// ID, through the docker CLI rather than drydock.
func networkExists(t *testing.T, id string) bool {
	t.Helper()
	cmd := exec.Command("docker", "network", "ls", "-q", "--no-trunc", "--filter", "id="+id)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("docker network ls: %v", err)
	}
	return slices.Contains(strings.Fields(string(out)), id)
}

// leftovers lists the IDs of the networks, or with containers the
// containers, labeled itRunLabel=run or attached to the run's networks.
func leftovers(t *testing.T, run string, nets []string) (networks, containers []string) {
	t.Helper()
	out, err := exec.Command("docker", "network", "ls", "-q", "--filter", "label="+itRunLabel+"="+run).Output()
	if err != nil {
		t.Errorf("docker network ls: %v", err)
	}
	networks = strings.Fields(string(out))
	for _, n := range nets {
		out, err := exec.Command("docker", "ps", "-aq", "--filter", "network="+n).Output()
		if err != nil {
			t.Errorf("docker ps for network %s: %v", n, err)
		}
		containers = append(containers, strings.Fields(string(out))...)
	}
	return networks, containers
}

// TestIntegrationNetworks creates labeled networks and containers on the
// real engine and, through the root model: checks IN USE for running and
// stopped users, that `docker network connect` with no other activity
// updates IN USE, and that prune removes the unused network and keeps the
// one whose only container is stopped. Everything it creates is removed
// when it ends, which a final cleanup checks.
func TestIntegrationNetworks(t *testing.T) {
	// Names no developer network uses, and that parallel runs do not
	// share.
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	run := hex.EncodeToString(suffix)
	prefix := "drydock-it-" + run + "-"
	usedName, stoppedName, unusedName, laterName := prefix+"used", prefix+"stopped", prefix+"unused", prefix+"later"
	names := []string{usedName, stoppedName, unusedName, laterName}

	// Registered first, so it runs last, after every other cleanup.
	t.Cleanup(func() {
		if ns, cs := leftovers(t, run, names); len(ns) > 0 || len(cs) > 0 {
			t.Errorf("left on the engine: networks %v, containers %v", ns, cs)
		}
	})
	// Networks are created before the containers attached to them, so that
	// their cleanups run after the containers' (cleanups run last first).
	used := dockerNetworkCreate(t, run, usedName)
	stopped := dockerNetworkCreate(t, run, stoppedName)
	unused := dockerNetworkCreate(t, run, unusedName)
	later := dockerNetworkCreate(t, run, laterName)
	web := dockerRun(t, "--init", "--network", usedName, "alpine:latest", "sleep", "300")
	dockerRunExited(t, "--network", usedName, "alpine:latest", "true")
	dockerRunExited(t, "--network", stoppedName, "alpine:latest", "true")

	engine, err := docker.NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	svc := &scopedNetworks{Service: engine, run: run, listed: map[string]bool{}}
	// Wide enough for the network names and the outcome report.
	e := newEnvOn(t, svc, nil, 200, 30)
	e.command("networks")
	if !strings.HasPrefix(e.header(), "drydock > networks") {
		t.Fatalf("networks view not shown; screen:\n%s", e.screen())
	}

	inUse := func(name string) string {
		f := rowFields(e.rowOf(name))
		if len(f) == 0 {
			return ""
		}
		return f[len(f)-1]
	}
	wantInUse := func(what string, want map[string]string) {
		t.Helper()
		e.until(what, func() bool {
			for name, n := range want {
				if inUse(name) != n {
					return false
				}
			}
			return true
		})
	}
	// A running and an exited user both count; the stopped network's only
	// user is exited.
	wantInUse("IN USE of the created networks", map[string]string{
		usedName: "2", stoppedName: "1", unusedName: "0", laterName: "0",
	})
	if got, want := e.networkNames(), []string{laterName, stoppedName, unusedName, usedName}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}

	// connect emits only a network event, which must refresh the
	// container list too.
	if out, err := exec.Command("docker", "network", "connect", laterName, web).CombinedOutput(); err != nil {
		t.Fatalf("docker network connect: %v: %s", err, out)
	}
	wantInUse("IN USE after network connect", map[string]string{laterName: "1", usedName: "2"})

	e.press("P")
	e.until("prune confirmation", func() bool { return strings.Contains(e.screen(), "Prune unused networks?") })
	d := strings.Fields(e.dialog())
	if !slices.Contains(d, unusedName) {
		t.Errorf("confirmation does not list %s:\n%s", unusedName, e.dialog())
	}
	for _, kept := range []string{usedName, stoppedName, laterName} {
		if slices.Contains(d, kept) {
			t.Errorf("confirmation lists %s:\n%s", kept, e.dialog())
		}
	}
	e.press("y")
	e.waitStatus("prune unused networks: 1 network removed")
	e.until("pruned network gone", func() bool {
		return slices.Equal(e.networkNames(), []string{laterName, stoppedName, usedName})
	})

	if networkExists(t, unused) {
		t.Errorf("network %s still exists after the prune", unusedName)
	}
	for name, id := range map[string]string{usedName: used, stoppedName: stopped, laterName: later} {
		if !networkExists(t, id) {
			t.Errorf("network %s was removed by the prune", name)
		}
	}
}
