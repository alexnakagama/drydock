package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderSpans(t *testing.T) {
	key, str, punct := Styles.JSONKey.Render, Styles.JSONString.Render, Styles.JSONPunct.Render
	for _, tc := range []struct {
		name  string
		text  string
		spans []Span
		want  string
	}{
		{"no spans", `"a": 1`, nil, `"a": 1`},
		{"empty text", "", []Span{{0, 3, KindJSONKey}}, ""},
		{
			"adjacent spans and gaps", `  "a": "b",`,
			[]Span{{2, 5, KindJSONKey}, {5, 6, KindJSONPunct}, {7, 10, KindJSONString}, {10, 11, KindJSONPunct}},
			`  ` + key(`"a"`) + punct(":") + " " + str(`"b"`) + punct(","),
		},
		{"span past the end is clamped", "abc", []Span{{1, 99, KindJSONKey}}, "a" + key("bc")},
		{"span starting past the end", "abc", []Span{{5, 9, KindJSONKey}}, "abc"},
		{"negative start is clamped", "abc", []Span{{-4, 2, KindJSONKey}}, key("ab") + "c"},
		{"empty and inverted spans", "abc", []Span{{1, 1, KindJSONKey}, {2, 1, KindJSONKey}}, "abc"},
		{"overlap starts where the previous span ends", "abcd", []Span{{0, 2, KindJSONKey}, {1, 3, KindJSONString}}, key("ab") + str("c") + "d"},
		{"unsorted spans are clamped, not reordered", "abcd", []Span{{2, 4, KindJSONKey}, {0, 2, KindJSONString}}, "ab" + key("cd")},
		{"unknown and none kinds are unstyled", "abcd", []Span{{0, 1, KindNone}, {1, 2, SpanKind(250)}, {2, 3, KindJSONKey}}, "ab" + key("c") + "d"},
		{"state kinds use row styles", "ok", []Span{{0, 1, KindStateGood}, {1, 2, KindStateDim}}, Styles.RowGood.Render("o") + Styles.RowDim.Render("k")},
		{"offsets inside a character move past it", "é中x", []Span{{1, 4, KindJSONKey}}, "é" + key("中") + "x"},
		{"replacement character", "a�b", []Span{{1, 4, KindJSONString}}, "a" + str("�") + "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RenderSpans(tc.text, tc.spans)
			if got != tc.want {
				t.Errorf("RenderSpans = %q, want %q", got, tc.want)
			}
			if s := ansi.Strip(got); s != tc.text {
				t.Errorf("stripped = %q, want %q", s, tc.text)
			}
		})
	}
}

func TestRenderSpansEveryKind(t *testing.T) {
	for k := KindJSONKey; k < kindCount; k++ {
		if got := RenderSpans("x", []Span{{0, 1, k}}); got == "x" {
			t.Errorf("kind %d renders unstyled", k)
		}
	}
}

// RenderSpans builds each kind's escape sequences once and wraps a whole
// span in them. For every style without an underline that is exactly what
// the style renders. lipgloss renders an underlined style one character at a
// time (to handle spaces), with the same attributes on each, so SearchMatch
// is checked to be its one-character rendering around the whole span.
func TestRenderSpansMatchesStyleRender(t *testing.T) {
	saved := Styles
	t.Cleanup(func() { Styles = saved })
	for _, dark := range []bool{true, false} {
		Styles = NewStyles(dark)
		for k := KindJSONKey; k < kindCount; k++ {
			for _, text := range []string{"x", "a b", "  lead", "trail  ", "中文 é", "\u00a0\u3000"} {
				got := RenderSpans(text, []Span{{0, len(text), k}})
				if k.style().GetUnderline() {
					open, close, _ := strings.Cut(k.style().Render("x"), "x")
					if want := open + text + close; got != want || !strings.Contains(open, "4") {
						t.Errorf("dark=%v kind %d %q: got %q, want %q", dark, k, text, got, want)
					}
					continue
				}
				if want := k.style().Render(text); got != want {
					t.Errorf("dark=%v kind %d %q: got %q, want %q", dark, k, text, got, want)
				}
			}
		}
	}
}

// Bytes that are not valid UTF-8 are written unchanged (ansi.Strip itself
// drops them, so the strip invariant cannot be checked on them).
func TestRenderSpansInvalidUTF8(t *testing.T) {
	seq := newSpanSeq(KindJSONKey)
	if got, want := RenderSpans("a\xffb", []Span{{1, 2, KindJSONKey}}), "a"+seq.open+"\xff"+seq.close+"b"; got != want {
		t.Errorf("RenderSpans = %q, want %q", got, want)
	}
}

func FuzzRenderSpans(f *testing.F) {
	f.Add("  \"Image\": \"nginx\",", 2, 9, 9, 12)
	f.Add("中文 é\uFFFD", -3, 4, 2, 99)
	f.Add("a\xffb", 1, 2, 0, 3)
	f.Fuzz(func(t *testing.T, text string, s1, e1, s2, e2 int) {
		text = strings.ToValidUTF8(Clean(text), "\uFFFD")
		spans := []Span{{s1, e1, KindJSONKey}, {s2, e2, KindMatch}}
		if got := ansi.Strip(RenderSpans(text, spans)); got != text {
			t.Errorf("stripped %q, want %q", got, text)
		}
	})
}

// BenchmarkRenderSpansManyMatches is a 16 KiB log line with a search match
// every 8 bytes, as a one-letter search produces.
func BenchmarkRenderSpansManyMatches(b *testing.B) {
	text := strings.Repeat("abcdefg ", 2048)
	spans := make([]Span, 0, 2048)
	for i := 0; i < len(text); i += 8 {
		spans = append(spans, Span{i, i + 1, KindMatch})
	}
	b.ReportAllocs()
	for b.Loop() {
		RenderSpans(text, spans)
	}
}
