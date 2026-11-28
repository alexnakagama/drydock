package views

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/components"
)

const (
	kTime  = components.KindLogTime
	kErr   = components.KindLogError
	kLWarn = components.KindLogWarn
	kDebug = components.KindLogDebug
	kMatch = components.KindMatch
)

// one returns the spans of a scanner that finds at most one span.
func one(sp components.Span, ok bool) []components.Span {
	if !ok {
		return nil
	}
	return []components.Span{sp}
}

func TestTimestampSpan(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want []tok
	}{
		{"RFC 3339 timestamp", "2026-11-28T10:15:02.123Z GET /health 200", []tok{{"2026-11-28T10:15:02.123Z", kTime}}},
		{"bracketed timestamp with a space", "[2026-11-28 10:15:02] started", []tok{{"[2026-11-28 10:15:02]", kTime}}},
		{"zone after a space", "[2026-11-28 10:15:02 +0100] started", []tok{{"[2026-11-28 10:15:02 +0100]", kTime}}},
		{"timestamp not at the start", "request at 2026-11-28T10:15:02Z failed", nil},
		{"comma fraction", "2026-11-28 10:15:02,123 INFO x", []tok{{"2026-11-28 10:15:02,123", kTime}}},
		{"colon zone", "2026-11-28T10:15:02+01:00 x", []tok{{"2026-11-28T10:15:02+01:00", kTime}}},
		{"compact zone", "2026-11-28T10:15:02+0100 x", []tok{{"2026-11-28T10:15:02+0100", kTime}}},
		{"hours-only zone", "2026-11-28T10:15:02-05 x", []tok{{"2026-11-28T10:15:02-05", kTime}}},
		{"spaced zone unbracketed", "2026-11-28 10:15:02 +0100 msg", []tok{{"2026-11-28 10:15:02 +0100", kTime}}},
		{"no zone, then text", "2026-11-28 10:15:02 msg", []tok{{"2026-11-28 10:15:02", kTime}}},
		{"whole line", "2026-11-28T10:15:02Z", []tok{{"2026-11-28T10:15:02Z", kTime}}},
		{"Z starting a word", "2026-11-28 10:15:02 Zookeeper started", []tok{{"2026-11-28 10:15:02", kTime}}},
		{"Z glued to a word", "2026-11-28T10:15:02Zfoo", []tok{{"2026-11-28T10:15:02", kTime}}},
		{"zone digits inside a number", "2026-11-28 10:15:02 -12345 x", []tok{{"2026-11-28 10:15:02", kTime}}},
		{"hours-only zone after a space", "2026-11-28 10:15:02 +12 items", []tok{{"2026-11-28 10:15:02 +12", kTime}}},
		{"bracketed, zone glued to a word", "[2026-11-28 10:15:02 Zx] y", nil},
		{"zone at the end of the line", "2026-11-28 10:15:02 Z", []tok{{"2026-11-28 10:15:02 Z", kTime}}},
		{"space before the closing bracket", "[2026-11-28 10:15:02.5 ] x", nil},
		{"unclosed bracket", "[2026-11-28 10:15:02 started", nil},
		{"bracket at the end of the line", "[2026-11-28 10:15:02", nil},
		{"date without time", "2026-11-28 started", nil},
		{"short time", "2026-11-28T10:15 started", nil},
		{"non-ASCII digits", "２026-11-28T10:15:02Z x", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkSpans(t, tt.line, one(timestampSpan(tt.line)), tt.want)
		})
	}
}

