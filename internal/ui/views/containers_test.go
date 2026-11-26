package views

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

func pub(ip string, public, private uint16, proto string) container.PortSummary {
	return container.PortSummary{IP: netip.MustParseAddr(ip), PublicPort: public, PrivatePort: private, Type: proto}
}

func exposed(private uint16, proto string) container.PortSummary {
	return container.PortSummary{PrivatePort: private, Type: proto}
}

// sample returns a fake engine with running containers web and db, a
// stopped container old, and a paused container frozen.
func sample() (*dockerfake.Service, map[string]string) {
	f := dockerfake.New()
	ids := map[string]string{
		"web":    f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StateRunning, "Up 5 minutes", pub("0.0.0.0", 8080, 80, "tcp"), pub("::", 8080, 80, "tcp"))),
		"db":     f.AddContainer(ctr(cid(1), "db", "postgres:16", container.StateRunning, "Up 2 hours", exposed(5432, "tcp"))),
		"old":    f.AddContainer(ctr(cid(2), "old", "alpine", container.StateExited, "Exited (0) 3 days ago")),
		"frozen": f.AddContainer(ctr(cid(3), "frozen", "redis:7", container.StatePaused, "Up 1 hour (Paused)")),
	}
	return f, ids
}

func TestFormatPorts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		ports []container.PortSummary
		want  string
	}{
		{"none", nil, ""},
		{"published", []container.PortSummary{pub("0.0.0.0", 8080, 80, "tcp")}, "8080->80/tcp"},
		{"ipv4 and ipv6 deduplicated", []container.PortSummary{pub("0.0.0.0", 8080, 80, "tcp"), pub("::", 8080, 80, "tcp")}, "8080->80/tcp"},
		{"exposed only", []container.PortSummary{exposed(5432, "tcp")}, "5432/tcp"},
		{"exposed and published shows published", []container.PortSummary{exposed(80, "tcp"), pub("0.0.0.0", 8080, 80, "tcp")}, "8080->80/tcp"},
		{"sorted by container port", []container.PortSummary{pub("0.0.0.0", 9443, 443, "tcp"), exposed(53, "udp"), pub("0.0.0.0", 8080, 80, "tcp")}, "53/udp, 8080->80/tcp, 9443->443/tcp"},
		{"same port, two protocols", []container.PortSummary{exposed(53, "udp"), exposed(53, "tcp")}, "53/tcp, 53/udp"},
		{"same container port, two host ports", []container.PortSummary{pub("0.0.0.0", 8081, 80, "tcp"), pub("0.0.0.0", 8080, 80, "tcp")}, "8080->80/tcp, 8081->80/tcp"},
		{"missing protocol defaults to tcp", []container.PortSummary{{PrivatePort: 22}}, "22/tcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatPorts(tt.ports); got != tt.want {
				t.Errorf("FormatPorts = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContainersGolden(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 10)
	checkGolden(t, "containers_running_100x10.golden", e.screen())

	e.press("a")
	checkGolden(t, "containers_all_100x10.golden", e.screen())
}

func TestContainersGoldenEmpty(t *testing.T) {
	e := newEnv(t, dockerfake.New(), 100, 8)
	checkGolden(t, "containers_empty_100x8.golden", e.screen())
}

func TestContainersDefaultListsRunningSortedByName(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)

	var names []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) > 0 {
			names = append(names, f[0])
		}
	}
	// frozen is paused: still "up", as in `docker ps`.
	if want := []string{"db", "frozen", "web"}; !slices.Equal(names, want) {
		t.Errorf("listed %v, want %v", names, want)
	}
	if !strings.Contains(e.header(), "drydock > containers [running]") {
		t.Errorf("header = %q, want the running-only indicator", e.header())
	}
	if row := e.rowOf("web"); !strings.Contains(row, "8080->80/tcp") || strings.Count(row, "8080->80/tcp") != 1 {
		t.Errorf("web row = %q, want its port once", row)
	}
	if row := e.rowOf("db"); !strings.Contains(row, "5432/tcp") {
		t.Errorf("db row = %q, want its exposed port", row)
	}
}

