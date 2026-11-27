package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// How long status messages stay visible once shown.
const (
	successTTL = 4 * time.Second
	errorTTL   = 15 * time.Second
)

// spinnerFrames animate the in-progress indicator, one frame per
// spinnerInterval.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 100 * time.Millisecond

// overlay is the modal element that has the keyboard, if any.
type overlay int

const (
	overlayNone overlay = iota
	overlayCommand
	overlayHelp
	overlayConfirm
	overlayPrompt
)

type statusKind int

const (
	statusNone statusKind = iota
	statusSuccess
	statusError
)

// status is the message shown in the status line.
type status struct {
	kind statusKind
	text string
	seq  uint64 // identifies the message, so only its own expiry clears it
}

type (
	statusExpireMsg struct{ seq uint64 }
	spinnerTickMsg  struct{}
)

// task is an in-progress action started with Track.
type task struct {
	id   uint64
	text string
}

// entry is a view on the stack. id identifies it for as long as it is on
// the stack, so that requests a view made asynchronously (push, dialogs)
// can be dropped once the view is gone.
type entry struct {
	id   uint64
	view View
}

// Model is the root Bubble Tea model: it owns the view stack, renders the
// header, footer, status line and overlays, and routes keys.
type Model struct {
	resources []Resource
	// rootIdx is the index in resources of the stack's root view. Pushed
	// views leave it alone, so the tab strip highlights the resource the
	// stack started from.
	rootIdx  int
	stack    []entry
	nextView uint64
	snap     store.Snapshot
	conn     store.ConnectionMsg

	// contextLabel is the header's context indicator text, "ctx:<name>"
	// with the name cleaned and truncated, or "" for the default context.
	contextLabel string

	width, height int

	overlay  overlay
	command  textinput.Model
	confirm  components.Confirm
	onYes    tea.Cmd
	prompt   components.Prompt
	onSubmit func(string) tea.Cmd
	dialogOf uint64 // id of the view that opened the confirm or prompt

	status        status
	statusPending time.Duration // expiry to arm once the status is visible; 0 if armed
	nextSeq       uint64

	tasks       []task
	nextTask    uint64
	frame       int
	spinnerLive bool // a spinner tick is scheduled

	// after schedules fn to run after d; tea.Tick, replaced in tests.
	after func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd

	quitting bool
}

// Option configures a Model built by New.
type Option func(*Model)

// maxContextWidth is the most terminal cells the context name takes in the
// header indicator; longer names are truncated with an ellipsis.
const maxContextWidth = 20

// WithContext makes the header show "ctx:<name>" before the connection
// state, so the engine that actions apply to is always visible. The name
// is cleaned of control characters and truncated to maxContextWidth cells.
// "" and "default" (the environment's engine) show no indicator.
func WithContext(name string) Option {
	return func(m *Model) {
		if name == "" || name == dockerctx.DefaultName {
			m.contextLabel = ""
			return
		}
		m.contextLabel = "ctx:" + components.Truncate(components.Clean(name), maxContextWidth)
	}
}

// New returns the root model with the view stack holding the resource view
// named initial, configured by opts. It panics if initial is not one of
// resources, which is a programming error.
func New(resources []Resource, initial string, opts ...Option) *Model {
	m := &Model{
		resources: resources,
		conn:      store.ConnectionMsg{State: store.StateConnecting},
		command:   components.NewInput(":", strings.Join(resourceNames(resources), " | ")),
		after:     tea.Tick,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	i, ok := m.lookupIdx(initial)
	if !ok {
		panic(fmt.Sprintf("ui: unknown initial resource %q", initial))
	}
	m.rootIdx = i
	m.stack = []entry{m.newEntry(m.resources[i].New(m.snap))}
	return m
}

// SetTimerFunc replaces the function the model uses to schedule delayed
// messages (status expiry, spinner frames), which is tea.Tick by default.
// It exists for tests that drive the model synchronously and must not wait
// on real timers. A nil fn restores tea.Tick.
func (m *Model) SetTimerFunc(fn func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd) {
	if fn == nil {
		fn = tea.Tick
	}
	m.after = fn
}

func resourceNames(rs []Resource) []string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name
	}
	return names
}

