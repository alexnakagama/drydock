package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

func press(t *testing.T, tb *Table, keys ...string) {
	t.Helper()
	for _, k := range keys {
		tb.Update(uitest.KeyPress(k))
	}
}

func typeText(tb *Table, s string) {
	for _, r := range s {
		tb.Update(uitest.KeyPress(string(r)))
	}
}

func newTestTable(rows ...Row) Table {
	tb := NewTable([]Column{
		{Title: "NAME", Width: 10, Filter: true},
		{Title: "IMAGE", Width: 10, Flex: true, Filter: true},
		{Title: "STATUS", Width: 8},
	})
	tb.SetSize(60, 10)
	tb.SetRows(rows)
	return tb
}

func row(key, name, image, status string) Row {
	return Row{Key: key, Cells: []string{name, image, status}}
}

func selectedKey(tb *Table) string {
	r, ok := tb.Selected()
	if !ok {
		return ""
	}
	return r.Key
}

func TestTableNavigation(t *testing.T) {
	tb := newTestTable(
		row("a", "alpha", "img", "up"),
		row("b", "bravo", "img", "up"),
		row("c", "charlie", "img", "up"),
	)
	steps := []struct {
		key  string
		want string
	}{
		{"j", "b"}, {"down", "c"}, {"j", "c"}, // stops at the end
		{"k", "b"}, {"up", "a"}, {"k", "a"}, // stops at the start
		{"G", "c"}, {"g", "a"}, {"end", "c"}, {"home", "a"},
	}
	for i, s := range steps {
		handled, _ := tb.Update(uitest.KeyPress(s.key))
		if !handled {
			t.Fatalf("step %d: key %q not handled", i, s.key)
		}
		if got := selectedKey(&tb); got != s.want {
			t.Fatalf("step %d: after %q selected %q, want %q", i, s.key, got, s.want)
		}
	}
	if handled, _ := tb.Update(uitest.KeyPress("x")); handled {
		t.Error("unbound key x was consumed by the table")
	}
}

func TestTableSelectionSurvivesRefresh(t *testing.T) {
	tb := newTestTable(row("id-db", "db", "postgres", "up"), row("id-web", "web", "nginx", "up"))
	if got := selectedKey(&tb); got != "id-db" {
		t.Fatalf("initial selection %q, want id-db", got)
	}
	// A new container sorting above db appears.
	tb.SetRows([]Row{
		row("id-cache", "cache", "redis", "up"),
		row("id-db", "db", "postgres", "up"),
		row("id-web", "web", "nginx", "up"),
	})
	if got := selectedKey(&tb); got != "id-db" {
		t.Errorf("after refresh selected %q, want id-db", got)
	}
}

func TestTableSelectedRowDisappears(t *testing.T) {
	tb := newTestTable(row("a", "a", "", ""), row("b", "b", "", ""), row("c", "c", "", ""))
	press(t, &tb, "j") // b
	tb.SetRows([]Row{row("a", "a", "", ""), row("c", "c", "", "")})
	if got := selectedKey(&tb); got != "c" {
		t.Errorf("after removing b selected %q, want the row now at its position (c)", got)
	}
	press(t, &tb, "G") // c, the last row
	tb.SetRows([]Row{row("a", "a", "", "")})
	if got := selectedKey(&tb); got != "a" {
		t.Errorf("after removing the last row selected %q, want the new last row (a)", got)
	}
	tb.SetRows(nil)
	if _, ok := tb.Selected(); ok {
		t.Error("empty table reports a selection")
	}
	if v := ansi.Strip(tb.View()); !strings.Contains(v, "No items.") {
		t.Errorf("empty table does not show the empty-state message:\n%s", v)
	}
	tb.SetEmptyText("No containers.")
	if v := ansi.Strip(tb.View()); !strings.Contains(v, "No containers.") {
		t.Errorf("custom empty-state message missing:\n%s", v)
	}
}

