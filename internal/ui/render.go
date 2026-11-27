package ui

import (
	"fmt"
	"slices"
	"strconv"
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

// baseChromeLines is the number of lines around the content area that are
// always drawn: the header, the status line and the footer. The resource
// tab strip adds one more on terminals of at least minTabsHeight lines,
// and the rule below it another on terminals of at least minRuleHeight
// lines.
const (
	baseChromeLines = 3
	minTabsHeight   = 5
	minRuleHeight   = 12
)

// showTabs reports whether the resource tab strip is drawn. It is left out
// on very short terminals, so the header, status line and footer keep
// their lines.
func (m *Model) showTabs() bool { return m.height >= minTabsHeight }

// showRule reports whether the rule below the tab strip is drawn. Short
// terminals keep that line for the content instead.
func (m *Model) showRule() bool { return m.showTabs() && m.height >= minRuleHeight }

// chromeLines returns the number of lines around the content area.
func (m *Model) chromeLines() int {
	n := baseChromeLines
	if m.showTabs() {
		n++
	}
	if m.showRule() {
		n++
	}
	return n
}

// contentSize returns the size of the area views render into.
func (m *Model) contentSize() (int, int) {
	return m.width, max(m.height-m.chromeLines(), 0)
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "drydock"
	return v
}

// render draws the full screen: header, resource tabs, the rule below
// them, content (with any overlay on top), status line and footer, each
// fitted to the terminal width.
func (m *Model) render() string {
	if m.quitting || len(m.stack) == 0 || m.width <= 0 || m.height <= 0 {
		return ""
	}
	_, ch := m.contentSize()
	// The body is the content area and the rule above it. An overlay is
	// centred over the whole body: the rule is decoration only, so a tall
	// dialog (the help overlay at 80x24) may cover it rather than be cut.
	var body []string
	if m.showRule() {
		body = append(body, components.Styles.Rule.Render(strings.Repeat("─", m.width)))
	}
	if ch > 0 {
		body = append(body, padLines(m.top().View(), m.width, ch)...)
	}
	if dialog := m.overlayView(); dialog != "" && len(body) > 0 {
		body = padLines(place(strings.Join(body, "\n"), dialog, m.width, len(body)), m.width, len(body))
	}

	lines := make([]string, 0, m.height)
	lines = append(lines, m.headerView())
	if m.showTabs() {
		lines = append(lines, m.tabsView())
	}
	lines = append(lines, body...)
	lines = append(lines, m.statusView(), m.footerView())
	// A terminal shorter than the chrome shows only what fits, header first.
	return strings.Join(lines[:min(len(lines), m.height)], "\n")
}

// headerView renders the breadcrumb, the top view's indicators, the
// active context (unless it is the default one) and the connection state
// on the header bar, which spans the full width. While the breadcrumb's
// gap is under 10 cells, the right side shrinks in steps: first the
// context indicator is dropped, then the connection state is shortened to
// its symbol, and as a last resort the breadcrumb goes. Every
// span in the line, padding included, carries the bar background: each
// styled span ends with a full reset, so an unstyled space would leave a
// hole in the bar.
func (m *Model) headerView() string {
	st := components.Styles
	sp := st.HeaderBar.Render(" ")
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
		left += sp + st.Indicator.Render("["+components.Truncate(components.Clean(ind), 30)+"]")
	}

	state := m.connectionView(false)
	right := state
	if m.contextLabel != "" {
		right = st.Indicator.Render(m.contextLabel) + sp + state
	}
	gap := m.width - ansi.StringWidth(right) - 1
	if gap < 10 && right != state {
		// Too narrow for both: the connection state matters more.
		right = state
		gap = m.width - ansi.StringWidth(right) - 1
	}
	if gap < 10 {
		// Too narrow for the full indicator: keep only its symbol.
		right = m.connectionView(true)
		gap = m.width - ansi.StringWidth(right) - 1
		if gap < 1 {
			return fitBar(right, m.width)
		}
	}
	return fitBar(sp+left, gap) + right + sp
}

