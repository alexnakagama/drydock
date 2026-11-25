package ui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

var update = flag.Bool("update", false, "rewrite golden files")

// listView is a minimal resource view built on the shared table: `l` pushes
// a detail view for the selected row and `d` asks to delete it.
type listView struct {
	name    string
	table   components.Table
	closed  int
	got     []tea.Msg // non-key messages received
	deleted []string
}

type deletedMsg struct{ key string }

func newListView(name string, rows ...components.Row) *listView {
	v := &listView{name: name, table: components.NewTable([]components.Column{
		{Title: "NAME", Width: 12, Filter: true},
		{Title: "IMAGE", Width: 20, Flex: true, Filter: true},
		{Title: "STATUS", Width: 12},
	})}
	v.table.SetEmptyText("No " + name + ".")
	v.table.SetRows(rows)
	return v
}

var (
	keyLogs   = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs"))
	keyDelete = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
)

func (v *listView) Init() tea.Cmd { return nil }
func (v *listView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if ok, cmd := v.table.Update(msg); ok {
			return cmd
		}
		r, ok := v.table.Selected()
		if !ok {
			return nil
		}
		switch {
		case key.Matches(msg, keyLogs):
			return Push(&detailView{crumbs: []string{r.Cells[0], "logs"}})
		case key.Matches(msg, keyDelete):
			k := r.Key
			return Confirm("Delete "+r.Cells[0]+"?", []string{"This cannot be undone."},
				func() tea.Msg { return deletedMsg{key: k} })
		}
	case tea.PasteMsg:
		_, cmd := v.table.Update(msg)
		return cmd
	case tea.BackgroundColorMsg:
		_, cmd := v.table.Update(msg)
		return cmd
	case deletedMsg:
		v.deleted = append(v.deleted, msg.key)
	default:
		v.got = append(v.got, msg)
	}
	return nil
}
func (v *listView) View() string     { return v.table.View() }
func (v *listView) SetSize(w, h int) { v.table.SetSize(w, h) }
func (v *listView) Crumbs() []string { return []string{v.name} }
func (v *listView) KeyHelp() []key.Binding {
	return append([]key.Binding{keyLogs, keyDelete}, keys.Table.Filter)
}
func (v *listView) InputFocused() bool { return v.table.InputFocused() }
func (v *listView) Back() bool         { return v.table.Back() }
func (v *listView) Close()             { v.closed++ }
func (v *listView) selected() string   { r, _ := v.table.Selected(); return r.Key }
func (v *listView) Position() (selected, listed, total int, filtered bool) {
	return v.table.Position()
}
func (v *listView) Indicators() []string {
	if f := v.table.Filter(); f != "" {
		return []string{"/" + f}
	}
	return nil
}

// detailView is a pushed view with no input of its own.
type detailView struct {
	crumbs []string
	closed int
	w, h   int
}

func (v *detailView) Init() tea.Cmd          { return nil }
func (v *detailView) Update(tea.Msg) tea.Cmd { return nil }
func (v *detailView) View() string           { return "detail" }
func (v *detailView) SetSize(w, h int)       { v.w, v.h = w, h }
func (v *detailView) Crumbs() []string       { return v.crumbs }
func (v *detailView) Indicators() []string   { return nil }
func (v *detailView) KeyHelp() []key.Binding { return nil }
func (v *detailView) InputFocused() bool     { return false }
func (v *detailView) Back() bool             { return false }
func (v *detailView) Close()                 { v.closed++ }

func row(key, name, image, status string) components.Row {
	return components.Row{Key: key, Cells: []string{name, image, status}}
}

// harness builds a root model whose resource views are recorded, so tests
// can inspect them.
type harness struct {
	t      *testing.T
	m      *Model
	views  map[string][]*listView // by resource name, in creation order
	timers []timer                // scheduled by the model, not yet fired
}

// timer is a call to Model.after recorded by the harness.
type timer struct {
	d  time.Duration
	fn func(time.Time) tea.Msg
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessAt(t, "containers")
}

// Engine totals the harness resources report once a snapshot has loaded.
const (
	countContainers = 12
	countImages     = 34
	countVolumes    = 5
)

func constCount(n int) func(store.Snapshot) int { return func(store.Snapshot) int { return n } }

// newHarnessAt is newHarness starting on the resource named (or aliased)
// initial.
func newHarnessAt(t *testing.T, initial string) *harness {
	t.Helper()
	h := &harness{t: t, views: map[string][]*listView{}}
	mk := func(name string, rows ...components.Row) func(store.Snapshot) View {
		return func(store.Snapshot) View {
			v := newListView(name, rows...)
			h.views[name] = append(h.views[name], v)
			return v
		}
	}
	h.m = New([]Resource{
		{Name: "containers", Aliases: []string{"co"}, Count: constCount(countContainers), New: mk("containers",
			row("c1", "db", "postgres:16", "Up 2 hours"),
			row("c2", "web", "nginx:1.27", "Up 5 minutes"),
			row("c3", "worker", "alpine", "Exited (0)"),
		)},
		{Name: "images", Aliases: []string{"im"}, Count: constCount(countImages), New: mk("images", row("i1", "nginx:1.27", "sha256:abc", "180MB"))},
		{Name: "volumes", Aliases: []string{"vol"}, Count: constCount(countVolumes), New: mk("volumes")},
	}, initial)
	h.m.after = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		h.timers = append(h.timers, timer{d: d, fn: fn})
		return nil
	}
	h.send(h.m.Init())
	h.update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

// update delivers msg and then every message its commands produce, until
// the model settles.
func (h *harness) update(msg tea.Msg) []tea.Msg {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	return h.send(cmd)
}

// send runs cmd and delivers its messages, recursively. It returns every
// message produced, including tea.QuitMsg (which is not delivered).
func (h *harness) send(cmd tea.Cmd) []tea.Msg {
	h.t.Helper()
	var all []tea.Msg
	queue := uitest.Run(h.t, cmd)
	for i := 0; len(queue) > 0; i++ {
		if i > 1000 {
			h.t.Fatal("model did not settle")
		}
		msg := queue[0]
		queue = queue[1:]
		all = append(all, msg)
		if _, ok := msg.(tea.QuitMsg); ok {
			continue
		}
		_, c := h.m.Update(msg)
		queue = append(queue, uitest.Run(h.t, c)...)
	}
	return all
}

// fire delivers the messages of every scheduled timer whose message
// matches keep, and forgets them.
func (h *harness) fire(keep func(tea.Msg) bool) {
	h.t.Helper()
	var rest []timer
	var due []tea.Msg
	for _, tm := range h.timers {
		msg := tm.fn(time.Time{})
		if keep(msg) {
			due = append(due, msg)
		} else {
			rest = append(rest, tm)
		}
	}
	h.timers = rest
	for _, msg := range due {
		h.update(msg)
	}
}

func (h *harness) statusTimers() []timer {
	var out []timer
	for _, tm := range h.timers {
		if _, ok := tm.fn(time.Time{}).(statusExpireMsg); ok {
			out = append(out, tm)
		}
	}
	return out
}

func (h *harness) press(names ...string) []tea.Msg {
	h.t.Helper()
	var all []tea.Msg
	for _, n := range names {
		all = append(all, h.update(uitest.KeyPress(n))...)
	}
	return all
}

func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		h.press(string(r))
	}
}

func (h *harness) screen() string { return ansi.Strip(h.m.render()) }

func (h *harness) header() string {
	return strings.TrimSpace(strings.SplitN(h.screen(), "\n", 2)[0])
}

func (h *harness) statusLine() string {
	lines := strings.Split(h.screen(), "\n")
	return strings.TrimSpace(lines[len(lines)-2])
}

func (h *harness) top() View { return h.m.top() }

func (h *harness) containers() *listView { return h.views["containers"][0] }

// checkGolden compares got with testdata/name, rewriting it with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("View() differs from %s (run with -update after checking):\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
	}
}