func TestContainersToggleAll(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	if row := e.rowOf("old"); !strings.Contains(row, "Exited (0)") {
		t.Errorf("old row = %q, want its exited status", row)
	}
	if !strings.Contains(e.header(), "[all]") {
		t.Errorf("header = %q, want the all indicator", e.header())
	}
	e.press("a")
	if e.rowOf("old") != "" || !strings.Contains(e.header(), "[running]") {
		t.Errorf("after a second toggle:\n%s", e.screen())
	}
}

func TestContainersEmptyStates(t *testing.T) {
	e := newEnv(t, dockerfake.New(), 100, 8)
	if !strings.Contains(e.screen(), "No running containers.") {
		t.Errorf("empty engine:\n%s", e.screen())
	}
	e.press("a")
	if !strings.Contains(e.screen(), "No containers.") {
		t.Errorf("empty engine in all mode:\n%s", e.screen())
	}

	f := dockerfake.New()
	f.AddContainer(ctr(cid(0), "old", "alpine", container.StateExited, "Exited (0)"))
	e = newEnv(t, f, 100, 8)
	if !strings.Contains(e.screen(), "No running containers (1 stopped; press a to show all).") {
		t.Errorf("only stopped containers:\n%s", e.screen())
	}
}

// allContainerStates lists every state the engine reports, plus an
// unknown one.
var allContainerStates = []container.ContainerState{
	container.StateCreated, container.StateRunning, container.StatePaused, container.StateRestarting,
	container.StateRemoving, container.StateExited, container.StateDead, "", "bogus",
}

