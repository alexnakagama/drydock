package ui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/store"
)

// View is a full-screen view on the root model's view stack: a resource
// list, or a detail view (logs, inspect, related containers) pushed on top
// of one.
//
// Views are mutable and used through a pointer. The root model calls every
// method on the UI goroutine.
type View interface {
	// Init returns the view's initial command. It is called once, when the
	// view is pushed (or becomes the root view), after SetSize.
	Init() tea.Cmd

	// Update handles a message. Key presses (and pastes) reach only the
	// view on top of the stack, after the root has taken the keys it owns;
	// every other message (snapshots, connection changes, action results,
	// the view's own asynchronous results) reaches every view on the stack,
	// so a view must ignore messages that are not meant for it.
	Update(msg tea.Msg) tea.Cmd

	// View renders the view into exactly the size given by SetSize.
	View() string

	// SetSize sets the size of the content area the view renders into.
	SetSize(width, height int)

	// Crumbs returns the view's own breadcrumb segments. The header shows
	// "drydock" followed by the crumbs of every view on the stack, so a
	// logs view pushed on the containers view returns {"web", "logs"} and
	// the header reads "drydock > containers > web > logs". Segments are
	// cleaned before rendering.
	Crumbs() []string

	// Indicators returns short state flags shown in the header next to the
	// breadcrumb, for example "all" or the active filter.
	Indicators() []string

	// KeyHelp returns the view's own key bindings, for the footer and the
	// help overlay. Global keys are added by the root.
	KeyHelp() []key.Binding

	// InputFocused reports whether a text input of the view (a filter or a
	// search) has the keyboard. While it does, every key except Ctrl+C goes
	// to the view, so `q`, `:` and `?` are typed as text.
	InputFocused() bool

	// Back handles Esc when no text input is focused. It returns true if
	// the view consumed it (for example by clearing an applied filter);
	// otherwise the root pops the view.
	Back() bool

	// Close releases the view's resources (streams, goroutines). It is
	// called exactly once, when the view leaves the stack or the program
	// quits; no other method is called afterwards.
	Close()
}

// Resource is a root view the command bar can switch to.
type Resource struct {
	// Name is the command, for example "containers".
	Name string
	// Aliases are the short forms, for example "co".
	Aliases []string
	// New creates the view, seeded with the latest snapshot (which is the
	// zero Snapshot until the store's first load arrives).
	New func(snap store.Snapshot) View
}

// Messages views use to drive the root model. They are produced by the
// exported command constructors below, so views never touch the root model
// directly.
//
// The root tags these messages with the view whose command produced them
// (origin), so that a request that arrives after its view left the top of
// the stack is dropped instead of acting on another view. Commands nested in
// tea.Sequence are not tagged and are always accepted; views should use
// tea.Batch.
type (
	pushMsg struct {
		view   View
		origin uint64
	}
	confirmMsg struct {
		title  string
		body   []string
		onYes  tea.Cmd
		origin uint64
	}
	promptMsg struct {
		title, placeholder string
		onSubmit           func(string) tea.Cmd
		origin             uint64
	}
	statusMsg struct {
		kind statusKind
		text string
	}
	foregroundMsg struct {
		cmd     tea.Cmd
		dropped string
		origin  uint64
	}
	trackStartMsg struct {
		text     string
		cmd      tea.Cmd
		progress <-chan string // optional; see TrackProgress
	}
	trackProgressMsg struct {
		id       uint64
		text     string
		progress <-chan string
	}
	trackDoneMsg struct {
		id  uint64
		msg tea.Msg
	}
)

// tagCmd wraps cmd so that the requests it produces carry origin (see
// above). A zero origin leaves cmd unchanged.
func tagCmd(origin uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil || origin == 0 {
		return cmd
	}
	return func() tea.Msg { return tagMsg(origin, cmd()) }
}

