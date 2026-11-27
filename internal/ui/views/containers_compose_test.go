package views

import (
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// composeSample returns a fake engine with the Compose project shop, whose
// containers are named after neither the project nor their service:
//   - web, running, service frontend, publishing 8080->80/tcp;
//   - db, running, service db;
//   - job, exited, service batch;
//
// and redis, running, in no project.
func composeSample() *dockerfake.Service {
	f := dockerfake.New()
	shop := func(svc string) dockerfake.Compose {
		return dockerfake.Compose{Project: "shop", Service: svc, WorkingDir: testHome + "/src/shop"}
	}
	web := composeCtr(0, "web", "nginx:1.27", container.StateRunning, "Up 5 minutes", shop("frontend"))
	web.Ports = []container.PortSummary{pub("0.0.0.0", 8080, 80, "tcp")}
	f.AddContainer(web)
	db := composeCtr(1, "db", "postgres:16", container.StateRunning, "Up 2 hours", shop("db"))
	db.Ports = []container.PortSummary{exposed(5432, "tcp")}
	f.AddContainer(db)
	f.AddContainer(composeCtr(2, "job", "alpine", container.StateExited, "Exited (0) 1 hour ago", shop("batch")))
	f.AddContainer(ctr(cid(3), "redis", "redis:7", container.StateRunning, "Up 2 hours"))
	return f
}

// cellOf returns the i-th space-separated field of name's row, or "".
func (e *env) cellOf(name string, i int) string {
	f := rowFields(e.rowOf(name))
	if i >= len(f) {
		return ""
	}
	return f[i]
}

// Each of the four column sets, with either Compose title, takes exactly
// the 100 cells of a 100-column terminal: the gutter, every minimum width
// and the gaps between columns.
func TestContainerColumnSetsFit100(t *testing.T) {
	const gutter, gap = 2, 2
	for _, stats := range []bool{false, true} {
		for _, compose := range []bool{false, true} {
			for _, mode := range []composeMode{composeProject, composeService} {
				cols := columnsFor(stats, compose, mode)
				total := gutter + gap*(len(cols)-1)
				var titles []string
				for _, c := range cols {
					total += c.Width
					titles = append(titles, c.Title)
					if c.Width < len(c.Title) {
						t.Errorf("stats=%v compose=%v: %s is narrower (%d) than its title", stats, compose, c.Title, c.Width)
					}
				}
				if total != 100 {
					t.Errorf("stats=%v compose=%v mode=%v %v: %d cells, want 100", stats, compose, mode, titles, total)
				}
				hasCompose := slices.Contains(titles, "PROJECT") || slices.Contains(titles, "SERVICE")
				if hasCompose != compose || (compose && titles[1] != mode.title()) {
					t.Errorf("stats=%v compose=%v mode=%v: titles %v", stats, compose, mode, titles)
				}
			}
		}
	}
	// The package-level sets are never modified by columnsFor.
	if composeContainerColumns[1].Title != "" || composeStatsColumns[1].Title != "" {
		t.Error("columnsFor modified a package-level column set")
	}
}

func TestComposeCell(t *testing.T) {
	labeled := container.Summary{Labels: dockerfake.Compose{Project: "shop", Service: "web"}.Labels()}
	for _, tt := range []struct {
		mode composeMode
		c    container.Summary
		want string
	}{
		{composeProject, labeled, "shop"},
		{composeService, labeled, "web"},
		{composeProject, container.Summary{}, noCompose},
		{composeService, container.Summary{}, noCompose},
		{composeService, container.Summary{Labels: map[string]string{store.ComposeProjectLabel: "shop"}}, noCompose},
	} {
		if got := tt.mode.cell(tt.c); got != tt.want {
			t.Errorf("%s cell of %v = %q, want %q", tt.mode.title(), tt.c.Labels, got, tt.want)
		}
	}
}

// Without Compose containers the layouts are unchanged; the existing golden
// files check their rows.
func TestComposeColumnAbsentWithoutCompose(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 10)
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "PORTS"}; !slices.Equal(got, want) {
		t.Errorf("columns %v, want %v", got, want)
	}
	e.press("t")
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("stats columns %v, want %v", got, want)
	}
}

func TestComposeColumnInRootView(t *testing.T) {
	e := newEnv(t, composeSample(), 100, 12)
	if got, want := e.headerTitles(), []string{"NAME", "PROJECT", "IMAGE", "STATUS", "PORTS"}; !slices.Equal(got, want) {
		t.Fatalf("columns %v, want %v", got, want)
	}
	if got := e.cellOf("web", 1); got != "shop" {
		t.Errorf("web PROJECT %q, want shop", got)
	}
	if got := e.cellOf("redis", 1); got != noCompose {
		t.Errorf("redis PROJECT %q, want %q", got, noCompose)
	}
	e.press("t")
	if got, want := e.headerTitles(), []string{"NAME", "PROJECT", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("stats columns %v, want %v", got, want)
	}
}

