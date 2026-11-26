package views

import (
	"slices"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

// helpEntries renders bindings as "key desc", prefixed with "-" when the
// binding is disabled, in order.
func helpEntries(bs []key.Binding) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		h := b.Help()
		s := h.Key + " " + h.Desc
		if !b.Enabled() {
			s = "-" + s
		}
		out[i] = s
	}
	return out
}

// findHelp returns the binding whose help key is k.
func findHelp(t *testing.T, bs []key.Binding, k string) key.Binding {
	t.Helper()
	for _, b := range bs {
		if b.Help().Key == k {
			return b
		}
	}
	t.Fatalf("no %q binding in %q", k, helpEntries(bs))
	return key.Binding{}
}

func checkHelp(t *testing.T, what string, got []key.Binding, want []string) {
	t.Helper()
	if g := helpEntries(got); !slices.Equal(g, want) {
		t.Errorf("%s: KeyHelp =\n  %q\nwant\n  %q", what, g, want)
	}
}

// rowTone returns the tone of the table row with the given key, selecting
// it.
func rowTone(t *testing.T, tbl *components.Table, rowKey string) components.Tone {
	t.Helper()
	if !tbl.Select(rowKey) {
		t.Fatalf("no row %q", rowKey)
	}
	r, ok := tbl.Selected()
	if !ok || r.Key != rowKey {
		t.Fatalf("selected %q, want %q", r.Key, rowKey)
	}
	return r.Tone
}

func TestContainerRowTones(t *testing.T) {
	f := dockerfake.New()
	states := []struct {
		state container.ContainerState
		want  components.Tone
	}{
		{container.StateRunning, components.ToneGood},
		{container.StatePaused, components.ToneWarn},
		{container.StateRestarting, components.ToneWarn},
		{container.StateExited, components.ToneDim},
		{container.StateCreated, components.ToneDim},
		{container.StateDead, components.ToneDim},
		{container.StateRemoving, components.ToneDefault},
		{"", components.ToneDefault},
	}
	for i, s := range states {
		f.AddContainer(ctr(cid(i), "c"+string(rune('a'+i)), "alpine", s.state, string(s.state)))
	}
	e := newEnv(t, f, 100, 16)
	e.press("a")
	v := e.root()
	for i, s := range states {
		if got := rowTone(t, &v.table, cid(i)); got != s.want {
			t.Errorf("state %q: tone %d, want %d", s.state, got, s.want)
		}
	}
}

func TestImageRowTones(t *testing.T) {
	e := imagesEnv(t, imageSample(), 100, 16)
	e.press("a")
	v := e.imagesView()
	if len(v.shown) == 0 {
		t.Fatal("no image rows")
	}
	sawDangling := false
	for k, r := range v.shown {
		want := components.ToneDefault
		if r.dangling {
			want = components.ToneDim
			sawDangling = true
		}
		if got := rowTone(t, &v.table, k); got != want {
			t.Errorf("image %s: tone %d, want %d", r.label, got, want)
		}
	}
	if !sawDangling {
		t.Error("sample lists no dangling image")
	}
}

func TestVolumeRowTones(t *testing.T) {
	e := volumesEnv(t, volumeSample(), 100, 16)
	v := e.volumesView()
	for name, want := range map[string]components.Tone{
		"pgdata":     components.ToneDefault, // used by a stopped container
		"shared":     components.ToneDefault,
		"tmp1":       components.ToneDim,
		"cache-data": components.ToneDim,
		anonVolume:   components.ToneDim,
	} {
		if got := rowTone(t, &v.table, name); got != want {
			t.Errorf("volume %s: tone %d, want %d", name, got, want)
		}
	}
}

func TestContainersKeyHelp(t *testing.T) {
	f, _ := sample()
	f.AddContainer(ctr(cid(4), "fresh", "alpine", container.StateCreated, "Created"))
	f.AddContainer(ctr(cid(5), "boot", "alpine", container.StateRestarting, "Restarting (1) 2 seconds ago"))
	e := newEnv(t, f, 100, 16)
	e.press("a")
	v := e.root()

	tests := []struct {
		name string
		want []string
	}{
		{"web", []string{"l logs", "s stop", "p pause", "x shell", "i inspect", "r restart", "d delete", "a all/running", "/ filter", "P prune stopped", "t stats"}},
		{"old", []string{"l logs", "s start", "-p pause/unpause", "-x shell", "i inspect", "r restart", "d delete", "a all/running", "/ filter", "P prune stopped", "t stats"}},
		{"fresh", []string{"l logs", "s start", "-p pause/unpause", "-x shell", "i inspect", "r restart", "d delete", "a all/running", "/ filter", "P prune stopped", "t stats"}},
		{"frozen", []string{"l logs", "-s start/stop", "p unpause", "-x shell", "i inspect", "r restart", "d delete", "a all/running", "/ filter", "P prune stopped", "t stats"}},
		{"boot", []string{"l logs", "-s start/stop", "-p pause/unpause", "-x shell", "i inspect", "r restart", "d delete", "a all/running", "/ filter", "P prune stopped", "t stats"}},
	}
	for _, tt := range tests {
		e.selectRow(tt.name)
		checkHelp(t, tt.name, v.KeyHelp(), tt.want)
	}
}

