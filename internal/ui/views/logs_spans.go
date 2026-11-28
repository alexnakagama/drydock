package views

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexnakagama/drydock/internal/ui/components"
)

// The scanners in this file style a log line at render time, for the shown
// lines only. A line is untrusted container output, already made
// display-safe and valid UTF-8 by cleanLogLine. It is about maxLineLen
// (16 KiB) long at most, and up to three times that when cleanLogLine
// replaced many invalid bytes. The scanners still accept invalid UTF-8 without panicking.
// Every span they return has byte offsets into the line, lies within it and
// starts and ends on a character boundary.

// logTimeRE matches a timestamp at the very start of a line: an ISO 8601
// date and time, with optional fractional seconds and zone (which may
// follow a space and may be hours only), optionally after a '['. Group 1
// is the '[', group 2 the zone with the space before it. It is anchored, so
// it costs the same on a long line as on a short one. \d is ASCII only in
// RE2.
var logTimeRE = regexp.MustCompile(`^(\[)?\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?( ?(?:Z|[+-]\d{2}(?::?\d{2})?))?`)

// timestampSpan returns the span of a timestamp at the start of l, and
// false if there is none. A zone must end the word it is in: one followed
// by a letter, digit or '_' (the "Z" of "Zookeeper", the "-1234" of
// "-12345") is not part of the timestamp. A timestamp that starts with '['
// must be closed by a ']' right after it, which the span includes, so
// "[2026-11-28 10:15:02 ]" is not a timestamp.
func timestampSpan(l string) (components.Span, bool) {
	m := logTimeRE.FindStringSubmatchIndex(l)
	if m == nil {
		return components.Span{}, false
	}
	end := m[1]
	if m[4] >= 0 && end < len(l) && isWordByte(l[end]) {
		end = m[4] // drop the zone
	}
	if m[2] >= 0 { // bracketed
		if end >= len(l) || l[end] != ']' {
			return components.Span{}, false
		}
		end++
	}
	return components.Span{Start: 0, End: end, Kind: components.KindLogTime}, true
}

// levelWindow is how many leading bytes of a line level detection looks
// at. The level is almost always near the start, and unanchored scans of a
// whole 16 KiB line would make a screen of long lines slow to draw.
const levelWindow = 512

// maxLevelLen is the length of the longest level word ("critical").
const maxLevelLen = 8

// levelKind returns the kind of a level word in lower case, and KindNone
// for a word that is not a level (INFO included).
func levelKind(lower string) components.SpanKind {
	switch lower {
	case "error", "err", "fatal", "panic", "crit", "critical":
		return components.KindLogError
	case "warn", "warning":
		return components.KindLogWarn
	case "debug", "trace":
		return components.KindLogDebug
	}
	return components.KindNone
}

// The structured level forms, in any case. Group 1 is the value.
var (
	levelKVRE      = regexp.MustCompile(`(?i)\b(?:level|lvl)="?(\w+)`)
	levelJSONRE    = regexp.MustCompile(`(?i)"level"\s*:\s*"(\w+)"`)
	levelBracketRE = regexp.MustCompile(`\[(\w+)\]`)
)

// levelSpan returns the span of the first log-level token that starts in
// the first levelWindow bytes of l, and false if there is none. The
// candidates are an uppercase level word standing alone (a word being a
// run of ASCII letters, digits and '_'), and, in any case, the value of
// level= or lvl=, a JSON "level" value or a word alone in square brackets.
// The leftmost wins, and only the level word itself is covered. A
// structured form is matched within the window only, so one that the
// window cuts (a "[error]" straddling byte levelWindow) is not found.
func levelSpan(l string) (components.Span, bool) {
	n := min(len(l), levelWindow)
	for n > 0 && n < len(l) && !utf8.RuneStart(l[n]) {
		n-- // cut back to a character boundary
	}
	w := l[:n]

	best, found := upperLevel(l, n)
	try := func(re *regexp.Regexp, need string) {
		// need holds bytes every match contains: a window without one
		// cannot match, and skipping the regexp keeps prose cheap.
		for _, c := range []byte(need) {
			if strings.IndexByte(w, c) < 0 {
				return
			}
		}
		for _, m := range re.FindAllStringSubmatchIndex(w, -1) {
			start, end := m[2], m[3]
			if found && start >= best.Start {
				return // matches come left to right; none can win now
			}
			// A value cut short by the window is part of a longer word.
			if end == len(w) && end < len(l) && isWordByte(l[end]) {
				continue
			}
			if k := levelKind(strings.ToLower(w[start:end])); k != components.KindNone {
				best, found = components.Span{Start: start, End: end, Kind: k}, true
				return
			}
		}
	}
	try(levelKVRE, "=")
	try(levelJSONRE, `":`)
	try(levelBracketRE, "[]")
	return best, found
}