// The column follows the whole engine, not the listed rows: it stays while
// the only Compose container is hidden by the running-only mode or by the
// filter.
func TestComposeColumnStaysWhenComposeContainersHidden(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(composeCtr(0, "job", "alpine", container.StateExited, "Exited (0) 1 hour ago",
		dockerfake.Compose{Project: "shop", Service: "batch"}))
	f.AddContainer(ctr(cid(1), "redis", "redis:7", container.StateRunning, "Up 2 hours"))
	e := newEnv(t, f, 100, 12)
	if e.rowOf("job") != "" {
		t.Fatalf("running-only mode lists the exited job:\n%s", e.screen())
	}
	if got := e.headerTitles(); !slices.Contains(got, "PROJECT") {
		t.Errorf("columns %v lack PROJECT while job is hidden", got)
	}
	if got := e.cellOf("redis", 1); got != noCompose {
		t.Errorf("redis PROJECT %q, want %q", got, noCompose)
	}

	e.press("a", "/")
	e.typeText("redis")
	e.press("enter")
	if e.rowOf("job") != "" {
		t.Fatalf("filter redis lists job:\n%s", e.screen())
	}
	if got := e.headerTitles(); !slices.Contains(got, "PROJECT") {
		t.Errorf("columns %v lack PROJECT while job is filtered out", got)
	}
}

// The column appears with the first Compose container and goes with the
// last one, keeping the selection, the filter and the stats layout.
func TestComposeColumnFollowsSnapshots(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("t", "/")
	e.typeText("b")
	e.press("enter")
	e.selectRow("db")

	id := f.AddContainer(composeCtr(4, "bweb", "nginx:1.27", container.StateRunning, "Up 1 second",
		dockerfake.Compose{Project: "blog", Service: "web"}))
	e.until("PROJECT column", func() bool { return slices.Contains(e.headerTitles(), "PROJECT") })
	if got, want := e.headerTitles(), []string{"NAME", "PROJECT", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("columns %v, want %v", got, want)
	}
	if got := e.cellOf("bweb", 1); got != "blog" {
		t.Errorf("bweb PROJECT %q, want blog", got)
	}
	if c, ok := e.root().selected(); !ok || store.ContainerName(c) != "db" {
		t.Errorf("selection %q after the column appeared, want db", store.ContainerName(c))
	}
	if got := e.root().table.Filter(); got != "b" {
		t.Errorf("filter %q after the column appeared, want b", got)
	}
	if !e.root().statsCols {
		t.Error("stats layout lost when the column appeared")
	}

	f.RemoveContainer(id)
	e.until("PROJECT column gone", func() bool { return !slices.Contains(e.headerTitles(), "PROJECT") })
	if got, want := e.headerTitles(), []string{"NAME", "IMAGE", "STATUS", "CPU", "MEM"}; !slices.Equal(got, want) {
		t.Errorf("columns %v, want %v", got, want)
	}
	if c, ok := e.root().selected(); !ok || store.ContainerName(c) != "db" {
		t.Errorf("selection %q after the column went, want db", store.ContainerName(c))
	}
	if got := e.root().table.Filter(); got != "b" {
		t.Errorf("filter %q after the column went, want b", got)
	}
}

// A hidden view keeps its Compose column current: the view below a
// drill-down shows the column when revealed.
func TestComposeColumnFollowsSnapshotsWhileHidden(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.push(NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return false }))
	f.AddContainer(composeCtr(4, "bweb", "nginx:1.27", container.StateRunning, "Up 1 second",
		dockerfake.Compose{Project: "blog", Service: "web"}))
	e.until("PROJECT column", func() bool { return slices.Contains(e.headerTitles(), "PROJECT") })
	e.press("esc")
	e.settle()
	if got := e.headerTitles(); !slices.Contains(got, "PROJECT") {
		t.Errorf("revealed root columns %v lack PROJECT", got)
	}
	if got := e.cellOf("bweb", 1); got != "blog" {
		t.Errorf("bweb PROJECT %q, want blog", got)
	}
}

// A view revealed after stats were toggled in a drill-down, while the
// Compose column appeared, takes both the stats layout and the column.
func TestComposeColumnWithStatsChosenWhileHidden(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.push(NewFilteredContainers(e.st, e.root().snap, e.prefs, []string{"x", "containers"}, "none",
		func(container.Summary) bool { return false }))
	e.press("t")
	f.AddContainer(composeCtr(4, "bweb", "nginx:1.27", container.StateRunning, "Up 1 second",
		dockerfake.Compose{Project: "blog", Service: "web"}))
	want := []string{"NAME", "PROJECT", "IMAGE", "STATUS", "CPU", "MEM"}
	e.until("drill-down stats and PROJECT columns", func() bool { return slices.Equal(e.headerTitles(), want) })
	e.press("esc")
	e.settle()
	if got := e.headerTitles(); !slices.Equal(got, want) {
		t.Errorf("revealed root columns %v, want %v", got, want)
	}
	if got := e.cellOf("bweb", 1); got != "blog" {
		t.Errorf("bweb PROJECT %q, want blog", got)
	}
}

