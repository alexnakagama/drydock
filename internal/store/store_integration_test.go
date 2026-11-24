//go:build integration

package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
)

// These end-to-end tests run a store with its production timings against the
// engine configured by the environment (DOCKER_HOST or the default socket):
//
//	go test -tags integration ./internal/store/...
//
// They change engine state through the docker CLI, as a user would outside
// drydock, and check that each change reaches the store's snapshots within
// the 2 seconds the docker-connection spec allows. Every resource they create
// is named "drydock-it-<random>" (images "drydock-it/<random>"), carries the
// label drydock-it=1, and is removed when the test ends. They need the docker
// CLI and the alpine:latest image (present or pullable). The CLI is pointed at
// the store's engine (see cliEnv), whatever docker context is active.

const (
	// updateDeadline is the spec's bound on how long an external change may
	// take to appear (docker-connection, "Live updates from engine events").
	updateDeadline = 2 * time.Second
	// cliTimeout bounds each docker CLI call, including a possible pull.
	cliTimeout = 2 * time.Minute
	// itLabel marks every resource these tests create.
	itLabelKey, itLabelValue = "drydock-it", "1"
)

// liveRecorder keeps the latest snapshot and connection state a store sent.
type liveRecorder struct {
	mu     sync.Mutex
	snap   Snapshot
	state  State
	reason string
	notify chan struct{} // signalled (without blocking) on every message
}

func newLiveRecorder() *liveRecorder {
	return &liveRecorder{state: StateConnecting, notify: make(chan struct{}, 1)}
}

func (r *liveRecorder) send(m tea.Msg) {
	r.mu.Lock()
	switch m := m.(type) {
	case SnapshotMsg:
		r.snap = m.Snapshot
	case ConnectionMsg:
		r.state, r.reason = m.State, m.Reason
	}
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *liveRecorder) current() (Snapshot, State, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap, r.state, r.reason
}

// waitFor waits until the store is connected and its latest snapshot
// satisfies cond, failing the test after timeout. It returns how long that
// took.
func (r *liveRecorder) waitFor(t *testing.T, timeout time.Duration, what string, cond func(Snapshot) bool) time.Duration {
	t.Helper()
	start := time.Now()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		snap, state, reason := r.current()
		if state == StateConnected && snap.Loaded() && cond(snap) {
			return time.Since(start)
		}
		select {
		case <-r.notify:
		case <-deadline.C:
			// A message may have arrived together with the deadline; judge
			// (and report) the latest state, not the one read above.
			snap, state, reason = r.current()
			if state == StateConnected && snap.Loaded() && cond(snap) {
				return time.Since(start)
			}
			t.Fatalf("%s: not reflected in the store within %v (state %s, reason %q, snapshot seq %d)",
				what, timeout, state, reason, snap.Seq())
		}
	}
}

// awaitChange waits, within the spec's deadline, for an external change that
// has just completed to show in the store, and logs the latency.
func (r *liveRecorder) awaitChange(t *testing.T, what string, cond func(Snapshot) bool) {
	t.Helper()
	t.Logf("%s: seen after %v", what, r.waitFor(t, updateDeadline, what, cond))
}

// startStore runs a store with the production configuration against the
// real engine until the test ends, and waits for its first snapshot.
func startStore(t *testing.T) *liveRecorder {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	svc, err := docker.NewSDKService("drydock-integration-test")
	if err != nil {
		t.Fatalf("NewSDKService: %v", err)
	}
	rec := newLiveRecorder()
	st := New(svc, rec.send)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Run did not return after cancel")
		}
		_ = svc.Close()
	})
	rec.waitFor(t, 15*time.Second, "initial load", func(Snapshot) bool { return true })
	return rec
}

// dockerCLI runs the docker CLI with args (never through a shell) and returns
// its trimmed standard output, failing the test on error.
func dockerCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runDocker(args...)
	if err != nil {
		t.Fatalf("docker %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// cliEnv returns the environment for docker CLI calls: the test's own, with
// DOCKER_HOST set to the endpoint the SDK resolves (the one the store
// watches) and DOCKER_CONTEXT removed. Unlike the SDK, the CLI honours docker
// contexts, so without this an active context would make the tests change
// one engine while the store watches another, as drydock's exec view also
// guards against.
var cliEnv = sync.OnceValues(func() ([]string, error) {
	svc, err := docker.NewSDKService("")
	if err != nil {
		return nil, fmt.Errorf("resolve engine endpoint: %w", err)
	}
	defer svc.Close()
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == "DOCKER_HOST" || k == "DOCKER_CONTEXT"
	})
	return append(env, "DOCKER_HOST="+svc.Host()), nil
})

