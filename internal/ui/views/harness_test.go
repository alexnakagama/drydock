package views

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite golden files")

const waitTimeout = 5 * time.Second

// env runs the real root model and store against a fake engine, like a
// small tea.Program: commands run in goroutines and their messages, and the
// store's, are delivered to the model on the test goroutine by until and
// settle.
type env struct {
	t   *testing.T
	f   *dockerfake.Service
	st  *store.Store
	m   *ui.Model
	in  chan tea.Msg
	ctx context.Context
	wg  sync.WaitGroup

	// Recorded by the containers views the model creates.
	containers []*containersView
	images     []*imagesView
	logs       []*logsView

	execMu sync.Mutex
	execs  []*exec.Cmd // shells actually handed the terminal

	// execEnv and logsCfg configure the views the model creates.
	execEnv execEnv
	logsCfg logsConfig
	// execResult is returned by the fake terminal handover.
	execResult error
}

func newEnv(t *testing.T, f *dockerfake.Service, width, height int) *env {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{t: t, f: f, in: make(chan tea.Msg, 256), ctx: ctx}
	e.logsCfg = defaultLogsConfig()
	e.execEnv = execEnv{
		lookPath: func(string) (string, error) { return "/usr/bin/docker", nil },
		environ:  func() []string { return []string{"PATH=/usr/bin", "DOCKER_CONTEXT=remote"} },
		run: func(cmd *exec.Cmd, done tea.ExecCallback) tea.Cmd {
			err := e.execResult
			return func() tea.Msg {
				e.recordExec(cmd)
				return done(err)
			}
		},
		poll: time.Millisecond,
	}

	e.st = store.New(f, func(msg tea.Msg) {
		select {
		case e.in <- msg:
		case <-ctx.Done():
		}
	})
	storeDone := make(chan struct{})
	go func() { defer close(storeDone); _ = e.st.Run(ctx) }()

	e.m = ui.New([]ui.Resource{
		{Name: "containers", Aliases: []string{"co"}, New: func(snap store.Snapshot) ui.View {
			v := newContainersView(e.st, snap, []string{"containers"}, "No containers.", nil, false, e.execEnv)
			e.wrapLogs(v)
			e.containers = append(e.containers, v)
			return v
		}},
		{Name: "images", Aliases: []string{"im"}, New: func(snap store.Snapshot) ui.View {
			v := newImagesView(e.st, snap, imagesConfig{now: func() time.Time { return testNow }})
			e.images = append(e.images, v)
			return v
		}},
	}, "containers")
	e.m.SetTimerFunc(func(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil })

	t.Cleanup(func() {
		e.m.Close()
		cancel()
		<-storeDone
		done := make(chan struct{})
		go func() { e.wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Error("commands still running after the test")
		}
		for _, l := range e.logs {
			select {
			case <-l.done:
			case <-time.After(waitTimeout):
				t.Error("log stream goroutines still running after the test")
			}
		}
	})

	e.run(e.m.Init())
	e.deliver(tea.WindowSizeMsg{Width: width, Height: height})
	e.until("connected with a snapshot", func() bool {
		return e.st.State() == store.StateConnected && strings.Contains(e.screen(), "connected")
	})
	return e
}

func (e *env) recordExec(cmd *exec.Cmd) {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	e.execs = append(e.execs, cmd)
}

// shells returns the commands that were handed the terminal.
func (e *env) shells() []*exec.Cmd {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	return slices.Clone(e.execs)
}

// wrapLogs makes v create its logs views with the env's config, and records
// them.
func (e *env) wrapLogs(v *containersView) {
	v.newLogs = func(id, name string) ui.View {
		lv := newLogsView(e.st, id, name, e.logsCfg)
		e.logs = append(e.logs, lv)
		return lv
	}
}

// push pushes a view built by the test, as a view's command would.
func (e *env) push(v ui.View) {
	e.t.Helper()
	e.run(ui.Push(v))
	want := strings.Join(v.Crumbs(), " > ")
	e.until("view pushed", func() bool { return strings.Contains(e.header(), want) })
}