func TestComposeColumnFilterByProject(t *testing.T) {
	e := newEnv(t, composeSample(), 100, 12)
	e.press("a", "/")
	e.typeText("SHOP")
	e.press("enter")
	var got []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) > 0 {
			got = append(got, f[0])
		}
	}
	if want := []string{"db", "job", "web"}; !slices.Equal(got, want) {
		t.Errorf("filter shop lists %v, want %v:\n%s", got, want, e.screen())
	}
}

func TestServiceColumnInProjectDrillDown(t *testing.T) {
	e := projectsEnv(t, composeSample(), 100, 12)
	e.selectProject("shop")
	e.press("enter")
	e.settle()
	if got, want := e.headerTitles(), []string{"NAME", "SERVICE", "IMAGE", "STATUS", "PORTS"}; !slices.Equal(got, want) {
		t.Fatalf("columns %v, want %v:\n%s", got, want, e.screen())
	}
	for name, svc := range map[string]string{"web": "frontend", "db": "db", "job": "batch"} {
		if got := e.cellOf(name, 1); got != svc {
			t.Errorf("%s SERVICE %q, want %q", name, got, svc)
		}
	}
	// The filter searches the SERVICE column.
	e.press("/")
	e.typeText("front")
	e.press("enter")
	if e.rowOf("web") == "" || e.rowOf("db") != "" || e.rowOf("job") != "" {
		t.Errorf("filter front:\n%s", e.screen())
	}
}

func TestProjectColumnInImageDrillDown(t *testing.T) {
	f := imageSample()
	c := composeCtr(3, "shop-web-1", "nginx:1.27", container.StateRunning, "Up 1 minute",
		dockerfake.Compose{Project: "shop", Service: "web"})
	c.ImageID = imgID('a') // the drill-down matches containers by image ID
	f.AddContainer(c)
	e := imagesEnv(t, f, 100, 20)
	e.selectImage("nginx:1.27")
	e.press("enter")
	e.settle()
	if !strings.Contains(e.header(), "nginx:1.27 > containers") {
		t.Fatalf("drill-down not shown:\n%s", e.screen())
	}
	if got := e.headerTitles(); len(got) < 2 || got[1] != "PROJECT" {
		t.Fatalf("drill-down columns %v lack PROJECT after NAME:\n%s", got, e.screen())
	}
	if got := e.cellOf("web", 1); got != noCompose {
		t.Errorf("web PROJECT %q, want %q", got, noCompose)
	}
	if got := e.cellOf("shop-web-1", 1); got != "shop" {
		t.Errorf("shop-web-1 PROJECT %q, want shop", got)
	}
}

func TestComposeColumnGolden(t *testing.T) {
	f := composeSample()
	e := newEnv(t, f, 100, 10)
	got := e.screen()
	checkGolden(t, "containers_compose_100x10.golden", got)
	for _, want := range []string{"NAME", "PROJECT", "IMAGE", "STATUS", "PORTS", "8080->80/tcp"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen lacks %q:\n%s", want, got)
		}
	}

	e.press("t")
	e.waitStreams(3)
	f.SetStats(cid(0), statsSample(12.5, mibs(512.3), 2*gib))
	// MEM is 15 cells wide at 100 columns with the Compose column (see
	// design.md), so a longer value such as "999.9MiB / 15.52GiB" is cut.
	f.SetStats(cid(1), statsSample(1600, mibs(999.9), 2*gib))
	f.SetStats(cid(3), statsSample(0.4, mibs(8.1), gib))
	e.until("values", func() bool {
		for _, n := range []string{"web", "db", "redis"} {
			if cpu, _ := e.statsCellsOf(n); cpu == noStats {
				return false
			}
		}
		return true
	})
	got = e.screen()
	checkGolden(t, "containers_compose_stats_100x10.golden", got)
	// No header, CPU or MEM value is cut at 100 columns.
	for _, want := range []string{"NAME", "PROJECT", "IMAGE", "STATUS", "CPU", "MEM",
		"12.5%", "512.3MiB / 2GiB", "1600.0%", "999.9MiB / 2GiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen lacks %q:\n%s", want, got)
		}
	}
}

// The Compose cell is untrusted engine data, rendered like any other cell.
func TestComposeColumnSanitized(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(composeCtr(0, "web", "nginx:1.27", container.StateRunning, "Up 1 minute",
		dockerfake.Compose{Project: "evil\x1b[2Jx", Service: "web"}))
	e := newEnv(t, f, 100, 10)
	raw := e.m.View().Content
	if strings.Contains(raw, "\x1b[2J") {
		t.Errorf("screen contains the project label's escape sequence")
	}
	if got := e.cellOf("web", 1); got != components.Clean("evil\x1b[2Jx") {
		t.Errorf("web PROJECT %q, want %q", got, components.Clean("evil\x1b[2Jx"))
	}
}
