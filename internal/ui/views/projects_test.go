package views

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

// composeCtr builds a container labeled by Compose as described by cmp.
func composeCtr(n int, name, img string, state container.ContainerState, status string, cmp dockerfake.Compose) container.Summary {
	c := ctr(cid(n), name, img, state, status)
	c.Labels = cmp.Labels()
	return c
}

// projectSample returns a fake engine with the projects:
//   - blog: blog-web-1 exited, working dir /srv/blog;
//   - myapp: myapp-web-1 and myapp-db-1 running, myapp-worker-1 exited and
//     a running one-off container, working dir ~/src/myapp;
//   - shop: shop-api-1 paused, working dir /home/meow/shop (a sibling of
//     the test home, not inside it);
//   - tools: a single exited one-off container, no working dir;
//
// and redis, running, in no project.
func projectSample() *dockerfake.Service {
	f := dockerfake.New()
	myapp := func(svc string, oneOff bool) dockerfake.Compose {
		return dockerfake.Compose{Project: "myapp", Service: svc, WorkingDir: testHome + "/src/myapp", OneOff: oneOff}
	}
	f.AddContainer(composeCtr(0, "myapp-web-1", "nginx:1.27", container.StateRunning, "Up 5 minutes", myapp("web", false)))
	f.AddContainer(composeCtr(1, "myapp-db-1", "postgres:16", container.StateRunning, "Up 5 minutes", myapp("db", false)))
	f.AddContainer(composeCtr(2, "myapp-worker-1", "alpine", container.StateExited, "Exited (0) 1 hour ago", myapp("worker", false)))
	f.AddContainer(composeCtr(3, "myapp-web-run-1a2b", "nginx:1.27", container.StateRunning, "Up 1 minute", myapp("web", true)))
	f.AddContainer(composeCtr(4, "blog-web-1", "nginx:1.27", container.StateExited, "Exited (0) 2 days ago",
		dockerfake.Compose{Project: "blog", Service: "web", WorkingDir: "/srv/blog"}))
	f.AddContainer(composeCtr(5, "shop-api-1", "node:22", container.StatePaused, "Up 3 hours (Paused)",
		dockerfake.Compose{Project: "shop", Service: "api", WorkingDir: "/home/meow/shop"}))
	f.AddContainer(composeCtr(6, "tools-cli-run-9f8e", "alpine", container.StateExited, "Exited (0) 3 days ago",
		dockerfake.Compose{Project: "tools", Service: "cli", OneOff: true}))
	f.AddContainer(ctr(cid(7), "redis", "redis:7", container.StateRunning, "Up 2 hours"))
	return f
}

// projectsEnv starts an env on the given engine and switches to the
// projects view.
func projectsEnv(t *testing.T, f *dockerfake.Service, width, height int) *env {
	t.Helper()
	e := newEnv(t, f, width, height)
	e.command("projects")
	if !strings.HasPrefix(e.header(), "drydock > projects") {
		t.Fatalf("projects view not shown; screen:\n%s", e.screen())
	}
	return e
}

func (e *env) projectsView() *projectsView { return e.projects[len(e.projects)-1] }

// selectProject moves the selection of the projects view to the named
// project.
func (e *env) selectProject(name string) {
	e.t.Helper()
	if !e.projectsView().table.Select(name) {
		e.t.Fatalf("no project row %s; screen:\n%s", name, e.screen())
	}
}

// projectNames returns the projects listed, in order.
func (e *env) projectNames() []string {
	var out []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) > 0 {
			out = append(out, f[0])
		}
	}
	return out
}

func TestProjectsGolden(t *testing.T) {
	e := projectsEnv(t, projectSample(), 100, 10)
	checkGolden(t, "projects_100x10.golden", e.screen())
}

func TestProjectsEmptyGolden(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(ctr(cid(0), "redis", "redis:7", container.StateRunning, "Up 2 hours"))
	e := projectsEnv(t, f, 100, 8)
	if !strings.Contains(e.screen(), "No Compose projects.") {
		t.Errorf("empty state:\n%s", e.screen())
	}
	checkGolden(t, "projects_empty_100x8.golden", e.screen())
}