func TestContainersKeyHelpEmpty(t *testing.T) {
	e := newEnv(t, dockerfake.New(), 100, 8)
	checkHelp(t, "empty list", e.root().KeyHelp(), []string{
		"-l logs", "-s start/stop", "-p pause/unpause", "-x shell", "-i inspect", "-r restart", "-d delete", "a all/running", "/ filter",
		"P prune stopped", "t stats",
	})

	// A filter matching nothing leaves no selection either.
	f, _ := sample()
	e = newEnv(t, f, 100, 8)
	e.press("/")
	e.typeText("zzz")
	e.press("enter")
	checkHelp(t, "filtered to nothing", e.root().KeyHelp(), []string{
		"-l logs", "-s start/stop", "-p pause/unpause", "-x shell", "-i inspect", "-r restart", "-d delete", "a all/running", "/ filter",
		"P prune stopped", "t stats",
	})
}

func TestContainersFooterPauseHint(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 200, 12)
	e.press("a")
	for name, want := range map[string]string{"web": "p pause", "frozen": "p unpause"} {
		e.selectRow(name)
		ft := e.footer()
		if !strings.Contains(ft, "  "+want+"  ") {
			t.Errorf("%s: footer %q, want %q", name, ft, want)
		}
		if !strings.Contains(ft, "s ") || strings.Index(ft, want) < strings.Index(ft, "s ") {
			t.Errorf("%s: footer %q, want %q right after the s hint", name, ft, want)
		}
	}
	e.selectRow("old")
	if ft := e.footer(); strings.Contains(ft, "p pause") || strings.Contains(ft, "p unpause") {
		t.Errorf("exited row: footer %q, want no p hint", ft)
	}
	if ft := e.footer(); !strings.Contains(ft, "P prune stopped  t stats") {
		t.Errorf("footer %q, want P prune stopped just before t stats", ft)
	}

	e = newEnv(t, dockerfake.New(), 200, 8)
	if ft := e.footer(); strings.Contains(ft, " p ") || !strings.Contains(ft, "P prune stopped") {
		t.Errorf("empty list: footer %q, want no p hint and P prune stopped", ft)
	}
}

// TestFooterDropsStatsHintFirst checks that the t hint stays the first view
// hint dropped from a narrow footer, now that P comes before it.
func TestFooterDropsStatsHintFirst(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 120, 12)
	e.selectRow("web")
	ft := e.footer()
	if strings.Contains(ft, "t stats") || !strings.Contains(ft, "P prune stopped") || !strings.HasSuffix(ft, "? more") {
		t.Errorf("footer %q, want P prune stopped kept and t stats dropped", ft)
	}
}

func TestHelpListsPauseDimmed(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 30)
	e.press("a")
	e.selectRow("old")
	if b := findHelp(t, e.root().KeyHelp(), "p"); b.Enabled() || b.Help().Desc != "pause/unpause" {
		t.Fatalf("p hint = %+v enabled=%v, want it disabled with the generic label", b.Help(), b.Enabled())
	}
	e.press("?")
	if s := e.screen(); !strings.Contains(s, "p          pause/unpause") {
		t.Errorf("help lacks the dimmed p entry:\n%s", s)
	}
}

func TestHiddenPauseKeyStillWorks(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	e.selectRow("old")
	e.press("p")
	e.waitStatus("pause/unpause is unavailable for a exited container (old)")
}

func TestImagesKeyHelp(t *testing.T) {
	e := imagesEnv(t, imageSample(), 100, 16)
	v := e.imagesView()

	e.selectImage("nginx:1.27")
	checkHelp(t, "used image", v.KeyHelp(), []string{
		"enter containers", "i inspect", "p pull", "d delete", "/ filter", "a all/tagged", "P prune dangling",
	})
	e.selectImage("app:latest")
	checkHelp(t, "unused image", v.KeyHelp(), []string{
		"-enter containers", "i inspect", "p pull", "d delete", "/ filter", "a all/tagged", "P prune dangling",
	})

	e = imagesEnv(t, dockerfake.New(), 100, 8)
	checkHelp(t, "empty list", e.imagesView().KeyHelp(), []string{
		"-enter containers", "-i inspect", "p pull", "-d delete", "/ filter", "a all/tagged", "P prune dangling",
	})
}

func TestVolumesKeyHelp(t *testing.T) {
	e := volumesEnv(t, volumeSample(), 100, 16)
	v := e.volumesView()

	e.selectVolume("shared")
	checkHelp(t, "used volume", v.KeyHelp(), []string{
		"enter containers", "i inspect", "c create", "d delete", "/ filter", "P prune unused",
	})
	e.selectVolume("tmp1")
	checkHelp(t, "unused volume", v.KeyHelp(), []string{
		"-enter containers", "i inspect", "c create", "d delete", "/ filter", "P prune unused",
	})

	e = volumesEnv(t, dockerfake.New(), 100, 8)
	checkHelp(t, "empty list", e.volumesView().KeyHelp(), []string{
		"-enter containers", "-i inspect", "c create", "-d delete", "/ filter", "P prune unused",
	})
}

