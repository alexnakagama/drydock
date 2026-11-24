package views

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
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
	if !strings.Contains(tabs, "1 containers 3") || !strings.HasSuffix(tabs, " 1/1") {
		t.Errorf("tabs %q, want containers 3 and position 1/1 (running only)", tabs)
	}
	e.press("a")
	if tabs := e.tabsLine(); !strings.Contains(tabs, "1 containers 3") || !strings.HasSuffix(tabs, "/3") {
		t.Errorf("all mode: tabs %q, want containers 3 and 3 rows listed", tabs)
	}
}

func TestTabsCountsUpdateLive(t *testing.T) {
	f := dockerfake.New()
	e := newEnv(t, f, 100, 12)
	if tabs := e.tabsLine(); !strings.Contains(tabs, "3 volumes 0") {
		t.Fatalf("tabs %q, want volumes 0", tabs)
	}
	f.AddVolume(volume.Volume{Name: "cache"})
	e.until("volumes count", func() bool { return strings.Contains(e.tabsLine(), "3 volumes 1") })
}

func TestTabsSwitchAndHighlightWithRealViews(t *testing.T) {
	st := components.Styles
	e := newEnv(t, mixedContainers(), 100, 12)
	e.press("2")
	e.settle()
	if h := e.header(); !strings.HasPrefix(h, "drydock > images") {
		t.Fatalf("header %q after 2", h)
	}
	if raw := e.m.View().Content; !strings.Contains(raw, st.TabActive.Render("2 images 0")) {
		t.Errorf("images tab not highlighted:\n%s", e.tabsLine())
	}
}

func TestFooterWideShowsEverything(t *testing.T) {
	e := newEnv(t, mixedContainers(), 200, 12)
	e.selectRow("web")
	want := " l logs  s stop  x shell  i inspect  r restart  d delete  a all/running  / filter  : command  ? help  q quit"
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
		if !strings.HasPrefix(got, " l logs  s stop  x shell") {
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
	for _, want := range []string{"x          shell", "l          logs", "1-3/tab    switch resource"} {
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
	for _, want := range []string{"Global", "1-3/tab", "Containers", "l", "/ ", "filter"} {
		if !strings.Contains(s, want) {
			t.Errorf("help lacks %q:\n%s", want, s)
		}
	}
}
