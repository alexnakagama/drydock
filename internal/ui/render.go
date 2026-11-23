package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// chromeLines is the number of lines around the content area: the header,
// the status line and the footer.
const chromeLines = 3

// contentSize returns the size of the area views render into.
func (m *Model) contentSize() (int, int) {
	return m.width, max(m.height-chromeLines, 0)
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "drydock"
	return v
}

// render draws the full screen: header, content (with any overlay on top),
// status line and footer, each fitted to the terminal width.
func (m *Model) render() string {
	if m.quitting || len(m.stack) == 0 || m.width <= 0 || m.height <= 0 {
		return ""
	}
	_, ch := m.contentSize()
	content := m.top().View()
	if dialog := m.overlayView(); dialog != "" {
		content = place(content, dialog, m.width, ch)
	}

	lines := make([]string, 0, m.height)
	lines = append(lines, m.headerView())
	if ch > 0 {
		lines = append(lines, padLines(content, m.width, ch)...)
	}
	lines = append(lines, m.statusView(), m.footerView())
	// A terminal shorter than the chrome shows only what fits, header first.
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

// headerView renders the breadcrumb, the top view's indicators and the
// connection state.
func (m *Model) headerView() string {
	st := components.Styles
	crumbs := []string{"drydock"}
	for _, e := range m.stack {
		for _, c := range e.view.Crumbs() {
			crumbs = append(crumbs, components.Truncate(components.Clean(c), 40))
		}
	}
	last := len(crumbs) - 1
	left := st.Breadcrumb.Render(strings.Join(crumbs[:last], " > ")+" > ") + st.Crumb.Render(crumbs[last])
	if last == 0 {
		left = st.Crumb.Render(crumbs[0])
	}
	for _, ind := range m.top().Indicators() {
		left += " " + st.Indicator.Render("["+components.Truncate(components.Clean(ind), 30)+"]")
	}

	right := m.connectionView(false)
	gap := m.width - ansi.StringWidth(right) - 1
	if gap < 10 {
		// Too narrow for the full indicator: keep only its symbol.
		right = m.connectionView(true)
		gap = m.width - ansi.StringWidth(right) - 1
		if gap < 1 {
			return components.Fit(right, m.width)
		}
	}
	return components.Fit(" "+left, gap) + right + " "
}

// connectionView renders the connection state, as a single symbol if
// compact. The reconnection delay is not shown: it would not count down
// between the store's reports.
func (m *Model) connectionView(compact bool) string {
	st := components.Styles
	symbol, text, style := "◌", "◌ connecting", st.Connecting
	switch m.conn.State {
	case store.StateConnected:
		symbol, text, style = "●", "● connected", st.Connected
	case store.StateDisconnected:
		symbol, text, style = "✗", "✗ disconnected · retrying", st.Disconnected
	}
	if compact {
		return style.Render(symbol)
	}
	return style.Render(text)
}

// statusView renders the command bar while it is open. Otherwise it shows,
// side by side, an error message, the in-progress indicator and a success
// message, whichever are present; an error comes first so a long indicator
// cannot push it off screen. With none of those it shows why the engine is
// unreachable, if it is.
func (m *Model) statusView() string {
	st := components.Styles
	if m.overlay == overlayCommand {
		return components.Fit(m.command.View(), m.width)
	}
	var parts []string
	if m.status.kind == statusError {
		parts = append(parts, st.StatusError.Render("✗ "+components.Clean(m.status.text)))
	}
	if len(m.tasks) > 0 {
		t := m.tasks[len(m.tasks)-1]
		text := components.Clean(t.text)
		if n := len(m.tasks); n > 1 {
			text += fmt.Sprintf(" (+%d more)", n-1)
		}
		parts = append(parts, st.StatusProgress.Render(spinnerFrames[m.frame]+" "+text))
	}
	if m.status.kind == statusSuccess {
		parts = append(parts, st.StatusSuccess.Render("✓ "+components.Clean(m.status.text)))
	}
	if len(parts) == 0 && m.conn.State == store.StateDisconnected && m.conn.Reason != "" {
		parts = append(parts, st.StatusError.Render("engine unreachable: "+components.Clean(m.conn.Reason)))
	}
	return components.Fit(" "+strings.Join(parts, "  ·  "), m.width)
}

// footerView renders the key hints: the text-input keys while an input is
// focused, otherwise the top view's keys followed by the global ones.
func (m *Model) footerView() string {
	var bs []key.Binding
	switch {
	case m.overlay == overlayCommand || m.overlay == overlayPrompt || m.top().InputFocused():
		bs = []key.Binding{keys.Input.Submit, keys.Input.Cancel}
	case m.overlay == overlayHelp:
		bs = []key.Binding{keys.Global.Help, keys.Global.Back}
		bs[1].SetHelp("esc", "close")
	default:
		bs = slices.Concat(m.top().KeyHelp(), []key.Binding{keys.Global.Command, keys.Global.Help, keys.Global.Quit})
	}
	return components.Fit(" "+hints(bs), m.width)
}

func hints(bs []key.Binding) string {
	st := components.Styles
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		h := b.Help()
		if !b.Enabled() || h.Key == "" {
			continue
		}
		parts = append(parts, st.FooterKey.Render(h.Key)+" "+st.FooterDesc.Render(h.Desc))
	}
	return strings.Join(parts, "  ")
}

// overlayView renders the open dialog, or "" if none is open (the command
// bar is rendered in the status line instead).
func (m *Model) overlayView() string {
	switch m.overlay {
	case overlayHelp:
		return m.helpView()
	case overlayConfirm:
		return m.confirm.View(m.width)
	case overlayPrompt:
		return m.prompt.View(m.width)
	}
	return ""
}

func (m *Model) helpView() string {
	st := components.Styles
	section := func(title string, bs []key.Binding) []string {
		out := []string{st.HelpSection.Render(title)}
		for _, b := range bs {
			h := b.Help()
			if !b.Enabled() || h.Key == "" {
				continue
			}
			out = append(out, st.HelpKey.Render(components.Fit(h.Key, 8))+" "+h.Desc)
		}
		return out
	}
	viewName := "View"
	if crumbs := m.top().Crumbs(); len(crumbs) > 0 {
		viewName = capitalize(components.Truncate(components.Clean(crumbs[len(crumbs)-1]), 30))
	}
	lines := section("Global", keys.Global.Bindings())
	if vk := m.top().KeyHelp(); len(vk) > 0 {
		lines = append(lines, "")
		lines = append(lines, section(viewName, vk)...)
	}
	lines = append(lines, "", st.DialogHint.Render("? or esc to close"))
	return st.Dialog.Render(strings.Join(lines, "\n"))
}

// place draws dialog centred over content, which is width x height cells.
// A dialog larger than the area is clipped.
func place(content, dialog string, width, height int) string {
	dw, dh := lipgloss.Width(dialog), lipgloss.Height(dialog)
	x, y := max((width-dw)/2, 0), max((height-dh)/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(content),
		lipgloss.NewLayer(dialog).X(x).Y(y).Z(1),
	).Render()
}

// padLines splits s into exactly height lines of exactly width cells.
func padLines(s string, width, height int) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i, l := range lines {
		lines[i] = components.Fit(l, width)
	}
	for len(lines) < height {
		lines = append(lines, strings.Repeat(" ", width))
	}
	return lines
}

// capitalize upper-cases the first letter of s.
func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}
