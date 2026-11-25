package components

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// Outcome is the result of a key handled by a dialog.
type Outcome int

const (
	// Pending means the dialog is still open.
	Pending Outcome = iota
	// Accepted means the user submitted the prompt or confirmed the dialog.
	Accepted
	// Cancelled means the user dismissed the dialog.
	Cancelled
)

// Prompt is a single-line text prompt: Enter submits, Esc cancels.
type Prompt struct {
	title string
	input textinput.Model
}

// promptHint is the key hint shown at the bottom of a prompt.
const promptHint = "enter submit · esc cancel"

// NewPrompt returns a focused prompt with the given title and placeholder,
// and the command that starts its cursor.
func NewPrompt(title, placeholder string) (Prompt, tea.Cmd) {
	// The placeholder is cleaned like every other shown text, so the width
	// measured for it is the width rendered.
	p := Prompt{title: title, input: NewInput("> ", Clean(placeholder))}
	return p, p.input.Focus()
}

// SetMaxWidth sizes the prompt's input for a dialog at most maxWidth cells
// wide. The input scrolls its text as it is edited, which needs its width
// before keys reach it, so call this when the prompt opens and whenever the
// terminal is resized.
func (p *Prompt) SetMaxWidth(maxWidth int) {
	fitInput(&p.input, p.inputWidth(p.inner(maxWidth)))
}

// fitInput sets in's width and scrolls its text so the cursor is visible
// within it. The input only adjusts its scroll window as the cursor moves,
// so after a width change the window is rebuilt: from the start of the
// text, then forward to the cursor.
func fitInput(in *textinput.Model, width int) {
	in.SetWidth(width)
	pos := in.Position()
	in.CursorStart()
	in.SetCursor(pos)
}

// inner returns the content width of the prompt's dialog: wide enough for
// its title, hint and placeholder, within the dialog width bounds.
func (p Prompt) inner(maxWidth int) int {
	content := max(
		ansi.StringWidth(Clean(p.title)),
		ansi.StringWidth(promptHint),
		ansi.StringWidth(p.input.Prompt)+ansi.StringWidth(Clean(p.input.Placeholder)),
	)
	return dialogWidth(content, maxWidth)
}

// inputWidth returns the width of the input's text area in a dialog of the
// given content width: what is left after the prompt and the cursor cell.
func (p Prompt) inputWidth(inner int) int {
	return max(inner-ansi.StringWidth(p.input.Prompt)-1, 1)
}

// Restyle applies the current Styles to the prompt's input, after a
// palette change.
func (p *Prompt) Restyle() { StyleInput(&p.input) }

// Value returns the text typed so far, with surrounding spaces removed.
func (p Prompt) Value() string { return strings.TrimSpace(p.input.Value()) }

// Update handles msg and reports whether the prompt was submitted or
// cancelled.
func (p Prompt) Update(msg tea.Msg) (Prompt, Outcome, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(kmsg, keys.Input.Submit):
			return p, Accepted, nil
		case key.Matches(kmsg, keys.Input.Cancel):
			return p, Cancelled, nil
		}
	}
	var cmd tea.Cmd
	p.input, cmd = UpdateInput(p.input, msg)
	return p, Pending, cmd
}

// View renders the prompt as a dialog at most maxWidth cells wide.
func (p Prompt) View(maxWidth int) string {
	inner := p.inner(maxWidth)
	if w := p.inputWidth(inner); w != p.input.Width() {
		// Not sized for this width yet (see SetMaxWidth): size this copy.
		fitInput(&p.input, w)
	}
	body := []string{
		Styles.DialogTitle.Render(Truncate(Clean(p.title), inner)),
		"",
		Truncate(p.input.View(), inner),
		"",
		Styles.DialogHint.Render(Truncate(promptHint, inner)),
	}
	return Styles.Dialog.Width(inner + dialogChrome).Render(strings.Join(body, "\n"))
}

// Confirm is a confirmation dialog for a destructive action: only `y`
// confirms, and every other key cancels.
type Confirm struct {
	title string
	body  []string
}

// NewConfirm returns a confirmation dialog. title names the action and body
// describes what it affects, one entry per line; both are untrusted and are
// cleaned before rendering.
func NewConfirm(title string, body ...string) Confirm {
	return Confirm{title: title, body: body}
}

// Update handles a key: `y` confirms, any other key cancels. Messages other
// than key presses leave the dialog open.
func (c Confirm) Update(msg tea.Msg) Outcome {
	kmsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return Pending
	}
	if key.Matches(kmsg, keys.Confirm.Yes) {
		return Accepted
	}
	return Cancelled
}