// lookupIdx returns the index of the resource whose name or alias is cmd.
func (m *Model) lookupIdx(cmd string) (int, bool) {
	for i, r := range m.resources {
		if r.Name == cmd || slices.Contains(r.Aliases, cmd) {
			return i, true
		}
	}
	return 0, false
}

func (m *Model) newEntry(v View) entry {
	m.nextView++
	return entry{id: m.nextView, view: v}
}

// Init implements tea.Model. Besides starting the top view, it asks the
// terminal for its background color, so the palette can follow it (see
// tea.BackgroundColorMsg in Update). Until the terminal answers, or if it
// never does, the dark palette is used.
func (m *Model) Init() tea.Cmd {
	t := m.stack[len(m.stack)-1]
	initCmd := tagCmd(t.id, t.view.Init())
	return tea.Batch(tea.RequestBackgroundColor, initCmd, setActive(t, true))
}

// setActive tells e's view whether it is on screen, if it implements
// Activatable, and returns its command tagged with e's id.
func setActive(e entry, active bool) tea.Cmd {
	a, ok := e.view.(Activatable)
	if !ok {
		return nil
	}
	return tagCmd(e.id, a.SetActive(active))
}

func (m *Model) top() View { return m.stack[len(m.stack)-1].view }

func (m *Model) topID() uint64 { return m.stack[len(m.stack)-1].id }