func TestLevelSpan(t *testing.T) {
	t.Parallel()
	pad := strings.Repeat(" ", levelWindow)
	tests := []struct {
		name string
		line string
		want []tok
	}{
		// Colored log levels.
		{"uppercase level word", "2026-11-28T10:15:02Z ERROR connection refused", []tok{{"ERROR", kErr}}},
		{"only the first level token", "WARN retrying after ERROR", []tok{{"WARN", kLWarn}}},
		{"info keeps the default color", "INFO server listening on :8080", nil},
		// Log level recognition.
		{"part of a longer word", "ERRORS_TOTAL=3", nil},
		{"lowercase in prose", "finished with no error", nil},
		{"level=", `level=error msg="boom"`, []tok{{"error", kErr}}},
		{"JSON level", `{"level":"warn","msg":"slow"}`, []tok{{"warn", kLWarn}}},
		{"bracketed lowercase", "[debug] cache miss", []tok{{"debug", kDebug}}},
		{"bracketed tag before the level", "[main] [error] boom", []tok{{"error", kErr}}},
		// More forms.
		{"quoted lvl", `lvl="WARN" msg=x`, []tok{{"WARN", kLWarn}}},
		{"lowercase lvl", "lvl=warn msg=x", []tok{{"warn", kLWarn}}},
		{"mixed-case structured forms", "lvl=Warn level=Error", []tok{{"Warn", kLWarn}}},
		{"mixed-case level=", "level=Error x", []tok{{"Error", kErr}}},
		{"mixed-case bracket", "[Warn] x", []tok{{"Warn", kLWarn}}},
		{"JSON level error", `{"level":"error"}`, []tok{{"error", kErr}}},
		{"no error", "no error", nil},
		{"bracketed uppercase", "[ERROR] boom", []tok{{"ERROR", kErr}}},
		{"ERR before a colon", "ERR: retry", []tok{{"ERR", kErr}}},
		{"INFO alone", "INFO", nil},
		{"every error word", "FATAL", []tok{{"FATAL", kErr}}},
		{"panic", "PANIC: oops", []tok{{"PANIC", kErr}}},
		{"crit", "CRIT disk", []tok{{"CRIT", kErr}}},
		{"critical", "CRITICAL disk", []tok{{"CRITICAL", kErr}}},
		{"warning", "WARNING slow", []tok{{"WARNING", kLWarn}}},
		{"trace", "TRACE enter", []tok{{"TRACE", kDebug}}},
		{"mixed case is prose", "Error: boom", nil},
		{"lowercase level after info", "level=info msg=x [warn]", []tok{{"warn", kLWarn}}},
		{"structured value not a level", "level=errors x", nil},
		{"leftmost of the forms", `[warn] level=error`, []tok{{"warn", kLWarn}}},
		{"uppercase before a structured form", `DEBUG level=error`, []tok{{"DEBUG", kDebug}}},
		{"structured before an uppercase word", `level=warn ERROR`, []tok{{"warn", kLWarn}}},
		{"level= inside a word", "loglevel=error", nil},
		{"JSON level with spaces", `{"Level" : "Error"}`, []tok{{"Error", kErr}}},
		{"JSON level unquoted", `{"level": error}`, nil},
		{"after non-ASCII", "é ERROR x", []tok{{"ERROR", kErr}}},
		{"next to U+FFFD", "�ERROR�", []tok{{"ERROR", kErr}}},
		// The window.
		{"after the window", pad + "ERROR boom", nil},
		{"word starting in the window", pad[:levelWindow-2] + "ERROR", []tok{{"ERROR", kErr}}},
		{"longer word cut by the window", pad[:levelWindow-3] + "ERRORS", nil},
		{"structured value cut by the window", pad[:levelWindow-9] + "level=errors", nil},
		{"window cut inside a character", strings.Repeat("x", levelWindow-1) + "é ERROR", nil},
		{"empty", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkSpans(t, tt.line, one(levelSpan(tt.line)), tt.want)
		})
	}
}

func TestMatchSpans(t *testing.T) {
	t.Parallel()
	const kelvin = "K" // K, 3 bytes, lower-cases to "k" (1 byte)
	tests := []struct {
		name   string
		line   string
		search string // as typed; see searchNeedle
		want   []tok
	}{
		{"every occurrence highlighted", "ERR: retry after err", "err", []tok{{"ERR", kMatch}, {"err", kMatch}}},
		{"non-overlapping repeats", "aaaa", "aaa", []tok{{"aaa", kMatch}}},
		{"adjacent repeats", "abab", "ab", []tok{{"ab", kMatch}, {"ab", kMatch}}},
		{"leading spaces trimmed", "a b  b", "  b", []tok{{"b", kMatch}, {"b", kMatch}}},
		{"no match", "hello", "x", nil},
		{"no search", "hello", "", nil},
		{"spaces only", "a  b", "   ", nil},
		{"needle longer than the line", "ab", "abc", nil},
		{"shrinking character before the match", "İstanbul error", "error", []tok{{"error", kMatch}}},
		{"match on a shrinking character", "xİy", "i", []tok{{"İ", kMatch}}},
		{"shrinking and growing characters", kelvin + "İİ" + " boom", "boom", []tok{{"boom", kMatch}}},
		{"growing character before the match", "Ⱥ boom", "boom", []tok{{"boom", kMatch}}},
		{"match across changed characters", "a" + kelvin + "Ⱥb", kelvin + "Ⱥb", []tok{{kelvin + "Ⱥb", kMatch}}},
		{"same total length, shifted offsets", kelvin + "ȺȺ x", "x", []tok{{"x", kMatch}}},
		{"invalid UTF-8 before the match", "\xff\xfe err", "err", []tok{{"err", kMatch}}},
		{"unchanged non-ASCII", "über über", "über", []tok{{"über", kMatch}, {"über", kMatch}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkSpans(t, tt.line, matchSpans(newLogLine(tt.line), searchNeedle(tt.search)), tt.want)
		})
	}
}

