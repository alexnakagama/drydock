package views

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/moby/moby/api/pkg/stdcopy"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// logsConfig holds the logs view's limits and timings. Tests shrink them.
type logsConfig struct {
	// tail is the number of past lines requested when the view opens.
	tail int
	// maxLines and maxBytes cap the lines kept (by count and by total
	// size); the oldest lines are dropped first.
	maxLines int
	maxBytes int
	// batchLines and batchInterval bound a batch of lines sent to the UI:
	// a batch is flushed when it reaches batchLines, or batchInterval after
	// its first line, whichever comes first.
	batchLines    int
	batchInterval time.Duration
	// maxLineLen splits longer lines (in bytes), so a stream without
	// newlines cannot grow a line without bound.
	maxLineLen int
	// inspectTimeout bounds the call that finds whether the container has
	// a TTY.
	inspectTimeout time.Duration
}

func defaultLogsConfig() logsConfig {
	return logsConfig{
		tail:           1000,
		maxLines:       10_000,
		maxBytes:       32 << 20,
		batchLines:     200,
		batchInterval:  50 * time.Millisecond,
		maxLineLen:     16 << 10,
		inspectTimeout: 10 * time.Second,
	}
}

// logLinesMsg delivers a batch of log lines to the logs view that owns the
// stream. The last batch of a stream has end set.
type logLinesMsg struct {
	view  uint64
	lines []string // display-safe
	end   bool
	// err is why the stream ended, if it failed; reason is its display
	// text.
	err    error
	reason string
}

// logsView follows a container's logs.
//
// The view owns the stream: it is opened when the view is pushed and
// closed (its context cancelled) when the view is closed. A goroutine reads
// the stream, splits it into lines and hands them, in batches, to a command
// the view keeps waiting on, so a chatty container cannot flood the update
// loop. At most maxLines lines, and maxBytes bytes of text, are kept.
type logsView struct {
	id          uint64
	st          *store.Store
	containerID string
	name        string
	cfg         logsConfig

	ctx     context.Context
	cancel  context.CancelFunc
	batches chan logLinesMsg
	// done is closed once every goroutine of the stream has exited.
	done chan struct{}

	lines   lineRing
	bytes   int // total size of the kept lines
	follow  bool
	ended   bool
	errText string

	// search is the applied search ("" for none); matches holds the
	// sequence numbers of the kept lines that contain it, in order.
	search    string
	needle    string // lower-cased search
	matches   []uint64
	searching bool // search input focused
	input     textinput.Model

	pager         components.Pager
	width, height int
	contentWidth  int
}

func newLogsView(st *store.Store, containerID, name string, cfg logsConfig) *logsView {
	// Unset limits take their defaults, so a partial config is safe.
	def := defaultLogsConfig()
	cfg.tail = cmp.Or(cfg.tail, def.tail)
	cfg.maxLines = cmp.Or(cfg.maxLines, def.maxLines)
	cfg.maxBytes = cmp.Or(cfg.maxBytes, def.maxBytes)
	cfg.batchLines = cmp.Or(cfg.batchLines, def.batchLines)
	cfg.batchInterval = cmp.Or(cfg.batchInterval, def.batchInterval)
	cfg.maxLineLen = cmp.Or(cfg.maxLineLen, def.maxLineLen)
	cfg.inspectTimeout = cmp.Or(cfg.inspectTimeout, def.inspectTimeout)
	ctx, cancel := context.WithCancel(context.Background())
	return &logsView{
		id:          nextViewID(),
		st:          st,
		containerID: containerID,
		name:        name,
		cfg:         cfg,
		ctx:         ctx,
		cancel:      cancel,
		batches:     make(chan logLinesMsg),
		done:        make(chan struct{}),
		lines:       newLineRing(cfg.maxLines),
		follow:      true,
		input:       components.NewInput("/", "search"),
	}
}

// Init starts the stream and the command that waits for its first batch.
func (v *logsView) Init() tea.Cmd {
	go v.stream()
	return v.wait()
}

// wait returns a command that delivers the next batch, or nothing once the
// view is closed.
func (v *logsView) wait() tea.Cmd {
	ctx, ch := v.ctx, v.batches
	return func() tea.Msg {
		select {
		case b := <-ch:
			return b
		case <-ctx.Done():
			return nil
		}
	}
}

