package views

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/components"
)

// tok is an expected span: the exact bytes it covers and its kind.
type tok struct {
	text string
	kind components.SpanKind
}

const (
	kKey   = components.KindJSONKey
	kStr   = components.KindJSONString
	kNum   = components.KindJSONNumber
	kPunct = components.KindJSONPunct
	kGood  = components.KindStateGood
	kWarn  = components.KindStateWarn
	kDim   = components.KindStateDim
)

// checkSpans fails unless spans are sorted, non-overlapping, inside l, and
// cover exactly the bytes and kinds of want, in order.
func checkSpans(t *testing.T, l string, spans []components.Span, want []tok) {
	t.Helper()
	got := make([]tok, 0, len(spans))
	prev := 0
	for _, sp := range spans {
		if sp.Start < prev || sp.End <= sp.Start || sp.End > len(l) {
			t.Fatalf("line %q: bad span %+v after offset %d (all: %+v)", l, sp, prev, spans)
		}
		prev = sp.End
		got = append(got, tok{l[sp.Start:sp.End], sp.Kind})
	}
	if len(got) != len(want) {
		t.Fatalf("line %q:\n got %v\nwant %v", l, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("line %q: span %d = %v, want %v\n got %v\nwant %v", l, i, got[i], want[i], got, want)
		}
	}
}

func TestJSONScannerLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want []tok
	}{
		{"open root", "{", []tok{{"{", kPunct}}},
		{"key and string", `  "Image": "nginx:1.27",`, []tok{{`"Image"`, kKey}, {":", kPunct}, {`"nginx:1.27"`, kStr}, {",", kPunct}}},
		{"string without comma", `  "Name": "/web"`, []tok{{`"Name"`, kKey}, {":", kPunct}, {`"/web"`, kStr}}},
		{"escaped quote", `  "Cmd": "say \"hi\": {a, b}",`, []tok{{`"Cmd"`, kKey}, {":", kPunct}, {`"say \"hi\": {a, b}"`, kStr}, {",", kPunct}}},
		{"escaped backslash before the closing quote", `  "Path": "C:\\",`, []tok{{`"Path"`, kKey}, {":", kPunct}, {`"C:\\"`, kStr}, {",", kPunct}}},
		{"escaped backslash then quote", `  "A": "x\\\"y"`, []tok{{`"A"`, kKey}, {":", kPunct}, {`"x\\\"y"`, kStr}}},
		{"key with colon and brace", `  "a:{,": 1`, []tok{{`"a:{,"`, kKey}, {":", kPunct}, {"1", kNum}}},
		{"zero", `  "RestartCount": 0,`, []tok{{`"RestartCount"`, kKey}, {":", kPunct}, {"0", kNum}, {",", kPunct}}},
		{"negative", `  "ExitCode": -1,`, []tok{{`"ExitCode"`, kKey}, {":", kPunct}, {"-1", kNum}, {",", kPunct}}},
		{"exponent", `  "X": -1.5e+10,`, []tok{{`"X"`, kKey}, {":", kPunct}, {"-1.5e+10", kNum}, {",", kPunct}}},
		{"upper exponent", `  "X": 2E-3`, []tok{{`"X"`, kKey}, {":", kPunct}, {"2E-3", kNum}}},
		{"true", `  "Running": true,`, []tok{{`"Running"`, kKey}, {":", kPunct}, {"true", kNum}, {",", kPunct}}},
		{"false", `  "Paused": false`, []tok{{`"Paused"`, kKey}, {":", kPunct}, {"false", kNum}}},
		{"null", `  "Args": null,`, []tok{{`"Args"`, kKey}, {":", kPunct}, {"null", kNum}, {",", kPunct}}},
		{"empty object", `  "Labels": {},`, []tok{{`"Labels"`, kKey}, {":", kPunct}, {"{},", kPunct}}},
		{"empty array", `  "Mounts": []`, []tok{{`"Mounts"`, kKey}, {":", kPunct}, {"[]", kPunct}}},
		{"opening object", `  "State": {`, []tok{{`"State"`, kKey}, {":", kPunct}, {"{", kPunct}}},
		{"opening array", `  "Env": [`, []tok{{`"Env"`, kKey}, {":", kPunct}, {"[", kPunct}}},
		{"array element string", `    "PATH=/usr/bin",`, []tok{{`"PATH=/usr/bin"`, kStr}, {",", kPunct}}},
		{"array element number", `    80`, []tok{{"80", kNum}}},
		{"array element object", `    {`, []tok{{"{", kPunct}}},
		{"closing with comma", `  },`, []tok{{"},", kPunct}}},
		{"closing without comma", `  ]`, []tok{{"]", kPunct}}},
		{"closing root", `}`, []tok{{"}", kPunct}}},
		{"U+FFFD from Clean", components.Clean("  \"a\": \"x\u009b31m\u202ey\","), []tok{{`"a"`, kKey}, {":", kPunct}, {"\"x\ufffd31m\ufffdy\"", kStr}, {",", kPunct}}},
		{"non-ASCII", `  "Ünï": "日本",`, []tok{{`"Ünï"`, kKey}, {":", kPunct}, {`"日本"`, kStr}, {",", kPunct}}},
		{"blank", "", nil},
		{"spaces only", "   ", nil},
		// Lines json.Indent never writes: scanning stops, nothing is misread.
		{"unterminated string", `  "a": "open`, []tok{{`"a"`, kKey}, {":", kPunct}}},
		{"unterminated key", `  "open`, nil},
		{"unknown word", `  "a": nope`, []tok{{`"a"`, kKey}, {":", kPunct}}},
		{"key with nothing after", `  "a":`, []tok{{`"a"`, kKey}, {":", kPunct}}},
		{"trailing backslash", `  "a\`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s jsonScanner
			checkSpans(t, tt.line, s.line(tt.line), tt.want)
		})
	}
}

// scanDoc indents raw and scans it like prettyJSON, returning each line and
// a copy of its spans.
func scanDoc(t *testing.T, raw string, containerState bool) ([]string, [][]components.Span) {
	t.Helper()
	lines, spans, err := prettyJSON(json.RawMessage(raw), containerState)
	if err != nil {
		t.Fatal(err)
	}
	return lines, spans
}

// valueOf returns the text and kind of the value span on the first line
// whose text, trimmed, starts with prefix, failing if there is none.
func valueOf(t *testing.T, lines []string, spans [][]components.Span, prefix string) tok {
	t.Helper()
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), prefix) {
			sp := spans[i]
			if len(sp) < 3 {
				t.Fatalf("line %q: spans %+v, want key, colon and value", l, sp)
			}
			return tok{l[sp[2].Start:sp[2].End], sp[2].Kind}
		}
	}
	t.Fatalf("no line starting with %q in\n%s", prefix, strings.Join(lines, "\n"))
	return tok{}
}

func TestJSONScannerContainerState(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		raw    string
		prefix string // the line holding the value
		want   tok
	}{
		{"running", `{"State":{"Status":"running"}}`, `"Status"`, tok{`"running"`, kGood}},
		{"paused", `{"State":{"Status":"paused"}}`, `"Status"`, tok{`"paused"`, kWarn}},
		{"restarting", `{"State":{"Status":"restarting"}}`, `"Status"`, tok{`"restarting"`, kWarn}},
		{"exited", `{"State":{"Status":"exited"}}`, `"Status"`, tok{`"exited"`, kDim}},
		{"created", `{"State":{"Status":"created"}}`, `"Status"`, tok{`"created"`, kDim}},
		{"dead", `{"State":{"Status":"dead"}}`, `"Status"`, tok{`"dead"`, kDim}},
		{"default tone", `{"State":{"Status":"removing"}}`, `"Status"`, tok{`"removing"`, kStr}},
		{"after other State keys", `{"State":{"Running":true,"Error":"a\"b","Status":"running"}}`, `"Status"`, tok{`"running"`, kGood}},
		{"after a nested block", `{"State":{"Health":{"Status":"healthy","Log":[]},"Status":"exited"}}`, `"Status": "exited"`, tok{`"exited"`, kDim}},
		{"health status", `{"State":{"Health":{"Status":"healthy"}}}`, `"Status"`, tok{`"healthy"`, kStr}},
		{"inside an array element", `{"Mounts":[{"State":{"Status":"running"}}]}`, `"Status"`, tok{`"running"`, kStr}},
		{"top-level Status after empty State", `{"State":{},"Status":"running"}`, `"Status"`, tok{`"running"`, kStr}},
		{"top-level Status after closed State", `{"State":{"Pid":1},"Status":"running"}`, `"Status"`, tok{`"running"`, kStr}},
		{"nested State", `{"Config":{"State":{"Status":"running"}}}`, `"Status"`, tok{`"running"`, kStr}},
		{"not a string", `{"State":{"Status":1}}`, `"Status"`, tok{"1", kNum}},
		{"escaped value", `{"State":{"Status":"run\u006eing"}}`, `"Status"`, tok{`"run\u006eing"`, kGood}},
		{"JSON-only escape", `{"State":{"Status":"\/running"}}`, `"Status"`, tok{`"\/running"`, kStr}},
		{"JSON escape of a state", `{"State":{"Status":"runnin\u0067"}}`, `"Status"`, tok{`"runnin\u0067"`, kGood}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines, spans := scanDoc(t, tt.raw, true)
			if got := valueOf(t, lines, spans, tt.prefix); got != tt.want {
				t.Errorf("value = %v, want %v\n%s", got, tt.want, strings.Join(lines, "\n"))
			}
		})
	}
}

func TestJSONScannerStateNeedsOption(t *testing.T) {
	t.Parallel()
	lines, spans := scanDoc(t, `{"State":{"Status":"running"}}`, false)
	if got, want := valueOf(t, lines, spans, `"Status"`), (tok{`"running"`, kStr}); got != want {
		t.Errorf("without the option, value = %v, want %v", got, want)
	}
}

// TestPrettyJSONSpansWholeDocument checks every line of a realistic
// document: spans are valid, never split a character, and stripping the
// rendered line gives the line back.
func TestPrettyJSONSpansWholeDocument(t *testing.T) {
	t.Parallel()
	raw := `{"Id":"abc","Args":["-c","echo \"x\""],"State":{"Status":"running","Running":true,"ExitCode":0,` +
		`"Health":{"Status":"healthy","FailingStreak":0,"Log":[{"Start":"t","ExitCode":-1,"Output":"a: {b}, [c]"}]}},` +
		`"Config":{"Labels":{},"Env":[],"Tty":false,"Ratio":1.5e-3},"Mounts":null,"Name":"ünï 日本"}`
	lines, spans := scanDoc(t, raw, true)
	if len(spans) != len(lines) {
		t.Fatalf("%d span lists for %d lines", len(spans), len(lines))
	}
	for i, l := range lines {
		prev := 0
		for _, sp := range spans[i] {
			if sp.Start < prev || sp.End <= sp.Start || sp.End > len(l) ||
				!utf8.RuneStart(l[sp.Start]) || (sp.End < len(l) && !utf8.RuneStart(l[sp.End])) {
				t.Fatalf("line %q: bad span %+v", l, sp)
			}
			prev = sp.End
		}
		if got := ansi.Strip(components.RenderSpans(l, spans[i])); got != l {
			t.Errorf("stripped render %q, want %q", got, l)
		}
		// Every non-space byte of json.Indent output belongs to a token.
		covered := 0
		for _, sp := range spans[i] {
			covered += len(strings.ReplaceAll(l[sp.Start:sp.End], " ", ""))
		}
		if want := len(strings.ReplaceAll(l, " ", "")); covered != want {
			t.Errorf("line %q: spans cover %d non-space bytes, want %d (%+v)", l, covered, want, spans[i])
		}
	}
}

// FuzzJSONScanner checks that no input, valid JSON or not, makes the
// scanner panic or return invalid spans.
func FuzzJSONScanner(f *testing.F) {
	for _, seed := range []string{
		`{"State":{"Status":"running"}}`,
		`{"a":"x\\\"y","b":[1,-2e3,true,null,{}],"c":{"d":[]}}`,
		`  "a\`, `}}}]]]`, `"`, `"\`, `  "a": "b`, "\"\u00e9\": \"\ufffd\"",
		"0\x99", "  \"a\": \"\xe6\x97\", \"\x9b[31m\"", // invalid UTF-8
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		var docs []string
		var buf bytes.Buffer
		if json.Indent(&buf, []byte(in), "", "  ") == nil {
			docs = append(docs, buf.String())
		}
		docs = append(docs, in) // the raw input, line by line, as an adversarial case
		for _, doc := range docs {
			s := jsonScanner{containerState: true}
			for _, l := range strings.Split(doc, "\n") {
				l = components.Clean(l)
				prev := 0
				for _, sp := range s.line(l) {
					if sp.Start < prev || sp.End <= sp.Start || sp.End > len(l) {
						t.Fatalf("line %q: bad span %+v", l, sp)
					}
					if splitsRune(l, sp.Start) || splitsRune(l, sp.End) {
						t.Fatalf("line %q: span %+v splits a character", l, sp)
					}
					prev = sp.End
				}
			}
		}
	})
}

// splitsRune reports whether offset i falls strictly inside a valid
// multi-byte character of l. Bytes of invalid UTF-8 are characters of their
// own, so a boundary next to one splits nothing.
func splitsRune(l string, i int) bool {
	for j := i - 1; j >= 0 && j > i-utf8.UTFMax; j-- {
		if utf8.RuneStart(l[j]) {
			r, size := utf8.DecodeRuneInString(l[j:])
			return (r != utf8.RuneError || size != 1) && j+size > i
		}
	}
	return false
}