func tagMsg(origin uint64, msg tea.Msg) tea.Msg {
	switch msg := msg.(type) {
	case tea.BatchMsg:
		out := make(tea.BatchMsg, len(msg))
		for i, c := range msg {
			out[i] = tagCmd(origin, c)
		}
		return out
	case pushMsg:
		if msg.origin == 0 {
			msg.origin = origin
		}
		return msg
	case confirmMsg:
		if msg.origin == 0 {
			msg.origin = origin
		}
		return msg
	case promptMsg:
		if msg.origin == 0 {
			msg.origin = origin
		}
		return msg
	case foregroundMsg:
		if msg.origin == 0 {
			msg.origin = origin
		}
		return msg
	case trackStartMsg:
		msg.cmd = tagCmd(origin, msg.cmd)
		return msg
	}
	return msg
}

// Push returns a command that pushes v onto the view stack. If the view
// that issued it is no longer the top view when it arrives, v is closed
// instead.
func Push(v View) tea.Cmd {
	return func() tea.Msg { return pushMsg{view: v} }
}

// Confirm returns a command that opens a confirmation dialog for a
// destructive action. title names the action and body lists what it
// affects, one entry per line. onYes runs only if the user presses `y`;
// any other key cancels. The request is dropped if another overlay is open
// or the view that issued it is no longer the top view.
func Confirm(title string, body []string, onYes tea.Cmd) tea.Cmd {
	return func() tea.Msg { return confirmMsg{title: title, body: body, onYes: onYes} }
}

// Prompt returns a command that opens a single-line text prompt. On Enter,
// onSubmit is called with the trimmed text (possibly empty) and its command
// runs; Esc cancels without calling it. The request is dropped if another
// overlay is open or the view that issued it is no longer the top view.
func Prompt(title, placeholder string, onSubmit func(string) tea.Cmd) tea.Cmd {
	return func() tea.Msg { return promptMsg{title: title, placeholder: placeholder, onSubmit: onSubmit} }
}

// Foreground returns a command that runs cmd only if, when the request
// arrives, the view that issued it is still the top view and no overlay is
// open. It is meant for commands that take over the screen, such as
// tea.ExecProcess, and that were decided asynchronously: if the user has
// moved on meanwhile (opened another view or a dialog), cmd is not run and
// the status line shows dropped as an error instead.
func Foreground(cmd tea.Cmd, dropped string) tea.Cmd {
	return func() tea.Msg { return foregroundMsg{cmd: cmd, dropped: dropped} }
}

// Track returns a command that runs cmd while the status line shows an
// in-progress indicator labelled text. The indicator is removed when cmd
// returns, and cmd's message is then delivered as usual. cmd should be a
// single blocking command: if it returns a batch, the indicator is removed
// as soon as the batch is returned, before the batched commands run.
func Track(text string, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg { return trackStartMsg{text: text, cmd: cmd} }
}

// TrackProgress is like Track, but the indicator's text is replaced by each
// value received from progress, so a long action (a pull) can report how
// far it has got. The producer must close progress no later than when cmd
// returns; until then the root keeps one receive pending on it. Values are
// display text and are cleaned before rendering. The producer should never
// block on a send: use a buffered channel and drop or replace values the
// UI has not consumed yet (see SendLatest).
func TrackProgress(text string, progress <-chan string, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg { return trackStartMsg{text: text, cmd: cmd, progress: progress} }
}

// SendLatest sends v on ch, a channel with a buffer of one that only ch's
// producer sends on, without blocking: if the buffer holds a value the
// consumer has not received yet, that value is replaced by v.
func SendLatest[T any](ch chan T, v T) {
	select {
	case ch <- v:
		return
	default:
	}
	select {
	case <-ch: // drop the stale value
	default:
	}
	select {
	case ch <- v:
	default: // only reachable with several producers; drop v
	}
}

// Success returns a command that shows a transient success message.
func Success(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{kind: statusSuccess, text: text} }
}

// Error returns a command that shows an error message.
func Error(text string) tea.Cmd {
	return func() tea.Msg { return statusMsg{kind: statusError, text: text} }
}