// Close closes every view on the stack. It is safe to call more than once.
func (m *Model) Close() {
	for i := len(m.stack) - 1; i >= 0; i-- {
		m.stack[i].view.Close()
	}
	m.stack = nil
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.quitting {
		if p, ok := msg.(pushMsg); ok && p.view != nil {
			p.view.Close() // never reaches the stack
		}
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tea.PasteMsg:
		return m, m.handleInput(msg)
	case tea.WindowSizeMsg:
		m.width, m.height = max(msg.Width, 0), max(msg.Height, 0)
		m.command.SetWidth(max(m.width-2, 1))
		if m.overlay == overlayPrompt {
			m.prompt.SetMaxWidth(m.width)
		}
		for _, e := range m.stack {
			e.view.SetSize(m.contentSize())
		}
		return m, nil

	case tea.BackgroundColorMsg:
		// Styles is read at render time, so swapping it restyles everything
		// on the next frame, except text inputs, which copy their styles:
		// the root's own are restyled here, and views restyle theirs when
		// the message reaches them.
		components.Styles = components.NewStyles(msg.IsDark())
		components.StyleInput(&m.command)
		if m.overlay == overlayPrompt {
			m.prompt.Restyle()
		}
		return m, m.broadcast(msg)

	case store.SnapshotMsg:
		if msg.Snapshot.Seq() <= m.snap.Seq() {
			return m, nil // stale or repeated
		}
		m.snap = msg.Snapshot
		return m, m.broadcast(msg)
	case store.ConnectionMsg:
		m.conn = msg
		return m, m.broadcast(msg)
	case store.ActionResultMsg:
		var cmd tea.Cmd
		if msg.Err != nil {
			cmd = m.setStatus(statusError, msg.Label+": "+msg.Reason)
		} else {
			detail := msg.Detail
			if detail == "" {
				detail = "done"
			}
			cmd = m.setStatus(statusSuccess, msg.Label+": "+detail)
		}
		return m, tea.Batch(cmd, m.broadcast(msg))

	case pushMsg:
		if msg.view == nil {
			return m, nil
		}
		if !m.fromTop(msg.origin) {
			msg.view.Close() // its view is gone or not in front
			return m, nil
		}
		return m, m.push(msg.view)
	case confirmMsg:
		if !m.canOpenDialog(msg.origin) {
			return m, nil
		}
		m.overlay, m.dialogOf = overlayConfirm, msg.origin
		m.confirm = components.NewConfirm(msg.title, msg.body...)
		m.onYes = msg.onYes
		return m, nil
	case promptMsg:
		if !m.canOpenDialog(msg.origin) {
			return m, nil
		}
		m.overlay, m.dialogOf = overlayPrompt, msg.origin
		var cmd tea.Cmd
		m.prompt, cmd = components.NewPrompt(msg.title, msg.placeholder)
		m.prompt.SetMaxWidth(m.width)
		m.onSubmit = msg.onSubmit
		return m, cmd
	case foregroundMsg:
		if !m.canOpenDialog(msg.origin) {
			return m, m.setStatus(statusError, msg.dropped)
		}
		return m, tagCmd(msg.origin, msg.cmd)
	case statusMsg:
		return m, m.setStatus(msg.kind, msg.text)
	case statusExpireMsg:
		if msg.seq == m.status.seq {
			m.status = status{}
		}
		return m, nil
	case trackStartMsg:
		return m, m.startTask(msg)
	case trackDoneMsg:
		m.tasks = slices.DeleteFunc(m.tasks, func(t task) bool { return t.id == msg.id })
		// A status that was hidden by the indicator is visible now, so its
		// expiry starts.
		cmd := m.armStatus()
		if msg.msg == nil {
			return m, cmd
		}
		inner := msg.msg
		return m, tea.Batch(cmd, func() tea.Msg { return inner })
	case trackProgressMsg:
		if i := slices.IndexFunc(m.tasks, func(t task) bool { return t.id == msg.id }); i >= 0 {
			m.tasks[i].text = msg.text
		}
		// Keep draining until the producer closes the channel, even once
		// the task is done, so a pending receive never outlives it.
		return m, listenProgress(msg.id, msg.progress)
	case spinnerTickMsg:
		m.spinnerLive = false
		if len(m.tasks) == 0 {
			return m, nil
		}
		m.frame = (m.frame + 1) % len(spinnerFrames)
		return m, m.tickSpinner()
	}

	// Anything else (views' own asynchronous results) goes to the focused
	// input and to every view.
	var cmds []tea.Cmd
	switch m.overlay {
	case overlayCommand:
		var cmd tea.Cmd
		m.command, cmd = components.UpdateInput(m.command, msg)
		cmds = append(cmds, cmd)
	case overlayPrompt:
		var cmd tea.Cmd
		m.prompt, _, cmd = m.prompt.Update(msg)
		cmds = append(cmds, cmd)
	}
	cmds = append(cmds, m.broadcast(msg))
	return m, tea.Batch(cmds...)
}

// fromTop reports whether a request made by the view with the given id may
// act on the stack: the view must be the top view. Requests with no origin
// (made outside any view, for example by the root itself) are accepted.
func (m *Model) fromTop(origin uint64) bool {
	return origin == 0 || origin == m.topID()
}

// canOpenDialog reports whether a dialog requested by origin may open: it
// must come from the top view, and no other overlay may be open, so a
// dialog never replaces one the user is looking at or typing in.
func (m *Model) canOpenDialog(origin uint64) bool {
	return m.overlay == overlayNone && m.fromTop(origin)
}

// broadcast sends a non-key message to every view on the stack.
func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.stack))
	for _, e := range m.stack {
		cmds = append(cmds, tagCmd(e.id, e.view.Update(msg)))
	}
	return tea.Batch(cmds...)
}

// updateTop sends msg to the top view.
func (m *Model) updateTop(msg tea.Msg) tea.Cmd {
	return tagCmd(m.topID(), m.top().Update(msg))
}

