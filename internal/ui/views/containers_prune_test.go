package views

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
)

// openPrune presses P and waits for the prune confirmation dialog.
func (e *env) openPrune() {
	e.t.Helper()
	e.press("P")
	e.until("prune confirmation", func() bool { return strings.Contains(e.screen(), "Prune stopped containers?") })
}

// pruneListed returns the container names the open prune dialog lists, in
// order, with their states.
func (e *env) pruneListed() []string {
	var out []string
	in := false
	for _, l := range strings.Split(e.dialog(), "\n") {
		switch {
		case strings.HasSuffix(l, "will be removed:"):
			in = true
		case l == "Their volumes are kept.":
			in = false
		case in && l != "":
			out = append(out, strings.Join(strings.Fields(l), " "))
		}
	}
	return out
}

// wantContainerRemoves checks that ContainerRemove was called, without
// force and keeping volumes, for exactly ids, in order.
func wantContainerRemoves(t *testing.T, f *dockerfake.Service, ids ...string) {
	t.Helper()
	var got []string
	for _, c := range f.CallsTo(dockerfake.MethodContainerRemove) {
		got = append(got, c.Args[0].(string))
		if opts := c.Args[1].(docker.ContainerRemoveOptions); opts != (docker.ContainerRemoveOptions{}) {
			t.Errorf("ContainerRemove%v, want no force and no volume removal", c.Args)
		}
	}
	if !slices.Equal(got, ids) {
		t.Errorf("ContainerRemove calls for %v, want %v", got, ids)
	}
}

// hasContainer reports whether the fake engine still has the container.
func hasContainer(t *testing.T, f *dockerfake.Service, id string) bool {
	t.Helper()
	cs, err := f.ContainerList(t.Context(), docker.ContainerListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(cs, func(c container.Summary) bool { return c.ID == id })
}

func TestContainerPruneStoppedContainers(t *testing.T) {
	f, ids := sample()
	ids["tmp"] = f.AddContainer(ctr(cid(4), "tmp", "alpine", container.StateExited, "Exited (1) 1 hour ago"))
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	if s := e.dialog(); !strings.Contains(s, "2 stopped containers will be removed:") ||
		!strings.Contains(s, "Their volumes are kept.") {
		t.Fatalf("confirmation:\n%s", s)
	}
	if got, want := e.pruneListed(), []string{"old exited", "tmp exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 2 containers removed")
	wantContainerRemoves(t, f, ids["old"], ids["tmp"])
	for _, n := range []string{"web", "db", "frozen"} {
		if !hasContainer(t, f, ids[n]) {
			t.Errorf("%s removed", n)
		}
	}
}

func TestContainerPruneIncludesCreatedAndDead(t *testing.T) {
	f := dockerfake.New()
	ids := map[string]string{
		"web":    f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StateRunning, "Up 5 minutes")),
		"new":    f.AddContainer(ctr(cid(1), "new", "alpine", container.StateCreated, "Created")),
		"broken": f.AddContainer(ctr(cid(2), "broken", "alpine", container.StateDead, "Dead")),
	}
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	if got, want := e.pruneListed(), []string{"broken dead", "new created"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 2 containers removed")
	wantContainerRemoves(t, f, ids["broken"], ids["new"])
	if !hasContainer(t, f, ids["web"]) {
		t.Error("web removed")
	}
}

func TestContainerPruneIncludesHiddenStoppedContainers(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 24)
	if e.root().all || e.rowOf("old") != "" {
		t.Fatalf("old listed in running-only mode:\n%s", e.screen())
	}
	e.openPrune()
	if got, want := e.pruneListed(), []string{"old exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")
	wantContainerRemoves(t, f, ids["old"])
}

func TestContainerPruneIgnoresFilter(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 24)
	e.press("a")
	e.press("/")
	e.typeText("web")
	e.press("enter")
	if e.rowOf("old") != "" || e.rowOf("web") == "" {
		t.Fatalf("filter not applied:\n%s", e.screen())
	}
	e.openPrune()
	if got, want := e.pruneListed(), []string{"old exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")
	wantContainerRemoves(t, f, ids["old"])
	if !hasContainer(t, f, ids["web"]) {
		t.Error("web removed")
	}
}

func TestContainerPruneDrillDownScope(t *testing.T) {
	f := dockerfake.New()
	ids := map[string]string{
		"web":   f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StateRunning, "Up 1 minute")),
		"old":   f.AddContainer(ctr(cid(1), "old", "nginx:1.27", container.StateExited, "Exited (0)")),
		"other": f.AddContainer(ctr(cid(2), "other", "postgres:16", container.StateExited, "Exited (0)")),
	}
	e := newEnv(t, f, 100, 24)
	nginxID := "sha256:" + strings.Repeat("n", 64)
	e.push(NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"nginx:1.27", "containers"},
		"No containers use nginx:1.27.", func(c container.Summary) bool { return c.ImageID == nginxID }))
	e.openPrune()
	if got, want := e.pruneListed(), []string{"old exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")
	wantContainerRemoves(t, f, ids["old"])
	if !hasContainer(t, f, ids["other"]) || !hasContainer(t, f, ids["web"]) {
		t.Error("a container outside the drill-down's scope was removed")
	}
}

func TestContainerPruneKeepsPausedAndRestarting(t *testing.T) {
	f := dockerfake.New()
	ids := map[string]string{
		"web":    f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StatePaused, "Up 1 hour (Paused)")),
		"worker": f.AddContainer(ctr(cid(1), "worker", "app", container.StateRestarting, "Restarting (1) 2 seconds ago")),
		"old":    f.AddContainer(ctr(cid(2), "old", "alpine", container.StateExited, "Exited (0)")),
	}
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	if got, want := e.pruneListed(), []string{"old exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")
	wantContainerRemoves(t, f, ids["old"])
	if !hasContainer(t, f, ids["web"]) || !hasContainer(t, f, ids["worker"]) {
		t.Error("an active container was removed")
	}
}

