package views

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
)

const (
	kib = 1024
	mib = 1024 * kib
	gib = 1024 * mib
)

// statsSample returns an engine stats record in which the container used
// cpuPct percent of one core (on a 4-core host) since the previous record,
// and memUsage of memLimit bytes of memory. A zero memLimit leaves the
// memory figures out, as on a host without the memory controller.
func statsSample(cpuPct float64, memUsage, memLimit uint64) container.StatsResponse {
	const sysDelta = 4_000_000_000 // 1 s on each of 4 cores, in ns
	cpuDelta := uint64(cpuPct / 100 / 4 * sysDelta)
	var s container.StatsResponse
	s.PreCPUStats.SystemUsage = 10 * sysDelta
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000_000_000
	s.CPUStats.SystemUsage = s.PreCPUStats.SystemUsage + sysDelta
	s.CPUStats.CPUUsage.TotalUsage = s.PreCPUStats.CPUUsage.TotalUsage + cpuDelta
	s.CPUStats.OnlineCPUs = 4
	if memLimit != 0 {
		s.MemoryStats.Usage = memUsage
		s.MemoryStats.Limit = memLimit
		s.MemoryStats.Stats = map[string]uint64{"inactive_file": 0}
	}
	return s
}

// statsCellsOf returns the CPU and MEM cells of name's row, which must be
// listed in the stats layout.
func (e *env) statsCellsOf(name string) (cpu, mem string) {
	e.t.Helper()
	v := e.topContainers()
	for _, c := range v.list {
		if store.ContainerName(c) == name {
			if !v.statsCols {
				e.t.Fatalf("view is not in the stats layout; screen:\n%s", e.screen())
			}
			return v.statsCells(c)
		}
	}
	e.t.Fatalf("no row %s; screen:\n%s", name, e.screen())
	return "", ""
}

// topContainers returns the containers view on top of the stack: the last
// one pushed by the test, or else the root.
func (e *env) topContainers() *containersView {
	if len(e.pushed) > 0 {
		return e.pushed[len(e.pushed)-1]
	}
	return e.root()
}

// waitStreams waits until n stats streams are open.
func (e *env) waitStreams(n int) {
	e.t.Helper()
	e.until("stats streams", func() bool { return e.f.StatsStreams() == n })
}

// statsTargetsCalled returns the container IDs the stats streams opened
// since the last ResetCalls were for, without duplicates, sorted.
func statsTargetsCalled(f *dockerfake.Service) []string {
	var ids []string
	for _, c := range f.CallsTo(dockerfake.MethodContainerStats) {
		if id, ok := c.Args[0].(string); ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// headerTitles returns the table's column titles.
func (e *env) headerTitles() []string { return rowFields(e.content()[0]) }

func TestFormatStats(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		got, want string
	}{
		{formatMem(mibs(512.3), 2*gib), "512.3MiB / 2GiB"},
		{formatMem(500*mib, 2*gib), "500MiB / 2GiB"},
		{formatMem(0, 15*gib+532*mib), "0B / 15.52GiB"},
		{formatCPU(100), "100.0%"},
		{formatCPU(200), "200.0%"},
		{formatCPU(0), "0.0%"},
		{formatCPU(12.345), "12.3%"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

func TestContainersStatsToggle(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 120, 30) // tall enough for the help overlay
	e.settle()
	if f.StatsStreams() != 0 || len(f.CallsTo(dockerfake.MethodContainerStats)) != 0 {
		t.Fatal("stats read while off")
	}

	e.press("t")
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("stats columns %q, want %q", got, want)
	}
	if h := e.header(); !strings.Contains(h, "[stats]") {
		t.Errorf("header %q lacks the stats indicator", h)
	}
	if ft := e.footer(); !strings.Contains(ft, "t ports") || strings.Contains(ft, "t stats") {
		t.Errorf("footer %q, want t ports", ft)
	}
	// web and db run; frozen is paused and old is not listed.
	e.waitStreams(2)
	if cpu, mem := e.statsCellsOf("frozen"); cpu != noStats || mem != noStats {
		t.Errorf("paused frozen: cells %q %q, want -", cpu, mem)
	}
	f.ResetCalls()

	e.press("?")
	if s := e.screen(); !strings.Contains(s, "t          ports") {
		t.Errorf("help lacks t:\n%s", s)
	}
	e.press("esc")

	e.press("t")
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "PORTS"}; !slices.Equal(got, want) {
		t.Errorf("columns %q after toggling back, want %q", got, want)
	}
	if h := e.header(); strings.Contains(h, "stats") {
		t.Errorf("header %q still indicates stats", h)
	}
	if ft := e.footer(); !strings.Contains(ft, "t stats") {
		t.Errorf("footer %q, want t stats", ft)
	}
	e.waitStreams(0)
}

func TestContainersStatsStreamsOnlyRunningListed(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 120, 12)
	e.press("a", "t")
	e.waitStreams(2)
	e.settle()
	want := []string{ids["db"], ids["web"]}
	slices.Sort(want)
	if got := statsTargetsCalled(f); !slices.Equal(got, want) {
		t.Errorf("stats read for %q, want web and db %q", got, want)
	}

	// A filter hiding a running container keeps its stream.
	e.press("/")
	e.typeText("web")
	e.press("enter")
	if e.rowOf("db") != "" {
		t.Fatalf("filter did not hide db:\n%s", e.screen())
	}
	e.settle()
	if n := f.StatsStreams(); n != 2 {
		t.Errorf("%d streams with db filtered out, want 2", n)
	}
}