func TestGoldenConfirmOverlay(t *testing.T) {
	h := newHarness(t)
	h.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.update(store.ConnectionMsg{State: store.StateConnected})
	h.press("j", "d")
	checkGolden(t, "confirm_80x24.golden", h.screen())
}

func TestGoldenRootView(t *testing.T) {
	h := newHarness(t)
	h.update(store.ConnectionMsg{State: store.StateConnected})
	h.press("j") // select web
	got := h.screen()

	lines := strings.Split(got, "\n")
	if len(lines) != 30 {
		t.Fatalf("screen has %d lines, want 30", len(lines))
	}
	for i, l := range strings.Split(h.m.render(), "\n") {
		if w := ansi.StringWidth(l); w != 100 {
			t.Errorf("line %d is %d cells wide, want 100", i+1, w)
		}
	}

	checkGolden(t, "root_100x30.golden", got)
}

func TestLayoutStartsOnContainers(t *testing.T) {
	h := newHarness(t)
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > containers") {
		t.Errorf("header %q, want breadcrumb drydock > containers", hd)
	}
	lines := strings.Split(h.screen(), "\n")
	footer := lines[len(lines)-1]
	for _, want := range []string{"l logs", "d delete", "/ filter", ": command", "? help", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q lacks %q", footer, want)
		}
	}
}

func TestConnectionIndicator(t *testing.T) {
	h := newHarness(t)
	if hd := h.header(); !strings.HasSuffix(hd, "◌ connecting") {
		t.Errorf("initial header %q, want connecting indicator", hd)
	}
	h.update(store.ConnectionMsg{State: store.StateConnected})
	if hd := h.header(); !strings.HasSuffix(hd, "● connected") {
		t.Errorf("header %q, want connected indicator", hd)
	}
	h.update(store.ConnectionMsg{State: store.StateDisconnected, Err: errors.New("x"), Reason: "connection refused\x1b[2J", RetryIn: 4 * time.Second})
	if hd := h.header(); !strings.HasSuffix(hd, "✗ disconnected · retrying") {
		t.Errorf("header %q, want disconnected indicator", hd)
	}
	if st := h.statusLine(); !strings.Contains(st, "engine unreachable: connection refused") {
		t.Errorf("status %q, want the disconnect reason", st)
	}
	if strings.Contains(h.m.render(), "\x1b[2J") {
		t.Error("control sequence in the disconnect reason reached the screen")
	}
}

func TestResizePreservesSelectionAndFilter(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("w")
	h.press("enter")
	h.press("j") // web, worker -> worker
	c := h.containers()
	if c.selected() != "c3" || c.table.Filter() != "w" {
		t.Fatalf("setup: selected %q filter %q", c.selected(), c.table.Filter())
	}
	for _, sz := range []tea.WindowSizeMsg{{Width: 60, Height: 12}, {Width: 200, Height: 50}, {Width: 100, Height: 30}} {
		h.update(sz)
		if c.selected() != "c3" || c.table.Filter() != "w" {
			t.Errorf("after resize to %dx%d: selected %q filter %q, want c3 and w", sz.Width, sz.Height, c.selected(), c.table.Filter())
		}
		lines := strings.Split(h.m.render(), "\n")
		if len(lines) != sz.Height {
			t.Errorf("after resize to %dx%d: %d lines", sz.Width, sz.Height, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz.Width {
				t.Errorf("after resize to %dx%d: line %d is %d wide", sz.Width, sz.Height, i, w)
			}
		}
	}
	if !strings.Contains(h.header(), "[/w]") {
		t.Errorf("header %q does not show the active filter", h.header())
	}
}

func TestTinyTerminalDoesNotPanic(t *testing.T) {
	h := newHarness(t)
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {10, 2}, {20, 3}, {5, 40}} {
		h.update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		h.press("?")
		_ = h.m.View()
		h.press("esc", "d")
		_ = h.m.View()
		h.press("n")
	}
}

// --- 4.2 view stack ---

func TestDrillIntoLogsAndBack(t *testing.T) {
	h := newHarness(t)
	h.press("j") // web
	h.press("l")
	d, ok := h.top().(*detailView)
	if !ok {
		t.Fatalf("top view is %T after l, want the pushed detail view", h.top())
	}
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > containers > web > logs") {
		t.Errorf("header %q, want drydock > containers > web > logs", hd)
	}
	if want := 30 - baseChromeLines - 1; d.w != 100 || d.h != want {
		t.Errorf("pushed view sized %dx%d, want 100x%d", d.w, d.h, want)
	}
	h.press("esc")
	if h.top() != View(h.containers()) {
		t.Fatalf("after esc top is %T, want the containers view", h.top())
	}
	if h.containers().selected() != "c2" {
		t.Errorf("after esc selected %q, want web (c2)", h.containers().selected())
	}
	if d.closed != 1 {
		t.Errorf("popped view closed %d times, want 1", d.closed)
	}
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > containers") || strings.Contains(hd, "logs") {
		t.Errorf("header after pop %q", hd)
	}
}

func TestEscAtRootDoesNothing(t *testing.T) {
	h := newHarness(t)
	msgs := h.press("esc", "esc")
	if uitest.IsQuit(msgs) {
		t.Fatal("esc at the root view quit")
	}
	if len(h.m.stack) != 1 || h.top() != View(h.containers()) {
		t.Errorf("stack changed: %d views", len(h.m.stack))
	}
	if h.containers().closed != 0 {
		t.Error("root view was closed")
	}
}

func TestEscClearsFilterBeforePopping(t *testing.T) {
	h := newHarness(t)
	h.press("l") // push detail for db
	h.press("esc")
	// Apply a filter on the root, then push again and come back.
	h.press("/")
	h.typeText("web")
	h.press("enter")
	h.press("l")
	h.press("esc")
	c := h.containers()
	if h.top() != View(c) || c.table.Filter() != "web" || c.selected() != "c2" {
		t.Fatalf("after pop: filter %q selected %q; want filter and selection restored", c.table.Filter(), c.selected())
	}
	h.press("esc") // clears the filter, does not pop
	if c.table.Filter() != "" || h.top() != View(c) {
		t.Errorf("first esc: filter %q, top %T; want filter cleared on the same view", c.table.Filter(), h.top())
	}
	if c.table.Len() != 3 {
		t.Errorf("after clearing, %d rows listed, want 3", c.table.Len())
	}
}

func TestEscClearsFilterOnPushedView(t *testing.T) {
	h := newHarness(t)
	h.press(":")
	h.typeText("images")
	h.press("enter")
	// Push a second list view directly, as a drill-down would.
	child := newListView("containers", row("c1", "db", "postgres", ""))
	h.send(Push(child))
	h.press("/")
	h.typeText("db")
	h.press("enter")
	h.press("esc")
	if h.top() != View(child) || child.table.Filter() != "" {
		t.Fatalf("esc with filter on pushed view: top %T filter %q", h.top(), child.table.Filter())
	}
	h.press("esc")
	if h.top() != View(h.views["images"][0]) || child.closed != 1 {
		t.Errorf("second esc did not pop to images (top %T, child closed %d)", h.top(), child.closed)
	}
}

func TestCommandReplacesStack(t *testing.T) {
	h := newHarness(t)
	h.press("l")
	d := h.top().(*detailView)
	h.press(":")
	h.typeText("images")
	h.press("enter")
	if len(h.m.stack) != 1 || h.top() != View(h.views["images"][0]) {
		t.Fatalf("stack after :images has %d views, top %T", len(h.m.stack), h.top())
	}
	if d.closed != 1 || h.containers().closed != 1 {
		t.Errorf("replaced views closed: detail %d, containers %d; want 1 each", d.closed, h.containers().closed)
	}
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > images") || strings.Contains(hd, "containers") {
		t.Errorf("header %q, want drydock > images", hd)
	}
	img := h.views["images"][0]
	if img.table.Len() != 1 {
		t.Error("new root view not sized or populated")
	}
	h.press("esc")
	if h.top() != View(img) {
		t.Error("esc popped the new root view")
	}
}