// runDocker runs the docker CLI with args and returns its trimmed standard
// output. Its context is not the test's, so that cleanups can use it after
// the test's context is cancelled.
func runDocker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	env, err := cliEnv()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Do not wait forever for output pipes held open by a helper process
	// (such as a credential helper) that outlives a killed CLI.
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// The cleanup helpers below are registered before the resource is created,
// so that a resource is removed even if the test fails right after creating
// it (or the CLI times out after the engine created it). They remove only a
// resource that carries the test label, so a name that happened to belong to
// someone else is never touched, and a resource that is already gone is not
// an error. Failures fail the test: a leaked resource must not go unnoticed.

// cleanupContainer force-removes the labelled container with the given name.
func cleanupContainer(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		out, err := runDocker("ps", "--all", "--no-trunc", "--filter", "label="+itLabelKey+"="+itLabelValue,
			"--format", "{{.ID}}\t{{.Names}}")
		if err != nil {
			t.Errorf("cleanup: list containers: %v", err)
			return
		}
		for line := range strings.Lines(out) {
			id, names, _ := strings.Cut(strings.TrimSpace(line), "\t")
			if !slices.Contains(strings.Split(names, ","), name) {
				continue
			}
			if _, err := runDocker("rm", "--force", id); err != nil && !isNoSuch(err, "container") {
				t.Errorf("cleanup: remove container %s: %v", name, err)
			}
		}
	})
}

// cleanupVolume removes the labelled volume with the given name. Register it
// before the cleanup of any container that mounts the volume, so that it
// runs after it.
func cleanupVolume(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		out, err := runDocker("volume", "ls", "--quiet", "--filter", "label="+itLabelKey+"="+itLabelValue)
		if err != nil {
			t.Errorf("cleanup: list volumes: %v", err)
			return
		}
		if !slices.Contains(strings.Fields(out), name) {
			return
		}
		if _, err := runDocker("volume", "rm", name); err != nil && !isNoSuch(err, "volume") {
			t.Errorf("cleanup: remove volume %s: %v", name, err)
		}
	})
}

// cleanupImageRef removes the image reference ref if it names a labelled
// image. Removal is never forced: if other references share the image, only
// ref is untagged.
func cleanupImageRef(t *testing.T, ref string) {
	t.Helper()
	t.Cleanup(func() {
		label, err := runDocker("image", "inspect", "--format", `{{index .Config.Labels "`+itLabelKey+`"}}`, ref)
		if err != nil {
			if !isNoSuch(err, "image") {
				t.Errorf("cleanup: inspect image %s: %v", ref, err)
			}
			return
		}
		if label != itLabelValue {
			t.Errorf("cleanup: image %s lacks the %s label; not removing it", ref, itLabelKey)
			return
		}
		if _, err := runDocker("rmi", ref); err != nil && !isNoSuch(err, "image") {
			t.Errorf("cleanup: remove image %s: %v", ref, err)
		}
	})
}

// isNoSuch reports whether a docker CLI error is the engine saying that the
// resource of the given kind ("container", "image" or "volume") does not
// exist. It is deliberately narrow, so that a connection failure such as
// "no such host" is not mistaken for a resource that is already gone.
func isNoSuch(err error, kind string) bool {
	return strings.Contains(strings.ToLower(err.Error()), "no such "+kind)
}

// randomHex returns 12 random hex digits.
func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// uniqueName returns a fresh container or volume name in the test namespace.
func uniqueName(t *testing.T) string { return "drydock-it-" + randomHex(t) }

func findContainer(s Snapshot, id string) (container.Summary, bool) {
	for _, c := range s.Containers() {
		if c.ID == id {
			return c, true
		}
	}
	return container.Summary{}, false
}

func findVolume(s Snapshot, name string) (volume.Volume, bool) {
	for _, v := range s.Volumes() {
		if v.Name == name {
			return v, true
		}
	}
	return volume.Volume{}, false
}

func findImage(s Snapshot, id string) (image.Summary, bool) {
	for _, img := range s.Images() {
		if img.ID == id {
			return img, true
		}
	}
	return image.Summary{}, false
}

func hasITLabel(labels map[string]string) bool { return labels[itLabelKey] == itLabelValue }

func containerInState(id, state string) func(Snapshot) bool {
	return func(s Snapshot) bool {
		c, ok := findContainer(s, id)
		return ok && string(c.State) == state
	}
}