func TestContainersStatsCells(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 120, 12)
	e.press("a", "t")
	e.waitStreams(2)

	f.SetStats(ids["web"], statsSample(100, mibs(512.3), 2*gib))
	f.SetStats(ids["db"], statsSample(25, 0, 0)) // no memory figures
	e.until("values", func() bool {
		cpu, _ := e.statsCellsOf("web")
		dbCPU, _ := e.statsCellsOf("db")
		return cpu != noStats && dbCPU != noStats
	})
	if cpu, mem := e.statsCellsOf("web"); cpu != "100.0%" || mem != "512.3MiB / 2GiB" {
		t.Errorf("web cells %q %q", cpu, mem)
	}
	if row := e.rowOf("web"); !strings.Contains(row, "100.0%") || !strings.Contains(row, "512.3MiB / 2GiB") {
		t.Errorf("web row %q does not show its values", row)
	}
	if cpu, mem := e.statsCellsOf("db"); cpu != "25.0%" || mem != noStats {
		t.Errorf("db without memory: cells %q %q, want 25.0%% and -", cpu, mem)
	}

	// Stopped and paused containers show - even if a value is at hand.
	v := e.root()
	batch := map[string]store.ContainerStats{}
	for _, id := range ids {
		batch[id] = store.ContainerStats{CPUPercent: 5, HasCPU: true, MemUsage: mib, MemLimit: gib, HasMem: true}
	}
	v.Update(statsMsg{view: v.id, sub: v.sub, stats: batch})
	for _, name := range []string{"old", "frozen"} {
		if cpu, mem := e.statsCellsOf(name); cpu != noStats || mem != noStats {
			t.Errorf("%s: cells %q %q, want -", name, cpu, mem)
		}
	}

	// A batch for another view is ignored, and so is one from a closed
	// subscription.
	other := map[string]store.ContainerStats{ids["web"]: {CPUPercent: 42, HasCPU: true}}
	if cmd := v.Update(statsMsg{view: v.id + 1000, sub: v.sub, stats: other}); cmd != nil {
		t.Error("a batch for another view returned a command")
	}
	foreign := e.st.SubscribeStats()
	foreign.Close()
	if cmd := v.Update(statsMsg{view: v.id, sub: foreign, stats: other}); cmd != nil {
		t.Error("a batch from another subscription returned a command")
	}
	if cpu, _ := e.statsCellsOf("web"); cpu != "5.0%" {
		t.Errorf("web CPU %q after foreign batches, want 5.0%%", cpu)
	}
}