func TestProjectsList(t *testing.T) {
	e := projectsEnv(t, projectSample(), 100, 14)
	if got, want := e.projectNames(), []string{"blog", "myapp", "shop", "tools"}; !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	if hd := e.content()[0]; !slices.Equal(rowFields(hd), []string{"PROJECT", "RUNNING", "STATE", "WORKING", "DIR"}) {
		t.Errorf("header row %q", hd)
	}
	for name, want := range map[string][]string{
		// The one-off container is not counted.
		"myapp": {"myapp", "2/3", "◐", "partial", "~/src/myapp"},
		"blog":  {"blog", "0/1", "○", "exited", "/srv/blog"},
		// Paused is not running, and a sibling of home is not shortened.
		"shop": {"shop", "0/1", "◐", "partial", "/home/meow/shop"},
		// Only a one-off container: 0/0, exited, no working dir.
		"tools": {"tools", "0/0", "○", "exited", "-"},
	} {
		if got := rowFields(e.rowOf(name)); !slices.Equal(got, want) {
			t.Errorf("%s row %q, want %q", name, got, want)
		}
	}
	if e.rowOf("redis") != "" {
		t.Error("a container without a project is listed as a project")
	}
}

func TestProjectsRunningStateAndTones(t *testing.T) {
	f := dockerfake.New()
	f.AddContainer(composeCtr(0, "up-web-1", "nginx", container.StateRunning, "Up", dockerfake.Compose{Project: "up", Service: "web"}))
	f.AddContainer(composeCtr(1, "down-web-1", "nginx", container.StateCreated, "Created", dockerfake.Compose{Project: "down", Service: "web"}))
	f.AddContainer(composeCtr(2, "half-web-1", "nginx", container.StateRunning, "Up", dockerfake.Compose{Project: "half", Service: "web"}))
	f.AddContainer(composeCtr(3, "half-db-1", "postgres", container.StateExited, "Exited (0)", dockerfake.Compose{Project: "half", Service: "db"}))
	e := projectsEnv(t, f, 100, 12)
	for name, want := range map[string]struct {
		running, state string
		tone           components.Tone
	}{
		"up":   {"1/1", "● running", components.ToneGood},
		"half": {"1/2", "◐ partial", components.ToneWarn},
		"down": {"0/1", "○ exited", components.ToneDim},
	} {
		r, ok := selectedRow(e.projectsView(), name)
		if !ok {
			t.Fatalf("no row %s", name)
		}
		if r.Cells[1] != want.running || r.Cells[2] != want.state || r.Tone != want.tone {
			t.Errorf("%s: cells %q tone %d, want %s, %s and tone %d", name, r.Cells, r.Tone, want.running, want.state, want.tone)
		}
	}
}

// selectedRow selects the row of v keyed key and returns it.
func selectedRow(v *projectsView, key string) (components.Row, bool) {
	if !v.table.Select(key) {
		return components.Row{}, false
	}
	return v.table.Selected()
}