func TestIntegrationStoreTracksContainers(t *testing.T) {
	rec := startStore(t)
	name := uniqueName(t)
	cleanupContainer(t, name)

	id := dockerCLI(t, "create", "--name", name, "--label", itLabelKey+"="+itLabelValue,
		"alpine:latest", "sleep", "300")
	rec.awaitChange(t, "container created", func(s Snapshot) bool {
		c, ok := findContainer(s, id)
		return ok && string(c.State) == "created" && hasITLabel(c.Labels) && ContainerName(c) == name
	})

	dockerCLI(t, "start", id)
	rec.awaitChange(t, "container started", containerInState(id, "running"))

	dockerCLI(t, "stop", "--time", "0", id)
	rec.awaitChange(t, "container stopped", containerInState(id, "exited"))

	dockerCLI(t, "rm", id)
	rec.awaitChange(t, "container removed", func(s Snapshot) bool {
		_, ok := findContainer(s, id)
		return !ok
	})
}

func TestIntegrationStoreTracksVolumes(t *testing.T) {
	rec := startStore(t)
	vol, ctr := uniqueName(t), uniqueName(t)
	// Cleanups run in reverse: the container must go before its volume.
	cleanupVolume(t, vol)
	cleanupContainer(t, ctr)

	dockerCLI(t, "volume", "create", "--label", itLabelKey+"="+itLabelValue, vol)
	rec.awaitChange(t, "volume created", func(s Snapshot) bool {
		v, ok := findVolume(s, vol)
		return ok && hasITLabel(v.Labels) && s.VolumeUseCount(vol) == 0
	})

	// A stopped container that mounts the volume counts as a user; usage
	// is refreshed by container events, not volume events.
	id := dockerCLI(t, "create", "--name", ctr, "--label", itLabelKey+"="+itLabelValue,
		"--mount", "type=volume,source="+vol+",target=/data", "alpine:latest", "true")
	rec.awaitChange(t, "volume user added", func(s Snapshot) bool {
		users := s.VolumeUsers(vol)
		return len(users) == 1 && users[0].ID == id
	})

	dockerCLI(t, "rm", id)
	rec.awaitChange(t, "volume user removed", func(s Snapshot) bool {
		_, ok := findVolume(s, vol)
		return ok && s.VolumeUseCount(vol) == 0
	})

	dockerCLI(t, "volume", "rm", vol)
	rec.awaitChange(t, "volume removed", func(s Snapshot) bool {
		_, ok := findVolume(s, vol)
		return !ok
	})
}

func TestIntegrationStoreTracksImages(t *testing.T) {
	rec := startStore(t)
	ctr := uniqueName(t)
	repo := "drydock-it/" + randomHex(t)
	ref1, ref2 := repo+":one", repo+":two"
	// The container is created from alpine:latest, not from the committed
	// image, so the cleanups do not depend on each other's order.
	cleanupContainer(t, ctr)
	cleanupImageRef(t, ref1)
	cleanupImageRef(t, ref2)

	// Commit a labelled image of its own, so that removing it never touches
	// an image the user relies on.
	dockerCLI(t, "create", "--name", ctr, "--label", itLabelKey+"="+itLabelValue, "alpine:latest", "true")
	dockerCLI(t, "commit", "--change", "LABEL "+itLabelKey+"="+itLabelValue, ctr, ref1)
	imgID := dockerCLI(t, "image", "inspect", "--format", "{{.Id}}", ref1)
	rec.awaitChange(t, "image committed", func(s Snapshot) bool {
		img, ok := findImage(s, imgID)
		return ok && hasITLabel(img.Labels) && slices.Equal(img.RepoTags, []string{ref1}) &&
			s.ImageUseCount(imgID) == 0
	})

	dockerCLI(t, "tag", ref1, ref2)
	rec.awaitChange(t, "second tag added", func(s Snapshot) bool {
		img, ok := findImage(s, imgID)
		return ok && len(img.RepoTags) == 2 && slices.Contains(img.RepoTags, ref2)
	})

	// Removing one of two tags only untags the image.
	dockerCLI(t, "rmi", ref1)
	rec.awaitChange(t, "first tag removed", func(s Snapshot) bool {
		img, ok := findImage(s, imgID)
		return ok && slices.Equal(img.RepoTags, []string{ref2})
	})

	dockerCLI(t, "rmi", ref2)
	rec.awaitChange(t, "image removed", func(s Snapshot) bool {
		_, ok := findImage(s, imgID)
		return !ok
	})
}