// --- 4.3 command bar ---

func TestCommandBar(t *testing.T) {
	cases := []struct {
		input, want string
	}{
		{"images", "images"}, {"im", "images"},
		{"volumes", "volumes"}, {"vol", "volumes"},
		{"containers", "containers"}, {"co", "containers"},
		{"  IM ", "images"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			h := newHarness(t)
			h.press(":")
			if !strings.HasPrefix(h.statusLine(), ":") {
				t.Fatalf("command bar not shown: status %q", h.statusLine())
			}
			h.typeText(c.input)
			h.press("enter")
			if hd := h.header(); !strings.HasPrefix(hd, "drydock > "+c.want) {
				t.Errorf("header %q, want drydock > %s", hd, c.want)
			}
			if h.m.overlay != overlayNone {
				t.Error("command bar still open")
			}
		})
	}
}

func TestCommandBarUnknownCommand(t *testing.T) {
	h := newHarness(t)
	before := h.top()
	h.press(":")
	h.typeText("foo")
	h.press("enter")
	if h.top() != before || before.(*listView).closed != 0 {
		t.Error("unknown command changed the view")
	}
	if st := h.statusLine(); !strings.Contains(st, "unknown command: foo") {
		t.Errorf("status %q, want an unknown-command error naming foo", st)
	}
}

func TestCommandBarEscCancels(t *testing.T) {
	h := newHarness(t)
	before := h.top()
	h.press(":")
	h.typeText("images")
	h.press("esc")
	if h.m.overlay != overlayNone || h.top() != before {
		t.Error("esc did not cancel the command bar")
	}
	if strings.HasPrefix(h.statusLine(), ":") {
		t.Errorf("command bar still rendered: %q", h.statusLine())
	}
	// It reopens empty.
	h.press(":")
	if v := h.m.command.Value(); v != "" {
		t.Errorf("command bar reopened with %q", v)
	}
}

func TestCommandBarTypesGlobalKeys(t *testing.T) {
	h := newHarness(t)
	h.press(":")
	msgs := h.press("q", "?")
	if uitest.IsQuit(msgs) || h.m.overlay != overlayCommand {
		t.Fatal("q or ? acted as a global key inside the command bar")
	}
	if v := h.m.command.Value(); v != "q?" {
		t.Errorf("command text %q, want q?", v)
	}
}

func TestEmptyCommandDoesNothing(t *testing.T) {
	h := newHarness(t)
	before := h.top()
	h.press(":", "enter")
	if h.top() != before || h.m.status.kind != statusNone {
		t.Error("empty command changed the view or set a status")
	}
}

// --- 4.5 overlays and key routing ---

func TestConfirmDialog(t *testing.T) {
	for _, k := range []string{"n", "esc", "Y", "q", "enter", "x"} {
		t.Run("cancel with "+k, func(t *testing.T) {
			h := newHarness(t)
			h.press("d")
			if h.m.overlay != overlayConfirm {
				t.Fatal("d did not open the confirm dialog")
			}
			if !strings.Contains(h.screen(), "Delete db?") {
				t.Errorf("dialog not rendered:\n%s", h.screen())
			}
			msgs := h.press(k)
			if h.m.overlay != overlayNone {
				t.Error("dialog still open")
			}
			if len(h.containers().deleted) != 0 {
				t.Error("deletion performed")
			}
			if uitest.IsQuit(msgs) {
				t.Error("key quit the program")
			}
			if strings.Contains(h.screen(), "Delete db?") {
				t.Error("dialog still rendered")
			}
		})
	}
	t.Run("confirm with y", func(t *testing.T) {
		h := newHarness(t)
		h.press("d", "y")
		if got := h.containers().deleted; len(got) != 1 || got[0] != "c1" {
			t.Errorf("deleted %v, want [c1]", got)
		}
		if h.m.overlay != overlayNone {
			t.Error("dialog still open")
		}
	})
}

func TestHelpOverlay(t *testing.T) {
	h := newHarness(t)
	h.press(":")
	h.typeText("im")
	h.press("enter")
	h.press("?")
	if h.m.overlay != overlayHelp {
		t.Fatal("? did not open help")
	}
	s := h.screen()
	for _, want := range []string{"Global", ": ", "command", "Images", "logs", "delete", "filter"} {
		if !strings.Contains(s, want) {
			t.Errorf("help lacks %q:\n%s", want, s)
		}
	}
	// Other keys are ignored while help is open and do not reach the view.
	img := h.views["images"][0]
	if msgs := h.press("q", "d", "j"); uitest.IsQuit(msgs) || h.m.overlay != overlayHelp {
		t.Fatal("help closed or quit on an unrelated key")
	}
	h.press("?")
	if h.m.overlay != overlayNone || h.top() != View(img) {
		t.Error("? did not close help")
	}
	h.press("?", "esc")
	if h.m.overlay != overlayNone || h.top() != View(img) {
		t.Error("esc did not close help, or popped the view")
	}
}

func TestCtrlCQuitsFromAnywhere(t *testing.T) {
	setups := map[string][]string{
		"no overlay":     nil,
		"confirm dialog": {"d"},
		"help":           {"?"},
		"command bar":    {":"},
		"filter":         {"/"},
	}
	for name, setup := range setups {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.press(setup...)
			msgs := h.press("ctrl+c")
			if !uitest.IsQuit(msgs) {
				t.Fatal("ctrl+c did not quit")
			}
			if h.containers().closed != 1 {
				t.Error("views not closed on quit")
			}
			if len(h.containers().deleted) != 0 {
				t.Error("ctrl+c confirmed the dialog")
			}
			if h.m.render() != "" {
				t.Error("screen not cleared on quit")
			}
		})
	}
	t.Run("prompt", func(t *testing.T) {
		h := newHarness(t)
		h.send(Prompt("Name", "", func(string) tea.Cmd { t.Error("prompt submitted"); return nil }))
		if h.m.overlay != overlayPrompt {
			t.Fatal("prompt not open")
		}
		if !uitest.IsQuit(h.press("ctrl+c")) {
			t.Error("ctrl+c did not quit")
		}
	})
}

func TestFilterTakesGlobalKeysAsText(t *testing.T) {
	h := newHarness(t)
	h.press(":")
	h.typeText("images")
	h.press("enter")
	h.press("/")
	var msgs []tea.Msg
	for _, r := range "quay?:" {
		msgs = append(msgs, h.press(string(r))...)
	}
	img := h.views["images"][0]
	if uitest.IsQuit(msgs) {
		t.Fatal("q quit while typing a filter")
	}
	if h.m.overlay != overlayNone {
		t.Fatalf("overlay %v opened while typing a filter", h.m.overlay)
	}
	if f := img.table.Filter(); f != "quay?:" {
		t.Errorf("filter %q, want quay?:", f)
	}
	// Paste goes to the filter too.
	h.update(tea.PasteMsg{Content: "xy"})
	if f := img.table.Filter(); f != "quay?:xy" {
		t.Errorf("after paste filter %q", f)
	}
}

func TestPrompt(t *testing.T) {
	h := newHarness(t)
	var got []string
	open := Prompt("Volume name", "empty for a generated name", func(s string) tea.Cmd {
		got = append(got, s)
		return Success("created " + s)
	})
	h.send(open)
	h.typeText("q?:data")
	if len(got) != 0 {
		t.Fatal("submitted before enter")
	}
	h.press("enter")
	if len(got) != 1 || got[0] != "q?:data" || h.m.overlay != overlayNone {
		t.Fatalf("submitted %v, overlay %v", got, h.m.overlay)
	}
	if st := h.statusLine(); !strings.Contains(st, "created q?:data") {
		t.Errorf("status %q", st)
	}
	h.send(open)
	h.typeText("x")
	h.press("esc")
	if len(got) != 1 || h.m.overlay != overlayNone {
		t.Errorf("esc: submitted %v, overlay %v", got, h.m.overlay)
	}
	// Empty submission is passed through (an empty volume name is valid).
	h.send(open)
	h.press("enter")
	if len(got) != 2 || got[1] != "" {
		t.Errorf("empty submit: %v", got)
	}
}

