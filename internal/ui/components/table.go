package components

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// columnGap is the number of spaces between table columns.
const columnGap = 2

// Column describes a table column.
type Column struct {
	Title string
	// Width is the column's width in cells. For a Flex column it is the
	// minimum width.
	Width int
	// Flex columns share the width left over by the fixed columns.
	Flex bool
	// Filter marks the column as searched by the list filter.
	Filter bool
}

// Row is one table row.
type Row struct {
	// Key identifies the resource shown by the row (for example a container
	// ID). It keeps the selection on the same resource when the rows are
	// replaced, so it must be unique within the table.
	Key string
	// Cells holds one value per column. Values are untrusted: the table
	// cleans them (see Clean) before rendering.
	Cells []string
}

// Table is a scrollable, filterable list with a selection that follows its
// resource across refreshes. Views embed it and forward every message to
// Update (key presses, pastes and the filter input's cursor blinks). The
// zero value is not usable; create one with NewTable.
type Table struct {
	cols      []Column
	all       []Row
	visible   []Row
	lowered   [][]string // lower-cased filter cells of all, by row
	cursor    int
	offset    int
	width     int
	height    int
	emptyText string

	filter    string // applied filter, as typed
	filtering bool   // filter input focused
	input     textinput.Model
}

// NewTable returns an empty table with the given columns.
func NewTable(cols []Column) Table {
	return Table{
		cols:      cols,
		emptyText: "No items.",
		input:     NewInput("/", "filter"),
	}
}

// SetEmptyText sets the message shown when the table has no rows and no
// filter is active.
func (t *Table) SetEmptyText(s string) { t.emptyText = s }

// SetSize sets the area the table renders into, including its header line
// and, while filtering, the filter input line.
func (t *Table) SetSize(width, height int) {
	t.width, t.height = max(width, 0), max(height, 0)
	t.input.SetWidth(max(t.width-2, 1))
	t.clampOffset()
}

// SetRows replaces the rows. The selection stays on the row with the same
// key if it is still listed (and matches the filter); otherwise it moves to
// the nearest remaining row, at the same position or the last one. The
// table keeps its own copy of rows (the Cells slices are shared and must not
// be modified afterwards).
func (t *Table) SetRows(rows []Row) {
	t.all = slices.Clone(rows)
	t.lowered = make([][]string, len(rows))
	for i, r := range rows {
		for c, col := range t.cols {
			if col.Filter && c < len(r.Cells) {
				t.lowered[i] = append(t.lowered[i], strings.ToLower(Clean(r.Cells[c])))
			}
		}
	}
	t.refilter(false)
}

// Selected returns the selected row, or false if the table shows no rows.
func (t *Table) Selected() (Row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.visible) {
		return Row{}, false
	}
	return t.visible[t.cursor], true
}

// Select moves the selection to the row with the given key, if it is
// visible, and reports whether it did.
func (t *Table) Select(rowKey string) bool {
	for i, r := range t.visible {
		if r.Key == rowKey {
			t.cursor = i
			t.clampOffset()
			return true
		}
	}
	return false
}

// Len returns the number of rows shown (after filtering).
func (t *Table) Len() int { return len(t.visible) }

// Filter returns the applied filter text, or "" if none.
func (t *Table) Filter() string { return t.filter }

// InputFocused reports whether the filter input has the keyboard, in which
// case every printable key is filter text.
func (t *Table) InputFocused() bool { return t.filtering }

// Back handles Esc when the filter input is not focused: it clears an
// applied filter and reports true, or reports false if there is nothing to
// clear.
func (t *Table) Back() bool {
	if t.filtering {
		t.stopFiltering(true)
		return true
	}
	if t.filter == "" {
		return false
	}
	t.setFilter("")
	return true
}

// ClearFilter removes the filter and closes the filter input.
func (t *Table) ClearFilter() {
	t.filtering = false
	t.input.Blur()
	t.input.Reset()
	t.setFilter("")
}

// Update handles navigation and filter keys. It reports whether msg was a
// key the table consumed; unconsumed keys are left to the view.
func (t *Table) Update(msg tea.Msg) (bool, tea.Cmd) {
	if t.filtering {
		return t.updateFilter(msg)
	}
	kmsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	km := keys.Table
	switch {
	case key.Matches(kmsg, km.Up):
		t.move(-1)
	case key.Matches(kmsg, km.Down):
		t.move(1)
	case key.Matches(kmsg, km.Top):
		t.move(-len(t.visible))
	case key.Matches(kmsg, km.Bottom):
		t.move(len(t.visible))
	case key.Matches(kmsg, km.PageUp):
		t.move(-max(t.bodyHeight(), 1))
	case key.Matches(kmsg, km.PageDown):
		t.move(max(t.bodyHeight(), 1))
	case key.Matches(kmsg, km.Filter):
		t.filtering = true
		t.input.SetValue(t.filter)
		t.input.CursorEnd()
		t.clampOffset()
		return true, t.input.Focus()
	default:
		return false, nil
	}
	return true, nil
}