// stream reads the log stream until it ends or the view is closed, and
// delivers its lines in batches. It closes v.done when it and the reader it
// starts have exited.
func (v *logsView) stream() {
	// The stream's state is read once: reopen replaces it, but only after
	// this stream has delivered its last batch.
	ctx, batches, done := v.ctx, v.batches, v.done
	defer close(done)
	lines := make(chan string, v.cfg.batchLines)
	var readErr error
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer close(lines)
		readErr = v.read(ctx, lines)
	}()
	// The reader exits promptly once ctx is cancelled: its stream is
	// closed and its sends give up.
	defer func() { <-readerDone }()

	var (
		batch []string
		timer = time.NewTimer(time.Hour)
		flush <-chan time.Time // non-nil while a batch is pending
	)
	timer.Stop()
	defer timer.Stop()
	send := func(m logLinesMsg) bool {
		m.view = v.id
		select {
		case batches <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				// readerDone is closed after lines, but readErr is written
				// before lines is closed, so reading it here is safe.
				msg := logLinesMsg{lines: batch, end: true}
				if readErr != nil && ctx.Err() == nil {
					msg.err, msg.reason = readErr, v.st.ErrorText(readErr)
				}
				send(msg)
				return
			}
			batch = append(batch, l)
			if len(batch) == 1 {
				timer.Reset(v.cfg.batchInterval)
				flush = timer.C
			}
			if len(batch) >= v.cfg.batchLines {
				timer.Stop()
				flush = nil
				if !send(logLinesMsg{lines: batch}) {
					return
				}
				batch = nil
			}
		case <-flush:
			flush = nil
			if !send(logLinesMsg{lines: batch}) {
				return
			}
			batch = nil
		case <-ctx.Done():
			return
		}
	}
}

// read opens the log stream and writes its lines to out until the stream
// ends or ctx is cancelled. It returns nil when the stream ended normally
// (for example because the container stopped) or the view was closed.
func (v *logsView) read(ctx context.Context, out chan<- string) error {
	err := v.st.Do(ctx, func(ctx context.Context, svc docker.Service) error {
		ictx, cancel := context.WithTimeout(ctx, v.cfg.inspectTimeout)
		info, err := svc.ContainerInspect(ictx, v.containerID)
		cancel()
		if err != nil {
			return err
		}
		tty := info.Container.Config != nil && info.Container.Config.Tty

		rc, err := svc.ContainerLogs(ctx, v.containerID, docker.LogsOptions{
			Stdout: true, Stderr: true, Follow: true, Tail: v.cfg.tail,
		})
		if err != nil {
			return err
		}
		// Close errors are ignored: the stream is being abandoned either way.
		defer func() { _ = rc.Close() }()
		// Unblock a pending Read as soon as the view is closed.
		stop := context.AfterFunc(ctx, func() { _ = rc.Close() })
		defer stop()

		w := &lineWriter{max: v.cfg.maxLineLen, emit: func(line string) bool {
			select {
			case out <- line:
				return true
			case <-ctx.Done():
				return false
			}
		}}
		if tty {
			_, err = io.Copy(w, rc)
		} else {
			// stdout and stderr are interleaved in the order the engine
			// sends their frames.
			_, err = stdcopy.StdCopy(w, w, rc)
		}
		w.Flush()
		return err
	})
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// errStopped is returned by lineWriter.Write once its consumer is gone.
var errStopped = errors.New("log reader stopped")

// lineWriter splits what is written to it into lines and passes each,
// made safe for display, to emit. Lines longer than max bytes are split.
type lineWriter struct {
	max     int
	emit    func(string) bool // false stops the writer
	partial []byte
	stopped bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.stopped {
		return 0, errStopped
	}
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.partial = append(w.partial, p...)
			if !w.splitLong() {
				return 0, errStopped
			}
			break
		}
		w.partial = append(w.partial, p[:i]...)
		p = p[i+1:]
		if !w.splitLong() {
			return 0, errStopped
		}
		if !w.send(w.partial) {
			return 0, errStopped
		}
		w.partial = w.partial[:0]
	}
	if cap(w.partial) > 4*w.max {
		w.partial = append([]byte(nil), w.partial...)
	}
	return n, nil
}

// escLookback is how far before a cut splitLong looks for the start of an
// escape sequence; colour (SGR) sequences are much shorter.
const escLookback = 64