// TestInitRequestsBackgroundColor checks that the model asks the terminal
// for its background color when it starts, which the palette depends on.
func TestInitRequestsBackgroundColor(t *testing.T) {
	want := reflect.TypeOf(tea.RequestBackgroundColor())
	m := New([]Resource{{Name: "containers", New: func(store.Snapshot) View { return newListView("containers") }}}, "containers")
	for _, msg := range uitest.Run(t, m.Init()) {
		if reflect.TypeOf(msg) == want {
			return
		}
	}
	t.Error("Init does not request the terminal background color")
}

// TestBackgroundColorSwapsPalette checks that a light background report
// switches to the light palette, including the prompts of text inputs that
// were created, and are open, before the report, and that only colors
// change.
func TestBackgroundColorSwapsPalette(t *testing.T) {
	dark, light := components.NewStyles(true), components.NewStyles(false)
	if dark.InputPrompt.Render(":") == light.InputPrompt.Render(":") {
		t.Fatal("dark and light InputPrompt render the same; the test cannot tell them apart")
	}
	screen := func(h *harness) string { return h.m.render() }
	// A dialog is checked before it is composited over the screen, which
	// merges its first style run with the next.
	dialog := func(h *harness) string { return h.m.overlayView() }
	cases := []struct {
		name   string
		open   func(h *harness)
		prompt string
		raw    func(h *harness) string // where the prompt is rendered
	}{
		{"command bar", func(h *harness) { h.press(":") }, ":", screen},
		{"list filter", func(h *harness) { h.press("/"); h.typeText("we") }, "/", screen},
		{"prompt", func(h *harness) { h.send(Prompt("Volume name", "name", nil)) }, "> ", dialog},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// components.Styles is a package global: this test must not run
			// in parallel with any test that renders.
			saved := components.Styles
			t.Cleanup(func() { components.Styles = saved })

			h := newHarness(t)
			if !components.Styles.Dark {
				t.Fatal("light palette in use before the terminal reported its background")
			}
			c.open(h)
			before, raw := h.m.render(), c.raw(h)
			if !strings.Contains(raw, dark.InputPrompt.Render(c.prompt)) {
				t.Fatalf("before the report, %q prompt not in the dark style:\n%q", c.prompt, raw)
			}

			h.update(tea.BackgroundColorMsg{Color: color.White})
			if components.Styles.Dark {
				t.Fatal("dark palette still in use after a light background report")
			}
			after, raw := h.m.render(), c.raw(h)
			if !strings.Contains(raw, light.InputPrompt.Render(c.prompt)) {
				t.Errorf("after the report, %q prompt not in the light style:\n%q", c.prompt, raw)
			}
			if strings.Contains(raw, dark.InputPrompt.Render(c.prompt)) {
				t.Errorf("after the report, %q prompt still in the dark style", c.prompt)
			}
			if b, a := ansi.Strip(before), ansi.Strip(after); b != a {
				t.Errorf("text changed with the palette:\n--- before ---\n%s\n--- after ---\n%s", b, a)
			}

			// A dark report switches back.
			h.update(tea.BackgroundColorMsg{Color: color.Black})
			if !components.Styles.Dark {
				t.Error("light palette still in use after a dark background report")
			}
			if got := c.raw(h); !strings.Contains(got, dark.InputPrompt.Render(c.prompt)) {
				t.Errorf("after a dark report, %q prompt not in the dark style", c.prompt)
			}
		})
	}
}

// --- 4.6 status and quit ---

func TestActionResultStatus(t *testing.T) {
	h := newHarness(t)
	h.update(store.ActionResultMsg{Label: "stop web"})
	if st := h.statusLine(); st != "✓ stop web: done" {
		t.Errorf("success status %q", st)
	}
	seq := h.m.status.seq
	engineErr := errors.New("Error response from daemon: container is paused")
	h.update(store.ActionResultMsg{Label: "stop web", Err: engineErr, Reason: engineErr.Error() + "\x1b]0;pwned\x07"})
	if st := h.statusLine(); !strings.HasPrefix(st, "✗ stop web: Error response from daemon: container is paused") {
		t.Errorf("error status %q", st)
	}
	if strings.Contains(h.m.render(), "\x1b]0;pwned") {
		t.Error("control sequence in engine error reached the screen")
	}
	// An older message's expiry does not clear the newer one.
	h.update(statusExpireMsg{seq: seq})
	if h.m.status.kind != statusError {
		t.Error("stale expiry cleared the current status")
	}
	h.update(statusExpireMsg{seq: h.m.status.seq})
	if h.m.status.kind != statusNone || h.statusLine() != "" {
		t.Errorf("status not cleared on expiry: %q", h.statusLine())
	}
	// Views receive action results too.
	var seen bool
	for _, m := range h.containers().got {
		if _, ok := m.(store.ActionResultMsg); ok {
			seen = true
		}
	}
	if !seen {
		t.Error("ActionResultMsg not forwarded to the view")
	}
}

func TestStatusExpiryIsScheduled(t *testing.T) {
	h := newHarness(t)
	h.update(statusMsg{kind: statusSuccess, text: "ok"})
	h.update(statusMsg{kind: statusError, text: "bad"})
	ts := h.statusTimers()
	if len(ts) != 2 || ts[0].d != successTTL || ts[1].d != errorTTL {
		t.Fatalf("scheduled expiries %v, want [%v %v]", ts, successTTL, errorTTL)
	}
	h.fire(func(m tea.Msg) bool { _, ok := m.(statusExpireMsg); return ok })
	if h.statusLine() != "" {
		t.Errorf("status %q after expiry", h.statusLine())
	}
}

func TestErrorVisibleWhileTaskRuns(t *testing.T) {
	h := newHarness(t)
	h.m.Update(trackStartMsg{text: "Pulling alpine…"})
	engineErr := errors.New("container is paused")
	h.update(store.ActionResultMsg{Label: "stop web", Err: engineErr, Reason: engineErr.Error()})
	st := h.statusLine()
	if !strings.Contains(st, "✗ stop web: container is paused") || !strings.Contains(st, "Pulling alpine…") {
		t.Fatalf("status %q, want both the error and the progress", st)
	}
	if !strings.HasPrefix(st, "✗") {
		t.Errorf("status %q: the error must come first so it is never cut off", st)
	}
	// The error's expiry starts only once nothing hides it.
	if n := len(h.statusTimers()); n != 0 {
		t.Fatalf("%d expiries scheduled while a task runs", n)
	}
	h.update(trackDoneMsg{id: 1})
	if ts := h.statusTimers(); len(ts) != 1 || ts[0].d != errorTTL {
		t.Fatalf("expiries after the task %v, want one of %v", ts, errorTTL)
	}
	if st := h.statusLine(); st != "✗ stop web: container is paused" {
		t.Errorf("status after the task %q", st)
	}
}

func TestSpinnerAnimatesOnlyWhileTasksRun(t *testing.T) {
	h := newHarness(t)
	isTick := func(m tea.Msg) bool { _, ok := m.(spinnerTickMsg); return ok }
	h.m.Update(trackStartMsg{text: "a"})
	h.m.Update(trackStartMsg{text: "b"})
	ticks := 0
	for _, tm := range h.timers {
		if isTick(tm.fn(time.Time{})) {
			ticks++
		}
	}
	if ticks != 1 {
		t.Fatalf("%d spinner ticks scheduled for two tasks, want 1", ticks)
	}
	before := h.m.frame
	h.fire(isTick)
	if h.m.frame == before {
		t.Error("spinner did not advance")
	}
	h.update(trackDoneMsg{id: 1})
	h.update(trackDoneMsg{id: 2})
	h.fire(isTick)
	for _, tm := range h.timers {
		if isTick(tm.fn(time.Time{})) {
			t.Fatal("spinner keeps ticking with no task")
		}
	}
}