// Hiding a hint never disables its key: the views match key presses against
// the package-level keymaps, which KeyHelp must leave untouched.

func TestHiddenShellKeyStillWorks(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	e.selectRow("old")
	if b := findHelp(t, e.root().KeyHelp(), "x"); b.Enabled() {
		t.Fatalf("x hint = %+v, want it disabled", b.Help())
	}
	e.press("x")
	e.waitStatus("old must be running to open a shell")
}

func TestHiddenStartStopKeyStillWorks(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("frozen")
	if b := findHelp(t, e.root().KeyHelp(), "s"); b.Enabled() {
		t.Fatalf("s hint = %+v, want it disabled", b.Help())
	}
	f.ResetCalls()
	e.press("s")
	e.waitStatus("start/stop is unavailable for a paused container")
	e.settle()
	for _, c := range f.Calls() {
		if c.Method == dockerfake.MethodContainerStart || c.Method == dockerfake.MethodContainerStop {
			t.Errorf("engine called: %+v", c)
		}
	}
}

func TestKeyHelpLeavesKeymapsUntouched(t *testing.T) {
	wantContainers := helpEntries(keys.Containers.Bindings())
	wantImages := helpEntries(keys.Images.Bindings())
	wantVolumes := helpEntries(keys.Volumes.Bindings())

	// Exercise every disabling and relabelling path.
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	for _, name := range []string{"web", "old", "frozen"} {
		e.selectRow(name)
		e.root().KeyHelp()
	}
	newEnv(t, dockerfake.New(), 100, 8).root().KeyHelp()
	imagesEnv(t, imageSample(), 100, 12).imagesView().KeyHelp()
	imagesEnv(t, dockerfake.New(), 100, 8).imagesView().KeyHelp()
	ve := volumesEnv(t, volumeSample(), 100, 12)
	ve.selectVolume("tmp1")
	ve.volumesView().KeyHelp()
	volumesEnv(t, dockerfake.New(), 100, 8).volumesView().KeyHelp()

	for _, c := range []struct {
		name      string
		got, want []string
	}{
		{"keys.Containers", helpEntries(keys.Containers.Bindings()), wantContainers},
		{"keys.Images", helpEntries(keys.Images.Bindings()), wantImages},
		{"keys.Volumes", helpEntries(keys.Volumes.Bindings()), wantVolumes},
		{"keys.Table.Filter", helpEntries([]key.Binding{keys.Table.Filter}), []string{"/ filter"}},
	} {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s changed: %q, want %q", c.name, c.got, c.want)
		}
		for _, s := range c.got {
			if s[0] == '-' {
				t.Errorf("%s: %q is disabled", c.name, s)
			}
		}
	}
	if got := helpEntries(keys.Containers.Bindings())[0]; got != "s start/stop" {
		t.Errorf("keys.Containers.StartStop help = %q, want the generic label", got)
	}

	// The bound keys themselves, and matching, are unchanged too.
	for _, c := range []struct {
		b    key.Binding
		want []string
	}{
		{keys.Containers.StartStop, []string{"s"}},
		{keys.Containers.Pause, []string{"p"}},
		{keys.Containers.Prune, []string{"P"}},
		{keys.Containers.Shell, []string{"x"}},
		{keys.Containers.Logs, []string{"l"}},
		{keys.Images.Containers, []string{"enter"}},
		{keys.Volumes.Containers, []string{"enter"}},
	} {
		if !slices.Equal(c.b.Keys(), c.want) {
			t.Errorf("binding %q keys = %q, want %q", c.b.Help().Key, c.b.Keys(), c.want)
		}
		if !key.Matches(uitest.KeyPress(c.want[0]), c.b) {
			t.Errorf("binding %q no longer matches %q", c.b.Help().Key, c.want[0])
		}
	}
}

func TestListViewsReportPosition(t *testing.T) {
	f := dockerfake.New()
	f.AddVolume(volume.Volume{Name: "a"})
	f.AddVolume(volume.Volume{Name: "b"})
	f.AddVolume(volume.Volume{Name: "c"})
	e := volumesEnv(t, f, 100, 12)
	var p ui.Positioner = e.volumesView()
	e.press("j")
	if s, l, tot, filt := p.Position(); s != 2 || l != 3 || tot != 3 || filt {
		t.Errorf("Position = %d,%d,%d,%v, want 2,3,3,false", s, l, tot, filt)
	}
	e.press("/")
	e.typeText("c")
	e.press("enter")
	if s, l, tot, filt := p.Position(); s != 1 || l != 1 || tot != 3 || !filt {
		t.Errorf("filtered Position = %d,%d,%d,%v, want 1,1,3,true", s, l, tot, filt)
	}
}
