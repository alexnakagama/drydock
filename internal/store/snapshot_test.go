package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

func ctr(id, name, imageID string, state container.ContainerState, mounts ...container.MountPoint) container.Summary {
	return container.Summary{
		ID: id, Names: []string{"/" + name}, ImageID: imageID, State: state, Mounts: mounts,
	}
}

func volMount(name, dest string) container.MountPoint {
	return container.MountPoint{Type: mount.TypeVolume, Name: name, Destination: dest}
}

func containerNames(cs []container.Summary) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, ContainerName(c))
	}
	return out
}

func TestBuildIndexes(t *testing.T) {
	t.Parallel()
	const nginx, app = "sha256:aaa", "sha256:bbb"
	cs := []container.Summary{
		ctr("1", "web", nginx, container.StateRunning, volMount("pgdata", "/a")),
		ctr("2", "old", nginx, container.StateExited),
		// Mounts the same volume twice: counted once.
		ctr("3", "db", app, container.StateExited, volMount("pgdata", "/b"), volMount("pgdata", "/c"),
			container.MountPoint{Type: mount.TypeBind, Source: "/host", Destination: "/d"}),
		ctr("4", "noimage", "", container.StateCreated, volMount("cache", "/e")),
	}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)

	if got := snap.ImageUseCount(nginx); got != 2 {
		t.Errorf("ImageUseCount(nginx) = %d, want 2 (running and stopped)", got)
	}
	if got := containerNames(snap.ImageUsers(nginx)); !slices.Equal(got, []string{"old", "web"}) {
		t.Errorf("ImageUsers(nginx) = %v, want sorted [old web]", got)
	}
	if got := snap.ImageUseCount(app); got != 1 {
		t.Errorf("ImageUseCount(app) = %d, want 1", got)
	}
	if got := snap.ImageUseCount(""); got != 0 {
		t.Errorf("containers without an image ID are indexed: %d", got)
	}
	if got := containerNames(snap.VolumeUsers("pgdata")); !slices.Equal(got, []string{"db", "web"}) {
		t.Errorf("VolumeUsers(pgdata) = %v, want [db web]", got)
	}
	if got := snap.VolumeUseCount("cache"); got != 1 {
		t.Errorf("VolumeUseCount(cache) = %d, want 1", got)
	}
	if got := snap.VolumeUseCount("/host"); got != 0 {
		t.Errorf("bind mounts are indexed as volumes: %d", got)
	}
	if got := snap.VolumeUseCount("missing"); got != 0 {
		t.Errorf("VolumeUseCount(missing) = %d, want 0", got)
	}
}

// All tags of an image share its ID, so every tag row reports the same
// IN USE count.
func TestMultiTagImageUsage(t *testing.T) {
	t.Parallel()
	const id = "sha256:ccc"
	cs := []container.Summary{
		ctr("1", "a", id, container.StateRunning),
		ctr("2", "b", id, container.StateExited),
	}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)
	// Rows "app:latest" and "app:1.0" both look up the image ID.
	for _, tag := range []string{"app:latest", "app:1.0"} {
		if got := snap.ImageUseCount(id); got != 2 {
			t.Errorf("%s: IN USE = %d, want 2", tag, got)
		}
	}
}

func TestSnapshotAccessorsReturnCopies(t *testing.T) {
	t.Parallel()
	cs := []container.Summary{ctr("1", "b", "sha256:x", container.StateRunning), ctr("2", "a", "sha256:x", container.StateRunning)}
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)

	got := snap.Containers()
	got[0].ID = "mutated"
	slices.Reverse(got)
	if snap.Containers()[0].ID != "1" {
		t.Error("modifying the result of Containers changed the snapshot")
	}
	users := snap.ImageUsers("sha256:x")
	users[0] = container.Summary{}
	if ContainerName(snap.ImageUsers("sha256:x")[0]) != "a" {
		t.Error("modifying the result of ImageUsers changed the snapshot")
	}
}