func TestContainerGlyph(t *testing.T) {
	t.Parallel()
	tests := []struct {
		state container.ContainerState
		want  string
	}{
		{container.StateRunning, "●"},
		{container.StatePaused, "◐"},
		{container.StateRestarting, "◐"},
		{container.StateExited, "○"},
		{container.StateCreated, "○"},
		{container.StateDead, "○"},
		{container.StateRemoving, " "},
		{"", " "},
		{"bogus", " "},
	}
	for _, tt := range tests {
		if got := containerGlyph(tt.state); got != tt.want {
			t.Errorf("containerGlyph(%q) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestContainerGlyphAgreesWithTone(t *testing.T) {
	t.Parallel()
	byTone := map[components.Tone]string{
		components.ToneGood:    "●",
		components.ToneWarn:    "◐",
		components.ToneDim:     "○",
		components.ToneDefault: " ",
	}
	for _, s := range allContainerStates {
		tone := containerTone(s)
		if got, want := containerGlyph(s), byTone[tone]; got != want {
			t.Errorf("state %q: glyph %q, but tone %d implies %q", s, got, tone, want)
		}
		if w := ansi.StringWidth(containerGlyph(s)); w != 1 {
			t.Errorf("state %q: glyph is %d cells wide, want 1", s, w)
		}
	}
}

// statusCell returns the STATUS cell of the content line listing name,
// starting at the STATUS header's column.
func (e *env) statusCell(name string) string {
	e.t.Helper()
	content := e.content()
	if len(content) == 0 {
		e.t.Fatalf("no content; screen:\n%s", e.screen())
	}
	col := strings.Index(content[0], "STATUS") // the header row is ASCII
	if col < 0 {
		e.t.Fatalf("no STATUS header in %q", content[0])
	}
	row := []rune(e.rowOf(name))
	if len(row) <= col {
		e.t.Fatalf("row %s = %q, too short for the STATUS column at %d", name, string(row), col)
	}
	return string(row[col:])
}

func TestContainersStatusGlyphs(t *testing.T) {
	f, _ := sample()
	f.AddContainer(ctr(cid(4), "gone", "alpine", container.StateRemoving, "Removal In Progress"))
	e := newEnv(t, f, 120, 14)
	e.press("a")
	for name, want := range map[string]string{
		"web":    "● Up 5 minutes",
		"db":     "● Up 2 hours",
		"frozen": "◐ Up 1 hour (Paused)",
		"old":    "○ Exited (0) 3 days ago",
		"gone":   "  Removal In Progress",
	} {
		if got := e.statusCell(name); !strings.HasPrefix(got, want) {
			t.Errorf("%s STATUS cell = %q, want it to begin with %q", name, got, want)
		}
	}
}

func TestContainersGlyphOnSelectedRow(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	if row := e.rowOf("web"); !strings.HasPrefix(row, "▌") {
		t.Fatalf("web row = %q, want it selected", row)
	}
	if got := e.statusCell("web"); !strings.HasPrefix(got, "● Up 5 minutes") {
		t.Errorf("selected web STATUS cell = %q, want it to begin with the running glyph", got)
	}
}

func TestStopRunningContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("s")
	e.waitStatus("stop web: done")
	if calls := f.CallsTo(dockerfake.MethodContainerStop); len(calls) != 1 || calls[0].Args[0] != ids["web"] {
		t.Errorf("ContainerStop calls = %+v", calls)
	}
	// Running-only mode: web leaves the list once the store refreshes.
	e.until("web gone", func() bool { return e.rowOf("web") == "" })

	e.press("a")
	if row := e.rowOf("web"); !strings.Contains(row, "Exited") {
		t.Errorf("web row in all mode = %q, want an exited status", row)
	}
}

func TestStartStoppedContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	e.selectRow("old")
	e.press("s")
	e.waitStatus("start old: done")
	if calls := f.CallsTo(dockerfake.MethodContainerStart); len(calls) != 1 || calls[0].Args[0] != ids["old"] {
		t.Errorf("ContainerStart calls = %+v", calls)
	}
	e.until("old running", func() bool { return strings.Contains(e.rowOf("old"), "Up") })
}

func TestStartStopUnavailableForPaused(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("frozen")
	f.ResetCalls()
	e.press("s")
	e.waitStatus("start/stop is unavailable for a paused container (frozen); press p to unpause")
	e.settle()
	for _, c := range f.Calls() {
		if c.Method == dockerfake.MethodContainerStart || c.Method == dockerfake.MethodContainerStop {
			t.Errorf("engine called: %+v", c)
		}
	}
}

// TestStartStopUnavailableForRestarting checks that the unpause hint is
// given only for a paused container.
func TestStartStopUnavailableForRestarting(t *testing.T) {
	f, _ := sample()
	f.AddContainer(ctr(cid(4), "boot", "alpine", container.StateRestarting, "Restarting (1) 2 seconds ago"))
	e := newEnv(t, f, 100, 12)
	e.selectRow("boot")
	e.press("s")
	e.waitStatus("start/stop is unavailable for a restarting container (boot)")
	if s := e.screen(); strings.Contains(s, "press p") {
		t.Errorf("restarting container offered the unpause hint:\n%s", s)
	}
}

func TestPauseRunningContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("p")
	e.waitStatus("pause web: done")
	if calls := f.CallsTo(dockerfake.MethodContainerPause); len(calls) != 1 || calls[0].Args[0] != ids["web"] {
		t.Errorf("ContainerPause calls = %+v", calls)
	}
	if calls := f.CallsTo(dockerfake.MethodContainerUnpause); len(calls) != 0 {
		t.Errorf("ContainerUnpause calls = %+v, want none", calls)
	}
	// The pause event refreshes the list; paused containers stay listed in
	// running-only mode.
	e.until("web paused", func() bool { return strings.Contains(e.statusCell("web"), "◐ Up 5 minutes (Paused)") })
}

func TestUnpausePausedContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("p")
	e.waitStatus("pause web: done")
	e.until("web paused", func() bool { return strings.Contains(e.rowOf("web"), "(Paused)") })

	e.press("p")
	e.waitStatus("unpause web: done")
	if calls := f.CallsTo(dockerfake.MethodContainerUnpause); len(calls) != 1 || calls[0].Args[0] != ids["web"] {
		t.Errorf("ContainerUnpause calls = %+v", calls)
	}
	e.until("web running", func() bool {
		cell := e.statusCell("web")
		return strings.HasPrefix(cell, "● Up") && !strings.Contains(cell, "Paused")
	})
}

func TestUnpauseInitiallyPausedContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("frozen")
	e.press("p")
	e.waitStatus("unpause frozen: done")
	if calls := f.CallsTo(dockerfake.MethodContainerUnpause); len(calls) != 1 || calls[0].Args[0] != ids["frozen"] {
		t.Errorf("ContainerUnpause calls = %+v", calls)
	}
	e.until("frozen running", func() bool { return strings.HasPrefix(e.statusCell("frozen"), "● ") })
}

func TestPauseUnavailable(t *testing.T) {
	tests := []struct {
		name, state string
		cs          container.ContainerState
	}{
		{"old", "exited", container.StateExited},
		{"fresh", "created", container.StateCreated},
		{"boot", "restarting", container.StateRestarting},
		{"dead", "dead", container.StateDead},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			f, _ := sample()
			if tt.name != "old" {
				f.AddContainer(ctr(cid(4), tt.name, "alpine", tt.cs, string(tt.cs)))
			}
			e := newEnv(t, f, 100, 12)
			e.press("a")
			e.selectRow(tt.name)
			f.ResetCalls()
			e.press("p")
			e.waitStatus("pause/unpause is unavailable for a " + tt.state + " container (" + tt.name + ")")
			e.settle()
			for _, c := range f.Calls() {
				if c.Method == dockerfake.MethodContainerPause || c.Method == dockerfake.MethodContainerUnpause {
					t.Errorf("engine called: %+v", c)
				}
			}
		})
	}
}

func TestPauseReportsEngineError(t *testing.T) {
	f, _ := sample()
	f.SetError(dockerfake.MethodContainerPause, errors.New("cgroup freezer unavailable"))
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("p")
	e.waitStatus("pause web: cgroup freezer unavailable")
	e.settle()
	if cell := e.statusCell("web"); !strings.HasPrefix(cell, "● Up 5 minutes") || strings.Contains(cell, "Paused") {
		t.Errorf("web STATUS cell = %q, want it still running", cell)
	}
}

func TestPauseRefusedWhileDisconnected(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	f.Disconnect(errors.New("gone"))
	e.until("disconnected", func() bool { return e.st.State() == store.StateDisconnected })
	f.ResetCalls()
	e.press("p")
	e.waitStatus("pause web: not connected")
	if calls := f.CallsTo(dockerfake.MethodContainerPause); len(calls) != 0 {
		t.Errorf("engine called while disconnected: %+v", calls)
	}
}

func TestStopReportsEngineError(t *testing.T) {
	f, _ := sample()
	f.SetError(dockerfake.MethodContainerStop, errors.New("engine says no"))
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("s")
	e.waitStatus("stop web: engine says no")
	// The view stays usable.
	e.press("g")
	if r, _ := e.root().table.Selected(); r.Cells[0] != "db" {
		t.Errorf("selection after g = %v, want db", r.Cells)
	}
}

func TestRestartContainer(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("r")
	e.waitStatus("restart web: done")
	if calls := f.CallsTo(dockerfake.MethodContainerRestart); len(calls) != 1 || calls[0].Args[0] != ids["web"] {
		t.Errorf("ContainerRestart calls = %+v", calls)
	}
}

