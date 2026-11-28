package views

import (
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// Tests of the root model's chrome (tab strip, footer, help overlay)
// driven by the real resource views.

// tabsLine returns the resource tab strip, right-trimmed.
func (e *env) tabsLine() string {
	e.t.Helper()
	ls := e.lines()
	if len(ls) < 5 {
		e.t.Fatalf("screen of %d lines has no tab strip", len(ls))
	}
	return strings.TrimRight(ls[1], " ")
}

func (e *env) footer() string {
	ls := e.lines()
	return strings.TrimRight(ls[len(ls)-1], " ")
}

func (e *env) resize(width, height int) {
	e.t.Helper()
	e.deliver(tea.WindowSizeMsg{Width: width, Height: height})
}

// mixedContainers returns an engine with web running, worker exited and
// fresh created.
func mixedContainers() *dockerfake.Service {
	f := dockerfake.New()
	f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up 5 minutes"))
	f.AddContainer(ctr(cid(1), "worker", "alpine", container.StateExited, "Exited (0)"))
	f.AddContainer(ctr(cid(2), "fresh", "busybox", container.StateCreated, "Created"))
	return f
}

func TestTabsCountIndependentOfRunningMode(t *testing.T) {
	e := newEnv(t, mixedContainers(), 100, 12)
	if e.root().all {
		t.Fatal("setup: the containers view starts in all mode")
	}
	tabs := e.tabsLine()
	if !strings.Contains(tabs, "1 containers (3)") || !strings.HasSuffix(tabs, " 1/1") {
		t.Errorf("tabs %q, want containers (3) and position 1/1 (running only)", tabs)
	}
	e.press("a")
	if tabs := e.tabsLine(); !strings.Contains(tabs, "1 containers (3)") || !strings.HasSuffix(tabs, "/3") {
		t.Errorf("all mode: tabs %q, want containers (3) and 3 rows listed", tabs)
	}
}

func TestTabsCountsUpdateLive(t *testing.T) {
	f := dockerfake.New()
	e := newEnv(t, f, 100, 12)
	if tabs := e.tabsLine(); !strings.Contains(tabs, "3 volumes (0)") {
		t.Fatalf("tabs %q, want volumes (0)", tabs)
	}
	f.AddVolume(volume.Volume{Name: "cache"})
	e.until("volumes count", func() bool { return strings.Contains(e.tabsLine(), "3 volumes (1)") })
}

func TestTabsSwitchAndHighlightWithRealViews(t *testing.T) {
	st := components.Styles
	e := newEnv(t, mixedContainers(), 100, 12)
	e.press("2")
	e.settle()
	if h := e.header(); !strings.HasPrefix(h, "drydock > images") {
		t.Fatalf("header %q after 2", h)
	}
	if raw := e.m.View().Content; !strings.Contains(raw, st.TabPillActive.Render("2 images (0)")) {
		t.Errorf("images tab not highlighted:\n%s", e.tabsLine())
	}
}

func TestProjectsResourceOpens(t *testing.T) {
	for _, open := range []func(e *env){
		func(e *env) { e.command("projects") },
		func(e *env) { e.command("pr") },
		func(e *env) { e.press("4"); e.settle() },
	} {
		e := newEnv(t, projectSample(), 100, 12)
		open(e)
		if h := e.header(); !strings.HasPrefix(h, "drydock > projects") {
			t.Fatalf("header %q, want drydock > projects", h)
		}
		if raw := e.m.View().Content; !strings.Contains(raw, components.Styles.TabPillActive.Render("4 projects (4)")) {
			t.Errorf("projects tab not highlighted with its count:\n%s", e.tabsLine())
		}
	}
}

func TestNetworksResourceOpens(t *testing.T) {
	for _, open := range []func(e *env){
		func(e *env) { e.command("networks") },
		func(e *env) { e.command("net") },
		func(e *env) { e.press("5"); e.settle() },
	} {
		e := newEnv(t, networkSample(t), 100, 12)
		open(e)
		if h := e.header(); !strings.HasPrefix(h, "drydock > networks") {
			t.Fatalf("header %q, want drydock > networks", h)
		}
		if raw := e.m.View().Content; !strings.Contains(raw, components.Styles.TabPillActive.Render("5 networks (8)")) {
			t.Errorf("networks tab not highlighted with its count:\n%s", e.tabsLine())
		}
	}
}

func TestTabsNetworksCountUpdatesLive(t *testing.T) {
	f := dockerfake.New()
	e := newEnv(t, f, 100, 12)
	if tabs := e.tabsLine(); !strings.Contains(tabs, "5 networks (3)") {
		t.Fatalf("tabs %q, want the 3 built-in networks", tabs)
	}
	if _, err := f.AddNetwork(network.Network{Name: "test-net"}); err != nil {
		t.Fatal(err)
	}
	e.until("networks count", func() bool { return strings.Contains(e.tabsLine(), "5 networks (4)") })
}

func TestTabsProjectsCount(t *testing.T) {
	f := dockerfake.New()
	e := newEnv(t, f, 100, 12)
	if tabs := e.tabsLine(); !strings.Contains(tabs, "4 projects (0)") {
		t.Fatalf("tabs %q, want projects (0) with no Compose container", tabs)
	}
	// Two containers of one project count once; a one-off container still
	// makes its project exist.
	f.AddContainer(composeCtr(0, "myapp-web-1", "nginx", container.StateRunning, "Up", dockerfake.Compose{Project: "myapp", Service: "web"}))
	f.AddContainer(composeCtr(1, "myapp-db-1", "postgres", container.StateExited, "Exited (0)", dockerfake.Compose{Project: "myapp", Service: "db"}))
	f.AddContainer(composeCtr(2, "tools-run-1", "alpine", container.StateExited, "Exited (0)", dockerfake.Compose{Project: "tools", OneOff: true}))
	e.until("projects count", func() bool { return strings.Contains(e.tabsLine(), "4 projects (2)") })
}

func TestFooterWideShowsEverything(t *testing.T) {
	e := newEnv(t, mixedContainers(), 200, 12)
	e.selectRow("web")
	want := " l logs  s stop  p pause  x shell  i inspect  r restart  d delete  a all/running  / filter  P prune stopped  t stats  o sort cpu  : command  ? help  q quit"
	if got := e.footer(); got != want {
		t.Errorf("footer\n  %q\nwant\n  %q", got, want)
	}
}

func TestFooterNarrowEndsWithMore(t *testing.T) {
	check := func(t *testing.T, footer string, width int) {
		t.Helper()
		if !strings.HasSuffix(footer, "? more") {
			t.Errorf("footer %q does not end with ? more", footer)
		}
		for _, bad := range []string{"…", "q quit", ": command", "? help"} {
			if strings.Contains(footer, bad) {
				t.Errorf("footer %q contains %q", footer, bad)
			}
		}
		if w := len([]rune(footer)); w > width {
			t.Errorf("footer is %d cells, want at most %d", w, width)
		}
	}
	t.Run("containers", func(t *testing.T) {
		e := newEnv(t, mixedContainers(), 60, 12)
		e.selectRow("web")
		got := e.footer()
		check(t, got, 60)
		if !strings.HasPrefix(got, " l logs  s stop  p pause  x shell") {
			t.Errorf("footer %q does not keep the highest-priority hints", got)
		}
	})
	t.Run("logs", func(t *testing.T) {
		e, _, _ := logEnv(t, 3, nil)
		e.resize(60, 12)
		e.openLogs()
		got := e.footer()
		check(t, got, 60)
		if !strings.HasPrefix(got, " G follow  / search") {
			t.Errorf("footer %q does not keep the highest-priority hints", got)
		}
	})
}

func TestFooterHidesInapplicableActions(t *testing.T) {
	e := newEnv(t, mixedContainers(), 200, 12)
	e.press("a")
	e.selectRow("worker")
	got := e.footer()
	if strings.Contains(got, "x shell") || !strings.Contains(got, "s start") {
		t.Errorf("exited container: footer %q, want s start and no x shell", got)
	}
	// The hidden key still answers with its error.
	e.press("x")
	e.waitStatus("must be running")
}

func TestHelpListsInapplicableKeys(t *testing.T) {
	// The dimmed style itself is checked by the ui package, on the overlay
	// before it is composited over the view.
	e := newEnv(t, mixedContainers(), 100, 30)
	e.press("a")
	e.selectRow("worker")
	e.press("?")
	s := e.screen()
	for _, want := range []string{"x          shell", "l          logs", "1-5/tab    switch resource"} {
		if !strings.Contains(s, want) {
			t.Errorf("help lacks %q:\n%s", want, s)
		}
	}
}

func TestHelpFitsAt80x24(t *testing.T) {
	e := newEnv(t, mixedContainers(), 80, 24)
	e.selectRow("web") // running: every container key applies
	e.press("?")
	s := e.screen()
	if !strings.Contains(s, "? or esc to close") || !strings.Contains(s, "╰") {
		t.Errorf("help overlay is clipped at 80x24:\n%s", s)
	}
	for _, want := range []string{"Global", "1-5/tab", "Containers", "l", "/ ", "filter", "t          stats"} {
		if !strings.Contains(s, want) {
			t.Errorf("help lacks %q:\n%s", want, s)
		}
	}
}

// TestBackgroundColorRestylesViewInputs checks that the real views restyle
// the text inputs they own when the terminal reports a light background:
// each resource view's list filter, and the logs view's search input.
func TestBackgroundColorRestylesViewInputs(t *testing.T) {
	dark, light := components.NewStyles(true), components.NewStyles(false)
	setup := map[string]func(e *env){
		"containers": func(*env) {},
		"images":     func(e *env) { e.command("images") },
		"volumes":    func(e *env) { e.command("volumes") },
		"networks":   func(e *env) { e.command("networks") },
		"logs":       func(e *env) { e.openLogs() },
	}
	for name, open := range setup {
		t.Run(name, func(t *testing.T) {
			// components.Styles is a package global: this test must not run
			// in parallel with any test that renders.
			saved := components.Styles
			t.Cleanup(func() { components.Styles = saved })

			e, _, _ := logEnv(t, 3, nil)
			open(e)
			e.press("/")
			e.typeText("x")
			raw := e.m.View().Content
			if !strings.Contains(raw, dark.InputPrompt.Render("/")) {
				t.Fatalf("before the report, input prompt not in the dark style:\n%q", raw)
			}
			e.deliver(tea.BackgroundColorMsg{Color: color.White})
			raw = e.m.View().Content
			if !strings.Contains(raw, light.InputPrompt.Render("/")) {
				t.Errorf("after a light report, input prompt not in the light style:\n%q", raw)
			}
		})
	}
}

// TestBackgroundColorRestylesViewsBelowTop checks that a view below the top
// of the stack restyles its input too: the containers filter, applied before
// the logs view was pushed, shows the light prompt once the user comes back
// and edits it.
func TestBackgroundColorRestylesViewsBelowTop(t *testing.T) {
	saved := components.Styles // a package global: this test must stay serial
	t.Cleanup(func() { components.Styles = saved })

	e, _, _ := logEnv(t, 3, nil)
	e.press("/")
	e.typeText("we")
	e.press("enter")
	e.openLogs()
	e.deliver(tea.BackgroundColorMsg{Color: color.White})
	e.press("esc") // back to the containers view
	if !strings.Contains(e.header(), "containers") {
		t.Fatalf("not back on containers: header %q", e.header())
	}
	e.press("/")
	raw := e.m.View().Content
	if !strings.Contains(raw, components.NewStyles(false).InputPrompt.Render("/")) {
		t.Errorf("containers filter below the logs view not restyled:\n%q", raw)
	}
}

// TestStatsSettingIsSessionWide drives the root model through every way a
// containers view is shown, checking the stats setting carries over and
// that stats are read only while a containers view with stats on is shown.
func TestStatsSettingIsSessionWide(t *testing.T) {
	f := imageSample() // web runs nginx; old (stopped) too
	e := newEnv(t, f, 120, 14)

	// Off at startup: the PORTS layout and no stream.
	e.settle()
	if got := e.headerTitles(); !slices.Contains(got, "PORTS") || slices.Contains(got, "CPU") {
		t.Errorf("startup columns %q, want PORTS", got)
	}
	if n := len(f.CallsTo(dockerfake.MethodContainerStats)); n != 0 {
		t.Errorf("%d stats streams opened at startup", n)
	}

	e.press("t")
	e.waitStreams(1)

	// Switching to images closes every stream.
	e.press("2")
	e.waitStreams(0)

	// A drill-down from an image shows CPU and MEM and reads its container.
	e.selectImage("nginx:1.27")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "nginx:1.27 > containers") })
	if got := e.headerTitles(); !slices.Contains(got, "CPU") || !slices.Contains(got, "MEM") {
		t.Errorf("drill-down columns %q, want CPU and MEM", got)
	}
	e.waitStreams(1)
	e.press("esc")
	e.waitStreams(0)

	// Switching back reopens them.
	e.press("1")
	e.waitStreams(1)
	if got := e.headerTitles(); !slices.Contains(got, "CPU") {
		t.Errorf("columns %q after switching back, want stats", got)
	}

	// So does :containers.
	e.command("images")
	e.waitStreams(0)
	e.command("containers")
	e.waitStreams(1)
	if !strings.Contains(e.header(), "[stats]") {
		t.Errorf("header %q after :containers, want the stats indicator", e.header())
	}

	// Logs pushed on top close the streams, and Esc reopens them.
	e.selectRow("web")
	e.press("l")
	e.until("logs view", func() bool { return strings.Contains(e.header(), "web > logs") })
	e.waitStreams(0)
	e.press("esc")
	e.waitStreams(1)

	// Quitting closes them.
	e.m.Close()
	e.waitStreams(0)
}

// The help sections go side by side only when stacking them would not fit;
// at any size the overlay stays within the screen width.
func TestHelpLayoutFollowsSpace(t *testing.T) {
	sameLine := func(s string) bool {
		for _, l := range strings.Split(s, "\n") {
			if strings.Contains(l, "Global") && strings.Contains(l, "Containers") {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		w, h int
		side bool
	}{{80, 24, true}, {100, 40, false}, {40, 24, false}} {
		e := newEnv(t, mixedContainers(), tc.w, tc.h)
		e.press("?")
		s := e.screen()
		if got := sameLine(s); got != tc.side {
			t.Errorf("%dx%d: side by side = %v, want %v:\n%s", tc.w, tc.h, got, tc.side, s)
		}
		for _, l := range e.lines() {
			if n := len([]rune(l)); n > tc.w {
				t.Errorf("%dx%d: line of %d cells", tc.w, tc.h, n)
			}
		}
	}
}