func TestContainersStatsLiveUpdates(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 120, 12)
	e.press("a", "t")
	e.waitStreams(2)
	e.selectRow("web")

	f.SetStats(ids["web"], statsSample(25, 100*mib, gib))
	e.until("first value", func() bool { cpu, _ := e.statsCellsOf("web"); return cpu == "25.0%" })
	f.SetStats(ids["web"], statsSample(50, 200*mib, gib))
	e.until("changed value", func() bool { cpu, _ := e.statsCellsOf("web"); return cpu == "50.0%" })
	if r, ok := e.root().table.Selected(); !ok || r.Key != ids["web"] {
		t.Errorf("selection moved off web to %+v", r)
	}

	// A container started meanwhile gains a stream and values.
	newID := f.AddContainer(ctr(cid(9), "fresh", "busybox", container.StateRunning, "Up 1 second"))
	e.waitStreams(3)
	f.SetStats(newID, statsSample(10, 5*mib, gib))
	e.until("fresh values", func() bool {
		v := e.root()
		if _, ok := v.shown[newID]; !ok {
			return false
		}
		cpu, _ := e.statsCellsOf("fresh")
		return cpu == "10.0%"
	})

	// A stopped one loses its stream and shows -.
	if err := f.ContainerStop(context.Background(), ids["web"]); err != nil {
		t.Fatal(err)
	}
	e.waitStreams(2)
	e.until("web stopped", func() bool {
		c, ok := e.root().shown[ids["web"]]
		return ok && c.State == container.StateExited
	})
	if cpu, mem := e.statsCellsOf("web"); cpu != noStats || mem != noStats {
		t.Errorf("stopped web: cells %q %q, want -", cpu, mem)
	}
}

func TestContainersStatsDisconnected(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 120, 12)
	e.press("t")
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(25, 100*mib, gib))
	e.until("value", func() bool { cpu, _ := e.statsCellsOf("web"); return cpu == "25.0%" })

	f.Disconnect(errors.New("engine gone"))
	e.until("disconnected", func() bool { return e.root().conn == store.StateDisconnected })
	for _, name := range []string{"web", "db"} {
		if cpu, mem := e.statsCellsOf(name); cpu != noStats || mem != noStats {
			t.Errorf("%s while disconnected: cells %q %q, want -", name, cpu, mem)
		}
	}
	v := e.root()
	v.Update(statsMsg{view: v.id, sub: v.sub, stats: map[string]store.ContainerStats{
		ids["web"]: {CPUPercent: 77, HasCPU: true, MemUsage: mib, MemLimit: gib, HasMem: true},
	}})
	if cpu, mem := e.statsCellsOf("web"); cpu != noStats || mem != noStats {
		t.Errorf("batch while disconnected shown: cells %q %q", cpu, mem)
	}

	// A view created while disconnected shows - too.
	e.prefs.Stats = true
	dv := NewFilteredContainers(e.st, v.snap, e.prefs, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return true }).(*containersView)
	t.Cleanup(dv.Close)
	dv.Update(statsMsg{view: dv.id, sub: dv.sub, stats: map[string]store.ContainerStats{
		ids["web"]: {CPUPercent: 77, HasCPU: true},
	}})
	for _, c := range dv.list {
		if cpu, mem := dv.statsCells(c); cpu != noStats || mem != noStats {
			t.Errorf("new view while disconnected: %s cells %q %q", store.ContainerName(c), cpu, mem)
		}
	}

	// Values come back after the reconnection, without user input.
	f.Reconnect()
	e.until("reconnected", func() bool { return e.root().conn == store.StateConnected })
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(30, 100*mib, gib))
	e.until("value after reconnect", func() bool { cpu, _ := e.statsCellsOf("web"); return cpu == "30.0%" })
}