// splitLong emits leading chunks of the pending line while it is longer
// than max bytes, cutting at a UTF-8 character boundary so that no
// character is split.
func (w *lineWriter) splitLong() bool {
	for len(w.partial) > w.max {
		n := w.max
		for n > 0 && !utf8.RuneStart(w.partial[n]) {
			n--
		}
		if n == 0 {
			n = w.max // not UTF-8 text; cut anywhere
		}
		// Cut before an escape sequence that starts just before the cut,
		// so a colour code is not split in two (its tail would show as
		// text once the line is cleaned).
		from := max(n-escLookback, 0)
		if e := bytes.LastIndexByte(w.partial[from:n], '\x1b'); e >= 0 && from+e > 0 {
			n = from + e
		}
		if !w.send(w.partial[:n]) {
			return false
		}
		w.partial = w.partial[n:]
	}
	return true
}

// Flush emits a final line that has no newline.
func (w *lineWriter) Flush() {
	if len(w.partial) > 0 && !w.stopped {
		w.send(w.partial)
	}
	w.partial = nil
}

func (w *lineWriter) send(b []byte) bool {
	if !w.emit(cleanLogLine(b)) {
		w.stopped = true
		return false
	}
	return true
}

// cleanLogLine makes a raw log line safe to display: a trailing CR is
// dropped, ANSI sequences (colours) are removed, and any other control
// character is replaced.
func cleanLogLine(b []byte) string {
	s := string(bytes.TrimSuffix(b, []byte("\r")))
	if strings.ContainsRune(s, '\x1b') || strings.ContainsRune(s, '\u009b') {
		s = ansi.Strip(s)
	}
	return components.Clean(s)
}

// lineRing keeps the most recent lines, up to a fixed capacity. Lines are
// numbered by a sequence number that increases with every line added.
type lineRing struct {
	buf   []logLine
	start int    // index in buf of the oldest line
	n     int    // number of lines kept
	next  uint64 // sequence number of the next line added
}

type logLine struct {
	text string
	// lower is text lower-cased for case-insensitive search, or "" when
	// that is text itself (the common case, which then costs nothing).
	lower string
	width int // display width of text, in cells
}

func newLogLine(text string) logLine {
	l := logLine{text: text, width: ansi.StringWidth(text)}
	if lower := strings.ToLower(text); lower != text {
		l.lower = lower
	}
	return l
}

// searchText returns the lower-cased text.
func (l logLine) searchText() string {
	if l.lower != "" {
		return l.lower
	}
	return l.text
}

// size approximates the memory a line holds.
func (l logLine) size() int { return len(l.text) + len(l.lower) }

func newLineRing(capacity int) lineRing {
	return lineRing{buf: make([]logLine, max(capacity, 1))}
}

// Len returns the number of lines kept.
func (r *lineRing) Len() int { return r.n }

// Full reports whether a Push would need room made first.
func (r *lineRing) Full() bool { return r.n == len(r.buf) }

// First returns the sequence number of the oldest kept line.
func (r *lineRing) First() uint64 { return r.next - uint64(r.n) }

// At returns the i-th oldest kept line.
func (r *lineRing) At(i int) logLine { return r.buf[(r.start+i)%len(r.buf)] }

// BySeq returns the kept line with the given sequence number.
func (r *lineRing) BySeq(seq uint64) logLine { return r.At(int(seq - r.First())) }

// PopOldest removes and returns the oldest line. The ring must not be
// empty.
func (r *lineRing) PopOldest() logLine {
	l := r.buf[r.start]
	r.buf[r.start] = logLine{}
	r.start = (r.start + 1) % len(r.buf)
	r.n--
	return l
}

// Push adds a line, dropping the oldest if the ring is full, and returns
// the new line's sequence number.
func (r *lineRing) Push(l logLine) uint64 {
	if r.Full() {
		r.PopOldest()
	}
	r.buf[(r.start+r.n)%len(r.buf)] = l
	r.n++
	seq := r.next
	r.next++
	return seq
}

