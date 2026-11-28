package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// anonVolume is the generated name of an anonymous volume.
var anonVolume = strings.Repeat("7", 64)

// mounting builds a container that mounts the named volumes.
func mounting(n int, name string, state container.ContainerState, vols ...string) container.Summary {
	c := ctr(cid(n), name, "postgres:16", state, string(state))
	for _, v := range vols {
		c.Mounts = append(c.Mounts, container.MountPoint{
			Type: mount.TypeVolume, Name: v, Destination: "/data/" + v, Driver: "local",
		})
	}
	return c
}

// volumeSample returns a fake engine with:
//   - pgdata, mounted by the stopped container db;
//   - shared, mounted by db and the running container web;
//   - cache-data and tmp1, unused;
//   - an unused anonymous volume.
func volumeSample() *dockerfake.Service {
	f := dockerfake.New()
	f.AddVolume(volume.Volume{Name: "pgdata"})
	f.AddVolume(volume.Volume{Name: "shared"})
	f.AddVolume(volume.Volume{Name: "tmp1"})
	f.AddVolume(volume.Volume{Name: "cache-data"})
	f.AddVolume(volume.Volume{Name: anonVolume, Labels: map[string]string{dockerfake.AnonymousVolumeLabel: ""}})
	f.AddContainer(mounting(0, "db", container.StateExited, "pgdata", "shared"))
	f.AddContainer(mounting(1, "web", container.StateRunning, "shared"))
	return f
}

// volumesEnv starts an env on the given engine and switches to the volumes
// view.
func volumesEnv(t *testing.T, f *dockerfake.Service, width, height int) *env {
	t.Helper()
	e := newEnv(t, f, width, height)
	e.command("vol")
	if !strings.HasPrefix(e.header(), "drydock > volumes") {
		t.Fatalf("volumes view not shown; screen:\n%s", e.screen())
	}
	return e
}

func (e *env) volumesView() *volumesView { return e.volumes[len(e.volumes)-1] }

// selectVolume moves the selection of the volumes view to the named
// volume.
func (e *env) selectVolume(name string) {
	e.t.Helper()
	if !e.volumesView().table.Select(name) {
		e.t.Fatalf("no volume row %s; screen:\n%s", name, e.screen())
	}
}

// volumeNames returns the names of the volumes listed, in order, as the
// screen shows them (long names are truncated with "…").
func (e *env) volumeNames() []string {
	var out []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) == 4 {
			out = append(out, f[0])
		}
	}
	return out
}

// shownAs returns name as a NAME cell of the given width shows it.
func shownAs(name string, width int) string {
	if len(name) <= width {
		return name
	}
	return name[:width-1] + "…"
}

// anonShown is anonVolume as the NAME column shows it at width 100: the 98
// cells right of the gutter leave 6 spare cells over the minimum widths,
// and NAME and MOUNTPOINT both want more than half of them, so each gets 3.
var anonShown = shownAs(anonVolume, 33)

func volumeRemoveCalls(f *dockerfake.Service) []dockerfake.Call {
	return f.CallsTo(dockerfake.MethodVolumeRemove)
}

// wantVolumeRemoves checks that VolumeRemove was called, without force,
// for exactly names, in order.
func wantVolumeRemoves(t *testing.T, f *dockerfake.Service, names ...string) {
	t.Helper()
	calls := volumeRemoveCalls(f)
	var got []string
	for _, c := range calls {
		got = append(got, c.Args[0].(string))
		if c.Args[1] != (docker.VolumeRemoveOptions{Force: false}) {
			t.Errorf("VolumeRemove%v, want no force", c.Args)
		}
	}
	if !slices.Equal(got, names) {
		t.Errorf("VolumeRemove calls for %v, want %v", got, names)
	}
}

func TestVolumesGolden(t *testing.T) {
	e := volumesEnv(t, volumeSample(), 100, 10)
	checkGolden(t, "volumes_100x10.golden", e.screen())
}

func TestVolumesList(t *testing.T) {
	e := volumesEnv(t, volumeSample(), 100, 14)
	want := []string{anonShown, "cache-data", "pgdata", "shared", "tmp1"}
	if got := e.volumeNames(); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	for name, uses := range map[string]string{"pgdata": "1", "shared": "2", "tmp1": "0"} {
		f := rowFields(e.rowOf(name))
		if len(f) != 4 || f[1] != "local" || f[2] != "/var/lib/docker/volumes/"+name+"/_data" || f[3] != uses {
			t.Errorf("%s row %q, want local driver, its mountpoint and IN USE %s", name, e.rowOf(name), uses)
		}
	}
	if h := e.header(); !strings.HasPrefix(h, "drydock > volumes") {
		t.Errorf("header %q", h)
	}
	// The anonymous volume is listed, and acted on, by its generated name.
	e.selectVolume(anonVolume)
	if f := rowFields(e.rowOf(anonShown)); len(f) != 4 || f[1] != "local" || f[3] != "0" {
		t.Errorf("anonymous volume row %q", e.rowOf(anonShown))
	}
}

