package views

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
)

// rowNames returns the names of the rows on screen, top to bottom.
func (e *env) rowNames() []string {
	var names []string
	content := e.content()
	if len(content) < 2 {
		return nil
	}
	for _, l := range content[1:] { // the first line holds the column titles
		if f := rowFields(l); len(f) > 0 {
			names = append(names, f[0])
		}
	}
	return names
}

// waitOrder waits until the rows on screen are listed as want.
func (e *env) waitOrder(what string, want ...string) {
	e.t.Helper()
	e.until(what, func() bool { return slices.Equal(e.rowNames(), want) })
}

// selectedName returns the name of the top containers view's selected
// container, or "".
func (e *env) selectedName() string {
	c, ok := e.topContainers().selected()
	if !ok {
		return ""
	}
	return store.ContainerName(c)
}

func checkIndicators(t *testing.T, what string, v *containersView, want ...string) {
	t.Helper()
	if got := v.Indicators(); !slices.Equal(got, want) {
		t.Errorf("%s: indicators %q, want %q", what, got, want)
	}
}

// Spec "Live re-sorting": the rows re-sort when a stats batch changes the
// order, and the selection stays on the container that moved.
func TestContainersSortLiveResort(t *testing.T) {
	f := dockerfake.New()
	api := f.AddContainer(ctr(cid(0), "api", "app", container.StateRunning, "Up"))
	worker := f.AddContainer(ctr(cid(1), "worker", "app", container.StateRunning, "Up"))
	e := newEnv(t, f, 120, 12)
	e.press("o")
	e.waitStreams(2)

	f.SetStats(api, statsSample(41.2, 100*mib, gib))
	f.SetStats(worker, statsSample(38, 100*mib, gib))
	// The name order is api, worker too, so wait for the values themselves.
	e.until("first samples", func() bool {
		a, _ := e.statsCellsOf("api")
		w, _ := e.statsCellsOf("worker")
		return a == "41.2%" && w == "38.0%"
	})
	if got, want := e.rowNames(), []string{"api", "worker"}; !slices.Equal(got, want) {
		t.Fatalf("rows %q, want %q", got, want)
	}
	e.selectRow("api")

	f.SetStats(worker, statsSample(60, 100*mib, gib))
	e.waitOrder("worker above api", "worker", "api")
	if got := e.selectedName(); got != "api" {
		t.Errorf("selected %q after api moved, want api", got)
	}
	if r, ok := e.root().table.Selected(); !ok || r.Key != api {
		t.Errorf("selected row %+v, want api's", r)
	}
}

// A snapshot puts a new container in its sorted place at once, without
// waiting for a stats batch.
func TestContainersSortSnapshotKeepsOrder(t *testing.T) {
	f := dockerfake.New()
	busy := f.AddContainer(ctr(cid(0), "zeta", "app", container.StateRunning, "Up"))
	e := newEnv(t, f, 120, 12)
	e.press("o")
	e.waitStreams(1)
	f.SetStats(busy, statsSample(50, 100*mib, gib))
	e.until("zeta value", func() bool { cpu, _ := e.statsCellsOf("zeta"); return cpu == "50.0%" })

	// alpha has no sample yet, so it is listed after zeta despite its name.
	f.AddContainer(ctr(cid(1), "alpha", "app", container.StateRunning, "Up"))
	e.until("alpha listed", func() bool { return e.rowOf("alpha") != "" })
	if got, want := e.rowNames(), []string{"zeta", "alpha"}; !slices.Equal(got, want) {
		t.Errorf("rows %q, want %q", got, want)
	}
}

