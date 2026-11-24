package views

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

func TestLineRing(t *testing.T) {
	t.Parallel()
	r := newLineRing(10)
	for i := range 25 {
		if seq := r.Push(newLogLine(fmt.Sprint(i))); seq != uint64(i) {
			t.Fatalf("Push %d returned seq %d", i, seq)
		}
	}
	if r.Len() != 10 || r.First() != 15 {
		t.Fatalf("Len = %d, First = %d; want 10, 15", r.Len(), r.First())
	}
	for i := range 10 {
		if got, want := r.At(i).text, fmt.Sprint(15+i); got != want {
			t.Errorf("At(%d) = %q, want %q", i, got, want)
		}
	}
	if got := r.BySeq(20).text; got != "20" {
		t.Errorf("BySeq(20) = %q", got)
	}
}

func TestLineWriter(t *testing.T) {
	t.Parallel()
	var got []string
	w := &lineWriter{max: 8, emit: func(s string) bool { got = append(got, s); return true }}
	for _, chunk := range []string{"hel", "lo\nwor", "ld\r\n", "0123456789abcdefXY\n", "\x1b[31mr\n", "ééééé\n", "12345678", "9\n", "tail"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	// "é" is two bytes: a chunk never splits it.
	want := []string{"hello", "world", "01234567", "89abcdef", "XY", "r", "éééé", "é", "12345678", "9", "tail"}
	if !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}

	// A consumer that goes away stops the writer.
	w = &lineWriter{max: 8, emit: func(string) bool { return false }}
	if _, err := w.Write([]byte("a\nb\n")); !errors.Is(err, errStopped) {
		t.Errorf("Write after stop = %v", err)
	}
}