func TestVolumesEmptyAndFilter(t *testing.T) {
	e := volumesEnv(t, dockerfake.New(), 100, 10)
	if !strings.Contains(e.screen(), "No volumes.") {
		t.Errorf("empty state:\n%s", e.screen())
	}

	e = volumesEnv(t, volumeSample(), 100, 14)
	e.press("/")
	e.typeText("DATA")
	e.press("enter")
	if got, want := e.volumeNames(), []string{"cache-data", "pgdata"}; !slices.Equal(got, want) {
		t.Errorf("filtered rows %v, want %v", got, want)
	}
	if !strings.Contains(e.header(), "[/DATA]") {
		t.Errorf("header %q, want the filter indicator", e.header())
	}
	e.press("esc")
	if len(e.volumeNames()) != 5 {
		t.Errorf("filter not cleared:\n%s", e.screen())
	}
}

func TestVolumesSelectionSurvivesRefresh(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	e.selectVolume("pgdata")
	f.AddVolume(volume.Volume{Name: "aaa"})
	e.until("new volume listed", func() bool { return e.rowOf("aaa") != "" })
	if vol, _ := e.volumesView().selected(); vol.Name != "pgdata" {
		t.Errorf("selection moved to %q", vol.Name)
	}
	// Mounting it elsewhere refreshes IN USE.
	f.AddContainer(mounting(5, "other", container.StateRunning, "pgdata"))
	e.until("usage refreshed", func() bool { return strings.HasSuffix(e.rowOf("pgdata"), " 2") })
}

func TestCreateNamedVolume(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	e.press("c")
	e.settle()
	if !strings.Contains(e.screen(), "Create volume") {
		t.Fatalf("prompt not shown:\n%s", e.screen())
	}
	e.typeText("cache-data2")
	e.press("enter")
	e.waitStatus("create volume cache-data2: created")
	e.until("volume listed", func() bool { return e.rowOf("cache-data2") != "" })
	calls := f.CallsTo(dockerfake.MethodVolumeCreate)
	if len(calls) != 1 || calls[0].Args[0] != "cache-data2" {
		t.Errorf("VolumeCreate calls %v, want one for cache-data2", calls)
	}
}

func TestCreateVolumeWithGeneratedName(t *testing.T) {
	f := dockerfake.New()
	e := volumesEnv(t, f, 100, 14)
	e.press("c")
	e.settle()
	e.press("enter")
	e.waitStatus("create volume: created ")
	calls := f.CallsTo(dockerfake.MethodVolumeCreate)
	if len(calls) != 1 || calls[0].Args[0] != "" {
		t.Fatalf("VolumeCreate calls %v, want one with an empty name", calls)
	}
	if c := f.CallsTo(dockerfake.MethodVolumeInspect); len(c) != 0 {
		t.Errorf("existence check for a generated name: %v", c)
	}
	e.until("volume listed", func() bool { return len(e.volumesView().shown) == 1 })
	for name := range e.volumesView().shown {
		if !strings.Contains(e.statusLine(), name) {
			t.Errorf("status %q does not name the created volume %s", e.statusLine(), name)
		}
	}
}

func TestCreateExistingVolumeRefused(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	f.ResetCalls()
	e.press("c")
	e.settle()
	e.typeText("pgdata")
	e.press("enter")
	e.waitStatus("volume pgdata already exists")
	e.settle()
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodVolumeCreate || c.Method == dockerfake.MethodVolumeInspect
	}) {
		t.Errorf("engine called for a name in the snapshot: %v", c)
	}
}

func TestCreateVolumeMissingFromSnapshotRefused(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	e.press("c")
	e.settle()
	f.ResetCalls()
	// Another client creates the volume; its event is not delivered to the
	// view before the prompt is submitted, so the snapshot does not have it.
	f.AddVolume(volume.Volume{Name: "late", Labels: map[string]string{"owner": "other"}})
	e.typeText("late")
	e.press("enter")
	e.waitStatus("create volume late: volume late already exists")
	if c := f.CallsTo(dockerfake.MethodVolumeCreate); len(c) != 0 {
		t.Errorf("VolumeCreate called for an existing name: %v", c)
	}
	if c := f.CallsTo(dockerfake.MethodVolumeInspect); len(c) != 1 || c[0].Args[0] != "late" {
		t.Errorf("VolumeInspect calls %v, want one for late", c)
	}
}