// run starts cmd; its message is delivered later by until or settle.
func (e *env) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		msg := cmd()
		if msg == nil {
			return
		}
		select {
		case e.in <- msg:
		case <-e.ctx.Done():
		}
	}()
}

// deliver hands msg to the model and starts the commands it returns.
func (e *env) deliver(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			e.run(c)
		}
	case tea.QuitMsg:
	default:
		_, cmd := e.m.Update(msg)
		e.run(cmd)
	}
}

// until delivers messages until pred holds. pred is also re-checked
// periodically, for conditions that change without a message (for example
// an engine call made by a command that returns nothing).
func (e *env) until(what string, pred func() bool) {
	e.t.Helper()
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !pred() {
		select {
		case msg := <-e.in:
			e.deliver(msg)
		case <-tick.C:
		case <-deadline.C:
			e.t.Fatalf("timed out waiting for %s; screen:\n%s", what, e.screen())
		}
	}
}

// settle delivers messages until none has arrived for a while.
func (e *env) settle() {
	e.t.Helper()
	quiet := time.NewTimer(100 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case msg := <-e.in:
			e.deliver(msg)
			quiet.Reset(100 * time.Millisecond)
		case <-quiet.C:
			return
		}
	}
}

func (e *env) press(names ...string) {
	e.t.Helper()
	for _, n := range names {
		e.deliver(uitest.KeyPress(n))
	}
}

func (e *env) typeText(s string) {
	for _, r := range s {
		e.press(string(r))
	}
}

func (e *env) screen() string { return ansi.Strip(e.m.View().Content) }

func (e *env) lines() []string { return strings.Split(e.screen(), "\n") }

func (e *env) header() string { return strings.TrimSpace(e.lines()[0]) }

func (e *env) statusLine() string {
	ls := e.lines()
	if len(ls) < 2 {
		return ""
	}
	return strings.TrimSpace(ls[len(ls)-2])
}

// content returns the lines of the content area (between the header and
// the status line), right-trimmed.
func (e *env) content() []string {
	ls := e.lines()
	if len(ls) < 3 {
		return nil
	}
	out := make([]string, 0, len(ls)-3)
	for _, l := range ls[1 : len(ls)-2] {
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

// rowOf returns the content line listing name as its first cell, or "".
func (e *env) rowOf(name string) string {
	for _, l := range e.content() {
		if f := strings.Fields(l); len(f) > 0 && f[0] == name {
			return l
		}
	}
	return ""
}

// waitStatus waits until the status line contains s.
func (e *env) waitStatus(s string) {
	e.t.Helper()
	e.until("status "+s, func() bool { return strings.Contains(e.statusLine(), s) })
}

// root returns the root containers view.
func (e *env) root() *containersView { return e.containers[len(e.containers)-1] }

// selectRow moves the selection of the root view to the container called
// name.
func (e *env) selectRow(name string) {
	e.t.Helper()
	v := e.root()
	for id, c := range v.shown {
		if store.ContainerName(c) == name && v.table.Select(id) {
			return
		}
	}
	e.t.Fatalf("no row %s; screen:\n%s", name, e.screen())
}

// testNow is the reference time of views that display ages.
var testNow = time.Date(2026, time.November, 24, 12, 0, 0, 0, time.UTC)

// command runs a command-bar entry, for example "images".
func (e *env) command(name string) {
	e.t.Helper()
	e.press(":")
	e.typeText(name)
	e.press("enter")
	e.settle()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("screen differs from %s (run with -update after checking):\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
	}
}

// ctr builds a container summary as the engine lists it.
func ctr(id, name, img string, state container.ContainerState, status string, ports ...container.PortSummary) container.Summary {
	return container.Summary{
		ID:      id,
		Names:   []string{"/" + name},
		Image:   img,
		ImageID: "sha256:" + strings.Repeat(img[:1], 64),
		State:   state,
		Status:  status,
		Ports:   ports,
	}
}

func cid(n int) string { return strings.Repeat(string(rune('a'+n)), 64) }