// Spec "Stats sort cycle": o cycles name → CPU → MEM → name, turning stats
// on when it leaves name, and the hint names the next order.
func TestContainersSortCycle(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 200, 30)
	e.settle()
	v := e.root()
	if ft := e.footer(); !strings.Contains(ft, "t stats  o sort cpu") {
		t.Errorf("startup footer %q, want t stats then o sort cpu", ft)
	}
	checkIndicators(t, "startup", v, "running")
	if f.StatsStreams() != 0 {
		t.Fatal("stats read at startup")
	}

	// name → CPU: stats turn on and the subscription starts.
	e.press("o")
	if !e.prefs.Stats || !v.statsCols || e.prefs.Sort != sortCPU || v.sortApplied != sortCPU {
		t.Fatalf("after o: stats %v/%v, sort %v/%v, want stats on and CPU", e.prefs.Stats, v.statsCols, e.prefs.Sort, v.sortApplied)
	}
	e.waitStreams(2)
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("columns %q, want %q", got, want)
	}
	if ft := e.footer(); !strings.Contains(ft, "t ports  o sort mem") {
		t.Errorf("CPU footer %q, want t ports then o sort mem", ft)
	}
	if h := e.header(); !strings.Contains(h, "[stats] [sort cpu]") {
		t.Errorf("CPU header %q, want [stats] [sort cpu]", h)
	}
	// web uses less CPU but more memory than db.
	f.SetStats(ids["web"], statsSample(1, 900*mib, 2*gib))
	f.SetStats(ids["db"], statsSample(30, 10*mib, 2*gib))
	e.waitOrder("CPU order", "db", "web", "frozen")

	// CPU → MEM.
	e.press("o")
	if e.prefs.Sort != sortMem || !e.prefs.Stats {
		t.Fatalf("after o o: sort %v stats %v, want MEM with stats on", e.prefs.Sort, e.prefs.Stats)
	}
	e.waitOrder("MEM order", "web", "db", "frozen")
	if ft := e.footer(); !strings.Contains(ft, "o sort name") {
		t.Errorf("MEM footer %q, want o sort name", ft)
	}
	checkIndicators(t, "MEM", v, "running", "stats", "sort mem")

	// MEM → name: stats stay on and the subscription stays open.
	e.press("o")
	if e.prefs.Sort != sortName || !e.prefs.Stats || !v.statsCols {
		t.Fatalf("after o o o: sort %v stats %v/%v, want name with stats on", e.prefs.Sort, e.prefs.Stats, v.statsCols)
	}
	e.waitOrder("name order", "db", "frozen", "web")
	if ft := e.footer(); !strings.Contains(ft, "t ports  o sort cpu") {
		t.Errorf("name footer %q, want t ports then o sort cpu", ft)
	}
	checkIndicators(t, "name with stats", v, "running", "stats")
	e.settle()
	if n := f.StatsStreams(); n != 2 {
		t.Errorf("%d stats streams after cycling back to name, want 2", n)
	}

	// Help lists o.
	e.press("?")
	if s := e.screen(); !strings.Contains(s, "o          sort cpu") {
		t.Errorf("help lacks o:\n%s", s)
	}
}

// The sort indicator comes after stats and before the filter.
func TestContainersSortIndicatorBeforeFilter(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 200, 12)
	e.press("o", "/")
	e.typeText("web")
	e.press("enter")
	checkIndicators(t, "CPU with a filter", e.root(), "running", "stats", "sort cpu", "/web")
	if h := e.header(); !strings.Contains(h, "[stats] [sort cpu] [/web]") {
		t.Errorf("header %q, want [stats] [sort cpu] [/web]", h)
	}
}

// Spec "Turn stats off while sorted by CPU".
func TestContainersSortResetByStatsOff(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 200, 12)
	e.press("o")
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(50, mib, gib))
	f.SetStats(ids["db"], statsSample(10, mib, gib))
	e.waitOrder("CPU order", "web", "db", "frozen")

	e.press("t")
	if e.prefs.Stats || e.prefs.Sort != sortName || e.root().sortApplied != sortName {
		t.Fatalf("after t: stats %v sort %v/%v, want stats off and name", e.prefs.Stats, e.prefs.Sort, e.root().sortApplied)
	}
	if got, want := e.rowNames(), []string{"db", "frozen", "web"}; !slices.Equal(got, want) {
		t.Errorf("rows %q after t, want name order %q", got, want)
	}
	checkIndicators(t, "stats off", e.root(), "running")
	if ft := e.footer(); !strings.Contains(ft, "t stats  o sort cpu") {
		t.Errorf("footer %q, want t stats then o sort cpu", ft)
	}
	e.waitStreams(0)

	// Turning stats back on keeps the name order.
	e.press("t")
	if e.prefs.Sort != sortName {
		t.Errorf("t turned the sort back to %v", e.prefs.Sort)
	}
}

// With the filter input focused, o is typed into the filter.
func TestContainersSortKeyTypedInFilter(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 120, 12)
	e.press("/", "o")
	if e.prefs.Stats || e.prefs.Sort != sortName {
		t.Error("o changed the sort while typing a filter")
	}
	if got := e.root().table.Filter(); got != "o" {
		t.Errorf("filter %q, want o", got)
	}
}

// Spec "Drill-down keeps the sort": an image's containers open sorted by
// CPU, with stats.
func TestContainersSortImageDrillDown(t *testing.T) {
	f := imageSample()
	e := newEnv(t, f, 200, 14)
	e.press("o")
	e.command("images")
	e.selectImage("nginx:1.27")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "nginx:1.27 > containers") })
	if h := e.header(); !strings.Contains(h, "[stats] [sort cpu]") {
		t.Errorf("drill-down header %q, want [stats] [sort cpu]", h)
	}
	if got := e.headerTitles(); !slices.Contains(got, "CPU") {
		t.Errorf("drill-down columns %q, want stats", got)
	}
	// Before web's first sample both rows show -, in name order; then web
	// has a value and old, which has exited, still shows - and comes last.
	if got, want := e.rowNames(), []string{"old", "web"}; !slices.Equal(got, want) {
		t.Errorf("drill-down rows %q before any sample, want %q", got, want)
	}
	e.waitStreams(1)
	f.SetStats(cid(0), statsSample(5, mib, gib)) // web
	e.waitOrder("web first", "web", "old")
}

