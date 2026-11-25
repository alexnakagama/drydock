package components

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/keys"
)

const (
	// columnGap is the number of spaces between table columns.
	columnGap = 2
	// gutterWidth is the number of cells left of the first column, on every
	// line, where the selected row shows its marker.
	gutterWidth = 2
	// selectionMarker marks the selected row in the gutter, so the
	// selection is visible without color.
	selectionMarker = "▌"
)

// Column describes a table column.
type Column struct {
	Title string
	// Width is the column's width in cells. For a Flex column it is the
	// minimum width.
	Width int
	// Flex columns grow from Width toward their natural width: the widest
	// of their title and the cells of the listed rows. Width left over once
	// every flex column fits goes to the last flex column.
	Flex bool
	// Filter marks the column as searched by the list filter.
	Filter bool
}

// Tone colors an unselected row by its resource's state. The selected row
// is always rendered with the selection style alone.
type Tone uint8

// Row tones. The zero value is ToneDefault.
const (
	ToneDefault Tone = iota // the terminal's default color
	ToneGood                // healthy, for example a running container
	ToneWarn                // needs attention, for example a paused container
	ToneDim                 // inactive, for example an exited container
)

// Row is one table row.
type Row struct {
	// Key identifies the resource shown by the row (for example a container
	// ID). It keeps the selection on the same resource when the rows are
	// replaced, so it must be unique within the table.
	Key string
	// Cells holds one value per column. Values are untrusted: the table
	// cleans them (see Clean) before rendering.
	Cells []string
	// Tone colors the row while it is not selected.
	Tone Tone
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
	cellWidth [][]int    // rendered width of each cleaned cell of all, by row
	natural   []int      // natural width of each column over visible
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
	t.cellWidth = make([][]int, len(rows))
	for i, r := range rows {
		n := min(len(r.Cells), len(t.cols))
		t.cellWidth[i] = make([]int, n)
		for c := range n {
			clean := Clean(r.Cells[c])
			t.cellWidth[i][c] = ansi.StringWidth(clean)
			if t.cols[c].Filter {
				t.lowered[i] = append(t.lowered[i], strings.ToLower(clean))
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

// Position reports where the selection is: selected is the 1-based index
// of the selected row among the listed rows (0 when none is listed), listed
// is the number of rows shown after filtering, total is the number of rows
// before filtering, and filtered reports whether a non-blank filter is
// applied (including one still being typed).
func (t *Table) Position() (selected, listed, total int, filtered bool) {
	listed = len(t.visible)
	if listed > 0 {
		selected = t.cursor + 1
	}
	// Match refilter: a whitespace-only filter (possible while typing)
	// narrows nothing, so it does not count as applied.
	return selected, listed, len(t.all), strings.TrimSpace(t.filter) != ""
}

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
	if _, ok := msg.(tea.BackgroundColorMsg); ok {
		// The palette changed; the filter input copied the old one.
		StyleInput(&t.input)
		return false, nil
	}
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

// refilter recomputes the visible rows and the columns' natural widths. The
// selection follows its key; if the key is gone it falls back to the first
// row when the filter changed (the list was narrowed or widened by the
// user), or to the nearest row otherwise (the resource disappeared).
func (t *Table) refilter(filterChanged bool) {
	prev, hadPrev := t.Selected()
	prevIdx := t.cursor

	needle := strings.ToLower(strings.TrimSpace(t.filter))
	t.visible = t.visible[:0:0]
	t.natural = make([]int, 0, len(t.cols)) // fresh: a copied Table must not share it
	for _, c := range t.cols {
		t.natural = append(t.natural, max(c.Width, 1, ansi.StringWidth(Clean(c.Title))))
	}
	for i, r := range t.all {
		if needle == "" || matches(t.lowered[i], needle) {
			t.visible = append(t.visible, r)
			for c, w := range t.cellWidth[i] {
				t.natural[c] = max(t.natural[c], w)
			}
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

// widths computes the rendered column widths for the current table width,
// less the gutter. Every column gets its minimum width (Column.Width). Any
// spare width grows the flex columns toward their natural width, one cell
// at a time in turn, and what is left after that goes to the last flex
// column. When even the minimums do not fit, the line is cut (renderLine
// fits it to the width).
func (t *Table) widths() []int {
	ws := make([]int, len(t.cols))
	used := columnGap * max(len(t.cols)-1, 0)
	var flex []int // indexes of the flex columns, in column order
	for i, c := range t.cols {
		ws[i] = max(c.Width, 1)
		used += ws[i]
		if c.Flex {
			flex = append(flex, i)
		}
	}
	extra := t.width - gutterWidth - used
	if extra <= 0 || len(flex) == 0 {
		return ws
	}
	extra = t.growToNatural(ws, flex, extra)
	ws[flex[len(flex)-1]] += extra
	return ws
}

// growToNatural water-fills up to extra cells into the flex columns of ws,
// none past its natural width, and returns the cells it did not use. The
// result is the same as handing out one cell at a time, left to right, to
// the columns still below their natural width.
func (t *Table) growToNatural(ws, flex []int, extra int) int {
	need := func(i int) int {
		if i >= len(t.natural) {
			return 0 // not measured yet: no rows were ever set
		}
		return max(t.natural[i]-ws[i], 0)
	}
	// Columns by growth need, smallest first; ties keep column order.
	order := slices.Clone(flex)
	slices.SortStableFunc(order, func(a, b int) int { return need(a) - need(b) })
	for k, i := range order {
		n := len(order) - k // columns still growing, including i
		if d := need(i); d*n <= extra {
			// i reaches its natural width before the others stop growing.
			ws[i] += d
			extra -= d
			continue
		}
		// Every remaining column needs more than an equal share: they all
		// get the share, and the leftmost ones one cell more each.
		rest := order[k:]
		slices.Sort(rest)
		share, rem := extra/n, extra%n
		for j, c := range rest {
			ws[c] += share
			if j < rem {
				ws[c]++
			}
		}
		return 0
	}
	return extra
}

// renderLine renders cells in columns of widths ws, fitted to the table
// width less the gutter. View adds the gutter.
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
	return Fit(b.String(), t.width-gutterWidth)
}

// gutter prepends the gutter to a rendered line: the selection marker and
// the start of the selection bar on the selected row, blanks otherwise. On
// a table narrower than the gutter, only the part of the gutter that fits
// is returned.
func (t *Table) gutter(line string, selected bool) string {
	if t.width < gutterWidth {
		if selected {
			return Fit(Styles.SelectionMarker.Render(selectionMarker), t.width)
		}
		return strings.Repeat(" ", t.width)
	}
	if selected {
		return Styles.SelectionMarker.Render(selectionMarker) +
			Styles.TableSelected.Render(" "+line)
	}
	return strings.Repeat(" ", gutterWidth) + line
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
	lines = append(lines, t.gutter(Styles.TableHeader.Render(t.renderLine(titles, ws)), false))

	body := t.bodyHeight()
	if len(t.visible) == 0 && body > 0 {
		lines = append(lines, t.emptyLines(body)...)
	}
	for i := t.offset; i < len(t.visible) && i < t.offset+body; i++ {
		r := t.visible[i]
		line := t.renderLine(r.Cells, ws)
		if i == t.cursor {
			// The selection bar is drawn over the untoned line: the tone's
			// color is not shown on the selected row, whose state stays
			// readable from its text (for example a state glyph).
			line = t.gutter(line, true)
		} else {
			line = t.gutter(toneStyle(r.Tone, line), false)
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

// emptyLines renders the body area of a table with no visible rows: the
// empty-list or no-match message, truncated to the table width and centered
// in body lines of the full table width.
func (t *Table) emptyLines(body int) []string {
	msg := t.emptyText
	if t.filter != "" {
		msg = "No rows match the filter " + Quote(t.filter) + "."
	}
	msg = Styles.Empty.Render(Truncate(Clean(msg), t.width))
	placed := strings.Split(lipgloss.Place(t.width, body, lipgloss.Center, lipgloss.Center, msg), "\n")
	for i, l := range placed {
		placed[i] = Fit(l, t.width) // Place leaves the width alone if nothing is to be added
	}
	return placed
}

// toneStyle renders an unselected row line in its tone's style. Unknown
// tones render like ToneDefault.
func toneStyle(tone Tone, line string) string {
	switch tone {
	case ToneGood:
		return Styles.RowGood.Render(line)
	case ToneWarn:
		return Styles.RowWarn.Render(line)
	case ToneDim:
		return Styles.RowDim.Render(line)
	default:
		return line
	}
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