// fitBar is components.Fit for the header line: s, made only of spans
// carrying the bar background, is cut or padded to exactly width cells,
// with the padding drawn on the bar too. A cut keeps the escape sequences
// after it, so the ellipsis lands inside the span that was cut.
func fitBar(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		s = ansi.Truncate(s, width, "…")
	}
	if pad := width - ansi.StringWidth(s); pad > 0 {
		s += components.Styles.HeaderBar.Render(strings.Repeat(" ", pad))
	}
	return s
}

// tabsView renders the resource tab strip, with the top view's list
// position right-aligned on the same line. The position always wins: the
// tabs get what is left, with each label shortened to its first alias if
// the full labels do not fit, and are clipped if even those do not.
func (m *Model) tabsView() string {
	right := m.positionView()
	if right != "" {
		right += " "
	}
	rw := ansi.StringWidth(right)
	// One leading space and one space between the tabs and the position.
	avail := m.width - rw - 2
	if right == "" {
		avail = m.width - 1
	}
	if avail < 1 {
		// No room for any tab. The position is right-aligned and is only cut
		// (with an ellipsis) if it is wider than the terminal itself.
		return components.Fit(strings.Repeat(" ", max(m.width-rw, 0))+right, m.width)
	}
	tabs := m.tabLabels(false)
	if ansi.StringWidth(tabs) > avail {
		tabs = m.tabLabels(true)
	}
	if right == "" {
		return " " + components.Fit(tabs, avail)
	}
	return " " + components.Fit(tabs, avail) + " " + right
}

// tabLabels renders every resource tab as a pill: its number (if a key
// selects it), its name (or, if short, its first alias) and, once the
// first snapshot has loaded, its count in parentheses, so the count cannot
// be mistaken for the number. The tab of the stack's root resource is a
// filled pill. Active and inactive pills are padded alike, so switching
// tabs never moves the labels, and that padding separates them.
func (m *Model) tabLabels(short bool) string {
	st := components.Styles
	numbered := len(keys.Global.TabKeys())
	parts := make([]string, 0, len(m.resources))
	for i, r := range m.resources {
		var label []string
		if i < numbered {
			label = append(label, strconv.Itoa(i+1))
		}
		name := r.Name
		if short && len(r.Aliases) > 0 {
			name = r.Aliases[0]
		}
		label = append(label, components.Clean(name))
		if r.Count != nil && m.snap.Loaded() {
			label = append(label, "("+strconv.Itoa(r.Count(m.snap))+")")
		}
		style := st.TabPill
		if i == m.rootIdx {
			style = st.TabPillActive
		}
		parts = append(parts, style.Render(strings.Join(label, " ")))
	}
	return strings.Join(parts, " ")
}

// positionView renders the top view's list position, or "" if the top view
// does not report one.
func (m *Model) positionView() string {
	p, ok := m.top().(Positioner)
	if !ok {
		return ""
	}
	return components.Styles.Position.Render(formatPosition(p.Position()))
}

// formatPosition formats a list position as "3/12", or "3/5 of 12" while a
// filter is applied.
func formatPosition(selected, listed, total int, filtered bool) string {
	s := strconv.Itoa(selected) + "/" + strconv.Itoa(listed)
	if filtered {
		s += " of " + strconv.Itoa(total)
	}
	return s
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

// moreHint ends a footer that could not fit every hint; `?` opens the help
// overlay, which lists them all. It is display-only (keys.Global.Help is
// what matches `?`), but needs a key to report itself enabled.
var moreHint = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "more"))

// hintSep separates footer hints.
const hintSep = "  "