func TestZeroSnapshot(t *testing.T) {
	t.Parallel()
	var snap Snapshot
	if snap.Loaded() {
		t.Error("zero snapshot reports Loaded")
	}
	if len(snap.Containers()) != 0 || len(snap.Images()) != 0 || len(snap.Volumes()) != 0 ||
		snap.ImageUseCount("x") != 0 || snap.VolumeUseCount("x") != 0 {
		t.Error("zero snapshot is not empty")
	}
}

func TestContainerNameAndShortID(t *testing.T) {
	t.Parallel()
	if got := ContainerName(container.Summary{Names: []string{"/web"}}); got != "web" {
		t.Errorf("ContainerName = %q, want web", got)
	}
	if got := ContainerName(container.Summary{ID: "0123456789abcdef0123"}); got != "0123456789ab" {
		t.Errorf("ContainerName without names = %q, want short ID", got)
	}
	if got := ShortID("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("ShortID = %q", got)
	}
	if got := ContainerName(container.Summary{Names: []string{"/evil\x1b[2J\x1b]0;title\a"}}); strings.ContainsAny(got, "\x1b\a") {
		t.Errorf("ContainerName keeps control characters: %q", got)
	}
	if got := ShortID("abc"); got != "abc" {
		t.Errorf("ShortID(short) = %q", got)
	}
}

func TestSnapshotCounts(t *testing.T) {
	t.Parallel()
	var zero Snapshot
	if n := zero.NumContainers() + zero.NumImages() + zero.NumVolumes(); n != 0 {
		t.Errorf("zero Snapshot counts sum to %d, want 0", n)
	}

	snap := Snapshot{
		seq: 1,
		containers: []container.Summary{
			ctr("1", "web", "sha256:a", container.StateRunning),
			ctr("2", "old", "sha256:a", container.StateExited),
			ctr("3", "new", "sha256:b", container.StateCreated),
		},
		images:  []image.Summary{{ID: "sha256:a"}, {ID: "sha256:b"}, {ID: "sha256:c"}, {ID: "sha256:d"}},
		volumes: []volume.Volume{{Name: "data"}, {Name: "cache"}},
	}
	if got, want := snap.NumContainers(), len(snap.Containers()); got != want || got != 3 {
		t.Errorf("NumContainers() = %d, want len(Containers()) = %d (3)", got, want)
	}
	if got, want := snap.NumImages(), len(snap.Images()); got != want || got != 4 {
		t.Errorf("NumImages() = %d, want len(Images()) = %d (4)", got, want)
	}
	if got, want := snap.NumVolumes(), len(snap.Volumes()); got != want || got != 2 {
		t.Errorf("NumVolumes() = %d, want len(Volumes()) = %d (2)", got, want)
	}
}

// --- Compose projects -----------------------------------------------

// composeCtr returns a container labeled as Compose labels it.
func composeCtr(id, name, project, service string, state container.ContainerState, oneoff bool, workingDir string) container.Summary {
	c := ctr(id, name, "", state)
	c.Labels = dockerfake.Compose{Project: project, Service: service, OneOff: oneoff, WorkingDir: workingDir}.Labels()
	return c
}

func svc(id, name, project string, state container.ContainerState) container.Summary {
	return composeCtr(id, name, project, "svc", state, false, "")
}

func snapshotOf(cs ...container.Summary) Snapshot {
	snap := Snapshot{seq: 1, containers: cs}
	snap.imageUsers, snap.volumeUsers = buildIndexes(cs)
	snap.projects, snap.projectNames = buildProjects(cs)
	return snap
}

func projectNames(ps []Project) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func onlyProject(t *testing.T, snap Snapshot) Project {
	t.Helper()
	ps := snap.Projects()
	if len(ps) != 1 {
		t.Fatalf("Projects() = %v, want exactly one", projectNames(ps))
	}
	return ps[0]
}

// The fake's label names must stay in step with the store's.
func TestComposeLabelNamesMatchFake(t *testing.T) {
	t.Parallel()
	pairs := [][2]string{
		{ComposeProjectLabel, dockerfake.ComposeProjectLabel},
		{ComposeServiceLabel, dockerfake.ComposeServiceLabel},
		{ComposeOneoffLabel, dockerfake.ComposeOneoffLabel},
		{ComposeWorkingDirLabel, dockerfake.ComposeWorkingDirLabel},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("store label %q != dockerfake label %q", p[0], p[1])
		}
	}
}

