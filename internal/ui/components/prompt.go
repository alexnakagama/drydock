package components

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

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

// NewPrompt returns a focused prompt with the given title and placeholder,
// and the command that starts its cursor.
func NewPrompt(title, placeholder string) (Prompt, tea.Cmd) {
	p := Prompt{title: title, input: NewInput("> ", placeholder)}
	return p, p.input.Focus()
}

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
	inner := dialogInner(maxWidth, 60)
	p.input.SetWidth(max(inner-2, 1))
	body := []string{
		Styles.DialogTitle.Render(Truncate(Clean(p.title), inner)),
		"",
		p.input.View(),
		"",
		Styles.DialogHint.Render(Truncate("enter submit · esc cancel", inner)),
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

// maxConfirmLines bounds the body of a confirmation dialog, so a long list
// (for example many containers) cannot push the hint off screen.
const maxConfirmLines = 12

// View renders the dialog at most maxWidth cells wide.
func (c Confirm) View(maxWidth int) string {
	inner := dialogInner(maxWidth, 70)
	lines := []string{Styles.DialogTitle.Render(Truncate(Clean(c.title), inner))}
	if len(c.body) > 0 {
		lines = append(lines, "")
	}
	for i, l := range c.body {
		if i == maxConfirmLines-1 && len(c.body) > maxConfirmLines {
			lines = append(lines, Truncate("… and "+itoa(len(c.body)-i)+" more", inner))
			break
		}
		lines = append(lines, Truncate(Clean(l), inner))
	}
	lines = append(lines, "", Styles.DialogHint.Render(Truncate("y confirm · any other key cancels", inner)))
	return Styles.Dialog.Width(inner + dialogChrome).Render(strings.Join(lines, "\n"))
}

// dialogChrome is the width taken by a dialog's border and padding.
const dialogChrome = 4

// dialogInner returns a dialog's content width: preferred, shrunk to fit in
// maxWidth.
func dialogInner(maxWidth, preferred int) int {
	return max(min(preferred, maxWidth-dialogChrome), 1)
}