func (v *logsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case logLinesMsg:
		if msg.view != v.id {
			return nil
		}
		v.append(msg.lines)
		if !msg.end {
			return v.wait()
		}
		v.ended = true
		if msg.err != nil {
			v.errText = msg.reason
			return ui.Error("logs " + v.name + ": " + msg.reason)
		}
		return nil
	case tea.KeyPressMsg:
		if v.searching {
			return v.updateSearch(msg)
		}
		return v.handleKey(msg)
	case tea.PasteMsg:
		if v.searching {
			return v.updateSearch(msg)
		}
	case tea.BackgroundColorMsg:
		// The palette changed; the search input copied the old one.
		components.StyleInput(&v.input)
	default:
		if v.searching {
			return v.updateSearch(msg)
		}
	}
	return nil
}

func (v *logsView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Logs.Search):
		v.searching = true
		v.input.SetValue(v.search)
		v.input.CursorEnd()
		v.layout()
		return v.input.Focus()
	case key.Matches(msg, keys.Logs.Follow):
		v.follow = true
		v.pager.GotoBottom()
		return nil
	case v.ended && key.Matches(msg, keys.Logs.Reopen):
		return v.reopen()
	}
	if v.pager.Update(msg) {
		// Scrolling away from the newest line pauses auto-follow; coming
		// back to it (with G, or by scrolling down) resumes it.
		v.follow = v.pager.AtBottom()
	}
	return nil
}

// updateSearch handles a message while the search input is focused: Enter
// keeps the search and closes the input, Esc clears the search, and
// anything else edits it (the shown lines follow as it is typed).
func (v *logsView) updateSearch(msg tea.Msg) tea.Cmd {
	if kmsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(kmsg, keys.Input.Submit):
			v.stopSearching(false)
			return nil
		case key.Matches(kmsg, keys.Input.Cancel):
			v.stopSearching(true)
			return nil
		}
	}
	var cmd tea.Cmd
	v.input, cmd = components.UpdateInput(v.input, msg)
	v.setSearch(v.input.Value())
	return cmd
}

func (v *logsView) stopSearching(clear bool) {
	v.searching = false
	v.input.Blur()
	if clear {
		v.input.Reset()
		v.setSearch("")
	} else {
		v.setSearch(strings.TrimSpace(v.search))
	}
	v.layout()
}

// setSearch applies a search and recomputes the matching lines.
func (v *logsView) setSearch(s string) {
	if s == v.search {
		return
	}
	v.search = s
	v.needle = strings.ToLower(strings.TrimSpace(s))
	v.matches = v.matches[:0:0]
	if v.needle != "" {
		first := v.lines.First()
		for i := range v.lines.Len() {
			if strings.Contains(v.lines.At(i).searchText(), v.needle) {
				v.matches = append(v.matches, first+uint64(i))
			}
		}
	}
	v.pager.SetTotal(v.shownLen())
	if v.follow {
		v.pager.GotoBottom()
	}
}

// searchActive reports whether lines are being narrowed by a search.
func (v *logsView) searchActive() bool { return v.needle != "" }

// shownLen returns the number of lines shown: all kept lines, or those
// matching the search.
func (v *logsView) shownLen() int {
	if v.searchActive() {
		return len(v.matches)
	}
	return v.lines.Len()
}

// shown returns the i-th shown line.
func (v *logsView) shown(i int) string {
	if v.searchActive() {
		return v.lines.BySeq(v.matches[i]).text
	}
	return v.lines.At(i).text
}

// append adds lines, dropping the oldest beyond the caps. While following,
// the view stays on the newest line; otherwise it stays on the lines the
// user is looking at.
func (v *logsView) append(lines []string) {
	droppedShown := 0
	widthGone := false
	dropOldest := func() {
		oldest := v.lines.First()
		switch {
		case !v.searchActive():
			droppedShown++
		case len(v.matches) > 0 && v.matches[0] == oldest:
			v.matches = v.matches[1:]
			droppedShown++
		}
		l := v.lines.PopOldest()
		v.bytes -= l.size()
		widthGone = widthGone || l.width >= v.contentWidth
	}
	for _, text := range lines {
		l := newLogLine(text)
		for v.lines.Len() > 0 && (v.lines.Full() || v.bytes+l.size() > v.cfg.maxBytes) {
			dropOldest()
		}
		seq := v.lines.Push(l)
		v.bytes += l.size()
		if v.searchActive() && strings.Contains(l.searchText(), v.needle) {
			v.matches = append(v.matches, seq)
		}
		v.contentWidth = max(v.contentWidth, l.width)
	}
	if widthGone {
		// The widest line may have been dropped.
		v.contentWidth = 0
		for i := range v.lines.Len() {
			v.contentWidth = max(v.contentWidth, v.lines.At(i).width)
		}
	}
	v.pager.SetContentWidth(v.contentWidth)
	if v.follow {
		v.pager.SetTotal(v.shownLen())
		v.pager.GotoBottom()
		return
	}
	// Shift before updating the total, so the clamp does not move the
	// window first.
	v.pager.ScrollBy(-droppedShown)
	v.pager.SetTotal(v.shownLen())
}

