package views

import (
	"encoding/json"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/ui/components"
)

// jsonScanner colors the lines of json.Indent output, one line at a time
// and in order. That output has at most one key and one value, or one
// closing bracket, per line, and a string never spans lines, so a line
// scanner is exact. It keeps a stack of the open blocks to find a
// container's State.Status.
//
// The lines come from untrusted engine data after components.Clean, which
// replaces characters but never adds or removes quotes, backslashes or
// brackets. The scanner stops at anything it does not expect and leaves the
// rest of the line unstyled, so a malformed line is never misread and never
// panics.
type jsonScanner struct {
	// containerState colors the value of the top-level State.Status like
	// the container's row (see containerTone).
	containerState bool
	// stack holds, for every open block, the quoted key that opened it, or
	// "" for the root value and for array elements.
	stack []string
	spans []components.Span // spans of the line being scanned
}

// line returns the spans of the next line, in order and non-overlapping,
// with byte offsets into l. The returned slice is only valid until the next
// call.
func (s *jsonScanner) line(l string) []components.Span {
	s.spans = s.spans[:0]
	i := skipSpaces(l, 0)
	if i >= len(l) {
		return s.spans
	}

	// A closing bracket, with an optional comma.
	if c := l[i]; c == '}' || c == ']' {
		s.add(i, i+1, components.KindJSONPunct)
		if len(s.stack) > 0 {
			s.stack = s.stack[:len(s.stack)-1]
		}
		s.comma(l, i+1)
		return s.spans
	}

	// An optional key.
	key := ""
	if l[i] == '"' {
		end, ok := stringEnd(l, i)
		if !ok {
			return s.spans
		}
		if j := skipSpaces(l, end); j < len(l) && l[j] == ':' {
			key = l[i:end]
			s.add(i, end, components.KindJSONKey)
			s.add(j, j+1, components.KindJSONPunct)
			i = skipSpaces(l, j+1)
			if i >= len(l) {
				return s.spans
			}
		}
	}

	// The value.
	var end int
	switch c := l[i]; {
	case c == '"':
		var ok bool
		if end, ok = stringEnd(l, i); !ok {
			return s.spans
		}
		s.add(i, end, s.stringKind(key, l[i:end]))
	case c == '{' || c == '[':
		end = i + 1
		if end < len(l) && l[end] == closing(c) {
			end++ // an empty block, opened and closed here
		} else {
			s.stack = append(s.stack, key)
		}
		s.add(i, end, components.KindJSONPunct)
	case c == '-' || isDigit(c):
		end = i + 1
		for end < len(l) && isNumberByte(l[end]) {
			end++
		}
		s.add(i, end, components.KindJSONNumber)
	default:
		end = i
		for end < len(l) && l[end] >= 'a' && l[end] <= 'z' {
			end++
		}
		switch l[i:end] {
		case "true", "false", "null":
			s.add(i, end, components.KindJSONNumber)
		default:
			return s.spans
		}
	}
	s.comma(l, end)
	return s.spans
}

// stringKind returns the kind of the string value quoted, the value of key.
// It is a state kind for a container's top-level State.Status (the stack is
// the root object, then State), and KindJSONString otherwise, including for
// a state containerTone leaves in the default color and for a value that
// does not decode. The value is decoded with JSON's own rules, not Go's
// (strconv.Unquote rejects the JSON escape \/, for example).
func (s *jsonScanner) stringKind(key, quoted string) components.SpanKind {
	if !s.containerState || key != `"Status"` || len(s.stack) != 2 || s.stack[0] != "" || s.stack[1] != `"State"` {
		return components.KindJSONString
	}
	var state string
	if err := json.Unmarshal([]byte(quoted), &state); err != nil {
		return components.KindJSONString
	}
	switch containerTone(container.ContainerState(state)) {
	case components.ToneGood:
		return components.KindStateGood
	case components.ToneWarn:
		return components.KindStateWarn
	case components.ToneDim:
		return components.KindStateDim
	default:
		return components.KindJSONString
	}
}

// comma adds the comma at l[i], if there is one.
func (s *jsonScanner) comma(l string, i int) {
	if i < len(l) && l[i] == ',' {
		s.add(i, i+1, components.KindJSONPunct)
	}
}

// add appends a span, merging it into the previous one when they touch and
// have the same kind (for example "}," or "{}"), so the line holds fewer
// escape sequences.
func (s *jsonScanner) add(start, end int, kind components.SpanKind) {
	if n := len(s.spans); n > 0 && s.spans[n-1].End == start && s.spans[n-1].Kind == kind {
		s.spans[n-1].End = end
		return
	}
	s.spans = append(s.spans, components.Span{Start: start, End: end, Kind: kind})
}

// stringEnd returns the offset just past the closing quote of the string
// that starts at l[i], honoring backslash escapes, and false if the string
// is not closed on this line.
func stringEnd(l string, i int) (int, bool) {
	for j := i + 1; j < len(l); j++ {
		switch l[j] {
		case '\\':
			j++ // skip the escaped byte; it is never a quote that ends the string
		case '"':
			return j + 1, true
		}
	}
	return 0, false
}

func skipSpaces(l string, i int) int {
	for i < len(l) && l[i] == ' ' {
		i++
	}
	return i
}

func closing(open byte) byte {
	if open == '{' {
		return '}'
	}
	return ']'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isNumberByte reports whether c can appear in a JSON number after its
// first byte.
func isNumberByte(c byte) bool {
	return isDigit(c) || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}