func TestDeleteContainer(t *testing.T) {
	tests := []struct {
		name      string
		target    string
		wantText  string
		wantForce bool
	}{
		{"stopped", "old", "old (exited) will be removed.", false},
		{"running", "web", "web is running and will be force-removed", true},
		{"paused", "frozen", "frozen is paused and will be force-removed", true},
		{"restarting", "flaky", "flaky is restarting and will be force-removed", true},
		{"created", "fresh", "fresh (created) will be removed.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, ids := sample()
			ids["flaky"] = f.AddContainer(ctr(cid(4), "flaky", "app", container.StateRestarting, "Restarting (1) 2 seconds ago"))
			ids["fresh"] = f.AddContainer(ctr(cid(5), "fresh", "app", container.StateCreated, "Created"))
			e := newEnv(t, f, 100, 24)
			e.press("a")
			e.selectRow(tt.target)
			e.openConfirm()
			if s := e.screen(); !strings.Contains(s, "Delete container "+tt.target+"?") ||
				!strings.Contains(s, tt.wantText) || !strings.Contains(s, "Its volumes are kept.") {
				t.Fatalf("confirmation:\n%s", s)
			}
			e.press("y")
			e.waitStatus("delete " + tt.target + ": done")
			calls := f.CallsTo(dockerfake.MethodContainerRemove)
			if len(calls) != 1 || calls[0].Args[0] != ids[tt.target] {
				t.Fatalf("ContainerRemove calls = %+v", calls)
			}
			want := docker.ContainerRemoveOptions{Force: tt.wantForce, RemoveVolumes: false}
			if got := calls[0].Args[1].(docker.ContainerRemoveOptions); got != want {
				t.Errorf("options = %+v, want %+v", got, want)
			}
			e.until("row gone", func() bool { return e.rowOf(tt.target) == "" })
		})
	}
}

func TestDeleteKeepsVolumes(t *testing.T) {
	f := dockerfake.New()
	f.AddVolume(volumeNamed("anon", map[string]string{dockerfake.AnonymousVolumeLabel: ""}))
	c := ctr(cid(0), "old", "alpine", container.StateExited, "Exited (0)")
	c.Mounts = []container.MountPoint{{Type: mount.TypeVolume, Name: "anon", Destination: "/data"}}
	f.AddContainer(c)
	e := newEnv(t, f, 100, 16)
	e.press("a")
	e.openConfirm()
	e.press("y")
	e.waitStatus("delete old: done")
	vols, _ := f.VolumeList(t.Context())
	if len(vols) != 1 {
		t.Errorf("volumes after delete = %+v, want anon kept", vols)
	}
}

func TestDeleteCancelled(t *testing.T) {
	for _, k := range []string{"n", "esc", "Y"} {
		t.Run(k, func(t *testing.T) {
			f, _ := sample()
			e := newEnv(t, f, 100, 16)
			e.selectRow("web")
			e.openConfirm()
			e.press(k)
			e.settle()
			if calls := f.CallsTo(dockerfake.MethodContainerRemove); len(calls) != 0 {
				t.Errorf("ContainerRemove called: %+v", calls)
			}
			if strings.Contains(e.screen(), "Delete container") {
				t.Errorf("dialog still open:\n%s", e.screen())
			}
		})
	}
}

func TestInspectContainer(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 80, 14)
	e.selectRow("web")
	e.press("i")
	e.until("inspection shown", func() bool { return strings.Contains(e.screen(), `"Status": "running"`) })
	if h := e.header(); !strings.HasPrefix(h, "drydock > containers > web > inspect") {
		t.Errorf("header = %q", h)
	}
	checkGolden(t, "inspect_container_80x14.golden", e.screen())

	// Scrolling.
	before := e.content()[0]
	e.press("j")
	if e.content()[0] == before {
		t.Error("j did not scroll")
	}
	e.press("g")
	if e.content()[0] != before {
		t.Error("g did not return to the top")
	}

	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > containers [") {
		t.Errorf("after esc header = %q", h)
	}
	if r, _ := e.root().table.Selected(); r.Cells[0] != "web" {
		t.Errorf("selection after esc = %v, want web", r.Cells)
	}
}

func TestInspectReportsError(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	f.SetError(dockerfake.MethodContainerInspect, errors.New("inspect broke"))
	e.press("i")
	e.waitStatus("inspect web: inspect broke")
	if !strings.Contains(e.screen(), "Could not inspect web: inspect broke") {
		t.Errorf("screen:\n%s", e.screen())
	}
}