func TestContainersStatsDrillDownReadsListedOnly(t *testing.T) {
	f := dockerfake.New()
	nginx := imgID('n')
	for i := range 12 {
		c := ctr(cid(i), "c"+string(rune('a'+i)), "busybox", container.StateRunning, "Up")
		if i < 2 {
			c.Image, c.ImageID = "nginx:1.27", nginx
		}
		f.AddContainer(c)
	}
	e := newEnv(t, f, 120, 12)
	e.press("t")
	e.waitStreams(12)

	dv := NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"nginx:1.27", "containers"}, "none",
		func(c container.Summary) bool { return c.ImageID == nginx }).(*containersView)
	e.pushed = append(e.pushed, dv)
	f.ResetCalls()
	e.push(dv)
	e.waitStreams(2)
	e.settle()
	if got, want := statsTargetsCalled(f), []string{cid(0), cid(1)}; !slices.Equal(got, want) {
		t.Errorf("drill-down read stats for %q, want %q", got, want)
	}
	if got := e.headerTitles(); !slices.Contains(got, "CPU") {
		t.Errorf("drill-down columns %q, want stats", got)
	}

	// Toggling off in the drill-down applies to the root revealed by Esc.
	e.press("t")
	e.waitStreams(0)
	e.press("esc")
	e.pushed = nil
	e.settle()
	if got := e.headerTitles(); !slices.Contains(got, "PORTS") {
		t.Errorf("root columns %q after toggling off in the drill-down, want PORTS", got)
	}
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("%d streams with stats off", n)
	}
}

func TestContainersStatsStopOnDeactivateAndClose(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 120, 12)
	e.press("t")
	e.waitStreams(2)

	// Deactivation: a view pushed on top.
	e.push(NewFilteredContainers(e.st, e.root().snap, &Prefs{}, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return false }))
	e.waitStreams(0)
	e.press("esc")
	e.waitStreams(2)

	// Close, and SetActive after it is ignored.
	v := e.root()
	v.Close()
	e.waitStreams(0)
	if cmd := v.SetActive(true); cmd != nil || v.sub != nil {
		t.Error("SetActive after Close reopened stats")
	}
	e.settle()
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("%d streams after Close", n)
	}
}

// mibs returns x MiB in bytes, rounded down.
func mibs(x float64) uint64 { return uint64(x * mib) }

func TestContainersStatsGolden(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 10)
	e.press("a", "t")
	e.waitStreams(2)
	e.settle()
	checkGolden(t, "containers_stats_pending_100x10.golden", e.screen())

	f.SetStats(ids["web"], statsSample(12.5, mibs(512.3), 2*gib))
	f.SetStats(ids["db"], statsSample(1600, mibs(999.9), 15*gib+532*mib))
	e.until("values", func() bool {
		web, _ := e.statsCellsOf("web")
		db, _ := e.statsCellsOf("db")
		return web != noStats && db != noStats
	})
	got := e.screen()
	checkGolden(t, "containers_stats_100x10.golden", got)
	// No CPU or MEM value is cut at 100 columns.
	for _, want := range []string{"12.5%", "512.3MiB / 2GiB", "1600.0%", "999.9MiB / 15.52GiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen lacks %q:\n%s", want, got)
		}
	}
}

// A view hidden and shown again does not show the values it had before it
// was hidden: they are pending until the new subscription's first batch.
func TestContainersStatsRevealedViewStartsPending(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 120, 12)
	e.press("t")
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(25, 100*mib, gib))
	e.until("value", func() bool { cpu, _ := e.statsCellsOf("web"); return cpu == "25.0%" })

	e.push(NewFilteredContainers(e.st, e.root().snap, &Prefs{}, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return false }))
	e.waitStreams(0)
	e.press("esc")
	if row := e.rowOf("web"); strings.Contains(row, "25.0%") {
		t.Errorf("revealed view shows the old value: %q", row)
	}
}

// With the filter input focused, t is typed into the filter.
func TestContainersStatsKeyTypedInFilter(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 120, 12)
	e.press("/", "t")
	if e.prefs.Stats || e.root().statsCols {
		t.Error("t toggled stats while typing a filter")
	}
	if got := e.root().table.Filter(); got != "t" {
		t.Errorf("filter %q, want t", got)
	}
}