func (v *logsView) View() string {
	if v.width <= 0 || v.height <= 0 {
		return ""
	}
	var body string
	if v.shownLen() == 0 {
		var msg string
		switch {
		case v.searchActive():
			msg = "No lines match " + components.Quote(v.search) + "."
		case v.errText != "":
			msg = "Could not read the logs of " + v.name + ": " + v.errText
		case v.ended:
			msg = "No log output."
		default:
			msg = "Waiting for log output…"
		}
		h := v.pager.Height()
		msg = components.Styles.Empty.Render(components.Truncate(components.Clean(msg), v.width))
		body = lipgloss.Place(v.width, h, lipgloss.Center, lipgloss.Center, msg)
		if h == 0 {
			body = ""
		}
	} else {
		body = v.pager.View(v.shown)
	}
	if !v.searching {
		return body
	}
	input := components.Fit(v.input.View(), v.width)
	if body == "" {
		return input
	}
	return body + "\n" + input
}

// layout sizes the pager: the whole view, minus the search input line
// while it is open.
func (v *logsView) layout() {
	h := v.height
	if v.searching {
		h--
	}
	v.pager.SetSize(v.width, max(h, 0))
	v.input.SetWidth(max(v.width-2, 1))
	if v.follow {
		v.pager.GotoBottom()
	}
}

func (v *logsView) SetSize(w, h int) {
	v.width, v.height = max(w, 0), max(h, 0)
	v.layout()
}

func (v *logsView) Crumbs() []string { return []string{v.name, "logs"} }

func (v *logsView) Indicators() []string {
	var ind []string
	switch {
	case v.ended:
		ind = append(ind, "ended")
	case v.follow:
		ind = append(ind, "following")
	default:
		ind = append(ind, "paused")
	}
	if v.search != "" {
		ind = append(ind, "/"+v.search)
	}
	return ind
}

func (v *logsView) KeyHelp() []key.Binding {
	if v.ended {
		return []key.Binding{keys.Logs.Reopen, keys.Logs.Follow, keys.Logs.Search, keys.Pager.Up, keys.Pager.Down,
			keys.Pager.PageUp, keys.Pager.PageDown, keys.Pager.Top, keys.Pager.Left, keys.Pager.Right}
	}
	return []key.Binding{keys.Logs.Follow, keys.Logs.Search, keys.Pager.Up, keys.Pager.Down,
		keys.Pager.PageUp, keys.Pager.PageDown, keys.Pager.Top, keys.Pager.Left, keys.Pager.Right}
}

func (v *logsView) InputFocused() bool { return v.searching }

// Back clears an applied search; with none, the view is popped.
func (v *logsView) Back() bool {
	if v.search == "" {
		return false
	}
	v.input.Reset()
	v.setSearch("")
	return true
}

// reopen starts a new stream after the previous one ended, for example
// because the container was restarted. The kept lines are replaced by the
// new stream's backlog, which includes the previous output, so nothing is
// shown twice; the search is kept.
func (v *logsView) reopen() tea.Cmd {
	v.cancel() // the ended stream's context
	v.ctx, v.cancel = context.WithCancel(context.Background())
	v.batches = make(chan logLinesMsg)
	v.done = make(chan struct{})
	v.lines = newLineRing(v.cfg.maxLines)
	v.bytes, v.contentWidth = 0, 0
	v.matches = v.matches[:0:0]
	v.ended, v.errText = false, ""
	v.follow = true
	v.pager.SetContentWidth(0)
	v.pager.SetTotal(0)
	v.pager.GotoBottom()
	return v.Init()
}

// Close stops the stream. Its goroutines exit promptly afterwards.
func (v *logsView) Close() { v.cancel() }