func TestTableFilter(t *testing.T) {
	tb := newTestTable(
		row("1", "web", "nginx", "up"),
		row("2", "db", "Postgres:16", "up"),
		row("3", "POS-api", "alpine", "up"),
		row("4", "worker", "busybox", "pos"), // STATUS is not a filter column
	)
	press(t, &tb, "/")
	if !tb.InputFocused() {
		t.Fatal("/ did not focus the filter input")
	}
	typeText(&tb, "pos")
	if tb.Filter() != "pos" {
		t.Fatalf("filter %q, want pos", tb.Filter())
	}
	var got []string
	for _, r := range tb.visible {
		got = append(got, r.Key)
	}
	if strings.Join(got, ",") != "2,3" {
		t.Errorf("filter pos lists rows %v, want [2 3] (name or image, case-insensitive)", got)
	}

	// Enter keeps the filter and returns the keys to the view.
	press(t, &tb, "enter")
	if tb.InputFocused() || tb.Filter() != "pos" || tb.Len() != 2 {
		t.Fatalf("after enter: focused=%v filter=%q len=%d", tb.InputFocused(), tb.Filter(), tb.Len())
	}
	// The filter applies to refreshed rows too.
	tb.SetRows(append(tb.all, row("5", "repos", "x", "")))
	if tb.Len() != 3 {
		t.Errorf("refreshed rows not filtered: len=%d, want 3", tb.Len())
	}

	// Esc (Back) clears the filter and lists every row again.
	if !tb.Back() {
		t.Fatal("Back did not consume Esc with a filter applied")
	}
	if tb.Filter() != "" || tb.Len() != 5 {
		t.Errorf("after Back: filter=%q len=%d, want all 5 rows", tb.Filter(), tb.Len())
	}
	if tb.Back() {
		t.Error("Back consumed Esc with no filter to clear")
	}
}

func TestTableFilterEscWhileTyping(t *testing.T) {
	tb := newTestTable(row("1", "web", "nginx", ""), row("2", "db", "postgres", ""))
	press(t, &tb, "/")
	typeText(&tb, "web")
	if tb.Len() != 1 {
		t.Fatalf("len %d, want 1", tb.Len())
	}
	press(t, &tb, "esc")
	if tb.InputFocused() || tb.Filter() != "" || tb.Len() != 2 {
		t.Errorf("esc while typing: focused=%v filter=%q len=%d", tb.InputFocused(), tb.Filter(), tb.Len())
	}
}

func TestTableFilterTypesEveryPrintableKey(t *testing.T) {
	tb := newTestTable(row("1", "quay?", "x", ""))
	press(t, &tb, "/")
	typeText(&tb, "q:?jkgG/")
	if tb.Filter() != "q:?jkgG/" {
		t.Errorf("filter %q: navigation or command keys were not typed as text", tb.Filter())
	}
}

func TestTableNoMatchMessage(t *testing.T) {
	tb := newTestTable(row("1", "web", "nginx", ""))
	press(t, &tb, "/")
	typeText(&tb, "zzz")
	press(t, &tb, "enter")
	if v := ansi.Strip(tb.View()); !strings.Contains(v, `No rows match the filter "zzz".`) {
		t.Errorf("no-match message missing:\n%s", v)
	}
}

func TestTableScrollsToKeepSelectionVisible(t *testing.T) {
	var rows []Row
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		rows = append(rows, row(n, n, "", ""))
	}
	tb := newTestTable(rows...) // height 10: header + 9 rows
	press(t, &tb, "G")
	lines := strings.Split(ansi.Strip(tb.View()), "\n")
	if len(lines) != 10 {
		t.Fatalf("view has %d lines, want 10", len(lines))
	}
	if !strings.HasPrefix(lines[9], "l ") {
		t.Errorf("last row not visible after G; last line %q", lines[9])
	}
}