func TestCreateVolumeInspectFailure(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	f.FailNext(dockerfake.MethodVolumeInspect, errors.New("permission denied"))
	e.press("c")
	e.settle()
	e.typeText("new1")
	e.press("enter")
	e.waitStatus("create volume new1: permission denied")
	if c := f.CallsTo(dockerfake.MethodVolumeCreate); len(c) != 0 {
		t.Errorf("VolumeCreate called after a failed existence check: %v", c)
	}
}

func TestCreateVolumeInvalidName(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	f.ResetCalls()
	e.press("c")
	e.settle()
	e.typeText("../etc")
	e.press("enter")
	e.waitStatus("create volume: invalid volume name")
	e.settle()
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodVolumeCreate || c.Method == dockerfake.MethodVolumeInspect
	}) {
		t.Errorf("engine called for an invalid name: %v", c)
	}
}

func TestCreateVolumeCancelled(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	e.press("c")
	e.settle()
	e.typeText("nope")
	e.press("esc")
	e.settle()
	if strings.Contains(e.screen(), "Create volume") {
		t.Errorf("prompt still open:\n%s", e.screen())
	}
	if c := f.CallsTo(dockerfake.MethodVolumeCreate); len(c) != 0 {
		t.Errorf("VolumeCreate called after cancel: %v", c)
	}
	if e.rowOf("nope") != "" {
		t.Errorf("volume created after cancel")
	}
}

func TestDeleteUnusedVolume(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("cache-data")
	e.press("d")
	e.settle()
	if d := e.dialogText(); !strings.Contains(d, "Delete volume cache-data?") || !strings.Contains(d, "Volume cache-data (local driver) and all its data will be removed.") {
		t.Fatalf("confirmation:\n%s", e.screen())
	}
	e.press("y")
	e.waitStatus("delete volume cache-data: done")
	e.until("row removed", func() bool { return e.rowOf("cache-data") == "" })
	wantVolumeRemoves(t, f, "cache-data")
}

func TestDeleteInUseVolumeRefused(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("shared")
	e.press("d")
	e.settle()
	s := e.screen()
	for _, want := range []string{"shared is mounted by 2 containers;", "the engine will refuse to remove it:", "db (exited)", "web (running)"} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, s)
		}
	}
	e.press("y")
	e.waitStatus("delete volume shared: remove shared: volume is in use")
	e.settle()
	if e.rowOf("shared") == "" {
		t.Errorf("volume no longer listed:\n%s", e.screen())
	}
	wantVolumeRemoves(t, f, "shared")
}

func TestDeleteVolumeCancelled(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("tmp1")
	e.press("d")
	e.settle()
	e.press("esc")
	e.settle()
	if len(volumeRemoveCalls(f)) != 0 || e.rowOf("tmp1") == "" {
		t.Errorf("cancelled delete removed the volume")
	}
}