// handleKey routes a key press: Ctrl+C first, then the open overlay, then
// the top view's focused input, then the global keys (including the
// resource-switching keys), then the top view.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	g := keys.Global
	if key.Matches(msg, g.ForceQuit) {
		return m.quit()
	}
	if m.overlay != overlayNone {
		return m.handleOverlayKey(msg)
	}
	if m.top().InputFocused() {
		return m.updateTop(msg)
	}
	switch {
	case key.Matches(msg, g.Command):
		m.overlay = overlayCommand
		m.command.Reset()
		return m.command.Focus()
	case key.Matches(msg, g.Help):
		m.overlay = overlayHelp
		return nil
	case key.Matches(msg, g.Quit):
		return m.quit()
	case key.Matches(msg, g.Back):
		if !m.top().Back() {
			return m.pop()
		}
		return nil
	case key.Matches(msg, g.NextTab):
		return m.switchTab(m.rootIdx + 1)
	case key.Matches(msg, g.PrevTab):
		return m.switchTab(m.rootIdx - 1)
	}
	for i, b := range g.TabKeys() {
		if key.Matches(msg, b) {
			if i >= len(m.resources) {
				return nil // no such resource; the key is not the view's either
			}
			return m.switchTab(i)
		}
	}
	return m.updateTop(msg)
}

// switchTab switches to resource i, wrapped into range, unless it is the
// active resource and its root view is the only view on the stack: then
// the key does nothing, so a stray press keeps the view's filter, selection
// and mode.
func (m *Model) switchTab(i int) tea.Cmd {
	n := len(m.resources)
	if n == 0 {
		return nil
	}
	i = ((i % n) + n) % n
	if i == m.rootIdx && len(m.stack) == 1 {
		return nil
	}
	return m.switchTo(i)
}

// switchTo replaces the stack with a new root view of resource i.
func (m *Model) switchTo(i int) tea.Cmd {
	m.rootIdx = i
	return m.replace(m.resources[i].New(m.snap))
}

// handleInput routes text that is not a key press (a paste) to whatever
// text input has the keyboard. It is dropped if none has.
func (m *Model) handleInput(msg tea.Msg) tea.Cmd {
	switch m.overlay {
	case overlayCommand:
		var cmd tea.Cmd
		m.command, cmd = components.UpdateInput(m.command, msg)
		return cmd
	case overlayPrompt:
		var cmd tea.Cmd
		m.prompt, _, cmd = m.prompt.Update(msg)
		return cmd
	case overlayNone:
		if m.top().InputFocused() {
			return m.updateTop(msg)
		}
	}
	return nil
}

func (m *Model) handleOverlayKey(msg tea.KeyPressMsg) tea.Cmd {
	switch m.overlay {
	case overlayHelp:
		if key.Matches(msg, keys.Global.Help, keys.Global.Back) {
			m.overlay = overlayNone
		}
		return nil

	case overlayConfirm:
		outcome := m.confirm.Update(msg)
		onYes, origin := m.onYes, m.dialogOf
		m.overlay, m.confirm, m.onYes, m.dialogOf = overlayNone, components.Confirm{}, nil, 0
		if outcome == components.Accepted {
			return tagCmd(origin, onYes)
		}
		return nil

	case overlayPrompt:
		var (
			outcome components.Outcome
			cmd     tea.Cmd
		)
		m.prompt, outcome, cmd = m.prompt.Update(msg)
		if outcome == components.Pending {
			return cmd
		}
		value, onSubmit, origin := m.prompt.Value(), m.onSubmit, m.dialogOf
		m.overlay, m.prompt, m.onSubmit, m.dialogOf = overlayNone, components.Prompt{}, nil, 0
		if outcome == components.Accepted && onSubmit != nil {
			return tagCmd(origin, onSubmit(value))
		}
		return nil

	case overlayCommand:
		switch {
		case key.Matches(msg, keys.Input.Cancel):
			m.closeCommand()
			return nil
		case key.Matches(msg, keys.Input.Submit):
			input := m.command.Value()
			m.closeCommand()
			return m.runCommand(input)
		}
		var cmd tea.Cmd
		m.command, cmd = components.UpdateInput(m.command, msg)
		return cmd
	}
	return nil
}

func (m *Model) closeCommand() {
	m.overlay = overlayNone
	m.command.Blur()
	m.command.Reset()
}