func TestTableViewSizeAndSanitizing(t *testing.T) {
	tb := newTestTable(row("1", "evil\x1b[2Jname\nwith-newline", "a-very-long-image-name-that-does-not-fit-anywhere-at-all", "up"))
	v := tb.View()
	if strings.Contains(v, "\x1b[2J") {
		t.Error("control sequence from a cell reached the output")
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 10 {
		t.Fatalf("view has %d lines, want 10", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 60 {
			t.Errorf("line %d is %d cells wide, want 60: %q", i, w, ansi.Strip(l))
		}
	}
	if !strings.Contains(ansi.Strip(lines[1]), "…") {
		t.Errorf("long cell not truncated with an ellipsis: %q", ansi.Strip(lines[1]))
	}
}

func TestTableTinySizesDoNotPanic(t *testing.T) {
	tb := newTestTable(row("1", "a", "b", "c"))
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {5, 2}, {3, 0}} {
		tb.SetSize(sz[0], sz[1])
		press(t, &tb, "j", "G", "/")
		_ = tb.View()
		press(t, &tb, "esc")
	}
}

func TestConfirmOnlyYConfirms(t *testing.T) {
	c := NewConfirm("Delete web?", "web (running) will be stopped")
	if c.Update(uitest.KeyPress("y")) != Accepted {
		t.Error("y did not confirm")
	}
	for _, k := range []string{"n", "esc", "Y", "enter", "q", "space", "ctrl+y"} {
		if got := c.Update(uitest.KeyPress(k)); got != Cancelled {
			t.Errorf("key %q gave %v, want Cancelled", k, got)
		}
	}
	if c.Update(tea.WindowSizeMsg{}) != Pending {
		t.Error("a non-key message closed the dialog")
	}
}

func TestPrompt(t *testing.T) {
	p, _ := NewPrompt("Volume name", "empty for a generated name")
	for _, r := range "  data " {
		p, _, _ = p.Update(uitest.KeyPress(string(r)))
	}
	p, out, _ := p.Update(uitest.KeyPress("enter"))
	if out != Accepted || p.Value() != "data" {
		t.Errorf("enter: outcome %v value %q, want Accepted \"data\"", out, p.Value())
	}
	p, _ = NewPrompt("x", "")
	if _, out, _ = p.Update(uitest.KeyPress("esc")); out != Cancelled {
		t.Errorf("esc: outcome %v, want Cancelled", out)
	}
	p, _ = NewPrompt("x", "")
	if _, out, _ = p.Update(uitest.KeyPress("q")); out != Pending {
		t.Errorf("q: outcome %v, want Pending (typed as text)", out)
	}
}

func TestDialogViewsFitWidth(t *testing.T) {
	long := strings.Repeat("x", 200)
	body := make([]string, 30)
	for i := range body {
		body[i] = long
	}
	for _, w := range []int{100, 40, 10} {
		v := NewConfirm(long, body...).View(w)
		for _, l := range strings.Split(v, "\n") {
			if lw := ansi.StringWidth(l); lw > max(w, dialogChrome+1) {
				t.Errorf("width %d: confirm line %d cells wide", w, lw)
			}
		}
		if n := strings.Count(v, "\n") + 1; n > maxConfirmLines+6 {
			t.Errorf("width %d: confirm dialog has %d lines; body not capped", w, n)
		}
		p, _ := NewPrompt(long, "")
		for _, l := range strings.Split(p.View(w), "\n") {
			if lw := ansi.StringWidth(l); lw > max(w, dialogChrome+1) {
				t.Errorf("width %d: prompt line %d cells wide", w, lw)
			}
		}
	}
}

func TestClean(t *testing.T) {
	cases := map[string]string{
		"a\r\nb":        "a b",
		"a\rb\nc\td":    "a b c d",
		"x\x1b[2Jy":     "x�[2Jy",
		"evil\u202etxt": "evil�txt",
		"plain ünïcödé": "plain ünïcödé",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInputsDropBidiControls(t *testing.T) {
	tb := newTestTable(row("1", "web", "nginx", ""))
	press(t, &tb, "/")
	tb.Update(tea.PasteMsg{Content: "we\u202eb\u2066"})
	if tb.Filter() != "web" {
		t.Errorf("filter %q, want the bidi controls removed", tb.Filter())
	}
	p, _ := NewPrompt("x", "")
	p, _, _ = p.Update(tea.PasteMsg{Content: "da\u202dta"})
	if p.Value() != "data" {
		t.Errorf("prompt value %q, want the bidi controls removed", p.Value())
	}
}

func TestFilterInputVisibleAtHeightOne(t *testing.T) {
	tb := newTestTable(row("1", "web", "nginx", ""))
	tb.SetSize(30, 1)
	press(t, &tb, "/")
	typeText(&tb, "we")
	if v := ansi.Strip(tb.View()); !strings.HasPrefix(v, "/we") {
		t.Errorf("view %q, want the filter input", v)
	}
}

func TestSetRowsCopiesTheSlice(t *testing.T) {
	rows := []Row{row("1", "web", "nginx", ""), row("2", "db", "postgres", "")}
	tb := newTestTable(rows...)
	rows[0] = row("9", "changed", "x", "")
	if r, _ := tb.Selected(); r.Key != "1" {
		t.Errorf("table follows the caller's slice: selected %q", r.Key)
	}
}

func TestTableRowTones(t *testing.T) {
	rows := []Row{
		{Key: "a", Cells: []string{"alpha", "img", "up"}, Tone: ToneGood},
		{Key: "b", Cells: []string{"bravo", "img", "paused"}, Tone: ToneWarn},
		{Key: "c", Cells: []string{"charlie", "img", "exited"}, Tone: ToneDim},
		{Key: "d", Cells: []string{"delta", "img", "removing"}},
		{Key: "e", Cells: []string{"echo", "img", "?"}, Tone: Tone(99)}, // unknown tone
	}
	tb := newTestTable(rows...)
	press(t, &tb, "j") // select b, so a, c and d are unselected
	ws := tb.widths()
	plain := func(i int) string { return tb.renderLine(rows[i].Cells, ws) }
	lines := strings.Split(tb.View(), "\n")

	want := []string{
		Styles.RowGood.Render(plain(0)),
		Styles.TableSelected.Render(plain(1)),
		Styles.RowDim.Render(plain(2)),
		plain(3),
		plain(4),
	}
	for i, w := range want {
		if got := lines[i+1]; got != w {
			t.Errorf("row %q rendered %q, want %q", rows[i].Key, got, w)
		}
	}
	// The styles must actually differ, or the comparisons above prove nothing.
	if Styles.RowDim.Render(plain(2)) == plain(2) || Styles.RowGood.Render(plain(0)) == plain(0) {
		t.Fatal("tone styles render rows unchanged")
	}

	// A dimmed row, once selected, is the plain selection of the untoned line.
	press(t, &tb, "j") // select c
	lines = strings.Split(tb.View(), "\n")
	if got, w := lines[3], Styles.TableSelected.Render(plain(2)); got != w {
		t.Errorf("selected dimmed row rendered %q, want %q", got, w)
	}
	if got, w := lines[2], Styles.RowWarn.Render(plain(1)); got != w {
		t.Errorf("unselected warn row rendered %q, want %q", got, w)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 60 {
			t.Errorf("line %d is %d cells wide, want 60", i, w)
		}
	}
}

func TestTablePosition(t *testing.T) {
	type pos struct {
		selected, listed, total int
		filtered                bool
	}
	get := func(tb *Table) pos {
		s, l, n, f := tb.Position()
		return pos{s, l, n, f}
	}
	var rows []Row
	for i, n := range []string{"web1", "web2", "web3", "web4", "web5", "db1", "db2", "db3", "db4", "db5", "db6", "db7"} {
		rows = append(rows, row(string(rune('a'+i)), n, "img", ""))
	}
	filtered := func(f string) *Table {
		tb := newTestTable(rows...)
		press(t, &tb, "/")
		typeText(&tb, f)
		press(t, &tb, "enter")
		return &tb
	}

	unfiltered := newTestTable(rows...)
	press(t, &unfiltered, "j", "j")
	empty := newTestTable()

	cases := []struct {
		name string
		tb   *Table
		want pos
	}{
		{"unfiltered", &unfiltered, pos{3, 12, 12, false}},
		{"filtered", filtered("web"), pos{1, 5, 12, true}},
		{"filter matching nothing", filtered("zzz"), pos{0, 0, 12, true}},
		{"filter matching all rows", filtered("img"), pos{1, 12, 12, true}},
		{"empty without a filter", &empty, pos{0, 0, 0, false}},
	}
	for _, c := range cases {
		if got := get(c.tb); got != c.want {
			t.Errorf("%s: Position() = %+v, want %+v", c.name, got, c.want)
		}
	}

	// A whitespace-only filter, still being typed, narrows nothing.
	blank := newTestTable(rows...)
	press(t, &blank, "/", "space")
	if !blank.InputFocused() || blank.Filter() != " " {
		t.Fatalf("setup: focused=%v filter=%q, want the input focused with %q", blank.InputFocused(), blank.Filter(), " ")
	}
	if got, want := get(&blank), (pos{1, 12, 12, false}); got != want {
		t.Errorf("blank filter while typing: Position() = %+v, want %+v", got, want)
	}
	// A filter still being typed counts as applied.
	press(t, &blank, "w", "e", "b")
	if got, want := get(&blank), (pos{1, 5, 12, true}); got != want {
		t.Errorf("filter while typing: Position() = %+v, want %+v", got, want)
	}

	press(t, &unfiltered, "j")
	if got, want := get(&unfiltered), (pos{4, 12, 12, false}); got != want {
		t.Errorf("after moving the selection: Position() = %+v, want %+v", got, want)
	}
	press(t, &unfiltered, "G")
	if got, want := get(&unfiltered), (pos{12, 12, 12, false}); got != want {
		t.Errorf("after G: Position() = %+v, want %+v", got, want)
	}
}