func TestTrackShowsProgress(t *testing.T) {
	h := newHarness(t)
	release := make(chan struct{})
	work := func() tea.Msg {
		<-release
		return store.ActionResultMsg{Label: "pull alpine"}
	}
	// Start the task by hand so the blocking command is not run yet.
	_, cmd := h.m.Update(trackStartMsg{text: "Pulling alpine…", cmd: work})
	if st := h.statusLine(); !strings.Contains(st, "Pulling alpine…") {
		t.Fatalf("progress not shown: %q", st)
	}
	// The UI stays responsive while the task runs.
	h.press("j")
	if h.containers().selected() != "c2" {
		t.Error("keys not handled while a task runs")
	}
	close(release)
	h.send(cmd)
	if len(h.m.tasks) != 0 {
		t.Errorf("%d tasks still running", len(h.m.tasks))
	}
	if st := h.statusLine(); st != "✓ pull alpine: done" {
		t.Errorf("status after task %q", st)
	}
}

func TestTrackMultipleTasks(t *testing.T) {
	h := newHarness(t)
	h.m.Update(trackStartMsg{text: "one"})
	h.m.Update(trackStartMsg{text: "two"})
	if st := h.statusLine(); !strings.Contains(st, "two (+1 more)") {
		t.Errorf("status %q", st)
	}
	h.update(trackDoneMsg{id: 2})
	if st := h.statusLine(); !strings.Contains(st, "one") || strings.Contains(st, "more") {
		t.Errorf("status %q", st)
	}
	h.update(trackDoneMsg{id: 1})
	if h.statusLine() != "" {
		t.Errorf("status %q after all tasks done", h.statusLine())
	}
}

func TestQuit(t *testing.T) {
	h := newHarness(t)
	h.press("l")
	d := h.top().(*detailView)
	if !uitest.IsQuit(h.press("q")) {
		t.Fatal("q did not quit")
	}
	if d.closed != 1 || h.containers().closed != 1 {
		t.Errorf("views not closed: detail %d containers %d", d.closed, h.containers().closed)
	}
	// Messages arriving after quit are ignored without panicking.
	h.update(store.SnapshotMsg{})
	h.update(tea.WindowSizeMsg{Width: 10, Height: 10})
	h.press("j")
	_ = h.m.View()
}

func TestBroadcastReachesViewsBelowTop(t *testing.T) {
	h := newHarness(t)
	h.press("l")
	type custom struct{}
	h.update(custom{})
	var n int
	for _, m := range h.containers().got {
		if _, ok := m.(custom); ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("view below the top received %d custom messages, want 1", n)
	}
}

func TestNewPanicsOnUnknownInitial(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted an unknown initial resource")
		}
	}()
	New(nil, "nope")
}

// --- requests from views that are no longer in front ---

func TestStalePushIsDroppedAndClosed(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(uitest.KeyPress("l")) // containers asks to push logs
	h.press(":")
	h.typeText("images")
	h.press("enter") // ...but the stack is replaced before the request lands
	msgs := uitest.Run(t, cmd)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages", len(msgs))
	}
	pm := msgs[0].(pushMsg)
	h.update(pm)
	if len(h.m.stack) != 1 || h.top() != View(h.views["images"][0]) {
		t.Fatalf("stale push changed the stack (top %T)", h.top())
	}
	if d := pm.view.(*detailView); d.closed != 1 {
		t.Errorf("dropped view closed %d times, want 1", d.closed)
	}
}

func TestStaleConfirmIsDropped(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(uitest.KeyPress("d"))
	h.press(":")
	h.typeText("images")
	h.press("enter")
	h.send(cmd)
	if h.m.overlay != overlayNone {
		t.Fatal("a delete confirmation from the replaced view opened over the images view")
	}
}

func TestConfirmFromViewBelowTopIsDropped(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(uitest.KeyPress("d"))
	h.press("l") // a detail view is now on top
	h.send(cmd)
	if h.m.overlay != overlayNone {
		t.Fatal("confirmation from a covered view opened")
	}
}

func TestDialogDoesNotReplaceOpenOverlay(t *testing.T) {
	t.Run("prompt", func(t *testing.T) {
		h := newHarness(t)
		_, confirm := h.m.Update(uitest.KeyPress("d"))
		var submitted []string
		h.send(Prompt("Name", "", func(s string) tea.Cmd { submitted = append(submitted, s); return nil }))
		h.typeText("abc")
		h.send(confirm)
		if h.m.overlay != overlayPrompt || h.m.prompt.Value() != "abc" {
			t.Fatalf("overlay %v value %q: the prompt was replaced", h.m.overlay, h.m.prompt.Value())
		}
		h.press("enter")
		if len(submitted) != 1 || submitted[0] != "abc" {
			t.Errorf("submitted %v", submitted)
		}
	})
	t.Run("command bar", func(t *testing.T) {
		h := newHarness(t)
		_, confirm := h.m.Update(uitest.KeyPress("d"))
		h.press(":")
		h.typeText("im")
		h.send(confirm)
		if h.m.overlay != overlayCommand || h.m.command.Value() != "im" {
			t.Fatalf("overlay %v value %q: the command bar was replaced", h.m.overlay, h.m.command.Value())
		}
	})
}

func TestPushAfterQuitClosesView(t *testing.T) {
	h := newHarness(t)
	_, cmd := h.m.Update(uitest.KeyPress("l"))
	h.press("q")
	pm := uitest.Run(t, cmd)[0].(pushMsg)
	h.m.Update(pm)
	if d := pm.view.(*detailView); d.closed != 1 {
		t.Errorf("view pushed after quit closed %d times, want 1", d.closed)
	}
}

func TestEveryViewClosedExactlyOnce(t *testing.T) {
	h := newHarness(t)
	var details []*detailView
	pushDetail := func() {
		h.press("l")
		details = append(details, h.top().(*detailView))
	}
	pushDetail()
	h.press("esc") // pop
	pushDetail()
	h.press(":")
	h.typeText("vol")
	h.press("enter") // replace (closes containers and the second detail)
	h.press(":")
	h.typeText("co")
	h.press("enter") // replace again
	pushDetail()
	h.press("q")
	h.m.Close() // as main's deferred Close does
	for i, d := range details {
		if d.closed != 1 {
			t.Errorf("detail %d closed %d times", i, d.closed)
		}
	}
	for name, vs := range h.views {
		for i, v := range vs {
			if v.closed != 1 {
				t.Errorf("%s view %d closed %d times", name, i, v.closed)
			}
		}
	}
}

func TestStaleSnapshotsIgnored(t *testing.T) {
	snap := loadSnapshot(t)
	h := newHarness(t)
	count := func() int {
		n := 0
		for _, m := range h.containers().got {
			if _, ok := m.(store.SnapshotMsg); ok {
				n++
			}
		}
		return n
	}
	h.update(store.SnapshotMsg{Snapshot: snap})
	if count() != 1 || h.m.snap.Seq() != snap.Seq() {
		t.Fatalf("snapshot not delivered (views got %d)", count())
	}
	h.update(store.SnapshotMsg{Snapshot: snap})             // repeated
	h.update(store.SnapshotMsg{Snapshot: store.Snapshot{}}) // older
	if count() != 1 || h.m.snap.Seq() != snap.Seq() {
		t.Errorf("stale snapshots delivered: views got %d", count())
	}
}

