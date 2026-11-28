package components

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

// SpanKind is the kind of token a Span covers. A kind, not a style, is
// stored with content, so a palette change (see Styles) recolors text that
// was scanned before it.
type SpanKind uint8

// Span kinds. KindNone renders unstyled.
const (
	KindNone SpanKind = iota

	// Inspection JSON tokens.
	KindJSONKey
	KindJSONString
	KindJSONNumber // numbers and the literals true, false, null
	KindJSONPunct

	// A container state value, colored like the container's row, and the
	// tones a table row or cell is drawn in (see Tone).
	KindStateGood
	KindStateWarn
	KindStateDim
	KindStateBad

	// Log lines.
	KindLogTime
	KindLogError
	KindLogWarn
	KindLogDebug
	KindMatch // a search match

	kindCount // number of kinds; not a kind
)

// Span marks the bytes [Start, End) of a line as a token of kind Kind.
type Span struct {
	Start, End int
	Kind       SpanKind
}

// style returns the current style for k, or nil for KindNone and unknown
// kinds.
func (k SpanKind) style() *lipgloss.Style {
	switch k {
	case KindJSONKey:
		return &Styles.JSONKey
	case KindJSONString:
		return &Styles.JSONString
	case KindJSONNumber:
		return &Styles.JSONNumber
	case KindJSONPunct:
		return &Styles.JSONPunct
	case KindStateGood:
		return &Styles.RowGood
	case KindStateWarn:
		return &Styles.RowWarn
	case KindStateDim:
		return &Styles.RowDim
	case KindStateBad:
		return &Styles.RowBad
	case KindLogTime:
		return &Styles.LogTime
	case KindLogError:
		return &Styles.LogError
	case KindLogWarn:
		return &Styles.LogWarn
	case KindLogDebug:
		return &Styles.LogDebug
	case KindMatch:
		return &Styles.SearchMatch
	}
	return nil
}

// RenderSpans returns text with each span wrapped in the style its kind
// maps to in the current Styles; text outside every span is unstyled.
//
// text must be plain and display-safe (see Clean): RenderSpans does not
// sanitize it, and a tab or line break would be rewritten by the styling.
// spans hold byte offsets into text; they must be sorted by Start and must
// not overlap. Callers that combine several span sets split and merge them
// first. Invalid input is clamped, never reordered: a span is cut to the
// text, a span that starts before the end of the previous one starts where
// that one ends, and an offset inside a multi-byte character moves to the
// end of that character. So the result always holds exactly the bytes of
// text once, in order (ansi.Strip(RenderSpans(text, spans)) == text), and a
// character is never split by an escape sequence.
func RenderSpans(text string, spans []Span) string {
	if len(spans) == 0 || text == "" {
		return text
	}
	// Each kind's escape sequences are built once per call, from the
	// current Styles, instead of once per span: a log line can hold
	// thousands of search matches.
	var seqs [kindCount]spanSeq
	var b strings.Builder
	b.Grow(len(text) + len(spans)*48)
	pos := 0 // bytes of text already written
	for _, sp := range spans {
		start := runeBoundary(text, max(sp.Start, pos))
		end := runeBoundary(text, sp.End)
		if start >= end || sp.Kind == KindNone || sp.Kind >= kindCount {
			continue // an unstyled span is written as part of the next gap
		}
		seq := &seqs[sp.Kind]
		if !seq.ok {
			*seq = newSpanSeq(sp.Kind)
		}
		b.WriteString(text[pos:start])
		b.WriteString(seq.open)
		b.WriteString(text[start:end])
		b.WriteString(seq.close)
		pos = end
	}
	b.WriteString(text[pos:])
	return b.String()
}

// spanSeq is the escape sequence that opens a kind's style and the one that
// closes it.
type spanSeq struct {
	open, close string
	ok          bool
}

// newSpanSeq splits the rendering of a one-character sample into the
// sequences around it. The styles hold only colors and text attributes, so
// rendering any plain text is open + text + close (see
// TestRenderSpansMatchesStyleRender).
func newSpanSeq(k SpanKind) spanSeq {
	const sample = "x"
	open, close, found := strings.Cut(k.style().Render(sample), sample)
	if !found {
		return spanSeq{ok: true}
	}
	return spanSeq{open: open, close: close, ok: true}
}

// runeBoundary clamps i to [0, len(s)] and moves it forward to the start of
// a character.
func runeBoundary(s string, i int) int {
	i = min(max(i, 0), len(s))
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}