func TestBuildProjectsGrouping(t *testing.T) {
	t.Parallel()
	emptyLabel := ctr("6", "empty", "", container.StateRunning)
	emptyLabel.Labels = map[string]string{ComposeProjectLabel: ""}
	cs := []container.Summary{
		svc("1", "myapp-web-1", "myapp", container.StateRunning),
		svc("2", "blog-web-1", "blog", container.StateExited),
		ctr("3", "redis", "", container.StateRunning), // no labels at all
		svc("4", "myapp-db-1", "myapp", container.StateExited),
		svc("5", "myapp-db-1", "myapp", container.StateExited), // same name: ordered by ID
		emptyLabel,
	}
	projects, names := buildProjects(cs)

	if !slices.Equal(names, []string{"blog", "myapp"}) {
		t.Errorf("names = %v, want [blog myapp]", names)
	}
	if len(projects) != 2 {
		t.Errorf("projects has %d entries, want 2 (unlabeled and empty labels ignored)", len(projects))
	}
	var ids []string
	for _, c := range projects["myapp"] {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, []string{"4", "5", "1"}) {
		t.Errorf("myapp containers = %v, want sorted by name then ID [4 5 1]", ids)
	}
	if got := containerNames(projects["blog"]); !slices.Equal(got, []string{"blog-web-1"}) {
		t.Errorf("blog containers = %v", got)
	}

	snap := snapshotOf(cs...)
	if snap.NumProjects() != 2 || !slices.Equal(projectNames(snap.Projects()), []string{"blog", "myapp"}) {
		t.Errorf("Projects() = %v (NumProjects %d), want [blog myapp]", projectNames(snap.Projects()), snap.NumProjects())
	}
}

func TestBuildProjectsNoCompose(t *testing.T) {
	t.Parallel()
	snap := snapshotOf(ctr("1", "redis", "", container.StateRunning))
	if snap.NumProjects() != 0 || len(snap.Projects()) != 0 {
		t.Errorf("unlabeled containers form projects: %v", projectNames(snap.Projects()))
	}
	var zero Snapshot
	if zero.NumProjects() != 0 || len(zero.Projects()) != 0 {
		t.Error("zero snapshot has projects")
	}
}

func TestProjectDisappearsWithLastContainer(t *testing.T) {
	t.Parallel()
	web := svc("1", "myapp-web-1", "myapp", container.StateRunning)
	blog := svc("2", "blog-web-1", "blog", container.StateExited)
	before := snapshotOf(web, blog)
	after := snapshotOf(web)

	if got := projectNames(before.Projects()); !slices.Equal(got, []string{"blog", "myapp"}) {
		t.Errorf("before removal = %v", got)
	}
	if got := projectNames(after.Projects()); !slices.Equal(got, []string{"myapp"}) {
		t.Errorf("after removing blog's last container = %v, want [myapp]", got)
	}
	if before.NumProjects() != 2 {
		t.Error("building a new snapshot changed an older one")
	}
}