func TestCleanLogLine(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"plain":                         "plain",
		"\x1b[1;31mERROR\x1b[0m failed": "ERROR failed",
		"title\x1b]0;evil\x07 after":    "title after",
		"bell\x07 and \x00nul":          "bell\ufffd and \ufffdnul",
		"tab\tsep":                      "tab sep",
		"bidi \u202eevil":               "bidi \ufffdevil",
		"crlf\r":                        "crlf",
	}
	for in, want := range tests {
		if got := cleanLogLine([]byte(in)); got != want {
			t.Errorf("cleanLogLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// logEnv returns an env with a running container web (no TTY) whose log
// holds n lines "line 0" ... "line n-1".
func logEnv(t *testing.T, n int, cfg func(*logsConfig)) (*env, *dockerfake.Service, string) {
	t.Helper()
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
	for i := range n {
		f.WriteLog(id, i%2 == 1, fmt.Sprintf("line %d\n", i))
	}
	e := newEnv(t, f, 80, 12)
	if cfg != nil {
		cfg(&e.logsCfg)
	}
	return e, f, id
}

func (e *env) openLogs() *logsView {
	e.t.Helper()
	e.selectRow("web")
	e.press("l")
	e.until("logs view", func() bool { return len(e.logs) > 0 && strings.Contains(e.header(), "web > logs") })
	return e.logs[len(e.logs)-1]
}

func (e *env) lastContentLine() string {
	c := e.content()
	for i := len(c) - 1; i >= 0; i-- {
		if strings.TrimSpace(c[i]) != "" {
			return strings.TrimSpace(c[i])
		}
	}
	return ""
}

func TestLogsShowBacklogAndFollow(t *testing.T) {
	e, f, id := logEnv(t, 30, nil)
	lv := e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 29" })
	if h := e.header(); !strings.HasPrefix(h, "drydock > containers > web > logs [following]") {
		t.Errorf("header = %q", h)
	}
	// stdout and stderr both shown, demultiplexed (no frame headers).
	if got := e.content()[len(e.content())-2]; strings.TrimSpace(got) != "line 28" {
		t.Errorf("second-to-last line = %q", got)
	}

	f.WriteLog(id, false, "fresh output\n")
	e.until("new line at the bottom", func() bool { return e.lastContentLine() == "fresh output" })

	call := f.CallsTo(dockerfake.MethodContainerLogs)[0]
	if opts := call.Args[1].(docker.LogsOptions); !opts.Follow || opts.Tail != 1000 || !opts.Stdout || !opts.Stderr {
		t.Errorf("logs options = %+v", opts)
	}
	_ = lv
}

func TestLogsTTYContainer(t *testing.T) {
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
	f.SetTTY(id, true)
	f.WriteLog(id, false, "raw tty line\r\n")
	e := newEnv(t, f, 80, 12)
	e.openLogs()
	e.until("tty line", func() bool { return e.lastContentLine() == "raw tty line" })
}

func TestLogsInitialBacklogIsBounded(t *testing.T) {
	e, _, _ := logEnv(t, 1500, nil)
	lv := e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 1499" })
	if lv.lines.Len() != 1000 || lv.lines.At(0).text != "line 500" {
		t.Errorf("kept %d lines starting at %q, want 1000 from line 500", lv.lines.Len(), lv.lines.At(0).text)
	}
}

func TestLogsRingBufferCap(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxLines = 100 })
	lv := e.openLogs()
	for i := range 250 {
		f.WriteLog(id, false, fmt.Sprintf("line %d\n", i))
	}
	e.until("all lines", func() bool { return e.lastContentLine() == "line 249" })
	if lv.lines.Len() != 100 || lv.lines.At(0).text != "line 150" {
		t.Errorf("kept %d lines starting at %q, want 100 from line 150", lv.lines.Len(), lv.lines.At(0).text)
	}
}

func TestLogsBatching(t *testing.T) {
	t.Run("by size", func(t *testing.T) {
		e, _, id := logEnv(t, 450, nil)
		lv := newLogsView(e.st, id, "web", logsConfig{
			tail: 1000, maxLines: 10_000, batchLines: 200, batchInterval: time.Hour,
			maxLineLen: 1024, inspectTimeout: time.Second,
		})
		defer func() { lv.Close(); <-lv.done }()
		go lv.stream()
		for i, want := range []int{200, 200} {
			select {
			case b := <-lv.batches:
				if len(b.lines) != want || b.end {
					t.Fatalf("batch %d has %d lines (end %v), want %d", i, len(b.lines), b.end, want)
				}
			case <-time.After(waitTimeout):
				t.Fatalf("no batch %d", i)
			}
		}
		// The remaining 50 lines wait for the (hour-long) interval.
		select {
		case b := <-lv.batches:
			t.Errorf("unexpected early batch of %d lines", len(b.lines))
		case <-time.After(100 * time.Millisecond):
		}
	})
	t.Run("by time", func(t *testing.T) {
		e, f, id := logEnv(t, 0, nil)
		lv := newLogsView(e.st, id, "web", logsConfig{
			tail: 1000, maxLines: 10_000, batchLines: 200, batchInterval: 50 * time.Millisecond,
			maxLineLen: 1024, inspectTimeout: time.Second,
		})
		defer func() { lv.Close(); <-lv.done }()
		go lv.stream()
		start := time.Now()
		f.WriteLog(id, false, "a\n")
		f.WriteLog(id, false, "b\n")
		f.WriteLog(id, true, "c\n")
		var got []string
		deadline := time.After(waitTimeout)
		for len(got) < 3 {
			select {
			case b := <-lv.batches:
				got = append(got, b.lines...)
			case <-deadline:
				t.Fatalf("got only %q", got)
			}
		}
		if !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("lines = %q", got)
		}
		if el := time.Since(start); el < 40*time.Millisecond {
			t.Errorf("batch flushed after %v, before the interval", el)
		}
	})
	t.Run("end flushes", func(t *testing.T) {
		f := dockerfake.New()
		id := f.AddContainer(ctr(cid(0), "old", "alpine", container.StateExited, "Exited"))
		f.WriteLog(id, false, "last words\n")
		e := newEnv(t, f, 80, 12)
		lv := newLogsView(e.st, id, "old", logsConfig{
			tail: 1000, maxLines: 10, batchLines: 200, batchInterval: time.Hour,
			maxLineLen: 1024, inspectTimeout: time.Second,
		})
		defer func() { lv.Close(); <-lv.done }()
		go lv.stream()
		select {
		case b := <-lv.batches:
			if !b.end || !slices.Equal(b.lines, []string{"last words"}) || b.err != nil {
				t.Errorf("final batch = %+v", b)
			}
		case <-time.After(waitTimeout):
			t.Fatal("no final batch")
		}
	})
}