func TestContainerPruneKeepsVolumes(t *testing.T) {
	f := dockerfake.New()
	f.AddVolume(volume.Volume{Name: "pgdata"})
	f.AddVolume(volume.Volume{Name: anonVolume, Labels: map[string]string{dockerfake.AnonymousVolumeLabel: ""}})
	c := ctr(cid(0), "db", "postgres:16", container.StateExited, "Exited (0)")
	c.Mounts = []container.MountPoint{
		{Type: mount.TypeVolume, Name: "pgdata", Destination: "/var/lib/postgresql/data"},
		{Type: mount.TypeVolume, Name: anonVolume, Destination: "/tmp/anon"},
	}
	id := f.AddContainer(c)
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	e.press("y")
	e.waitStatus("prune stopped containers: 1 container removed")
	wantContainerRemoves(t, f, id)
	vols, err := f.VolumeList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range vols {
		names = append(names, v.Name)
	}
	slices.Sort(names)
	if want := []string{anonVolume, "pgdata"}; !slices.Equal(names, want) {
		t.Errorf("volumes after prune = %v, want %v", names, want)
	}
}

func TestContainerPruneContainerStartedAfterPreview(t *testing.T) {
	f, ids := sample()
	ids["tmp"] = f.AddContainer(ctr(cid(4), "tmp", "alpine", container.StateExited, "Exited (0)"))
	e := newEnv(t, f, 160, 24)
	e.openPrune()
	// Another client starts old after the preview: the engine refuses to
	// remove it without force, and tmp is still removed.
	if err := f.ContainerStart(t.Context(), ids["old"]); err != nil {
		t.Fatal(err)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 1 removed, 1 failed (old): old: ")
	wantContainerRemoves(t, f, ids["old"], ids["tmp"])
	if !hasContainer(t, f, ids["old"]) {
		t.Error("old removed although it was running")
	}
	if hasContainer(t, f, ids["tmp"]) {
		t.Error("tmp not removed")
	}
}

func TestContainerPruneContainerAlreadyGone(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	// Removed with the docker CLI after the preview.
	if !f.RemoveContainer(ids["old"]) {
		t.Fatal("old not in the fake")
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 0 containers removed, 1 already gone")
	if s := e.statusLine(); strings.Contains(s, "failed") {
		t.Errorf("status = %q, want no failure", s)
	}
}

func TestContainerPruneNothingToPrune(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StateRunning, "Up 5 minutes"))
	f.AddContainer(ctr(cid(1), "frozen", "redis:7", container.StatePaused, "Up 1 hour (Paused)"))
	e := newEnv(t, f, 100, 20)
	e.press("P")
	e.waitStatus("nothing to prune: no stopped containers")
	e.settle()
	if strings.Contains(e.screen(), "Prune stopped containers?") {
		t.Fatalf("confirmation shown with nothing to prune:\n%s", e.screen())
	}
	wantContainerRemoves(t, f)
}

func TestContainerPruneBeforeLoad(t *testing.T) {
	v := newContainersView(store.New(dockerfake.New(), func(tea.Msg) {}), store.Snapshot{}, nil,
		[]string{"containers"}, "No containers.", nil, false, defaultExecEnv())
	if msg := fmt.Sprintf("%+v", v.confirmPrune()()); !strings.Contains(msg, "prune: containers are still loading") {
		t.Errorf("prune before load: %s", msg)
	}
}

func TestContainerPruneCancelled(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 24)
	e.openPrune()
	e.press("n")
	e.settle()
	wantContainerRemoves(t, f)
	if strings.Contains(e.screen(), "Prune stopped containers?") {
		t.Errorf("dialog still open:\n%s", e.screen())
	}
}

func TestContainerPruneGolden(t *testing.T) {
	f, _ := sample()
	f.AddContainer(ctr(cid(4), "fresh", "app", container.StateCreated, "Created"))
	f.AddContainer(ctr(cid(5), "tmp", "alpine", container.StateExited, "Exited (1) 1 hour ago"))
	e := newEnv(t, f, 80, 24)
	e.openPrune()
	checkGolden(t, "containers_prune_80x24.golden", e.screen())
}

// TestContainerPruneNeutralizesHostileText checks that container names
// carrying escape sequences reach neither the dialog nor the failure report
// raw.
func TestContainerPruneNeutralizesHostileText(t *testing.T) {
	f := dockerfake.New()
	hostile := "ev\x1b]52;c;cHduZWQ=\a\u202e\x1b[2Jil\rx"
	id := f.AddContainer(ctr(cid(0), hostile, "alpine", container.StateExited, "Exited (0)"))
	e := newEnv(t, f, 160, 24)
	check := func(step string) {
		t.Helper()
		raw := e.m.View().Content
		if strings.Contains(raw, "\x1b]52") || strings.Contains(raw, "\x1b[2J") ||
			strings.ContainsRune(raw, 0x202e) || strings.ContainsRune(raw, '\r') {
			t.Errorf("%s: injected control sequence reached the screen", step)
		}
	}
	e.openPrune()
	check("dialog")
	// Started after the preview, so the report names it as failed.
	if err := f.ContainerStart(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	e.press("y")
	e.waitStatus("prune stopped containers: 0 removed, 1 failed (")
	check("failure report")
}