// upperLevel returns the span of the first uppercase level word that
// starts before byte n of l. A word is read to its end in l, past n, so a
// longer word that the window cuts is not mistaken for a level.
func upperLevel(l string, n int) (components.Span, bool) {
	for i := 0; i < n; {
		if !isWordByte(l[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(l) && isWordByte(l[j]) {
			j++
		}
		if word := l[i:j]; len(word) <= maxLevelLen && isUpperASCII(word) {
			if k := levelKind(strings.ToLower(word)); k != components.KindNone {
				return components.Span{Start: i, End: j, Kind: k}, true
			}
		}
		i = j
	}
	return components.Span{}, false
}

// isWordByte reports whether c is an ASCII letter, digit or '_', the word
// characters of RE2's \b.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || isDigit(c) || c == '_'
}

func isUpperASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// searchNeedle returns what a search for s looks for in the lower-cased
// lines: s trimmed and lower-cased, or "" for no search.
func searchNeedle(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// matchSpans returns the spans of every non-overlapping occurrence of
// needle (see searchNeedle) in l, left to right, case-insensitively: needle
// is looked for in the lower-cased text the search filters on, and each
// occurrence is mapped back to the bytes of the original text it came from.
// It returns nil for an empty needle.
func matchSpans(l logLine, needle string) []components.Span {
	if needle == "" {
		return nil
	}
	low := l.searchText()
	var spans []components.Span
	for pos := 0; pos <= len(low)-len(needle); {
		i := strings.Index(low[pos:], needle)
		if i < 0 {
			break
		}
		start := pos + i
		pos = start + len(needle)
		spans = append(spans, components.Span{Start: start, End: pos, Kind: components.KindMatch})
	}
	// l.lower is "" when lower-casing left the text as it is.
	if len(spans) > 0 && l.lower != "" && lowerShifts(l.text) {
		toOriginalOffsets(l.text, spans)
	}
	return spans
}

// lowerShifts reports whether lower-casing s, as strings.ToLower does,
// changes the byte length of any of its characters, so that offsets in the
// lower-cased text differ from those in s. Comparing the total lengths is
// not enough: one character can shrink and another grow.
func lowerShifts(s string) bool {
	for i := 0; i < len(s); {
		if s[i] < utf8.RuneSelf {
			i++ // ASCII keeps its length
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if lowerLen(r, size) != size {
			return true
		}
		i += size
	}
	return false
}

// lowerLen returns the byte length that strings.ToLower gives the character
// r, decoded from size bytes of the original text. An invalid byte (r is
// utf8.RuneError, size 1) becomes the 3-byte replacement character.
func lowerLen(r rune, size int) int {
	if r == utf8.RuneError && size == 1 {
		return utf8.RuneLen(utf8.RuneError)
	}
	return utf8.RuneLen(unicode.ToLower(r))
}

// toOriginalOffsets rewrites spans, sorted and holding offsets into
// strings.ToLower(s) that fall on character boundaries, to the offsets of
// the same characters in s. It walks s once, character by character.
func toOriginalOffsets(s string, spans []components.Span) {
	orig, low := 0, 0 // the same position in s and in its lower-cased text
	conv := func(off int) int {
		for low < off && orig < len(s) {
			r, size := utf8.DecodeRuneInString(s[orig:])
			low += lowerLen(r, size)
			orig += size
		}
		return orig
	}
	for i := range spans {
		spans[i].Start = conv(spans[i].Start)
		spans[i].End = conv(spans[i].End)
	}
}

// logLineSpans returns the spans that style l: its leading timestamp and
// first level token, with the occurrences of needle laid over them.
func logLineSpans(l logLine, needle string) []components.Span {
	var base [2]components.Span
	nb := 0
	ts, hasTS := timestampSpan(l.text)
	if hasTS {
		base[nb] = ts
		nb++
	}
	if lv, ok := levelSpan(l.text); ok && (!hasTS || lv.Start >= ts.End) {
		base[nb] = lv
		nb++
	}
	return overlaySpans(base[:nb], matchSpans(l, needle))
}

// overlaySpans lays top over base: both sorted and non-overlapping, the
// result is too, and holds every span of top, plus the parts of base spans
// that no top span covers.
func overlaySpans(base, top []components.Span) []components.Span {
	if len(top) == 0 {
		return append([]components.Span(nil), base...)
	}
	out := make([]components.Span, 0, len(top)+2*len(base))
	ti, covered := 0, 0 // next top span; end of the last top span added
	for _, b := range base {
		start := max(b.Start, covered)
		for ti < len(top) && top[ti].Start < b.End {
			t := top[ti]
			if t.Start > start {
				out = append(out, components.Span{Start: start, End: t.Start, Kind: b.Kind})
			}
			out = append(out, t)
			covered = t.End
			start = max(start, t.End)
			ti++
		}
		if start < b.End {
			out = append(out, components.Span{Start: start, End: b.End, Kind: b.Kind})
		}
	}
	return append(out, top[ti:]...)
}