func TestLogsScrollPausesFollow(t *testing.T) {
	e, f, id := logEnv(t, 50, nil)
	e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 49" })

	e.press("k", "k")
	if !strings.Contains(e.header(), "[paused]") {
		t.Errorf("header after scrolling up = %q", e.header())
	}
	top := e.content()[0]
	f.WriteLog(id, false, "while paused\n")
	e.until("line received", func() bool { return e.logs[0].lines.Len() == 51 })
	if e.content()[0] != top || e.lastContentLine() == "while paused" {
		t.Errorf("view moved while paused:\n%s", e.screen())
	}

	e.press("G")
	if !strings.Contains(e.header(), "[following]") || e.lastContentLine() != "while paused" {
		t.Errorf("after G:\n%s", e.screen())
	}
	f.WriteLog(id, false, "following again\n")
	e.until("followed", func() bool { return e.lastContentLine() == "following again" })
}

func TestLogsPausedViewStaysPutWhenLinesAreDropped(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxLines = 20 })
	lv := e.openLogs()
	for i := range 20 {
		f.WriteLog(id, false, fmt.Sprintf("line %d\n", i))
	}
	e.until("lines", func() bool { return e.lastContentLine() == "line 19" })
	e.press("k") // window now starts one line earlier
	top := strings.TrimSpace(e.content()[0])
	for i := 20; i < 25; i++ {
		f.WriteLog(id, false, fmt.Sprintf("line %d\n", i))
	}
	e.until("dropped", func() bool { return lv.lines.First() == 5 })
	if got := strings.TrimSpace(e.content()[0]); got != top {
		t.Errorf("top line = %q, want %q to stay in place", got, top)
	}
}

func TestLogsSearchAndEscOrdering(t *testing.T) {
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
	for _, l := range []string{"starting", "ERROR: disk", "ok", "an error here", "done"} {
		f.WriteLog(id, false, l+"\n")
	}
	e := newEnv(t, f, 80, 12)
	lv := e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "done" })

	e.press("/")
	e.typeText("error")
	e.press("enter")
	var shown []string
	for _, l := range e.content() {
		if s := strings.TrimSpace(l); s != "" {
			shown = append(shown, s)
		}
	}
	if want := []string{"ERROR: disk", "an error here"}; !slices.Equal(shown, want) {
		t.Errorf("shown with search = %q, want %q", shown, want)
	}
	if !strings.Contains(e.header(), "[/error]") {
		t.Errorf("header = %q", e.header())
	}
	// New lines are searched too.
	f.WriteLog(id, false, "another Error\n")
	f.WriteLog(id, false, "fine\n")
	e.until("new match", func() bool { return e.lastContentLine() == "another Error" })

	// Esc clears the search first; the view stays open and streaming.
	e.press("esc")
	if !strings.Contains(e.header(), "web > logs") || strings.Contains(e.header(), "/error") {
		t.Errorf("after first esc header = %q", e.header())
	}
	f.WriteLog(id, false, "still streaming\n")
	e.until("streaming", func() bool { return e.lastContentLine() == "still streaming" })

	// Esc again closes the view and stops the stream.
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > containers [") {
		t.Errorf("after second esc header = %q", h)
	}
	select {
	case <-lv.done:
	case <-time.After(waitTimeout):
		t.Fatal("log stream still running after the view was popped")
	}
	if r, _ := e.root().table.Selected(); r.Cells[0] != "web" {
		t.Errorf("selection = %v, want web", r.Cells)
	}
}