func TestInspectVolume(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("pgdata")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"Mountpoint"`) })
	if raw := e.m.View().Content; !strings.Contains(raw, components.Styles.JSONKey.Render(`"Name"`)) {
		t.Errorf("volume inspection not colored:\n%q", raw)
	}
	if h := e.header(); !strings.HasPrefix(h, "drydock > volumes > pgdata > inspect") {
		t.Errorf("header %q", h)
	}
	if calls := f.CallsTo(dockerfake.MethodVolumeInspect); len(calls) != 1 || calls[0].Args[0] != "pgdata" {
		t.Errorf("VolumeInspect calls %v, want one for pgdata", calls)
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > volumes") || strings.Contains(h, "inspect") {
		t.Errorf("header after esc %q", h)
	}
}

func TestVolumeDrillDownToContainers(t *testing.T) {
	f := volumeSample()
	f.AddContainer(mounting(5, "other", container.StateRunning, "tmp1"))
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("pgdata")
	e.press("enter")
	e.settle()
	if h := e.header(); !strings.HasPrefix(h, "drydock > volumes > pgdata > containers [all]") {
		t.Fatalf("header %q", h)
	}
	if e.rowOf("db") == "" || e.rowOf("web") != "" || e.rowOf("other") != "" {
		t.Errorf("drill-down lists the wrong containers:\n%s", e.screen())
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > volumes") || strings.Contains(h, "containers") {
		t.Errorf("header after esc %q", h)
	}
	if vol, _ := e.volumesView().selected(); vol.Name != "pgdata" {
		t.Errorf("selection after esc %q", vol.Name)
	}
}

func TestPruneUnusedVolumes(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	s := e.dialog()
	if !strings.Contains(s, "Prune unused volumes?") || !strings.Contains(s, "3 unused volumes and all their data will be removed:") {
		t.Fatalf("confirmation:\n%s", s)
	}
	// The anonymous volume's name is too long for the dialog: it is
	// shortened in the middle, keeping both ends.
	anon := anonVolume[:20] + "…"
	for _, want := range []string{"cache-data", "tmp1", anon, anonVolume[len(anonVolume)-20:]} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, s)
		}
	}
	// Volumes mounted by any container, even a stopped one, are kept.
	if strings.Contains(s, "pgdata") || strings.Contains(s, "shared") {
		t.Errorf("confirmation lists a volume in use:\n%s", s)
	}
	e.press("y")
	e.waitStatus("prune unused volumes: 3 volumes removed")
	wantVolumeRemoves(t, f, anonVolume, "cache-data", "tmp1")
	e.until("pruned volumes gone", func() bool {
		return slices.Equal(e.volumeNames(), []string{"pgdata", "shared"})
	})
}

func TestPruneVolumesPartialFailure(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	// After the preview, another client starts using tmp1: the engine
	// refuses to remove it and the others are still removed.
	f.AddContainer(mounting(5, "late", container.StateRunning, "tmp1"))
	e.press("y")
	e.waitStatus("prune unused volumes: 2 removed, 1 failed (tmp1): tmp1: remove tmp1: volume is in use")
	wantVolumeRemoves(t, f, anonVolume, "cache-data", "tmp1")
	e.settle()
	if e.rowOf("tmp1") == "" {
		t.Errorf("volume in use removed:\n%s", e.screen())
	}
}

func TestPruneVolumesNothingToPrune(t *testing.T) {
	f := dockerfake.New()
	f.AddVolume(volume.Volume{Name: "pgdata"})
	f.AddContainer(mounting(0, "db", container.StateExited, "pgdata"))
	e := volumesEnv(t, f, 100, 20)
	e.press("P")
	e.settle()
	if strings.Contains(e.screen(), "Prune unused volumes?") {
		t.Fatalf("confirmation shown with nothing to prune:\n%s", e.screen())
	}
	e.waitStatus("nothing to prune")
	if c := volumeRemoveCalls(f); len(c) != 0 {
		t.Errorf("VolumeRemove called: %v", c)
	}
}

func TestPruneVolumesCancelled(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	e.press("n")
	e.settle()
	if c := volumeRemoveCalls(f); len(c) != 0 {
		t.Errorf("VolumeRemove called after cancel: %v", c)
	}
}

// listVolumes returns the fake's volumes with the given names, as the
// snapshot lists them.
func listVolumes(t *testing.T, f *dockerfake.Service, names ...string) []volume.Volume {
	t.Helper()
	all, err := f.VolumeList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []volume.Volume
	for _, n := range names {
		i := slices.IndexFunc(all, func(v volume.Volume) bool { return v.Name == n })
		if i < 0 {
			t.Fatalf("no volume %s", n)
		}
		out = append(out, all[i])
	}
	return out
}

func TestRemoveVolumesReport(t *testing.T) {
	f := dockerfake.New()
	names := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7"}
	for _, n := range names {
		f.AddVolume(volume.Volume{Name: n})
	}
	vols := listVolumes(t, f, names...)
	f.AddContainer(mounting(0, "c", container.StateRunning, "a1", "a2", "a3", "a4", "a5"))
	f.RemoveVolume("a6")
	_, err := removeVolumes(context.Background(), f, vols)
	want := "1 removed, 1 already gone, 5 failed (a1, a2, a3, 2 more): a1: remove a1: volume is in use"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("error %v, want %q", err, want)
	}

	f.AddVolume(volume.Volume{Name: "b1"})
	detail, err := removeVolumes(context.Background(), f, append(listVolumes(t, f, "b1"), vols[5]))
	if err != nil || detail != "1 volume removed, 1 already gone" {
		t.Errorf("removeVolumes = %q, %v; want a gone volume counted as such", detail, err)
	}
}

// cancellingService cancels a context on the given VolumeRemove call, as a
// deadline expiring during a prune would.
type cancellingService struct {
	*dockerfake.Service
	cancel context.CancelFunc
	after  int
	n      int
}

func (s *cancellingService) VolumeRemove(ctx context.Context, name string, opts docker.VolumeRemoveOptions) error {
	s.n++
	if s.n == s.after {
		s.cancel()
		return ctx.Err()
	}
	return s.Service.VolumeRemove(ctx, name, opts)
}

func TestRemoveVolumesCancelledPartway(t *testing.T) {
	f := dockerfake.New()
	for _, n := range []string{"a1", "a2", "a3", "a4"} {
		f.AddVolume(volume.Volume{Name: n})
	}
	vols := listVolumes(t, f, "a1", "a2", "a3", "a4")
	f.AddContainer(mounting(0, "c", container.StateRunning, "a1"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &cancellingService{Service: f, cancel: cancel, after: 3}
	_, err := removeVolumes(ctx, svc, vols)
	// a1 fails (in use), a2 is removed, a3 is cut off and a4 never tried:
	// neither counts as failed.
	want := "1 removed, 1 failed (a1), 1 interrupted (a3), 1 not attempted: context canceled; a1: remove a1: volume is in use"
	if err == nil || !strings.HasPrefix(err.Error(), want) || !errors.Is(err, context.Canceled) {
		t.Errorf("error %v, want %q", err, want)
	}
	if svc.n != 3 {
		t.Errorf("%d VolumeRemove calls, want 3", svc.n)
	}

	f.ResetCalls()
	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	if _, err := removeVolumes(done, f, vols[3:]); !errors.Is(err, context.Canceled) ||
		!strings.Contains(err.Error(), "0 removed, 1 not attempted") {
		t.Errorf("cancelled prune error %v", err)
	}
	if c := f.Calls(); len(c) != 0 {
		t.Errorf("engine called after cancellation: %v", c)
	}
}

func TestDeleteReplacedVolumeRefused(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("tmp1")
	e.press("d")
	e.settle()
	// Another client replaces tmp1 by a new volume of the same name; the
	// view has not seen it yet.
	f.RemoveVolume("tmp1")
	f.AddVolume(volume.Volume{Name: "tmp1", CreatedAt: "2030-01-01T00:00:00Z"})
	e.press("y")
	e.waitStatus("delete volume tmp1: volume tmp1 was replaced since it was listed; nothing was removed")
	if c := volumeRemoveCalls(f); len(c) != 0 {
		t.Errorf("VolumeRemove called for a replaced volume: %v", c)
	}
}

func TestDeleteVolumeAlreadyGone(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	e.selectVolume("tmp1")
	e.press("d")
	e.settle()
	f.RemoveVolume("tmp1")
	e.press("y")
	e.waitStatus("delete volume tmp1: already removed")
}

func TestPruneVolumesReplacedAndGone(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	f.RemoveVolume("cache-data")
	f.RemoveVolume("tmp1")
	f.AddVolume(volume.Volume{Name: "tmp1", CreatedAt: "2030-01-01T00:00:00Z"})
	e.press("y")
	e.waitStatus("prune unused volumes: 1 removed, 1 already gone, 1 failed (tmp1): tmp1: volume tmp1 was replaced")
	wantVolumeRemoves(t, f, anonVolume)
}

func TestVolumesUnsupportedNames(t *testing.T) {
	f := dockerfake.New()
	// Plugin drivers may accept names drydock cannot put in a request path.
	f.AddVolume(volume.Volume{Name: "has space", Driver: "plugin"})
	e := volumesEnv(t, f, 100, 20)
	f.ResetCalls()
	e.selectVolume("has space")
	e.press("d")
	e.waitStatus(`delete: drydock cannot address volume "has space"`)
	e.press("i")
	e.waitStatus(`inspect: drydock cannot address volume "has space"`)
	e.press("P")
	e.waitStatus("nothing to prune: 1 unused volume skipped (unsupported names)")

	f.AddVolume(volume.Volume{Name: "ok1"})
	e.until("listed", func() bool { return e.rowOf("ok1") != "" })
	e.press("P")
	e.settle()
	if d := e.dialog(); !strings.Contains(d, "1 unused volume and all its data will be removed:") ||
		!strings.Contains(d, "(1 volume with unsupported names skipped)") || strings.Contains(d, "has space") {
		t.Errorf("confirmation:\n%s", d)
	}
	e.press("n")
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodVolumeRemove || c.Method == dockerfake.MethodVolumeInspect
	}) {
		t.Errorf("engine called for an unsupported name: %v", c)
	}
}

func TestCreateVolumeRejectsPathLikeNames(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 14)
	f.ResetCalls()
	for _, name := range []string{"..", ".", "a/b", `a\b`, "a?b", "a#b", "a%2fb", "a b"} {
		e.press("c")
		e.settle()
		e.deliver(tea.PasteMsg{Content: name})
		e.press("enter")
		e.waitStatus("create volume: invalid volume name")
		e.settle()
	}
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodVolumeCreate || c.Method == dockerfake.MethodVolumeInspect
	}) {
		t.Errorf("engine called for an invalid name: %v", c)
	}
}

func TestVolumesBeforeLoad(t *testing.T) {
	v := newVolumesView(store.New(dockerfake.New(), func(tea.Msg) {}), store.Snapshot{}, nil)
	v.SetSize(80, 6)
	if !strings.Contains(ansi.Strip(v.View()), "Loading volumes…") {
		t.Errorf("view before load:\n%s", v.View())
	}
	if msg := fmt.Sprintf("%+v", v.confirmPrune()()); !strings.Contains(msg, "prune: volumes are still loading") {
		t.Errorf("prune before load: %s", msg)
	}
}

func TestVolumesViewNeutralizesHostileText(t *testing.T) {
	f := dockerfake.New()
	// A name rejected by ValidateVolumeName still reaches the list,
	// dialogs, crumbs and status line, so it must be rendered safely.
	hostile := "v1\x1b]52;c;cHduZWQ=\a\u202e\x1b[2J"
	f.AddVolume(volume.Volume{Name: hostile, Driver: "evil\x1b[2J", Mountpoint: "/x\x1b]52;c;cHduZWQ=\a\u202e"})
	f.AddVolume(volume.Volume{Name: "v2", Driver: "evil\x1b[2J"})
	f.AddContainer(mounting(0, "db\x1b[2J", container.StateRunning, "v2"))
	e := volumesEnv(t, f, 100, 20)
	check := func(step string) {
		t.Helper()
		raw := e.m.View().Content
		if strings.Contains(raw, "\x1b]52") || strings.Contains(raw, "\x1b[2J") || strings.ContainsRune(raw, 0x202e) {
			t.Errorf("%s: injected escape sequence reached the screen", step)
		}
	}
	check("list")
	e.selectVolume(hostile)
	e.press("d")
	e.settle()
	check("delete refused")
	e.press("enter")
	e.settle()
	check("drill-down")
	e.press("esc")
	e.selectVolume("v2")
	e.press("d")
	e.settle()
	check("delete dialog")
	e.press("n")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"Mountpoint"`) })
	check("inspect")
	e.press("esc")
	e.press("P")
	e.settle()
	check("prune")
	e.press("n")
}