// A sort changed in a drill-down applies to the root view revealed by Esc,
// which re-sorts though its layout did not change.
func TestContainersSortChangedInDrillDown(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 200, 12)
	e.press("o") // CPU, stats on
	e.waitStreams(2)

	dv := NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return true }).(*containersView)
	e.pushed = append(e.pushed, dv)
	e.push(dv)
	if dv.sortApplied != sortCPU {
		t.Fatalf("drill-down sort %v, want CPU", dv.sortApplied)
	}
	e.press("o") // MEM
	if e.prefs.Sort != sortMem || dv.sortApplied != sortMem {
		t.Fatalf("drill-down o: sort %v/%v, want MEM", e.prefs.Sort, dv.sortApplied)
	}
	if root := e.root(); root.sortApplied != sortCPU {
		t.Errorf("hidden root re-sorted to %v before it was shown", root.sortApplied)
	}

	e.press("esc")
	e.pushed = nil
	e.until("root shown", func() bool { return !strings.Contains(e.header(), "x > containers") })
	root := e.root()
	if root.sortApplied != sortMem {
		t.Errorf("root sort %v after Esc, want MEM", root.sortApplied)
	}
	if h := e.header(); !strings.Contains(h, "[sort mem]") {
		t.Errorf("root header %q, want [sort mem]", h)
	}
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(1, 900*mib, 2*gib))
	f.SetStats(ids["db"], statsSample(30, 10*mib, 2*gib))
	e.waitOrder("MEM order in the root", "web", "db", "frozen")
}

// Turning stats on with o in a drill-down applies the layout and the sort
// to the root view revealed by Esc.
func TestContainersSortDrillDownTurnsStatsOn(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 200, 12)
	dv := NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return true }).(*containersView)
	e.pushed = append(e.pushed, dv)
	e.push(dv)
	e.press("o")
	e.press("esc")
	e.pushed = nil
	e.until("root shown", func() bool { return !strings.Contains(e.header(), "x > containers") })
	if root := e.root(); !root.statsCols || root.sortApplied != sortCPU {
		t.Errorf("root stats %v sort %v, want stats on and CPU", root.statsCols, root.sortApplied)
	}
	e.waitStreams(2)
}

// Spec "Reopening keeps the sort".
func TestContainersSortReopened(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 200, 12)
	first := e.root()
	e.press("o", "o")
	e.command("images")
	e.command("containers")
	v := e.root()
	if v == first {
		t.Fatal(":containers did not open a new view")
	}
	if !v.statsCols || v.sortApplied != sortMem {
		t.Errorf("reopened view stats %v sort %v, want stats on and MEM", v.statsCols, v.sortApplied)
	}
	if h := e.header(); !strings.Contains(h, "[stats] [sort mem]") {
		t.Errorf("reopened header %q, want [stats] [sort mem]", h)
	}
}

// A view whose layout lags behind prefs never sorts on columns it does
// not show.
func TestContainersEffectiveSortFollowsLayout(t *testing.T) {
	v := &containersView{prefs: &Prefs{Stats: true, Sort: sortMem}}
	if got := v.effectiveSort(); got != sortName {
		t.Errorf("ports layout: effective sort %v, want name", got)
	}
	v.statsCols = true
	if got := v.effectiveSort(); got != sortMem {
		t.Errorf("stats layout: effective sort %v, want MEM", got)
	}
}

func TestSortOrderNextAndLabel(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		o     sortOrder
		next  sortOrder
		label string
	}{
		{sortName, sortCPU, "name"},
		{sortCPU, sortMem, "cpu"},
		{sortMem, sortName, "mem"},
		{sortOrder(99), sortName, "name"},
		{sortOrder(-1), sortName, "name"},
	} {
		if got := tt.o.next(); got != tt.next {
			t.Errorf("%d.next() = %d, want %d", tt.o, got, tt.next)
		}
		if got := tt.o.label(); got != tt.label {
			t.Errorf("%d.label() = %q, want %q", tt.o, got, tt.label)
		}
	}
}

// The CPU-sorted stats layout at 100 columns: the rows with a value first,
// largest first, then the paused and exited rows in name order.
func TestContainersSortGolden(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 10)
	e.press("a", "o")
	e.waitStreams(2)
	f.SetStats(ids["web"], statsSample(1600, mibs(999.9), 15*gib+532*mib))
	f.SetStats(ids["db"], statsSample(12.5, mibs(512.3), 2*gib))
	e.waitOrder("CPU order", "web", "db", "frozen", "old")
	checkGolden(t, "containers_stats_sort_cpu_100x10.golden", e.screen())
}