func TestShortenHome(t *testing.T) {
	for _, tc := range []struct{ path, home, want string }{
		{"/home/me", "/home/me", "~"},
		{"/home/me/src/myapp", "/home/me", "~/src/myapp"},
		{"/home/me/src/myapp", "/home/me/", "~/src/myapp"}, // trailing slash in $HOME
		{"/home/meow/app", "/home/me", "/home/meow/app"},
		{"/srv/app", "/home/me", "/srv/app"},
		{"/home/me/app", "", "/home/me/app"},        // home unknown
		{"/srv/app", "/", "/srv/app"},               // root home is never replaced
		{"/home/me/app", "home/me", "/home/me/app"}, // relative home is ignored
		{"relative/dir", "/home/me", "relative/dir"},
		{"/home/me/", "/home/me", "~"},
		{"/home/me//src/./app/", "/home/me", "~/src/app"},
		{"/home/me/../other/x", "/home/me", "/home/me/../other/x"}, // outside home once cleaned
		{"/home/me/..", "/home/me", "/home/me/.."},
	} {
		if got := shortenHome(tc.path, tc.home); got != tc.want {
			t.Errorf("shortenHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

func TestProjectsFilter(t *testing.T) {
	f := projectSample()
	f.AddContainer(composeCtr(8, "api-app-1", "node:22", container.StateRunning, "Up",
		dockerfake.Compose{Project: "api", Service: "app", WorkingDir: testHome + "/work/shop"}))
	e := projectsEnv(t, f, 100, 14)
	e.press("/")
	e.typeText("SHOP")
	e.press("enter")
	// api matches by its working dir, shop by its name and working dir.
	if got, want := e.projectNames(), []string{"api", "shop"}; !slices.Equal(got, want) {
		t.Errorf("filtered rows %v, want %v", got, want)
	}
	if !strings.Contains(e.header(), "[/SHOP]") {
		t.Errorf("header %q, want the filter indicator", e.header())
	}
	e.press("esc")
	// The filter matches the displayed ~, not the home directory's path.
	e.press("/")
	e.typeText("~/src")
	e.press("enter")
	if got, want := e.projectNames(), []string{"myapp"}; !slices.Equal(got, want) {
		t.Errorf("rows filtered by ~/src %v, want %v", got, want)
	}
	e.press("esc", "/")
	e.typeText("/home/me/")
	e.press("enter")
	if n := e.projectsView().table.Len(); n != 0 {
		t.Errorf("%d rows filtered by the home path, want none:\n%s", n, e.screen())
	}
}

func TestProjectsLiveUpdate(t *testing.T) {
	f := projectSample()
	e := projectsEnv(t, f, 100, 14)
	e.selectProject("myapp")
	f.AddContainer(composeCtr(8, "shop2-web-1", "nginx", container.StateRunning, "Up",
		dockerfake.Compose{Project: "aaa", Service: "web"}))
	e.until("new project listed", func() bool { return e.rowOf("aaa") != "" })
	if p, _ := e.projectsView().selected(); p.Name != "myapp" {
		t.Errorf("selection moved to %q", p.Name)
	}
	// A project disappears with its last container.
	f.RemoveContainer(cid(4))
	e.until("blog gone", func() bool { return e.rowOf("blog") == "" })
	if !strings.Contains(e.tabsLine(), "4 projects (4)") {
		t.Errorf("tabs %q, want 4 projects", e.tabsLine())
	}
	// A state change updates the row.
	f.RemoveContainer(cid(2))
	e.until("myapp running", func() bool {
		return slices.Equal(rowFields(e.rowOf("myapp")), []string{"myapp", "2/2", "●", "running", "~/src/myapp"})
	})
}

func TestProjectsBeforeLoad(t *testing.T) {
	st := store.New(dockerfake.New(), func(tea.Msg) {})
	v := newProjectsView(st, store.Snapshot{}, nil, projectsConfig{})
	v.SetSize(100, 6)
	if !strings.Contains(v.View(), "Loading projects…") {
		t.Errorf("view before the first snapshot:\n%s", v.View())
	}
	if bs := v.KeyHelp(); bs[0].Enabled() {
		t.Error("enter containers is enabled with no project selected")
	}
	if cmd := v.Update(uitest.KeyPress("enter")); cmd != nil {
		t.Error("enter with no project selected returned a command")
	}
}

func TestProjectsKeyHelp(t *testing.T) {
	e := projectsEnv(t, projectSample(), 200, 14)
	e.selectProject("myapp")
	if got := e.footer(); !strings.HasPrefix(got, " enter containers  s stop  r restart  / filter") {
		t.Errorf("footer %q", got)
	}
	e.press("/")
	e.typeText("zzz")
	e.press("enter")
	if got := e.footer(); strings.Contains(got, "enter containers") {
		t.Errorf("footer %q lists enter with no project selected", got)
	}
}

func TestProjectsHostileText(t *testing.T) {
	f := dockerfake.New()
	const project = "evil\x1b[31m\x07\u202e"
	f.AddContainer(composeCtr(0, "evil-web-1", "nginx", container.StateRunning, "Up",
		dockerfake.Compose{Project: project, Service: "web", WorkingDir: "/tmp/\x1b]0;pwned\x07\u2066"}))
	e := projectsEnv(t, f, 100, 10)
	bad := []string{"\x1b[31m", "\x1b]0;", "\x07", "\u202e", "\u2066"}
	clean := func(where string) {
		t.Helper()
		raw := e.m.View().Content
		for _, b := range bad {
			if strings.Contains(raw, b) {
				t.Errorf("%s: screen contains the engine's control sequence %q", where, b)
			}
		}
	}
	clean("projects view")
	// The drill-down matches the exact label value; its breadcrumb and its
	// empty text, which name the project, are cleaned too.
	e.press("enter")
	e.until("drill-down", func() bool { return e.rowOf("evil-web-1") != "" })
	clean("drill-down")
	f.RemoveContainer(cid(0))
	e.until("empty drill-down", func() bool { return strings.Contains(e.screen(), "No containers in project evil") })
	clean("empty drill-down")
}

func TestProjectDrillDown(t *testing.T) {
	e := projectsEnv(t, projectSample(), 100, 20)
	e.selectProject("myapp")
	e.press("enter")
	e.settle()
	if h := e.header(); !strings.HasPrefix(h, "drydock > projects > myapp > containers [all]") {
		t.Fatalf("header %q", h)
	}
	// Running or stopped, one-off included, and nothing of other projects.
	for _, name := range []string{"myapp-db-1", "myapp-web-1", "myapp-web-run-1a2b", "myapp-worker-1"} {
		if e.rowOf(name) == "" {
			t.Errorf("drill-down does not list %s:\n%s", name, e.screen())
		}
	}
	for _, name := range []string{"blog-web-1", "shop-api-1", "redis", "tools-cli-run-9f8e"} {
		if e.rowOf(name) != "" {
			t.Errorf("drill-down lists %s of another project", name)
		}
	}
	// The projects tab stays highlighted.
	if raw := e.m.View().Content; !strings.Contains(raw, components.Styles.TabPillActive.Render("4 projects (4)")) {
		t.Errorf("projects tab not highlighted:\n%s", e.tabsLine())
	}

	// P is scoped to the project's stopped containers.
	e.openPrune()
	if got, want := e.pruneListed(), []string{"myapp-worker-1 exited"}; !slices.Equal(got, want) {
		t.Errorf("prune lists %v, want %v", got, want)
	}
	e.press("n")
	e.settle()

	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > projects") || strings.Contains(h, "containers") {
		t.Errorf("header after esc %q", h)
	}
	if p, _ := e.projectsView().selected(); p.Name != "myapp" {
		t.Errorf("selection after esc %q, want myapp", p.Name)
	}
}

func TestProjectDrillDownFollowsProject(t *testing.T) {
	f := projectSample()
	e := projectsEnv(t, f, 100, 20)
	e.selectProject("blog")
	e.press("enter")
	e.settle()
	// A container created for the project afterwards is listed; one of
	// another project is not.
	f.AddContainer(composeCtr(8, "blog-db-1", "postgres", container.StateRunning, "Up", dockerfake.Compose{Project: "blog", Service: "db"}))
	f.AddContainer(composeCtr(9, "blogs-db-1", "postgres", container.StateRunning, "Up", dockerfake.Compose{Project: "blogs", Service: "db"}))
	e.until("new container listed", func() bool { return e.rowOf("blog-db-1") != "" })
	e.settle()
	if e.rowOf("blogs-db-1") != "" {
		t.Error("drill-down lists a container of project blogs")
	}
	// Once its last container is gone the drill-down is empty.
	f.RemoveContainer(cid(4))
	f.RemoveContainer(cid(8))
	e.until("empty drill-down", func() bool { return strings.Contains(e.screen(), "No containers in project blog.") })
}

func TestInProject(t *testing.T) {
	c := ctr(cid(0), "x", "alpine", container.StateRunning, "Up")
	if inProject(c, "") {
		t.Error("an unlabeled container is in the empty project")
	}
	c.Labels = map[string]string{store.ComposeProjectLabel: ""}
	if inProject(c, "") {
		t.Error("an empty label matches the empty project")
	}
	c.Labels[store.ComposeProjectLabel] = "myapp"
	if !inProject(c, "myapp") || inProject(c, "MYAPP") || inProject(c, "my") {
		t.Error("inProject does not match the exact label value")
	}
}