// maxConfirmLines bounds the body of a confirmation dialog, in lines, so a
// long list (for example many containers) cannot push the hint off screen.
const maxConfirmLines = 12

// maxConfirmTitleLines bounds the title of a confirmation dialog, in lines.
const maxConfirmTitleLines = 2

// View renders the dialog at most maxWidth cells wide, with the error-colored
// border that marks a destructive action. The title and body wrap at spaces,
// so what the action does and what it affects are never cut off; a single
// word wider than the dialog (such as a long image reference) is shortened
// in the middle, keeping both ends, which are what tell similar names apart.
func (c Confirm) View(maxWidth int) string {
	title := Clean(c.title)
	// Each body entry takes at least one line, so only the first
	// maxConfirmLines entries can be shown, and only they are measured.
	shown := make([]string, min(len(c.body), maxConfirmLines))
	content := max(ansi.StringWidth(title), ansi.StringWidth(confirmHint))
	for i := range shown {
		shown[i] = Clean(c.body[i])
		content = max(content, ansi.StringWidth(shown[i]))
	}
	inner := dialogWidth(content, maxWidth)

	var lines []string
	for _, l := range confirmTitle(title, inner) {
		lines = append(lines, Styles.DialogTitle.Render(l))
	}
	if len(c.body) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, confirmBody(shown, len(c.body), inner)...)
	lines = append(lines, "", Styles.DialogHint.Render(Truncate(confirmHint, inner)))
	return Styles.ConfirmDialog.Width(inner + dialogChrome).Render(strings.Join(lines, "\n"))
}

// confirmTitle returns the lines of a confirmation's cleaned title, wrapped
// to width and capped at maxConfirmTitleLines; text past the cap is cut
// with an ellipsis.
func confirmTitle(title string, width int) []string {
	lines := wrapText(title, width)
	if len(lines) <= maxConfirmTitleLines {
		return lines
	}
	last := maxConfirmTitleLines - 1
	lines[last] = Truncate(lines[last]+" "+strings.Join(lines[last+1:], " "), width)
	return lines[:maxConfirmTitleLines]
}

// confirmBody returns the lines of a confirmation's body, each at most width
// cells wide: the cleaned entries wrapped by wrapText. total is the number
// of body entries, of which entries are the first. When the lines exceed
// maxConfirmLines, the last line shown counts the entries left out or shown
// only in part.
func confirmBody(entries []string, total, width int) []string {
	var (
		lines []string
		owner []int // owner[i] is the index of the entry lines[i] belongs to
	)
	for i, e := range entries {
		for _, l := range wrapText(e, width) {
			lines = append(lines, l)
			owner = append(owner, i)
		}
	}
	if len(lines) <= maxConfirmLines && len(entries) == total {
		return lines
	}
	keep := min(len(lines), maxConfirmLines-1)
	// Entries cut short count as left out: the first is the entry of the
	// first line dropped, or the entry after the last line kept.
	first := len(entries)
	if keep < len(lines) {
		first = owner[keep]
	}
	more := Truncate("… and "+itoa(total-first)+" more", width)
	return append(lines[:keep], more)
}

// wrapText wraps cleaned text s at spaces into lines at most width cells
// wide. A line still wider than width, a single long word, is shortened in
// the middle by TruncateMiddle.
func wrapText(s string, width int) []string {
	lines := strings.Split(ansi.Wordwrap(s, width, ""), "\n")
	for i, l := range lines {
		lines[i] = TruncateMiddle(l, width)
	}
	return lines
}

// confirmHint is the key hint shown at the bottom of a confirmation.
const confirmHint = "y confirm · any other key cancels"

// dialogChrome is the width taken by a dialog's border and padding.
const dialogChrome = 4

// Bounds on a dialog's total width, border included.
const (
	minDialogWidth = 40
	maxDialogWidth = 60
)

// dialogWidth returns the content width of a dialog whose widest content
// line is content cells: the total width is fitted to the content, clamped
// to [minDialogWidth, maxDialogWidth] and then to maxWidth, the terminal
// width. The result is at least 1, so on a terminal narrower than
// dialogChrome+1 cells the dialog is wider than the terminal; the screen
// compositor clips it there.
func dialogWidth(content, maxWidth int) int {
	total := min(max(content+dialogChrome, minDialogWidth), maxDialogWidth, maxWidth)
	return max(total-dialogChrome, 1)
}