// loadSnapshot returns the first snapshot a store publishes for an empty
// fake engine.
func loadSnapshot(t *testing.T) store.Snapshot {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	msgs := make(chan tea.Msg)
	st := store.New(dockerfake.New(), func(m tea.Msg) {
		select {
		case msgs <- m:
		case <-ctx.Done():
		}
	})
	done := make(chan struct{})
	go func() { defer close(done); _ = st.Run(ctx) }()
	defer func() { cancel(); <-done }()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-msgs:
			if s, ok := m.(store.SnapshotMsg); ok {
				return s.Snapshot
			}
		case <-timeout:
			t.Fatal("no snapshot from the store")
		}
	}
}

type foregroundRanMsg struct{}

func TestForegroundRunsOnlyWhenInFront(t *testing.T) {
	ran := func() tea.Msg { return foregroundRanMsg{} }
	ranIn := func(msgs []tea.Msg) bool {
		for _, m := range msgs {
			if _, ok := m.(foregroundRanMsg); ok {
				return true
			}
		}
		return false
	}

	// From the top view with nothing open: runs.
	h := newHarness(t)
	if !ranIn(h.send(tagCmd(h.m.topID(), Foreground(ran, "dropped")))) {
		t.Error("not run from the top view")
	}

	// From a view below the top: dropped with the given status.
	h = newHarness(t)
	root := h.m.topID()
	h.press("l") // push a detail view
	if ranIn(h.send(tagCmd(root, Foreground(ran, "shell not opened")))) {
		t.Error("run from a view below the top")
	}
	if !strings.Contains(h.statusLine(), "shell not opened") {
		t.Errorf("status = %q", h.statusLine())
	}

	// With an overlay open: dropped, and the overlay stays.
	h = newHarness(t)
	h.press("?")
	if ranIn(h.send(tagCmd(h.m.topID(), Foreground(ran, "dropped")))) {
		t.Error("run with the help overlay open")
	}
	if h.m.overlay != overlayHelp {
		t.Error("overlay closed")
	}
}

func TestTrackProgressUpdatesIndicator(t *testing.T) {
	h := newHarness(t)
	progress := make(chan string, 1)
	release := make(chan struct{})
	work := func() tea.Msg {
		<-release
		close(progress)
		return store.ActionResultMsg{Label: "pull redis:7"}
	}
	_, cmd := h.m.Update(trackStartMsg{text: "pulling redis:7", cmd: work, progress: progress})
	if st := h.statusLine(); !strings.Contains(st, "pulling redis:7") {
		t.Fatalf("status %q", st)
	}
	id := h.m.tasks[0].id

	SendLatest(progress, "pulling redis:7: 1/3 layers")
	SendLatest(progress, "pulling redis:7: 2/3 layers") // replaces the unread value
	msg := listenProgress(id, progress)()
	if p, ok := msg.(trackProgressMsg); !ok || p.text != "pulling redis:7: 2/3 layers" {
		t.Fatalf("progress message %#v", msg)
	}
	_, next := h.m.Update(msg)
	if next == nil {
		t.Fatal("no further receive pending on the progress channel")
	}
	if st := h.statusLine(); !strings.Contains(st, "pulling redis:7: 2/3 layers") {
		t.Errorf("status %q, want the latest progress", st)
	}

	close(release)
	h.send(cmd)
	if st := h.statusLine(); st != "✓ pull redis:7: done" {
		t.Errorf("status after the task %q", st)
	}
	// The closed channel ends the pending receive without a message.
	if msg := next(); msg != nil {
		t.Errorf("receive after close returned %#v", msg)
	}
	// A late update for a finished task changes nothing.
	h.m.Update(trackProgressMsg{id: id, text: "stale"})
	if strings.Contains(h.statusLine(), "stale") {
		t.Error("progress of a finished task shown")
	}
}

func TestTrackWithoutProgressListensToNothing(t *testing.T) {
	if listenProgress(1, nil) != nil {
		t.Error("listenProgress(nil) returned a command")
	}
}

func TestActionResultDetail(t *testing.T) {
	h := newHarness(t)
	h.update(store.ActionResultMsg{Label: "prune dangling images", Detail: "reclaimed 450MB"})
	if st := h.statusLine(); st != "✓ prune dangling images: reclaimed 450MB" {
		t.Errorf("status %q", st)
	}
}

// --- resource tabs: switching keys ---

// rootName returns the name of the resource the stack's root view is.
func (h *harness) rootName() string {
	h.t.Helper()
	return h.m.resources[h.m.rootIdx].Name
}

// onlyView fails the test unless the stack holds exactly one view, the
// newest view of resource name, and returns it.
func (h *harness) onlyView(name string) *listView {
	h.t.Helper()
	vs := h.views[name]
	if len(vs) == 0 {
		h.t.Fatalf("no %s view was created", name)
	}
	v := vs[len(vs)-1]
	if len(h.m.stack) != 1 || h.top() != View(v) {
		h.t.Fatalf("stack has %d views with %T on top, want only the newest %s view", len(h.m.stack), h.top(), name)
	}
	if got := h.rootName(); got != name {
		h.t.Errorf("active tab %q, want %q", got, name)
	}
	return v
}

func TestNewWithAliasSetsActiveTab(t *testing.T) {
	h := newHarnessAt(t, "im")
	h.onlyView("images")
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > images") {
		t.Errorf("header %q", hd)
	}
}

func TestDigitSwitchesResource(t *testing.T) {
	h := newHarness(t)
	h.press("2")
	h.onlyView("images")
	if hd := h.header(); !strings.HasPrefix(hd, "drydock > images") {
		t.Errorf("header %q, want drydock > images", hd)
	}
	if h.containers().closed != 1 {
		t.Errorf("containers view closed %d times, want 1", h.containers().closed)
	}
	h.press("3")
	h.onlyView("volumes")
	h.press("1")
	h.onlyView("containers")
}

func TestTabCyclesResources(t *testing.T) {
	h := newHarnessAt(t, "volumes")
	h.press("tab")
	h.onlyView("containers")
	h.press("tab")
	h.onlyView("images")

	h = newHarness(t)
	h.press("shift+tab")
	h.onlyView("volumes")
	h.press("shift+tab")
	h.onlyView("images")
}

func TestDigitFromDrillDownReplacesStack(t *testing.T) {
	h := newHarness(t)
	h.press("j", "l") // logs of web
	d, ok := h.top().(*detailView)
	if !ok {
		t.Fatalf("top %T, want the detail view", h.top())
	}
	h.press("3")
	h.onlyView("volumes")
	if d.closed != 1 || h.containers().closed != 1 {
		t.Errorf("closed: detail %d, containers %d; want 1 each", d.closed, h.containers().closed)
	}

	// The active resource's key from a drill-down gives a fresh root.
	h = newHarness(t)
	h.press("j", "l")
	h.press("1")
	if got := len(h.views["containers"]); got != 2 {
		t.Fatalf("%d containers views created, want 2", got)
	}
	fresh := h.onlyView("containers")
	if fresh.selected() != "c1" {
		t.Errorf("fresh root selected %q, want the first row", fresh.selected())
	}
}

func TestActiveTabKeyAtRootKeepsView(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("w")
	h.press("enter", "j") // web, worker -> worker
	c := h.containers()
	h.press("1")
	if len(h.views["containers"]) != 1 || c.closed != 0 {
		t.Fatal("1 replaced the active root view")
	}
	if c.table.Filter() != "w" || c.selected() != "c3" {
		t.Errorf("after 1: filter %q selected %q, want w and c3", c.table.Filter(), c.selected())
	}
	// Tab wrapping onto the active resource (a single resource) is a no-op too.
	only := New([]Resource{{Name: "containers", New: func(store.Snapshot) View { return newListView("containers") }}}, "containers")
	v := only.top()
	only.Update(uitest.KeyPress("tab"))
	only.Update(uitest.KeyPress("shift+tab"))
	if only.top() != v {
		t.Error("tab with a single resource replaced the root view")
	}
}