func TestLogsSearchTypesGlobalKeys(t *testing.T) {
	e, _, _ := logEnv(t, 3, nil)
	e.openLogs()
	e.press("/")
	e.typeText("q:?")
	if !e.logs[0].searching || e.logs[0].search != "q:?" {
		t.Errorf("search = %q (focused %v)", e.logs[0].search, e.logs[0].searching)
	}
	// Esc in the input clears the search and keeps the view.
	e.press("esc")
	if e.logs[0].search != "" || !strings.Contains(e.header(), "web > logs") {
		t.Errorf("after esc: search %q, header %q", e.logs[0].search, e.header())
	}
}

func TestLogsSearchTypesSwitchKeys(t *testing.T) {
	e, _, _ := logEnv(t, 3, nil)
	e.openLogs()
	e.press("/")
	e.typeText("3")
	e.press("tab")
	e.typeText("1")
	if !e.logs[0].searching || e.logs[0].search != "31" {
		t.Errorf("search = %q (focused %v), want 31", e.logs[0].search, e.logs[0].searching)
	}
	if h := e.header(); !strings.Contains(h, "web > logs") {
		t.Errorf("header = %q, want the logs view still open", h)
	}
}

func TestLogsPopCancelsStream(t *testing.T) {
	e, f, _ := logEnv(t, 5, nil)
	lv := e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 4" })
	e.press("esc")
	select {
	case <-lv.done:
	case <-time.After(waitTimeout):
		t.Fatal("log stream still running after the view was popped")
	}
	if lv.ctx.Err() == nil {
		t.Error("view context not cancelled")
	}
	_ = f
}

func TestLogsStreamEndAndError(t *testing.T) {
	t.Run("container stops", func(t *testing.T) {
		e, f, id := logEnv(t, 2, nil)
		e.openLogs()
		e.until("backlog", func() bool { return e.lastContentLine() == "line 1" })
		if err := f.ContainerStop(t.Context(), id); err != nil {
			t.Fatal(err)
		}
		e.until("ended", func() bool { return strings.Contains(e.header(), "[ended]") })
		if strings.Contains(e.statusLine(), "✗") {
			t.Errorf("status = %q, want no error for a normal end", e.statusLine())
		}
	})
	t.Run("engine error", func(t *testing.T) {
		e, f, _ := logEnv(t, 0, nil)
		f.SetError(dockerfake.MethodContainerLogs, errors.New("logs unavailable"))
		e.openLogs()
		e.waitStatus("logs web: logs unavailable")
		if !strings.Contains(e.screen(), "Could not read the logs of web: logs unavailable") {
			t.Errorf("screen:\n%s", e.screen())
		}
	})
}

func TestLogsGolden(t *testing.T) {
	e, _, _ := logEnv(t, 20, nil)
	e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 19" })
	checkGolden(t, "logs_80x12.golden", e.screen())
}

func TestLogLineStoresLowerCaseOnlyWhenDifferent(t *testing.T) {
	t.Parallel()
	if l := newLogLine("all lower"); l.lower != "" || l.searchText() != "all lower" || l.size() != 9 {
		t.Errorf("lower-case line = %+v", l)
	}
	if l := newLogLine("ERROR x"); l.searchText() != "error x" || l.size() != 14 {
		t.Errorf("mixed-case line = %+v", l)
	}
}

func TestLogsByteCap(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxBytes = 100 })
	lv := e.openLogs()
	for i := range 30 {
		f.WriteLog(id, false, fmt.Sprintf("line %02d\n", i)) // 7 bytes each
	}
	e.until("all lines", func() bool { return e.lastContentLine() == "line 29" })
	if lv.bytes > 100 || lv.lines.Len() != 14 || lv.lines.At(0).text != "line 16" {
		t.Errorf("kept %d lines (%d bytes) from %q, want 14 lines from line 16", lv.lines.Len(), lv.bytes, lv.lines.At(0).text)
	}
}