// runCommand executes a command-bar entry. An empty entry does nothing.
func (m *Model) runCommand(input string) tea.Cmd {
	name := strings.ToLower(strings.TrimSpace(input))
	if name == "" {
		return nil
	}
	i, ok := m.lookupIdx(name)
	if !ok {
		return m.setStatus(statusError, "unknown command: "+components.Truncate(components.Clean(strings.TrimSpace(input)), 40))
	}
	return m.switchTo(i)
}

// push adds v on top of the stack. The view it covers is deactivated first,
// and v is activated after its Init.
func (m *Model) push(v View) tea.Cmd {
	deactivate := setActive(m.stack[len(m.stack)-1], false)
	v.SetSize(m.contentSize())
	e := m.newEntry(v)
	m.stack = append(m.stack, e)
	initCmd := tagCmd(e.id, v.Init())
	return tea.Batch(deactivate, initCmd, setActive(e, true))
}

// pop removes the top view, but never the root view, and activates the view
// it reveals.
func (m *Model) pop() tea.Cmd {
	if len(m.stack) <= 1 {
		return nil
	}
	top := m.top()
	m.stack[len(m.stack)-1] = entry{}
	m.stack = m.stack[:len(m.stack)-1]
	top.Close()
	return setActive(m.stack[len(m.stack)-1], true)
}

// replace closes every view and makes v the new root view, activated after
// its Init. The old views are only closed, never deactivated.
func (m *Model) replace(v View) tea.Cmd {
	m.Close()
	v.SetSize(m.contentSize())
	e := m.newEntry(v)
	m.stack = []entry{e}
	initCmd := tagCmd(e.id, v.Init())
	return tea.Batch(initCmd, setActive(e, true))
}

func (m *Model) quit() tea.Cmd {
	m.quitting = true
	m.overlay = overlayNone
	m.Close()
	return tea.Quit
}

// setStatus shows a status message. Its expiry starts when it becomes
// visible: right away, or once the in-progress indicator, which takes
// precedence in the status line, goes away.
func (m *Model) setStatus(kind statusKind, text string) tea.Cmd {
	m.nextSeq++
	m.status = status{kind: kind, text: text, seq: m.nextSeq}
	m.statusPending = successTTL
	if kind == statusError {
		m.statusPending = errorTTL
	}
	return m.armStatus()
}

// armStatus schedules the current status's expiry if it is visible and not
// yet scheduled.
func (m *Model) armStatus() tea.Cmd {
	if m.status.kind == statusNone || m.statusPending == 0 || len(m.tasks) > 0 {
		return nil
	}
	ttl, seq := m.statusPending, m.status.seq
	m.statusPending = 0
	return m.after(ttl, func(time.Time) tea.Msg { return statusExpireMsg{seq: seq} })
}

func (m *Model) startTask(msg trackStartMsg) tea.Cmd {
	m.nextTask++
	id := m.nextTask
	m.tasks = append(m.tasks, task{id: id, text: msg.text})
	inner := msg.cmd
	run := func() tea.Msg {
		var res tea.Msg
		if inner != nil {
			res = inner()
		}
		return trackDoneMsg{id: id, msg: res}
	}
	return tea.Batch(run, m.tickSpinner(), listenProgress(id, msg.progress))
}

// listenProgress returns a command that waits for the next progress update
// of task id, or nil if the task reports none. It returns no message once
// the producer closes the channel.
func listenProgress(id uint64, progress <-chan string) tea.Cmd {
	if progress == nil {
		return nil
	}
	return func() tea.Msg {
		text, ok := <-progress
		if !ok {
			return nil
		}
		return trackProgressMsg{id: id, text: text, progress: progress}
	}
}

// tickSpinner schedules the next spinner frame unless one is scheduled.
func (m *Model) tickSpinner() tea.Cmd {
	if m.spinnerLive {
		return nil
	}
	m.spinnerLive = true
	return m.after(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}