func TestCommandOnActiveRootStillReplaces(t *testing.T) {
	h := newHarness(t)
	h.press("/")
	h.typeText("w")
	h.press("enter")
	h.press(":")
	h.typeText("containers")
	h.press("enter")
	if got := len(h.views["containers"]); got != 2 {
		t.Fatalf("%d containers views created, want 2", got)
	}
	if v := h.onlyView("containers"); v.table.Filter() != "" {
		t.Errorf("new root has filter %q", v.table.Filter())
	}
}

func TestCommandSetsActiveTab(t *testing.T) {
	h := newHarness(t)
	h.press(":")
	h.typeText("vol")
	h.press("enter")
	h.onlyView("volumes")
	h.press("1")
	h.onlyView("containers")
}

func TestSwitchKeysTypedIntoFilter(t *testing.T) {
	h := newHarnessAt(t, "images")
	img := h.onlyView("images")
	h.press("/")
	h.press("2", "tab", "1")
	if f := img.table.Filter(); f != "21" {
		t.Errorf("filter %q, want 21", f)
	}
	h.onlyView("images")
}

func TestSwitchKeysIgnoredInOverlays(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		h := newHarness(t)
		h.press("?", "2", "tab", "shift+tab", "3")
		if h.m.overlay != overlayHelp {
			t.Error("help closed")
		}
		h.onlyView("containers")
	})
	t.Run("prompt", func(t *testing.T) {
		h := newHarness(t)
		var got string
		h.send(Prompt("Name", "", func(s string) tea.Cmd { got = s; return nil }))
		h.press("2", "3", "enter")
		if got != "23" {
			t.Errorf("prompt submitted %q, want 23", got)
		}
		h.onlyView("containers")
	})
	t.Run("confirm", func(t *testing.T) {
		h := newHarness(t)
		h.press("d", "2") // any key but y cancels; it must not switch
		h.onlyView("containers")
	})
	t.Run("command bar", func(t *testing.T) {
		h := newHarness(t)
		h.press(":", "2", "esc")
		h.onlyView("containers")
	})
}

// --- resource tabs: rendering ---

var _ Positioner = (*listView)(nil)

// tabsLine returns the styled tab strip line (the second screen line).
func (h *harness) tabsLine() string {
	h.t.Helper()
	lines := strings.Split(h.m.render(), "\n")
	if len(lines) < 2 {
		h.t.Fatalf("screen has %d lines", len(lines))
	}
	return lines[1]
}

// loaded delivers a loaded snapshot, so the tabs show counts.
func (h *harness) loaded() {
	h.t.Helper()
	h.update(store.SnapshotMsg{Snapshot: loadSnapshot(h.t)})
}

func TestTabsShowCounts(t *testing.T) {
	st := components.Styles
	h := newHarness(t)
	before := ansi.Strip(h.tabsLine())
	if !strings.HasPrefix(before, " 1 containers  2 images  3 volumes ") {
		t.Errorf("tabs before the first load %q, want no counts", before)
	}
	h.loaded()
	line := h.tabsLine()
	for _, want := range []string{
		st.TabActive.Render("1 containers 12"),
		st.Tab.Render("2 images 34"),
		st.Tab.Render("3 volumes 5"),
	} {
		if !strings.Contains(line, want) {
			t.Errorf("tabs %q lack %q", line, want)
		}
	}
	h.press("2")
	line = h.tabsLine()
	if !strings.Contains(line, st.Tab.Render("1 containers 12")) || !strings.Contains(line, st.TabActive.Render("2 images 34")) {
		t.Errorf("after 2 the images tab is not the highlighted one: %q", line)
	}
}

func TestTabsCountIgnoresViewRows(t *testing.T) {
	// The count is the resource's engine total, whatever the view lists:
	// here the containers view lists 3 rows of the 12 the engine reports.
	h := newHarness(t)
	h.loaded()
	plain := ansi.Strip(h.tabsLine())
	if !strings.Contains(plain, "1 containers 12") || !strings.HasSuffix(plain, " 1/3 ") {
		t.Errorf("tabs %q, want containers 12 and position 1/3", plain)
	}
}

func TestTabsHighlightFollowsRootOfDrillDown(t *testing.T) {
	st := components.Styles
	h := newHarnessAt(t, "images")
	h.loaded()
	h.send(Push(newListView("containers", row("c2", "web", "nginx:1.27", "Up"))))
	if len(h.m.stack) != 2 {
		t.Fatalf("stack has %d views, want 2", len(h.m.stack))
	}
	line := h.tabsLine()
	if !strings.Contains(line, st.TabActive.Render("2 images 34")) || !strings.Contains(line, st.Tab.Render("1 containers 12")) {
		t.Errorf("tabs %q, want images highlighted after the drill-down", line)
	}
	// The detail view on top reports no position.
	h.press("l")
	if plain := strings.TrimRight(ansi.Strip(h.tabsLine()), " "); strings.Contains(plain, "/") {
		t.Errorf("tabs %q show a position for a view without one", plain)
	}
}

func TestTabsNarrowUseAliases(t *testing.T) {
	st := components.Styles
	h := newHarness(t)
	h.loaded()
	// Full labels plus the position need 1+41+1+3+1 = 47 cells.
	h.update(tea.WindowSizeMsg{Width: 40, Height: 10})
	line := h.tabsLine()
	for _, want := range []string{st.TabActive.Render("1 co 12"), st.Tab.Render("2 im 34"), st.Tab.Render("3 vol 5")} {
		if !strings.Contains(line, want) {
			t.Errorf("narrow tabs %q lack %q", ansi.Strip(line), ansi.Strip(want))
		}
	}
	if w := ansi.StringWidth(line); w != 40 {
		t.Errorf("tabs line is %d cells, want 40", w)
	}

	// A resource without an alias keeps its name.
	m := New([]Resource{
		{Name: "containers", Aliases: []string{"co"}, New: func(store.Snapshot) View { return newListView("containers") }},
		{Name: "networks", New: func(store.Snapshot) View { return newListView("networks") }},
	}, "containers")
	m.Update(tea.WindowSizeMsg{Width: 22, Height: 10})
	if got := ansi.Strip(strings.Split(m.render(), "\n")[1]); !strings.HasPrefix(got, " 1 co  2 networks") {
		t.Errorf("tabs %q, want the alias and the bare name", got)
	}
}

func TestTabsPositionWinsWhenNarrow(t *testing.T) {
	st := components.Styles
	h := newHarness(t)
	h.loaded()
	h.press("/")
	h.typeText("w")
	h.press("enter") // 1/2 of 3
	for _, w := range []int{30, 16, 12, 11} {
		h.update(tea.WindowSizeMsg{Width: w, Height: 10})
		line := h.tabsLine()
		if !strings.HasSuffix(line, st.Position.Render("1/2 of 3")+" ") {
			t.Errorf("width %d: tabs %q do not end with the full position", w, ansi.Strip(line))
		}
		if got := ansi.StringWidth(line); got != w {
			t.Errorf("width %d: tabs line is %d cells", w, got)
		}
	}
}

func TestPositionText(t *testing.T) {
	for _, tc := range []struct {
		sel, listed, total int
		filtered           bool
		want               string
	}{
		{3, 12, 12, false, "3/12"},
		{1, 5, 12, true, "1/5 of 12"},
		{0, 0, 12, true, "0/0 of 12"},
		{1, 12, 12, true, "1/12 of 12"},
		{0, 0, 0, false, "0/0"},
	} {
		if got := formatPosition(tc.sel, tc.listed, tc.total, tc.filtered); got != tc.want {
			t.Errorf("formatPosition(%d, %d, %d, %v) = %q, want %q", tc.sel, tc.listed, tc.total, tc.filtered, got, tc.want)
		}
	}

	h := newHarness(t)
	pos := func() string {
		f := strings.Fields(ansi.Strip(h.tabsLine()))
		if i := slices.Index(f, "of"); i > 0 {
			return strings.Join(f[i-1:], " ")
		}
		return f[len(f)-1]
	}
	if got := pos(); got != "1/3" {
		t.Errorf("position %q, want 1/3", got)
	}
	h.press("j")
	if got := pos(); got != "2/3" {
		t.Errorf("after j position %q, want 2/3", got)
	}
	h.press("/")
	h.typeText("zzz")
	if got := pos(); got != "0/0 of 3" {
		t.Errorf("filter matching nothing: position %q, want 0/0 of 3", got)
	}
	h.press("esc")
	h.press("3") // the volumes view lists nothing
	if got := pos(); got != "0/0" {
		t.Errorf("empty volumes: position %q, want 0/0", got)
	}
}