// updateFilter handles a message while the filter input is focused. Every
// key is consumed: Enter keeps the filter and closes the input, Esc clears
// the filter, and anything else edits it.
func (t *Table) updateFilter(msg tea.Msg) (bool, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(kmsg, keys.Input.Submit):
			t.stopFiltering(false)
			return true, nil
		case key.Matches(kmsg, keys.Input.Cancel):
			t.stopFiltering(true)
			return true, nil
		}
	}
	var cmd tea.Cmd
	t.input, cmd = UpdateInput(t.input, msg)
	if v := t.input.Value(); v != t.filter {
		t.setFilter(v)
	}
	_, isKey := msg.(tea.KeyPressMsg)
	_, isPaste := msg.(tea.PasteMsg)
	return isKey || isPaste, cmd
}

func (t *Table) stopFiltering(clear bool) {
	t.filtering = false
	t.input.Blur()
	if clear {
		t.input.Reset()
		t.setFilter("")
	} else {
		t.setFilter(strings.TrimSpace(t.filter))
	}
	t.clampOffset()
}

func (t *Table) setFilter(f string) {
	if f == t.filter {
		return
	}
	t.filter = f
	t.refilter(true)
}

// refilter recomputes the visible rows. The selection follows its key; if
// the key is gone it falls back to the first row when the filter changed
// (the list was narrowed or widened by the user), or to the nearest row
// otherwise (the resource disappeared).
func (t *Table) refilter(filterChanged bool) {
	prev, hadPrev := t.Selected()
	prevIdx := t.cursor

	needle := strings.ToLower(strings.TrimSpace(t.filter))
	t.visible = t.visible[:0:0]
	for i, r := range t.all {
		if needle == "" || matches(t.lowered[i], needle) {
			t.visible = append(t.visible, r)
		}
	}

	switch {
	case hadPrev && t.Select(prev.Key):
		return
	case filterChanged:
		t.cursor = 0
	default:
		t.cursor = min(prevIdx, len(t.visible)-1)
	}
	t.cursor = max(t.cursor, 0)
	t.clampOffset()
}

func matches(cells []string, needle string) bool {
	for _, c := range cells {
		if strings.Contains(c, needle) {
			return true
		}
	}
	return false
}

func (t *Table) move(delta int) {
	if len(t.visible) == 0 {
		return
	}
	t.cursor = min(max(t.cursor+delta, 0), len(t.visible)-1)
	t.clampOffset()
}

// bodyHeight is the number of row lines: the area minus the column header
// and, while filtering, the filter input.
func (t *Table) bodyHeight() int {
	h := t.height - 1
	if t.filtering {
		h--
	}
	return max(h, 0)
}

// clampOffset scrolls so that the cursor is visible.
func (t *Table) clampOffset() {
	body := t.bodyHeight()
	if body <= 0 {
		t.offset = t.cursor
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+body {
		t.offset = t.cursor - body + 1
	}
	t.offset = max(min(t.offset, len(t.visible)-body), 0)
}

// widths computes the rendered column widths for the current table width.
func (t *Table) widths() []int {
	ws := make([]int, len(t.cols))
	used := columnGap * max(len(t.cols)-1, 0)
	flex := 0
	for i, c := range t.cols {
		ws[i] = max(c.Width, 1)
		used += ws[i]
		if c.Flex {
			flex++
		}
	}
	if extra := t.width - used; extra > 0 && flex > 0 {
		share, rem := extra/flex, extra%flex
		for i, c := range t.cols {
			if c.Flex {
				ws[i] += share
				if rem > 0 {
					ws[i]++
					rem--
				}
			}
		}
	}
	return ws
}

func (t *Table) renderLine(cells []string, ws []int) string {
	var b strings.Builder
	for i, w := range ws {
		if i > 0 {
			b.WriteString(strings.Repeat(" ", columnGap))
		}
		var v string
		if i < len(cells) {
			v = Clean(cells[i])
		}
		b.WriteString(Fit(v, w))
	}
	return Fit(b.String(), t.width)
}

// View renders the table into exactly its height (lines) and width (cells).
func (t *Table) View() string {
	if t.width <= 0 || t.height <= 0 {
		return ""
	}
	if t.filtering && t.height == 1 {
		// No room for the header: the input being typed in wins.
		return Fit(t.input.View(), t.width)
	}
	ws := t.widths()
	titles := make([]string, len(t.cols))
	for i, c := range t.cols {
		titles[i] = c.Title
	}
	lines := make([]string, 0, t.height)
	lines = append(lines, Styles.TableHeader.Render(t.renderLine(titles, ws)))

	body := t.bodyHeight()
	if len(t.visible) == 0 && body > 0 {
		msg := t.emptyText
		if t.filter != "" {
			msg = "No rows match the filter " + Quote(t.filter) + "."
		}
		lines = append(lines, Styles.Empty.Render(Fit(" "+Clean(msg), t.width)))
	}
	for i := t.offset; i < len(t.visible) && i < t.offset+body; i++ {
		line := t.renderLine(t.visible[i].Cells, ws)
		if i == t.cursor {
			line = Styles.TableSelected.Render(line)
		}
		lines = append(lines, line)
	}
	for len(lines) < t.height-boolInt(t.filtering) {
		lines = append(lines, strings.Repeat(" ", t.width))
	}
	if t.filtering {
		lines = append(lines, Fit(t.input.View(), t.width))
	}
	return strings.Join(lines[:min(len(lines), t.height)], "\n")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Quote wraps untrusted text in double quotes for display, cleaned and
// shortened so that a long value cannot push the message off screen.
func Quote(s string) string {
	return `"` + Truncate(Clean(s), 40) + `"`
}