func TestVolumeActionsRefusedWhileDisconnected(t *testing.T) {
	f := volumeSample()
	e := volumesEnv(t, f, 100, 20)
	f.Disconnect(errors.New("connection refused"))
	e.until("disconnected", func() bool { return e.st.State() == store.StateDisconnected })
	f.ResetCalls()

	e.selectVolume("tmp1")
	e.press("d")
	e.settle()
	e.press("y")
	e.waitStatus("delete volume tmp1: not connected")

	e.press("c")
	e.settle()
	e.typeText("new1")
	e.press("enter")
	e.waitStatus("create volume new1: not connected")

	e.press("P")
	e.settle()
	e.press("y")
	e.waitStatus("prune unused volumes: not connected")
	if c := f.Calls(); slices.ContainsFunc(c, func(c dockerfake.Call) bool {
		return c.Method == dockerfake.MethodVolumeRemove || c.Method == dockerfake.MethodVolumeCreate ||
			c.Method == dockerfake.MethodVolumeInspect
	}) {
		t.Errorf("engine called while disconnected: %v", c)
	}
}

// dialog returns the text inside the open dialog's border.
func (e *env) dialog() string {
	var out []string
	for _, l := range e.lines() {
		if _, in, ok := strings.Cut(l, "│"); ok {
			in, _, _ = strings.Cut(in, "│")
			out = append(out, strings.TrimSpace(in))
		}
	}
	return strings.Join(out, "\n")
}

// dialogText returns the text inside the open dialog's border as one line,
// with runs of spaces collapsed, so text the dialog wraps can be matched.
func (e *env) dialogText() string { return strings.Join(strings.Fields(e.dialog()), " ") }