// footerView renders the key hints: the text-input keys while an input is
// focused, otherwise the top view's enabled keys followed by the global
// ones. Hints are never cut: if they do not all fit, the global hints are
// dropped, the view's are kept in priority order while they fit, and
// `? more` ends the line.
func (m *Model) footerView() string {
	var bs []key.Binding
	switch {
	case m.overlay == overlayCommand || m.overlay == overlayPrompt || m.top().InputFocused():
		bs = []key.Binding{keys.Input.Submit, keys.Input.Cancel}
	case m.overlay == overlayHelp:
		bs = []key.Binding{keys.Global.Help, keys.Global.Back}
		bs[1].SetHelp("esc", "close")
	default:
		return components.Fit(" "+fitHints(hints(m.top().KeyHelp()), m.width-1), m.width)
	}
	return components.Fit(" "+strings.Join(hints(bs), hintSep), m.width)
}

// fitHints joins the view hints and the global hints if they fit in width
// cells. Otherwise it joins as many leading view hints as fit with
// `? more` after them.
func fitHints(view []string, width int) string {
	global := hints([]key.Binding{keys.Global.Command, keys.Global.Help, keys.Global.Quit})
	if all := strings.Join(slices.Concat(view, global), hintSep); ansi.StringWidth(all) <= width {
		return all
	}
	more := hints([]key.Binding{moreHint})[0]
	used := ansi.StringWidth(more)
	var kept []string
	for _, h := range view {
		w := ansi.StringWidth(h) + len(hintSep)
		if used+w > width {
			break
		}
		kept = append(kept, h)
		used += w
	}
	return strings.Join(append(kept, more), hintSep)
}

// hints renders one footer segment per enabled binding that has help.
func hints(bs []key.Binding) []string {
	st := components.Styles
	parts := make([]string, 0, len(bs))
	for _, b := range bs {
		h := b.Help()
		if !b.Enabled() || h.Key == "" {
			continue
		}
		parts = append(parts, st.FooterKey.Render(components.Clean(h.Key))+" "+st.FooterDesc.Render(components.Clean(h.Desc)))
	}
	return parts
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

// helpKeyWidth is the width of the help overlay's key column.
const helpKeyWidth = 10

func (m *Model) helpView() string {
	st := components.Styles
	// Bindings whose action does not apply right now are listed dimmed.
	section := func(title string, bs []key.Binding) []string {
		out := []string{st.HelpSection.Render(title)}
		for _, b := range bs {
			h := b.Help()
			if h.Key == "" {
				continue
			}
			k, desc := components.Fit(components.Clean(h.Key), helpKeyWidth), components.Clean(h.Desc)
			if b.Enabled() {
				out = append(out, st.HelpKey.Render(k)+" "+desc)
			} else {
				out = append(out, st.FooterDesc.Render(k)+" "+st.FooterDesc.Render(desc))
			}
		}
		return out
	}
	viewName := "View"
	if crumbs := m.top().Crumbs(); len(crumbs) > 0 {
		viewName = capitalize(components.Truncate(components.Clean(crumbs[len(crumbs)-1]), 30))
	}
	global := section("Global", keys.Global.Bindings())
	var view []string
	if vk := m.top().KeyHelp(); len(vk) > 0 {
		view = section(viewName, vk)
	}
	// No blank line before the hint, so the help stays as short as it can.
	hint := st.DialogHint.Render("? or esc to close")

	lines := global
	if len(view) > 0 {
		lines = slices.Concat(global, []string{""}, view)
		// The sections are stacked when they fit the body (the content
		// area and the rule above it) and side by side when only that
		// fits, so a long view section is not cut at 80x24.
		_, ch := m.contentSize()
		if m.showRule() {
			ch++
		}
		fw, fh := st.Dialog.GetHorizontalFrameSize(), st.Dialog.GetVerticalFrameSize()
		left := lipgloss.NewStyle().PaddingRight(helpColumnGap).Render(strings.Join(global, "\n"))
		side := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Join(view, "\n"))
		if len(lines)+1+fh > ch && lipgloss.Width(side)+fw <= m.width {
			lines = strings.Split(side, "\n")
		}
	}
	lines = append(lines, hint)
	return st.Dialog.Render(strings.Join(lines, "\n"))
}

// helpColumnGap is the space between the help overlay's sections when they
// are side by side.
const helpColumnGap = 4

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