func TestLogsSearchSurvivesDroppedLines(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxLines = 10 })
	lv := e.openLogs()
	e.press("/")
	e.typeText("hit")
	e.press("enter")
	for i := range 30 {
		text := fmt.Sprintf("miss %d", i)
		if i%3 == 0 {
			text = fmt.Sprintf("hit %d", i)
		}
		f.WriteLog(id, false, text+"\n")
	}
	e.until("all lines", func() bool { return lv.lines.Len() == 10 && lv.lines.At(9).text == "miss 29" })
	var shown []string
	for i := range lv.shownLen() {
		shown = append(shown, lv.shown(i))
	}
	// Kept lines are 20..29; the hits among them are 21, 24 and 27.
	if want := []string{"hit 21", "hit 24", "hit 27"}; !slices.Equal(shown, want) {
		t.Errorf("shown = %q, want %q", shown, want)
	}
}

func TestLogsPausedSearchStaysPutWhenMatchesAreDropped(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxLines = 40 })
	lv := e.openLogs()
	for i := range 40 {
		f.WriteLog(id, false, fmt.Sprintf("hit %d\n", i))
	}
	e.until("lines", func() bool { return e.lastContentLine() == "hit 39" })
	e.press("/")
	e.typeText("hit")
	e.press("enter", "k")
	top := strings.TrimSpace(e.content()[0])
	for i := 40; i < 45; i++ {
		f.WriteLog(id, false, fmt.Sprintf("hit %d\n", i))
	}
	e.until("dropped", func() bool { return lv.lines.First() == 5 })
	if got := strings.TrimSpace(e.content()[0]); got != top {
		t.Errorf("top line = %q, want %q", got, top)
	}
}

func TestLogsContentWidthShrinks(t *testing.T) {
	e, f, id := logEnv(t, 0, func(c *logsConfig) { c.maxLines = 3 })
	lv := e.openLogs()
	f.WriteLog(id, false, strings.Repeat("w", 200)+"\n")
	for i := range 3 {
		f.WriteLog(id, false, fmt.Sprintf("short %d\n", i))
	}
	e.until("lines", func() bool { return e.lastContentLine() == "short 2" })
	if lv.contentWidth != len("short 0") {
		t.Errorf("contentWidth = %d after the wide line was dropped", lv.contentWidth)
	}
}

func TestLogsVeryLongLineIsSplit(t *testing.T) {
	e, f, id := logEnv(t, 0, nil)
	lv := e.openLogs()
	f.WriteLog(id, false, strings.Repeat("x", 40_000)+"\n")
	e.until("split", func() bool { return lv.lines.Len() == 3 })
	for i, want := range []int{16 << 10, 16 << 10, 40_000 - 32<<10} {
		if got := len(lv.lines.At(i).text); got != want {
			t.Errorf("chunk %d is %d bytes, want %d", i, got, want)
		}
	}
}

func TestLogsReopenAfterRestart(t *testing.T) {
	e, f, id := logEnv(t, 2, nil)
	lv := e.openLogs()
	e.until("backlog", func() bool { return e.lastContentLine() == "line 1" })
	e.press("r") // not ended yet: ignored
	if lv.ended || lv.lines.Len() != 2 {
		t.Fatalf("r before the end changed the view: ended %v, %d lines", lv.ended, lv.lines.Len())
	}
	if err := f.ContainerStop(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	e.until("ended", func() bool { return strings.Contains(e.header(), "[ended]") })
	if err := f.ContainerStart(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	e.press("r")
	e.until("following again", func() bool { return strings.Contains(e.header(), "[following]") })
	f.WriteLog(id, false, "after restart\n")
	e.until("new output", func() bool { return e.lastContentLine() == "after restart" })
	var got []string
	for i := range lv.lines.Len() {
		got = append(got, lv.lines.At(i).text)
	}
	if want := []string{"line 0", "line 1", "after restart"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q (no duplicates)", got, want)
	}
}

func TestLineWriterDoesNotSplitEscapes(t *testing.T) {
	var got []string
	w := &lineWriter{max: 100, emit: func(l string) bool { got = append(got, l); return true }}
	// The colour code would straddle the 100-byte cut.
	in := strings.Repeat("a", 97) + "\x1b[31mred\x1b[0m\n"
	if _, err := w.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}
	if want := []string{strings.Repeat("a", 97), "red"}; !slices.Equal(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}