func TestProjectSummary(t *testing.T) {
	t.Parallel()
	const (
		r = container.StateRunning
		p = container.StatePaused
		x = container.StateExited
		c = container.StateCreated
		d = container.StateDead
		s = container.StateRestarting
	)
	tests := []struct {
		name             string
		states           []container.ContainerState
		oneoffs          int // running one-off containers added
		running, total   int
		state            ProjectState
		stateName        string
		wantOneOffsCount int
	}{
		{name: "all running", states: []container.ContainerState{r, r}, running: 2, total: 2, state: ProjectRunning, stateName: "running"},
		{name: "partly running", states: []container.ContainerState{r, r, x}, running: 2, total: 3, state: ProjectPartial, stateName: "partial"},
		{name: "paused is not running", states: []container.ContainerState{r, p}, running: 1, total: 2, state: ProjectPartial, stateName: "partial"},
		{name: "all paused is partial", states: []container.ContainerState{p, p}, running: 0, total: 2, state: ProjectPartial, stateName: "partial"},
		{name: "restarting is not running", states: []container.ContainerState{s}, running: 0, total: 1, state: ProjectPartial, stateName: "partial"},
		{name: "exited, created and dead", states: []container.ContainerState{x, c, d}, running: 0, total: 3, state: ProjectExited, stateName: "exited"},
		{name: "one-off not counted", states: []container.ContainerState{r, r}, oneoffs: 1, running: 2, total: 2, state: ProjectRunning, stateName: "running", wantOneOffsCount: 1},
		{name: "only one-off", oneoffs: 2, running: 0, total: 0, state: ProjectExited, stateName: "exited", wantOneOffsCount: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var cs []container.Summary
			for i, st := range tc.states {
				id := string(rune('a' + i))
				cs = append(cs, svc(id, "app-"+id, "app", st))
			}
			for i := range tc.oneoffs {
				id := string(rune('A' + i))
				cs = append(cs, composeCtr(id, "app-run-"+id, "app", "web", r, true, ""))
			}
			got := onlyProject(t, snapshotOf(cs...))
			if got.Running != tc.running || got.Total() != tc.total {
				t.Errorf("RUNNING = %d/%d, want %d/%d", got.Running, got.Total(), tc.running, tc.total)
			}
			if got.State != tc.state || got.State.String() != tc.stateName {
				t.Errorf("State = %v (%d), want %s", got.State, got.State, tc.stateName)
			}
			if len(got.OneOffs) != tc.wantOneOffsCount {
				t.Errorf("OneOffs = %d, want %d", len(got.OneOffs), tc.wantOneOffsCount)
			}
			for _, o := range got.OneOffs {
				if !IsOneoff(o) {
					t.Errorf("%s classified as one-off without the label", ContainerName(o))
				}
			}
		})
	}
}

func TestIsOneoff(t *testing.T) {
	t.Parallel()
	for v, want := range map[string]bool{"True": true, "true": true, "False": false, "": false, "yes": false} {
		c := container.Summary{Labels: map[string]string{ComposeOneoffLabel: v}}
		if got := IsOneoff(c); got != want {
			t.Errorf("IsOneoff(%q) = %v, want %v", v, got, want)
		}
	}
	if IsOneoff(container.Summary{}) {
		t.Error("IsOneoff without labels = true")
	}
}

func TestProjectWorkingDir(t *testing.T) {
	t.Parallel()
	t.Run("first container in name order", func(t *testing.T) {
		t.Parallel()
		got := onlyProject(t, snapshotOf(
			composeCtr("1", "myapp-web-1", "myapp", "web", container.StateRunning, false, "/home/me/web"),
			composeCtr("2", "myapp-db-1", "myapp", "db", container.StateRunning, false, "/home/me/db"),
		))
		if got.WorkingDir != "/home/me/db" {
			t.Errorf("WorkingDir = %q, want the one of myapp-db-1", got.WorkingDir)
		}
	})
	t.Run("skips containers without one", func(t *testing.T) {
		t.Parallel()
		got := onlyProject(t, snapshotOf(
			composeCtr("1", "a", "myapp", "web", container.StateRunning, false, ""),
			composeCtr("2", "b", "myapp", "db", container.StateRunning, true, "/srv/myapp"),
		))
		if got.WorkingDir != "/srv/myapp" {
			t.Errorf("WorkingDir = %q, want /srv/myapp", got.WorkingDir)
		}
	})
	t.Run("none recorded", func(t *testing.T) {
		t.Parallel()
		got := onlyProject(t, snapshotOf(svc("1", "a", "myapp", container.StateRunning)))
		if got.WorkingDir != "" {
			t.Errorf("WorkingDir = %q, want empty", got.WorkingDir)
		}
	})
}

func TestProjectsReturnCopies(t *testing.T) {
	t.Parallel()
	snap := snapshotOf(
		svc("1", "b", "myapp", container.StateRunning),
		svc("2", "a", "myapp", container.StateRunning),
	)
	p := snap.Projects()[0]
	p.Services[0] = container.Summary{}
	slices.Reverse(p.Services)
	if got := containerNames(snap.Projects()[0].Services); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("modifying a Projects() result changed the snapshot: %v", got)
	}
}