func TestPrettyJSONSanitizes(t *testing.T) {
	t.Parallel()
	lines, err := prettyJSON([]byte("{\"a\":\"x\u009b31my\u202e\",\"b\":[1,2]}"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(lines, "\n")
	if strings.ContainsRune(got, '\u009b') || strings.ContainsRune(got, '\u202e') {
		t.Errorf("unsafe characters kept: %q", got)
	}
	if !strings.Contains(got, `  "b": [`) {
		t.Errorf("not indented: %q", got)
	}
	if _, err := prettyJSON([]byte("{not json")); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestFilteredContainersView(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(ctr(cid(0), "web", "nginx:1.27", container.StateRunning, "Up 1 minute"))
	f.AddContainer(ctr(cid(1), "web-old", "nginx:1.27", container.StateExited, "Exited (0)"))
	f.AddContainer(ctr(cid(2), "db", "postgres:16", container.StateRunning, "Up 1 minute"))
	f.AddContainer(ctr(cid(3), "db-old", "postgres:16", container.StateExited, "Exited (0)"))
	e := newEnv(t, f, 100, 12)

	nginxID := "sha256:" + strings.Repeat("n", 64)
	snap := e.root().snap
	v := NewFilteredContainers(e.st, snap, e.prefs, []string{"nginx:1.27", "containers"}, "No containers use this image.",
		func(c container.Summary) bool { return c.ImageID == nginxID })
	e.push(v)

	if h := e.header(); !strings.HasPrefix(h, "drydock > containers > nginx:1.27 > containers [all]") {
		t.Errorf("header = %q", h)
	}
	var names []string
	for _, l := range e.content()[1:] {
		if fs := rowFields(l); len(fs) > 0 {
			names = append(names, fs[0])
		}
	}
	if want := []string{"web", "web-old"}; !slices.Equal(names, want) {
		t.Errorf("listed %v, want %v", names, want)
	}

	// `a` toggles as usual.
	e.press("a")
	if e.rowOf("web-old") != "" || e.rowOf("web") == "" {
		t.Errorf("running-only drill-down:\n%s", e.screen())
	}

	// Live updates keep the predicate.
	f.AddContainer(ctr(cid(4), "web2", "nginx:1.27", container.StateRunning, "Up 1 second"))
	f.AddContainer(ctr(cid(5), "cache", "redis:7", container.StateRunning, "Up 1 second"))
	e.until("web2 listed", func() bool { return e.rowOf("web2") != "" })
	if e.rowOf("cache") != "" {
		t.Error("unrelated container listed")
	}

	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > containers [") {
		t.Errorf("after esc header = %q", h)
	}
}

func TestFilteredContainersEmpty(t *testing.T) {
	e := newEnv(t, dockerfake.New(), 100, 8)
	e.push(NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"x", "containers"}, "No containers use this image.",
		func(container.Summary) bool { return false }))
	if !strings.Contains(e.screen(), "No containers use this image.") {
		t.Errorf("screen:\n%s", e.screen())
	}
}

func TestActionsRefusedWhileDisconnected(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	f.Disconnect(errors.New("gone"))
	e.until("disconnected", func() bool { return e.st.State() == store.StateDisconnected })
	f.ResetCalls()
	e.press("s")
	e.waitStatus("stop web: not connected")
	for _, c := range f.Calls() {
		if c.Method == dockerfake.MethodContainerStop {
			t.Error("engine called while disconnected")
		}
	}
}

func volumeNamed(name string, labels map[string]string) volume.Volume {
	return volume.Volume{Name: name, Labels: labels}
}

// openConfirm presses d and waits for the confirmation dialog.
func (e *env) openConfirm() {
	e.t.Helper()
	e.press("d")
	e.until("confirmation dialog", func() bool { return strings.Contains(e.screen(), "y confirm") })
}