func TestLogLineSpansPrecedence(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		line   string
		search string
		want   []tok
	}{
		{"all three apart", "2026-11-28T10:15:02Z ERROR disk full", "full",
			[]tok{{"2026-11-28T10:15:02Z", kTime}, {"ERROR", kErr}, {"full", kMatch}}},
		{"match inside the level", "2026-11-28T10:15:02Z ERROR disk", "rro",
			[]tok{{"2026-11-28T10:15:02Z", kTime}, {"E", kErr}, {"RRO", kMatch}, {"R", kErr}}},
		{"match covers the level", "ERROR disk", "error",
			[]tok{{"ERROR", kMatch}}},
		{"match across timestamp and level", "2026-11-28T10:15:02Z ERROR", "02z er",
			[]tok{{"2026-11-28T10:15:", kTime}, {"02Z ER", kMatch}, {"ROR", kErr}}},
		{"matches before, inside and after", "x WARN x", "x",
			[]tok{{"x", kMatch}, {"WARN", kLWarn}, {"x", kMatch}}},
		{"several matches in one span", "2026-11-28T10:15:02Z boom", "2",
			[]tok{{"2", kMatch}, {"0", kTime}, {"2", kMatch}, {"6-11-", kTime}, {"2", kMatch}, {"8T10:15:0", kTime}, {"2", kMatch}, {"Z", kTime}}},
		{"no search", "2026-11-28T10:15:02Z WARN x", "",
			[]tok{{"2026-11-28T10:15:02Z", kTime}, {"WARN", kLWarn}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkSpans(t, tt.line, logLineSpans(newLogLine(tt.line), searchNeedle(tt.search)), tt.want)
		})
	}
}

// FuzzLogLineSpans checks, for any line and search, that the spans are
// valid for RenderSpans (sorted, non-overlapping, inside the line, on
// character boundaries), that every match is an occurrence of the search,
// and that styling never changes the text shown.
func FuzzLogLineSpans(f *testing.F) {
	for _, seed := range [][2]string{
		{"2026-11-28T10:15:02Z ERROR boom", "err"},
		{"[2026-11-28 10:15:02 +0100] level=warn", "0"},
		{"KİİȺ boom \xff", "k"},
		{"[main] [error] boom", "] ["},
		{strings.Repeat("é", 300) + " ERROR", "é"},
		{"é\xa9", "\ufffd"}, // invalid UTF-8 becomes U+FFFD
		{"\xc3", "\xef"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, line, search string) {
		line = cleanLogLine([]byte(line))
		needle := searchNeedle(search)
		spans := logLineSpans(newLogLine(line), needle)
		prev := 0
		for _, sp := range spans {
			if sp.Start < prev || sp.End <= sp.Start || sp.End > len(line) {
				t.Fatalf("line %q: bad span %+v after %d (all: %+v)", line, sp, prev, spans)
			}
			if !boundary(line, sp.Start) || !boundary(line, sp.End) {
				t.Fatalf("line %q: span %+v splits a character", line, sp)
			}
			if sp.Kind == kMatch && strings.ToLower(line[sp.Start:sp.End]) != needle {
				t.Fatalf("line %q: match %q is not %q", line, line[sp.Start:sp.End], needle)
			}
			prev = sp.End
		}
		// Compared after ansi.Strip on both sides, as the screen is: it
		// also drops invalid UTF-8.
		if got, want := ansi.Strip(components.RenderSpans(line, spans)), ansi.Strip(line); got != want {
			t.Fatalf("styled text %q, want %q", got, want)
		}
	})
}

// boundary reports whether i is where a character of s starts, or the end
// of s, decoding s as utf8.DecodeRuneInString does (an invalid byte is a
// character of its own).
func boundary(s string, i int) bool {
	for j := 0; j < len(s); {
		if j >= i {
			return j == i
		}
		_, size := utf8.DecodeRuneInString(s[j:])
		j += size
	}
	return i == len(s)
}

// BenchmarkLogsShown draws one long line of prose, with no level word,
// while a search is active: the worst case for a shown line.
func BenchmarkLogsShown(b *testing.B) {
	const prose = "the quick brown fox jumps over the lazy dog while nobody watches; "
	line := strings.Repeat(prose, defaultLogsConfig().maxLineLen/len(prose))
	v := newLogsView(nil, "id", "web", logsConfig{})
	defer v.Close()
	v.append([]string{line})
	v.setSearch("fox")
	if v.shownLen() != 1 {
		b.Fatal("the line does not match the search")
	}
	b.SetBytes(int64(len(line)))
	b.ReportAllocs()
	for b.Loop() {
		text, spans := v.shownStyled(0)
		_ = components.RenderSpans(text, spans)
	}
}