func TestShortTerminalHasNoTabs(t *testing.T) {
	h := newHarness(t)
	h.loaded()
	h.update(tea.WindowSizeMsg{Width: 80, Height: 4})
	lines := strings.Split(h.screen(), "\n")
	if len(lines) != 4 {
		t.Fatalf("screen has %d lines, want 4:\n%s", len(lines), h.screen())
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[0]), "drydock > containers") {
		t.Errorf("first line %q, want the header", lines[0])
	}
	if strings.Contains(h.screen(), "1 containers") {
		t.Errorf("tab strip shown at height 4:\n%s", h.screen())
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[1]), "NAME") {
		t.Errorf("content line %q, want the table header", lines[1])
	}
	if !strings.Contains(lines[3], "l logs") {
		t.Errorf("footer %q, want the key hints", lines[3])
	}
	if c := h.containers(); c.table.Len() != 3 {
		t.Fatalf("setup: %d rows", c.table.Len())
	}
	if _, ch := h.m.contentSize(); ch != 1 {
		t.Errorf("content height %d at height 4, want 1", ch)
	}
	// An error still reaches the status line.
	h.send(Error("boom"))
	if got := strings.TrimSpace(strings.Split(h.screen(), "\n")[2]); got != "✗ boom" {
		t.Errorf("status line %q, want the error", got)
	}

	h.update(tea.WindowSizeMsg{Width: 80, Height: 5})
	lines = strings.Split(h.screen(), "\n")
	if len(lines) != 5 || !strings.Contains(lines[1], "1 containers 12") {
		t.Errorf("at height 5 the tab strip is missing:\n%s", h.screen())
	}
	if _, ch := h.m.contentSize(); ch != 1 {
		t.Errorf("content height %d at height 5, want 1", ch)
	}
}

// --- footer and help overlay ---

func TestFooterFitsWholeHints(t *testing.T) {
	h := newHarness(t)
	footer := func() string {
		lines := strings.Split(h.screen(), "\n")
		return strings.TrimRight(lines[len(lines)-1], " ")
	}
	const all = " l logs  d delete  / filter  : command  ? help  q quit"
	if got := footer(); got != all {
		t.Errorf("wide footer %q, want %q", got, all)
	}
	for _, tc := range []struct {
		width int
		want  string
	}{
		{len(all), all},
		{len(all) - 1, " l logs  d delete  / filter  ? more"},
		{len(" l logs  d delete  / filter  ? more"), " l logs  d delete  / filter  ? more"},
		{len(" l logs  d delete  ? more"), " l logs  d delete  ? more"},
		{len(" l logs  d delete  / filter  ? more") - 1, " l logs  d delete  ? more"},
		{len(" ? more"), " ? more"},
	} {
		h.update(tea.WindowSizeMsg{Width: tc.width, Height: 10})
		if got := footer(); got != tc.want {
			t.Errorf("width %d: footer %q, want %q", tc.width, got, tc.want)
		}
	}
}

func TestFitHintsSkipsDisabled(t *testing.T) {
	off := key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "shell"))
	off.SetEnabled(false)
	got := ansi.Strip(fitHints(hints([]key.Binding{keyLogs, off, keyDelete}), 200))
	if want := "l logs  d delete  : command  ? help  q quit"; got != want {
		t.Errorf("fitHints = %q, want %q", got, want)
	}
}

// shellView is a list view whose `x shell` key applies only to rows whose
// status starts with "Up".
type shellView struct{ *listView }

var keyShell = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "shell"))

func (v shellView) KeyHelp() []key.Binding {
	shell := keyShell
	if r, ok := v.table.Selected(); !ok || !strings.HasPrefix(r.Cells[2], "Up") {
		shell.SetEnabled(false)
	}
	return []key.Binding{keyLogs, shell}
}

func TestHelpDimsDisabledKeys(t *testing.T) {
	st := components.Styles
	m := New([]Resource{{Name: "containers", New: func(store.Snapshot) View {
		return shellView{newListView("containers",
			row("c1", "web", "nginx", "Up 5 minutes"),
			row("c3", "worker", "alpine", "Exited (0)"))}
	}}}, "containers")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(uitest.KeyPress("j")) // worker: shell does not apply
	m.Update(uitest.KeyPress("?"))
	help := m.helpView()
	dimmed := st.FooterDesc.Render(components.Fit("x", helpKeyWidth)) + " " + st.FooterDesc.Render("shell")
	if !strings.Contains(help, dimmed) {
		t.Errorf("help does not list x shell dimmed:\n%q", help)
	}
	if enabled := st.HelpKey.Render(components.Fit("l", helpKeyWidth)) + " logs"; !strings.Contains(help, enabled) {
		t.Errorf("help does not list l logs as enabled:\n%q", help)
	}
	if strings.Contains(m.footerView(), "shell") {
		t.Error("footer shows the disabled x shell")
	}
	m.Update(uitest.KeyPress("esc"))
	m.Update(uitest.KeyPress("k")) // web: shell applies
	if !strings.Contains(m.helpView(), st.HelpKey.Render(components.Fit("x", helpKeyWidth))+" shell") {
		t.Error("help does not list x shell as enabled on a running row")
	}
}

func TestHelpListsSwitchKeysOnce(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	s := h.screen()
	if !strings.Contains(s, "1-3/tab    switch resource") {
		t.Errorf("help lacks the full switch entry:\n%s", s)
	}
	for _, k := range []string{"shift+tab", "tab "} {
		if strings.Contains(s, k) && !strings.Contains(s, "1-3/"+k) {
			t.Errorf("help lists %q on its own:\n%s", k, s)
		}
	}
	if !strings.Contains(s, "? or esc to close") {
		t.Errorf("help lacks the close hint:\n%s", s)
	}
}

// TestPromptScrollsAtTerminalWidth checks that the root sizes an open
// prompt's input for the terminal, when it opens and on resize, so long
// typed text scrolls inside the dialog instead of wrapping or being cut,
// with the cursor kept in view.
func TestPromptScrollsAtTerminalWidth(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "%02d,", i) // every 3-character run occurs once
	}
	typed := b.String()
	h := newHarness(t)
	h.send(Prompt("Volume name", "name", nil))
	h.typeText(typed)
	for range 43 {
		h.press("left")
	}
	pos := len(typed) - 43
	for _, w := range []int{80, 30} {
		h.update(tea.WindowSizeMsg{Width: w, Height: 24})
		d := strings.Split(ansi.Strip(h.m.overlayView()), "\n")
		if len(d) != 7 {
			t.Fatalf("width %d: prompt has %d lines, want 7 (typed text wrapped?):\n%s", w, len(d), strings.Join(d, "\n"))
		}
		for i, l := range d {
			if lw := ansi.StringWidth(l); lw > w {
				t.Errorf("width %d: prompt line %d is %d cells wide", w, i, lw)
			}
		}
		in := strings.Trim(d[3], "│ ")
		shown, ok := strings.CutPrefix(in, "> ")
		start := strings.Index(typed, shown)
		if !ok || shown == "" || start < 0 {
			t.Fatalf("width %d: input line %q is not a part of the typed text", w, in)
		}
		if pos < start || pos > start+len(shown) {
			t.Errorf("width %d: cursor %d outside the shown text [%d, %d)", w, pos, start, start+len(shown))
		}
	}
}
