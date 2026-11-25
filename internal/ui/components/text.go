package components

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/docker"
)

// maxInputLen bounds every single-line text input (command bar, prompt,
// filter). It is far above any resource name the engine accepts.
const maxInputLen = 512

// Clean makes untrusted text (engine data, user input) safe to render on a
// single line: terminal control characters are replaced (see
// docker.SanitizeText) and newlines and tabs become spaces.
func Clean(s string) string {
	// Line breaks and tabs first: SanitizeText would turn a CR into U+FFFD.
	if strings.ContainsAny(s, "\n\t\r") {
		s = lineBreaks.Replace(s)
	}
	return docker.SanitizeText(s)
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ")

// UpdateInput passes msg to in and then removes from its value anything
// Clean would replace. The input's own sanitizer drops control characters
// but keeps bidirectional-text controls, which can make the rendered line
// read differently from what was typed or pasted.
func UpdateInput(in textinput.Model, msg tea.Msg) (textinput.Model, tea.Cmd) {
	in, cmd := in.Update(msg)
	if v := in.Value(); Clean(v) != v {
		pos := in.Position()
		in.SetValue(strings.Map(func(r rune) rune {
			if Clean(string(r)) != string(r) {
				return -1
			}
			return r
		}, v))
		in.SetCursor(min(pos, len([]rune(in.Value()))))
	}
	return in, cmd
}

// Fit truncates s to at most width terminal cells, ending it with an
// ellipsis when it is cut, and pads it with spaces to exactly width cells.
// s may contain ANSI styling.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	if pad := width - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

// Truncate cuts s to at most width terminal cells, ending it with an
// ellipsis when it is cut. Unlike Fit it does not pad.
func Truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		return ansi.Truncate(s, width, "…")
	}
	return s
}

// TruncateMiddle shortens s to at most width cells by replacing its middle
// with "…", keeping its start and its end, which is where similar names
// (versioned image references, generated volume names) usually differ. s
// must hold no escape sequences (see Clean).
func TruncateMiddle(s string, width int) string {
	sw := ansi.StringWidth(s)
	if sw <= width {
		return s
	}
	if width < 3 {
		return Truncate(s, width)
	}
	tail := (width - 1) / 2
	head := width - 1 - tail
	out := ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, sw-tail, "")
	// A wide rune split at either cut can leave the result a cell over.
	if ansi.StringWidth(out) > width {
		return Truncate(s, width)
	}
	return out
}

// NewInput returns a single-line text input with drydock's styling and
// length limit, showing prompt before the text. It is not focused.
func NewInput(prompt, placeholder string) textinput.Model {
	in := textinput.New()
	in.Prompt = prompt
	in.Placeholder = placeholder
	in.CharLimit = maxInputLen
	StyleInput(&in)
	return in
}

// StyleInput applies the current Styles to in: the text input defaults for
// the palette's background, drydock's prompt style and a static cursor.
// Inputs copy their styles when they are styled, so an input that outlives
// a palette change must be styled again (see tea.BackgroundColorMsg).
func StyleInput(in *textinput.Model) {
	st := textinput.DefaultStyles(Styles.Dark)
	st.Focused.Prompt = Styles.InputPrompt
	st.Blurred.Prompt = Styles.InputPrompt
	st.Cursor.Blink = false // a static cursor; no timer while typing
	in.SetStyles(st)
}

func itoa(n int) string { return strconv.Itoa(n) }
