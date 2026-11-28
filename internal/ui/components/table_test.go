package components

import (
	"fmt"
	"image/color"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	if !strings.HasPrefix(lines[9], selectionMarker+" l ") {
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

// withStyles sets the package-global Styles to st for the rest of the test.
func withStyles(t *testing.T, st StyleSet) {
	t.Helper()
	old := Styles
	t.Cleanup(func() { Styles = old })
	Styles = st
}

// dialogLines returns the dialog's lines and checks that they all have the
// same width, which it returns.
func dialogLines(t *testing.T, v string) ([]string, int) {
	t.Helper()
	lines := strings.Split(v, "\n")
	w := ansi.StringWidth(lines[0])
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw != w {
			t.Fatalf("line %d is %d cells wide, line 0 is %d:\n%s", i, lw, w, ansi.Strip(v))
		}
	}
	return lines, w
}

func TestDialogWidth(t *testing.T) {
	cases := []struct{ content, maxWidth, want int }{
		{0, 200, minDialogWidth - dialogChrome},
		{10, 200, minDialogWidth - dialogChrome},
		{45, 200, 45},
		{maxDialogWidth - dialogChrome, 200, maxDialogWidth - dialogChrome},
		{90, 200, maxDialogWidth - dialogChrome},
		{90, 30, 30 - dialogChrome},
		{10, 30, 30 - dialogChrome},
		{10, dialogChrome, 1},
		{10, 0, 1},
		{10, -5, 1},
	}
	for _, c := range cases {
		if got := dialogWidth(c.content, c.maxWidth); got != c.want {
			t.Errorf("dialogWidth(%d, %d) = %d, want %d", c.content, c.maxWidth, got, c.want)
		}
	}
}

func TestConfirmFitsContent(t *testing.T) {
	// Short content: the minimum width, not the terminal's.
	_, w := dialogLines(t, NewConfirm("Delete container?", "web").View(80))
	if w != minDialogWidth {
		t.Errorf("short confirmation is %d cells wide, want %d", w, minDialogWidth)
	}

	// A long body line caps the width and is truncated with an ellipsis.
	ref := "registry.example.com/team/" + strings.Repeat("a", 90-len("registry.example.com/team/"))
	lines, w := dialogLines(t, NewConfirm("Delete image?", ref).View(120))
	if w != maxDialogWidth {
		t.Errorf("long confirmation is %d cells wide, want %d", w, maxDialogWidth)
	}
	var shown string
	for _, l := range lines {
		if s := ansi.Strip(l); strings.Contains(s, "registry.example.com") {
			shown = s
		}
	}
	if !strings.Contains(shown, "…") || strings.Contains(shown, ref) {
		t.Errorf("long reference not truncated with an ellipsis: %q", shown)
	}

	// Content between the bounds sets the width exactly.
	mid := strings.Repeat("m", 45)
	if _, w := dialogLines(t, NewConfirm("Delete?", mid).View(120)); w != 45+dialogChrome {
		t.Errorf("confirmation with a 45-cell line is %d cells wide, want %d", w, 45+dialogChrome)
	}

	// Narrow terminal: fits in it.
	if _, w := dialogLines(t, NewConfirm("Delete image?", ref).View(30)); w > 30 {
		t.Errorf("confirmation on a 30-column terminal is %d cells wide", w)
	}

	// Only the shown body lines are measured: a long line past the cap
	// does not widen the dialog.
	body := make([]string, maxConfirmLines+5)
	for i := range body {
		body[i] = "c" + itoa(i)
	}
	body[len(body)-1] = strings.Repeat("z", 90)
	if _, w := dialogLines(t, NewConfirm("Delete containers?", body...).View(120)); w != minDialogWidth {
		t.Errorf("hidden long body line widened the dialog to %d cells", w)
	}
}

func TestConfirmWrapsProse(t *testing.T) {
	sentence := "Volume cache-data (local driver) and all its data will be removed."
	v := NewConfirm("Delete volume cache-data?", sentence, "Its volumes are kept.").View(80)
	lines, w := dialogLines(t, v)
	if w != maxDialogWidth {
		t.Errorf("dialog is %d cells wide, want %d", w, maxDialogWidth)
	}
	text := ansi.Strip(v)
	if strings.Contains(text, "…") {
		t.Errorf("prose truncated instead of wrapped:\n%s", text)
	}
	// Rejoin the wrapped words: the whole sentence must be there.
	var words []string
	for _, l := range lines {
		words = append(words, strings.Fields(strings.Trim(ansi.Strip(l), "│╭╮╰╯─ "))...)
	}
	if !strings.Contains(strings.Join(words, " "), sentence) {
		t.Errorf("sentence not shown in full:\n%s", text)
	}
}

func TestConfirmBodyCap(t *testing.T) {
	long := strings.Repeat("word ", 30) // wraps to several lines
	tests := []struct {
		name     string
		body     []string
		wantMore string // the last body line, or "" when nothing is left out
	}{
		{"fits", []string{"a", "b", "c"}, ""},
		{"exactly the cap", slices.Repeat([]string{"x"}, maxConfirmLines), ""},
		{"one entry over", slices.Repeat([]string{"x"}, maxConfirmLines+1), "… and 2 more"},
		{"many entries", slices.Repeat([]string{"x"}, 30), "… and 19 more"},
		{"wrapped entries", slices.Repeat([]string{long}, 5), "… and 3 more"},
		{"one huge entry", []string{strings.Repeat(long, 5)}, "… and 1 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown := tt.body[:min(len(tt.body), maxConfirmLines)]
			got := confirmBody(shown, len(tt.body), 36)
			if len(got) > maxConfirmLines {
				t.Fatalf("%d body lines, want at most %d", len(got), maxConfirmLines)
			}
			for i, l := range got {
				if lw := ansi.StringWidth(l); lw > 36 {
					t.Errorf("line %d is %d cells wide", i, lw)
				}
			}
			last := got[len(got)-1]
			if tt.wantMore == "" {
				if strings.HasPrefix(last, "… and") {
					t.Errorf("unexpected summary %q", last)
				}
				return
			}
			if last != tt.wantMore {
				t.Errorf("last line %q, want %q", last, tt.wantMore)
			}
		})
	}
}

func TestPromptFitsContent(t *testing.T) {
	p, _ := NewPrompt("Volume name", "empty for a generated name")
	p.SetMaxWidth(80)
	if _, w := dialogLines(t, p.View(80)); w != minDialogWidth {
		t.Errorf("short prompt is %d cells wide, want %d", w, minDialogWidth)
	}

	p, _ = NewPrompt("Image to pull", strings.Repeat("p", 90))
	p.SetMaxWidth(120)
	if _, w := dialogLines(t, p.View(120)); w != maxDialogWidth {
		t.Errorf("prompt with a long placeholder is %d cells wide, want %d", w, maxDialogWidth)
	}
	p.SetMaxWidth(30)
	if _, w := dialogLines(t, p.View(30)); w > 30 {
		t.Errorf("prompt on a 30-column terminal is %d cells wide", w)
	}
}

// numbered returns n comma-terminated two-digit numbers, a text in which
// every run of three or more characters occurs once.
func numbered(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%02d,", i)
	}
	return b.String()
}

// checkPromptWindow checks that the prompt dialog v keeps its typed text on
// one input line that shows a contiguous part of typed around the cursor
// at pos, neither wrapped nor cut with an ellipsis.
func checkPromptWindow(t *testing.T, v, typed string, pos, maxWidth int) {
	t.Helper()
	lines, w := dialogLines(t, v)
	if len(lines) != 7 {
		t.Fatalf("width %d: prompt has %d lines, want 7 (typed text wrapped?):\n%s", maxWidth, len(lines), ansi.Strip(v))
	}
	if w > maxWidth {
		t.Errorf("width %d: prompt is %d cells wide", maxWidth, w)
	}
	in := strings.Trim(ansi.Strip(lines[3]), "│ ")
	shown, ok := strings.CutPrefix(in, "> ")
	start := strings.Index(typed, shown)
	if !ok || shown == "" || start < 0 {
		t.Fatalf("width %d: input line %q is not a part of the typed text", maxWidth, in)
	}
	if pos < start || pos > start+len(shown) {
		t.Errorf("width %d: cursor %d outside the shown text [%d, %d)", maxWidth, pos, start, start+len(shown))
	}
}

func TestPromptScrollsTypedText(t *testing.T) {
	p, _ := NewPrompt("Volume name", "")
	p.SetMaxWidth(80)
	typed := numbered(30)
	for _, r := range typed {
		p, _, _ = p.Update(uitest.KeyPress(string(r)))
	}
	checkPromptWindow(t, p.View(80), typed, len(typed), 80)
	// Move the cursor back into the middle, then shrink and widen the
	// terminal: the text must still scroll, with the cursor in view.
	for range 43 {
		p, _, _ = p.Update(uitest.KeyPress("left"))
	}
	pos := len(typed) - 43
	for _, maxWidth := range []int{80, 30, 100} {
		p.SetMaxWidth(maxWidth)
		checkPromptWindow(t, p.View(maxWidth), typed, pos, maxWidth)
	}
	if p.Value() != typed {
		t.Errorf("value %q, want %q", p.Value(), typed)
	}
}

func TestDialogBorderColors(t *testing.T) {
	for _, dark := range []bool{true, false} {
		withStyles(t, NewStyles(dark))
		pal := lightPalette
		if dark {
			pal = darkPalette
		}
		// The SGR sequence that colors the top-left border corner, followed
		// by the corner itself.
		corner := func(c color.Color) string {
			sgr, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(c).Render("╭"), "╭")
			return sgr + "╭"
		}
		first := func(v string) string { return strings.SplitN(v, "\n", 2)[0] }

		c := first(NewConfirm("Delete web?", "web").View(80))
		if !strings.HasPrefix(c, corner(pal.error)) {
			t.Errorf("dark=%v: confirm border not in the error color: %q", dark, c)
		}
		p, _ := NewPrompt("Image to pull", "nginx:latest")
		pv := first(p.View(80))
		if !strings.HasPrefix(pv, corner(pal.accent)) {
			t.Errorf("dark=%v: prompt border not in the accent color: %q", dark, pv)
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
	plain := func(i int) string { l, _ := tb.renderLine(rows[i].Cells, ws); return l }
	selected := func(i int) string {
		return Styles.SelectionMarker.Render(selectionMarker) + Styles.TableSelected.Render(" "+plain(i))
	}
	lines := strings.Split(tb.View(), "\n")

	want := []string{
		"  " + Styles.RowGood.Render(plain(0)),
		selected(1),
		"  " + Styles.RowDim.Render(plain(2)),
		"  " + plain(3),
		"  " + plain(4),
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
	if got, w := lines[3], selected(2); got != w {
		t.Errorf("selected dimmed row rendered %q, want %q", got, w)
	}
	if strings.Contains(lines[3], Styles.RowDim.Render(plain(2))) {
		t.Error("selected dimmed row carries its tone style")
	}
	if got, w := lines[2], "  "+Styles.RowWarn.Render(plain(1)); got != w {
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

// colStarts returns the cell offset at which each column starts in a
// rendered line of tb, gutter included.
func colStarts(tb *Table) []int {
	var starts []int
	x := gutterWidth
	for _, w := range tb.widths() {
		starts = append(starts, x)
		x += w + columnGap
	}
	return starts
}

func TestTableWidthsFitContent(t *testing.T) {
	// Like the containers view: NAME, IMAGE and PORTS grow, STATUS doesn't.
	cols := []Column{
		{Title: "NAME", Width: 20, Flex: true, Filter: true},
		{Title: "IMAGE", Width: 24, Flex: true, Filter: true},
		{Title: "STATUS", Width: 22},
		{Title: "PORTS", Width: 24, Flex: true},
	}
	newTable := func(rows ...Row) Table {
		tb := NewTable(cols)
		tb.SetSize(120, 10)
		tb.SetRows(rows)
		return tb
	}
	r := func(key, name, image string) Row {
		return Row{Key: key, Cells: []string{name, image, "Up 2 hours", "80/tcp"}}
	}
	sum := func(ws []int) int {
		n := columnGap * (len(ws) - 1)
		for _, w := range ws {
			n += w
		}
		return n
	}

	t.Run("short names keep their minimum", func(t *testing.T) {
		tb := newTable(r("1", "db", "postgres"), r("2", "web", "nginx"), r("3", "worker", "busybox"))
		ws := tb.widths()
		if ws[0] != 20 || ws[1] != 24 || ws[2] != 22 {
			t.Errorf("widths %v: NAME, IMAGE and STATUS should stay at their minimums 20, 24, 22", ws)
		}
		if want := 120 - gutterWidth - (20 + 24 + 22 + 3*columnGap); ws[3] != want {
			t.Errorf("PORTS width %d, want the remainder %d", ws[3], want)
		}
		if got := sum(ws); got != 120-gutterWidth {
			t.Errorf("columns take %d cells, want %d", got, 120-gutterWidth)
		}
	})

	long := strings.Repeat("i", 40)
	t.Run("long value widens its column", func(t *testing.T) {
		tb := newTable(r("1", "web", "nginx"), r("2", "app", long))
		ws := tb.widths()
		if ws[1] != 40 || ws[0] != 20 {
			t.Errorf("widths %v: want NAME 20 and IMAGE 40", ws)
		}
		lines := strings.Split(ansi.Strip(tb.View()), "\n")
		if !strings.Contains(lines[2], long) {
			t.Errorf("40-cell image truncated: %q", lines[2])
		}
		if strings.Contains(lines[2], "…") {
			t.Errorf("line truncated with room to spare: %q", lines[2])
		}
	})

	t.Run("filtering re-measures", func(t *testing.T) {
		tb := newTable(r("1", "web", "nginx"), r("2", "app", long))
		press(t, &tb, "/")
		typeText(&tb, "web")
		if ws := tb.widths(); ws[1] != 24 {
			t.Errorf("IMAGE width %d after the long row was filtered out, want 24", ws[1])
		}
		press(t, &tb, "esc")
		if ws := tb.widths(); ws[1] != 40 {
			t.Errorf("IMAGE width %d after the filter was cleared, want 40", ws[1])
		}
		tb.SetRows([]Row{r("1", "web", "nginx")})
		if ws := tb.widths(); ws[1] != 24 {
			t.Errorf("IMAGE width %d after the long row was removed, want 24", ws[1])
		}
	})

	t.Run("growth is shared when not everything fits", func(t *testing.T) {
		tb := newTable(r("1", strings.Repeat("n", 60), strings.Repeat("i", 60)))
		ws := tb.widths()
		// 22 spare cells, and both NAME and IMAGE want more than half of
		// them: each grows by 11, and PORTS keeps its minimum.
		if ws[0] != 31 || ws[1] != 35 || ws[3] != 24 {
			t.Errorf("widths %v, want NAME 31, IMAGE 35, PORTS 24", ws)
		}
		if got := sum(ws); got != 120-gutterWidth {
			t.Errorf("columns take %d cells, want %d", got, 120-gutterWidth)
		}
	})

	t.Run("small need is met before sharing", func(t *testing.T) {
		tb := newTable(r("1", strings.Repeat("n", 23), strings.Repeat("i", 80)))
		ws := tb.widths()
		// 22 spare cells: NAME needs 3 and gets them, IMAGE the other 19.
		if ws[0] != 23 || ws[1] != 24+19 {
			t.Errorf("widths %v, want NAME 23 and IMAGE the rest of the spare width", ws)
		}
	})

	t.Run("titles count", func(t *testing.T) {
		tb := NewTable([]Column{{Title: "A-LONG-TITLE", Width: 4, Flex: true}, {Title: "B", Width: 4, Flex: true}})
		tb.SetSize(40, 5)
		tb.SetRows([]Row{{Key: "1", Cells: []string{"x", "y"}}})
		if ws := tb.widths(); ws[0] != 12 || ws[1] != 40-gutterWidth-12-columnGap {
			t.Errorf("widths %v, want the first column as wide as its title", ws)
		}
	})

	t.Run("cut when the minimums do not fit", func(t *testing.T) {
		tb := newTable(r("1", "web", long))
		tb.SetSize(50, 5)
		ws := tb.widths()
		if ws[0] != 20 || ws[1] != 24 || ws[2] != 22 || ws[3] != 24 {
			t.Errorf("widths %v, want the minimums", ws)
		}
		for i, l := range strings.Split(tb.View(), "\n") {
			if w := ansi.StringWidth(l); w != 50 {
				t.Errorf("line %d is %d cells wide, want 50", i, w)
			}
		}
	})
}

func TestTableSelectionGutter(t *testing.T) {
	tb := newTestTable(row("a", "db", "postgres", "up"), row("b", "web", "nginx", "up"), row("c", "worker", "busybox", "up"))
	press(t, &tb, "j") // web
	check := func(want int) {
		t.Helper()
		lines := strings.Split(ansi.Strip(tb.View()), "\n")
		for i, l := range lines {
			marked := strings.HasPrefix(l, selectionMarker+" ")
			if i == want+1 {
				if !marked {
					t.Errorf("selected line %d %q does not start with the marker", i, l)
				}
			} else if !strings.HasPrefix(l, "  ") {
				t.Errorf("line %d %q does not start with a blank gutter", i, l)
			}
			if w := ansi.StringWidth(l); w != 60 {
				t.Errorf("line %d is %d cells wide, want 60", i, w)
			}
		}
		// Columns line up with the header, selected row included.
		header := lines[0]
		for c, x := range colStarts(&tb) {
			title := tb.cols[c].Title
			if got := ansi.Cut(header, x, x+len(title)); got != title {
				t.Errorf("header column %d at %d reads %q, want %q", c, x, got, title)
			}
		}
		x := colStarts(&tb)[1]
		for i, img := range []string{"postgres", "nginx", "busybox"} {
			if got := ansi.Cut(lines[i+1], x, x+len(img)); got != img {
				t.Errorf("row %d IMAGE at %d reads %q, want %q", i, x, got, img)
			}
		}
	}
	check(1)
	press(t, &tb, "j")
	check(2)

	if v := tb.View(); strings.Contains(v, "\x1b[7m") {
		t.Error("the selection still uses reverse video")
	}
}

func TestTableGutterNarrowerThanItself(t *testing.T) {
	tb := newTestTable(row("a", "db", "postgres", "up"))
	tb.SetSize(1, 3)
	for i, l := range strings.Split(tb.View(), "\n") {
		if w := ansi.StringWidth(l); w != 1 {
			t.Errorf("line %d is %d cells wide, want 1", i, w)
		}
	}
}

// messageAt returns the row and column, in cells, at which msg starts in
// the stripped view v, or -1, -1 if it is not shown.
func messageAt(v, msg string) (row, col int) {
	for i, l := range strings.Split(ansi.Strip(v), "\n") {
		if j := strings.Index(l, msg); j >= 0 {
			return i, ansi.StringWidth(l[:j])
		}
	}
	return -1, -1
}

func TestTableEmptyMessageCentered(t *testing.T) {
	const msg = "No running containers (2 stopped; press a to show all)."
	tb := newTestTable()
	tb.SetSize(100, 9) // header + 8 body lines
	tb.SetEmptyText(msg)
	v := tb.View()
	r, c := messageAt(v, msg)
	// 8 body lines, 1 for the message: 3 above it, 4 below. 45 spare cells:
	// 22 to the left, 23 to the right.
	if r != 1+3 || c != (100-len(msg))/2 {
		t.Errorf("message at row %d column %d, want row 4 column %d:\n%s", r, c, (100-len(msg))/2, ansi.Strip(v))
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 9 {
		t.Fatalf("view has %d lines, want 9", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 100 {
			t.Errorf("line %d is %d cells wide, want 100", i, w)
		}
	}
}

func TestTableNoMatchMessageCentered(t *testing.T) {
	tb := newTestTable(row("1", "web", "nginx", ""))
	press(t, &tb, "/")
	typeText(&tb, "zzz")
	const msg = `No rows match the filter "zzz".`
	// While typing, the body is the 8 lines between the header and the
	// input: 3 above the message, 4 below. Of the 29 spare cells, 14 go to
	// the left.
	if r, c := messageAt(tb.View(), msg); r != 1+3 || c != (60-len(msg))/2 {
		t.Errorf("while typing: message at row %d column %d, want row 4 column %d", r, c, (60-len(msg))/2)
	}
	press(t, &tb, "enter")
	// With the input closed the body is 9 lines: 4 above, 4 below.
	v := tb.View()
	if r, c := messageAt(v, msg); r != 1+4 || c != (60-len(msg))/2 {
		t.Errorf("message at row %d column %d, want row 5 column %d:\n%s", r, c, (60-len(msg))/2, ansi.Strip(v))
	}
}

func TestTableEmptyMessageTruncated(t *testing.T) {
	tb := newTestTable()
	tb.SetSize(20, 4)
	tb.SetEmptyText(strings.Repeat("m", 50))
	lines := strings.Split(tb.View(), "\n")
	if len(lines) != 4 {
		t.Fatalf("view has %d lines, want 4", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("line %d is %d cells wide, want 20", i, w)
		}
	}
	if !strings.Contains(ansi.Strip(tb.View()), "…") {
		t.Error("long empty message not truncated with an ellipsis")
	}
}

// naiveGrow is the definition growToNatural must match: hand out one cell
// at a time, left to right, to the flex columns still below their natural
// width, and return the cells left over.
func naiveGrow(ws, flex, natural []int, extra int) int {
	for extra > 0 {
		gave := false
		for _, i := range flex {
			if extra > 0 && ws[i] < natural[i] {
				ws[i]++
				extra--
				gave = true
			}
		}
		if !gave {
			break
		}
	}
	return extra
}

func TestGrowToNaturalMatchesOneCellAtATime(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for n := range 5000 {
		cols := 1 + rng.IntN(6)
		ws := make([]int, cols)
		natural := make([]int, cols)
		var flex []int
		for i := range cols {
			ws[i] = 1 + rng.IntN(10)
			natural[i] = ws[i] + rng.IntN(6) // equal needs are common
			if rng.IntN(3) > 0 {
				flex = append(flex, i)
			}
		}
		extra := rng.IntN(30)
		want := slices.Clone(ws)
		wantLeft := naiveGrow(want, flex, natural, extra)

		tb := Table{natural: natural}
		got := slices.Clone(ws)
		gotLeft := tb.growToNatural(got, slices.Clone(flex), extra)
		if !slices.Equal(got, want) || gotLeft != wantLeft {
			t.Fatalf("case %d: ws %v natural %v flex %v extra %d: got %v left %d, want %v left %d",
				n, ws, natural, flex, extra, got, gotLeft, want, wantLeft)
		}
	}
}

func TestTableExactSizeAtTinySizes(t *testing.T) {
	populated := func() Table { return newTestTable(row("a", "db", "postgres", "up"), row("b", "web", "nginx", "up")) }
	empty := func() Table { return newTestTable() }
	noMatch := func(submit bool) func() Table {
		return func() Table {
			tb := newTestTable(row("a", "db", "postgres", "up"))
			press(t, &tb, "/")
			typeText(&tb, "zzz")
			if submit {
				press(t, &tb, "enter")
			}
			return tb
		}
	}
	cases := map[string]func() Table{
		"populated":             populated,
		"empty":                 empty,
		"no match while typing": noMatch(false),
		"no match":              noMatch(true),
	}
	for name, mk := range cases {
		for w := range 5 {
			for h := range 5 {
				tb := mk()
				tb.SetSize(w, h)
				v := tb.View()
				if w == 0 || h == 0 {
					if v != "" {
						t.Errorf("%s %dx%d: view %q, want empty", name, w, h, v)
					}
					continue
				}
				lines := strings.Split(v, "\n")
				if len(lines) != h {
					t.Errorf("%s %dx%d: %d lines, want %d", name, w, h, len(lines), h)
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw != w {
						t.Errorf("%s %dx%d: line %d is %d cells wide: %q", name, w, h, i, lw, ansi.Strip(l))
					}
				}
			}
		}
	}
}

func TestTableEmptyMessagesSanitized(t *testing.T) {
	const hostile = "evil\x1b]0;pwned\x07\u202etxt\x1b[2J"
	check := func(name string, tb *Table) {
		t.Helper()
		v := tb.View()
		stripped := ansi.Strip(v)
		for _, bad := range []string{"\x1b", "\u202e", "\x07"} {
			if strings.Contains(stripped, bad) {
				t.Errorf("%s: %q reached the output: %q", name, bad, stripped)
			}
		}
		if strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x1b]0;") {
			t.Errorf("%s: control sequence from untrusted text reached the output", name)
		}
		if !strings.Contains(stripped, "evil") {
			t.Errorf("%s: message not shown: %q", name, stripped)
		}
		for i, l := range strings.Split(v, "\n") {
			if w := ansi.StringWidth(l); w != 60 {
				t.Errorf("%s: line %d is %d cells wide, want 60", name, i, w)
			}
		}
	}

	tb := newTestTable()
	tb.SetEmptyText(hostile)
	check("empty text", &tb)

	tb = newTestTable(row("a", "db", "postgres", "up"))
	press(t, &tb, "/")
	tb.Update(tea.PasteMsg{Content: hostile})
	check("no-match while typing", &tb)
	press(t, &tb, "enter")
	check("no-match", &tb)
}

// TestDialogsSanitizeUntrustedText checks that text from the engine (names
// in titles and bodies) cannot inject escape sequences, bidi overrides or
// line breaks through a dialog, and that the dialog stays rectangular.
func TestDialogsSanitizeUntrustedText(t *testing.T) {
	hostile := "x\x1b]0;pwned\x07y\x1b[31mz\u202eevil\r\nnext\tcol\x9b2J"
	check := func(t *testing.T, name, v string, maxWidth int) {
		t.Helper()
		for _, bad := range []string{"\x1b]", "\x07", "\u202e", "\x1b[31m", "\x1b[2J", "\x9b", "\r", "\t", "pwned\x07"} {
			if strings.Contains(v, bad) {
				t.Errorf("%s: output contains %q", name, bad)
			}
		}
		if _, w := dialogLines(t, v); w > maxWidth {
			t.Errorf("%s: %d cells wide on a %d-column terminal", name, w, maxWidth)
		}
	}
	for _, w := range []int{80, 30} {
		long := strings.Repeat(hostile+" ", 10)
		check(t, "confirm", NewConfirm("Delete "+hostile+"?", hostile, long).View(w), w)
		p, _ := NewPrompt("Pull "+hostile, hostile)
		p.SetMaxWidth(w)
		check(t, "prompt", p.View(w), w)
	}
}

// TestDialogsWideRunes checks the width math with double-width runes: every
// line has the same width, within the limits.
func TestDialogsWideRunes(t *testing.T) {
	for _, w := range []int{80, 31, 30} {
		v := NewConfirm("削除しますか 日本語のコンテナ名?", strings.Repeat("日本", 40), "ボリューム は 保持 されます "+strings.Repeat("語", 50)).View(w)
		if _, dw := dialogLines(t, v); dw > min(w, maxDialogWidth) {
			t.Errorf("width %d: confirm is %d cells wide", w, dw)
		}
		p, _ := NewPrompt("イメージ名", "例 nginx")
		p.SetMaxWidth(w)
		for _, r := range strings.Repeat("日本語", 20) {
			p, _, _ = p.Update(uitest.KeyPress(string(r)))
		}
		lines, dw := dialogLines(t, p.View(w))
		if len(lines) != 7 || dw > w {
			t.Errorf("width %d: prompt has %d lines, %d cells wide:\n%s", w, len(lines), dw, ansi.Strip(p.View(w)))
		}
	}
}

func TestTruncateMiddle(t *testing.T) {
	cases := []struct {
		s     string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"abcdefghijkl", 7, "abc…jkl"},
		{"abcdefghijkl", 8, "abcd…jkl"},
		{"abcdefghijkl", 2, "a…"},
		{"abcdefghijkl", 0, ""},
	}
	for _, c := range cases {
		if got := TruncateMiddle(c.s, c.width); got != c.want {
			t.Errorf("TruncateMiddle(%q, %d) = %q, want %q", c.s, c.width, got, c.want)
		}
	}
	for w := 3; w < 20; w++ {
		if got := TruncateMiddle(strings.Repeat("日本", 20), w); ansi.StringWidth(got) > w {
			t.Errorf("wide runes at width %d: %q is %d cells", w, got, ansi.StringWidth(got))
		}
	}
}

// TestConfirmKeepsDistinguishingEnds checks that two long names differing
// only at the end stay distinguishable in their confirmation dialogs.
func TestConfirmKeepsDistinguishingEnds(t *testing.T) {
	prefix := strings.Repeat("shared_prefix_", 6) // no spaces or hyphens to wrap at
	a, b := prefix+"alpha", prefix+"bravo"
	va := ansi.Strip(NewConfirm("Delete volume "+a+"?", a).View(80))
	vb := ansi.Strip(NewConfirm("Delete volume "+b+"?", b).View(80))
	if va == vb {
		t.Fatalf("dialogs for %q and %q render the same:\n%s", a, b, va)
	}
	if !strings.Contains(va, "alpha") || !strings.Contains(vb, "bravo") {
		t.Errorf("name ends not shown:\n%s\n%s", va, vb)
	}
}

func TestConfirmTitleWraps(t *testing.T) {
	v := NewConfirm("Delete image registry.example.com/team/service:1.2.3 now?", "x").View(80)
	lines, _ := dialogLines(t, v)
	title := strings.TrimSpace(strings.Trim(ansi.Strip(lines[1]), "│")) + " " +
		strings.TrimSpace(strings.Trim(ansi.Strip(lines[2]), "│"))
	if title != "Delete image registry.example.com/team/service:1.2.3 now?" {
		t.Errorf("title not wrapped in full over two lines:\n%s", ansi.Strip(v))
	}

	// Past two lines, the title is cut with an ellipsis.
	long := strings.TrimSpace(strings.Repeat("word ", 40))
	got := confirmTitle(long, 36)
	if len(got) != maxConfirmTitleLines || !strings.HasSuffix(got[1], "…") {
		t.Errorf("long title gave %q, want %d lines ending in …", got, maxConfirmTitleLines)
	}
	for _, l := range got {
		if ansi.StringWidth(l) > 36 {
			t.Errorf("title line %q wider than 36", l)
		}
	}
}

// TestConfirmViewCapsWrappedBody checks the line cap through View, where
// the width comes from the content: long wrapped entries end in the count
// of what is left out, with the hint still shown.
func TestConfirmViewCapsWrappedBody(t *testing.T) {
	entry := strings.TrimSpace(strings.Repeat("container-name ", 10)) // 149 cells, 3 lines at 56
	v := NewConfirm("Delete containers?", slices.Repeat([]string{entry}, 8)...).View(80)
	lines, w := dialogLines(t, v)
	if w != maxDialogWidth {
		t.Errorf("dialog is %d cells wide, want %d", w, maxDialogWidth)
	}
	// border + title + blank + body + blank + hint + border
	if body := len(lines) - 6; body != maxConfirmLines {
		t.Errorf("%d body lines, want %d:\n%s", body, maxConfirmLines, ansi.Strip(v))
	}
	strip := func(i int) string { return strings.TrimSpace(strings.Trim(ansi.Strip(lines[i]), "│")) }
	if got := strip(len(lines) - 4); got != "… and 5 more" {
		t.Errorf("last body line %q, want %q", got, "… and 5 more")
	}
	if got := strip(len(lines) - 2); got != confirmHint {
		t.Errorf("hint line %q, want %q", got, confirmHint)
	}
}

func TestPromptPlaceholderCleaned(t *testing.T) {
	p, _ := NewPrompt("t", "a\x1b[31mb\u202ec")
	if v := p.View(80); strings.Contains(v, "\x1b[31m") || strings.Contains(v, "\u202e") {
		t.Errorf("placeholder rendered raw: %q", v)
	}
}

func TestTableWhitespaceFilterShowsEmptyText(t *testing.T) {
	const msg = "No containers."
	tb := newTestTable()
	tb.SetEmptyText(msg)
	press(t, &tb, "/")
	// While typing: Enter trims the filter, but the open input does not.
	typeText(&tb, " ")
	v := ansi.Strip(tb.View())
	if !strings.Contains(v, msg) || strings.Contains(v, "No rows match") {
		t.Errorf("a whitespace-only filter is not applied, want the empty-list message:\n%s", v)
	}
}

// Column sets for the SetColumns tests: four columns, and five where a new
// column takes the last one's place and NAME is no longer filtered.
var (
	fourCols = []Column{
		{Title: "NAME", Width: 8, Flex: true, Filter: true},
		{Title: "IMAGE", Width: 8, Flex: true, Filter: true},
		{Title: "STATUS", Width: 8},
		{Title: "PORTS", Width: 8, Flex: true},
	}
	fiveCols = []Column{
		{Title: "NAME", Width: 8, Flex: true},
		{Title: "IMAGE", Width: 8, Flex: true, Filter: true},
		{Title: "STATUS", Width: 8},
		{Title: "CPU", Width: 6},
		{Title: "MEM", Width: 10, Flex: true, Filter: true},
	}
)

func rowsFor(cols []Column, n int) []Row {
	rows := make([]Row, n)
	for i := range rows {
		cells := make([]string, len(cols))
		for c, col := range cols {
			cells[c] = fmt.Sprintf("%s-%d", strings.ToLower(col.Title), i)
		}
		rows[i] = Row{Key: fmt.Sprintf("k%d", i), Cells: cells}
	}
	return rows
}

func checkTableLines(t *testing.T, tb *Table, width, height int) {
	t.Helper()
	lines := strings.Split(tb.View(), "\n")
	if len(lines) != height {
		t.Fatalf("view has %d lines, want %d", len(lines), height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != width {
			t.Errorf("line %d is %d cells wide, want %d: %q", i, w, width, ansi.Strip(l))
		}
	}
}

func TestTableSetColumns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to []Column
	}{
		{"4 to 5", fourCols, fiveCols},
		{"5 to 4", fiveCols, fourCols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const width, height = 70, 6
			tb := NewTable(tc.from)
			tb.SetSize(width, height)
			tb.SetRows(rowsFor(tc.from, 20))
			press(t, &tb, "G", "k", "k") // k17, scrolled down
			wantOffset := tb.offset
			if wantOffset == 0 {
				t.Fatal("test setup: table did not scroll")
			}

			tb.SetColumns(tc.to)
			// Between SetColumns and SetRows: the new header over an empty
			// body, no panic, and no stale row data.
			v := ansi.Strip(tb.View())
			if !strings.Contains(v, tc.to[3].Title) {
				t.Errorf("header before SetRows lacks %s:\n%s", tc.to[3].Title, v)
			}
			checkTableLines(t, &tb, width, height)
			if _, ok := tb.Selected(); ok || tb.Len() != 0 {
				t.Errorf("rows of the old columns still listed: len=%d", tb.Len())
			}
			_, _, total, _ := tb.Position()
			if total != 0 {
				t.Errorf("Position total %d before SetRows, want 0", total)
			}

			tb.SetRows(rowsFor(tc.to, 20))
			if got := selectedKey(&tb); got != "k17" {
				t.Errorf("selected %q after SetRows, want k17", got)
			}
			if tb.offset != wantOffset {
				t.Errorf("offset %d after SetRows, want %d", tb.offset, wantOffset)
			}
			header := ansi.Strip(strings.Split(tb.View(), "\n")[0])
			for _, c := range tc.to {
				if !strings.Contains(header, c.Title) {
					t.Errorf("header %q lacks %s", header, c.Title)
				}
			}
			for _, c := range tc.from {
				if !slices.ContainsFunc(tc.to, func(n Column) bool { return n.Title == c.Title }) &&
					strings.Contains(header, c.Title) {
					t.Errorf("header %q still shows the old column %s", header, c.Title)
				}
			}
			checkTableLines(t, &tb, width, height)
		})
	}
}

func TestTableSetColumnsKeepsFilter(t *testing.T) {
	tb := NewTable(fourCols)
	tb.SetSize(80, 10)
	tb.SetRows([]Row{
		{Key: "a", Cells: []string{"web", "nginx", "up", "80/tcp"}},
		{Key: "b", Cells: []string{"db", "postgres", "up", ""}},
		{Key: "c", Cells: []string{"cache", "redis", "up", ""}},
	})
	press(t, &tb, "/")
	typeText(&tb, "web")
	press(t, &tb, "enter")
	if tb.Len() != 1 {
		t.Fatalf("test setup: filter web lists %d rows, want 1", tb.Len())
	}

	tb.SetColumns(fiveCols)
	if tb.Filter() != "web" {
		t.Errorf("filter %q after SetColumns, want web", tb.Filter())
	}
	// NAME is not a filter column of the new set, but MEM is.
	tb.SetRows([]Row{
		{Key: "a", Cells: []string{"web", "nginx", "up", "1.0%", "1MiB"}},
		{Key: "b", Cells: []string{"db", "postgres", "up", "1.0%", "web-mem"}},
		{Key: "c", Cells: []string{"cache", "redis", "up", "1.0%", "1MiB"}},
	})
	if got := selectedKey(&tb); tb.Len() != 1 || got != "b" {
		t.Errorf("filter web on the new columns lists %d rows, selected %q; want only b (MEM matches, NAME no longer searched)", tb.Len(), got)
	}
	_, _, _, filtered := tb.Position()
	if !filtered {
		t.Error("filter no longer reported as applied")
	}
	checkTableLines(t, &tb, 80, 10)
}

func TestTableSetColumnsKeepsFilterFiveToFour(t *testing.T) {
	tb := NewTable(fiveCols)
	tb.SetSize(80, 10)
	tb.SetRows([]Row{
		{Key: "a", Cells: []string{"web", "nginx", "up", "1.0%", "1MiB"}},
		{Key: "b", Cells: []string{"db", "postgres", "up", "1.0%", "2MiB"}},
	})
	press(t, &tb, "/")
	typeText(&tb, "web")
	press(t, &tb, "enter")
	if tb.Len() != 0 {
		t.Fatalf("test setup: NAME is not filtered in the 5-column set, but web lists %d rows", tb.Len())
	}
	tb.SetColumns(fourCols)
	tb.SetRows([]Row{
		{Key: "a", Cells: []string{"web", "nginx", "up", ""}},
		{Key: "b", Cells: []string{"db", "postgres", "up", ""}},
	})
	if got := selectedKey(&tb); tb.Filter() != "web" || tb.Len() != 1 || got != "a" {
		t.Errorf("filter %q lists %d rows, selected %q; want web, only a (NAME searched again)", tb.Filter(), tb.Len(), got)
	}
	checkTableLines(t, &tb, 80, 10)
}

func TestTableSetColumnsWhileFiltering(t *testing.T) {
	tb := NewTable(fourCols)
	tb.SetSize(60, 8)
	tb.SetRows(rowsFor(fourCols, 5))
	press(t, &tb, "/")
	typeText(&tb, "image")
	tb.SetColumns(fiveCols)
	checkTableLines(t, &tb, 60, 8)
	typeText(&tb, "-3") // edits the filter with no rows set
	tb.SetRows(rowsFor(fiveCols, 5))
	if !tb.InputFocused() || tb.Filter() != "image-3" {
		t.Errorf("filter input focused=%v filter=%q, want focused with image-3", tb.InputFocused(), tb.Filter())
	}
	if got := selectedKey(&tb); tb.Len() != 1 || got != "k3" {
		t.Errorf("lists %d rows, selected %q; want only k3", tb.Len(), got)
	}
	checkTableLines(t, &tb, 60, 8)
}

func TestTableSetColumnsTwiceBeforeSetRows(t *testing.T) {
	tb := NewTable(fourCols)
	tb.SetSize(60, 8)
	tb.SetRows(rowsFor(fourCols, 5))
	press(t, &tb, "j", "j") // k2
	tb.SetColumns(fiveCols)
	tb.SetColumns(fourCols)
	tb.SetRows(rowsFor(fourCols, 5))
	if got := selectedKey(&tb); got != "k2" {
		t.Errorf("selected %q, want k2", got)
	}
	// The kept key is used once: a later refresh without it falls back as
	// usual instead of reviving it.
	tb.SetRows(rowsFor(fourCols, 2))
	tb.SetRows(rowsFor(fourCols, 5))
	if got := selectedKey(&tb); got != "k1" {
		t.Errorf("selected %q after the key left and came back, want k1", got)
	}
}

func TestTableSetColumnsSelectedKeyGone(t *testing.T) {
	tb := NewTable(fourCols)
	tb.SetSize(60, 8)
	tb.SetRows(rowsFor(fourCols, 5))
	press(t, &tb, "G") // k4
	tb.SetColumns(fiveCols)
	tb.SetRows(rowsFor(fiveCols, 3))
	if got := selectedKey(&tb); got != "k2" {
		t.Errorf("selected %q, want the nearest row k2", got)
	}
}

func TestTableColumnsCopiesTheSlice(t *testing.T) {
	for name, set := range map[string]func(cols []Column) Table{
		"NewTable": func(cols []Column) Table { return NewTable(cols) },
		"SetColumns": func(cols []Column) Table {
			tb := NewTable(fourCols)
			tb.SetColumns(cols)
			return tb
		},
	} {
		t.Run(name, func(t *testing.T) {
			cols := slices.Clone(fourCols)
			tb := set(cols)
			cols[0].Title = "CHANGED"
			tb.SetSize(60, 4)
			tb.SetRows(rowsFor(fourCols, 1))
			if v := ansi.Strip(tb.View()); strings.Contains(v, "CHANGED") {
				t.Errorf("table shares the caller's column slice:\n%s", v)
			}
		})
	}
}

// Anything that clamps the cursor or the offset while SetColumns has left
// the table without rows must not lose the selection or the scroll offset.
func TestTableSetColumnsStateSurvivesEventsBeforeSetRows(t *testing.T) {
	for name, between := range map[string]func(t *testing.T, tb *Table){
		"nothing": func(*testing.T, *Table) {},
		"resize":  func(_ *testing.T, tb *Table) { tb.SetSize(70, 6) },
		"tiny sizes": func(t *testing.T, tb *Table) {
			for _, sz := range [][2]int{{0, 0}, {1, 1}, {5, 2}} {
				tb.SetSize(sz[0], sz[1])
				_ = tb.View()
				checkTableLines(t, tb, sz[0], max(sz[1], 1)) // a 0x0 view is ""
			}
			tb.SetSize(70, 6)
		},
		"keys": func(t *testing.T, tb *Table) { press(t, tb, "j", "G", "g") },
		"filter edited and cleared": func(t *testing.T, tb *Table) {
			press(t, tb, "/")
			typeText(tb, "x")
			press(t, tb, "backspace", "enter")
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, gone := range []bool{false, true} {
				tb := NewTable(fourCols)
				tb.SetSize(70, 6)
				tb.SetRows(rowsFor(fourCols, 20))
				press(t, &tb, "G", "k", "k") // k17, scrolled down
				wantOffset := tb.offset

				tb.SetColumns(fiveCols)
				between(t, &tb)
				rows := rowsFor(fiveCols, 20)
				if gone {
					rows = slices.Delete(rows, 17, 18)
				}
				tb.SetRows(rows)
				switch got := selectedKey(&tb); {
				case !gone && got != "k17":
					t.Errorf("selected %q, want k17", got)
				case gone && got != "k18":
					t.Errorf("k17 gone: selected %q, want the row now at its position (k18)", got)
				}
				if !gone && tb.offset != wantOffset {
					t.Errorf("offset %d, want %d", tb.offset, wantOffset)
				}
				checkTableLines(t, &tb, 70, 6)
			}
		})
	}
}

// cellRangeOf returns the line renderLine builds for cells and the byte
// range of column c's cell in it, clamped to the line.
func cellRangeOf(tb *Table, cells []string, c int) (line string, start, end int) {
	line, ranges := tb.renderLine(cells, tb.widths())
	return line, ranges[c].start, min(ranges[c].end, len(line))
}

func TestTableCellTones(t *testing.T) {
	rows := []Row{
		{Key: "a", Cells: []string{"alpha", "img", "up"}, Tone: ToneGood, CellTones: []Tone{ToneDefault, ToneBad}},
		{Key: "b", Cells: []string{"bravo", "img", "up"}, CellTones: []Tone{ToneDefault, ToneDefault, ToneWarn}},
		{Key: "c", Cells: []string{"charlie", "img", "up"}, Tone: ToneDim, CellTones: []Tone{Tone(99), ToneDefault}},
		{Key: "d", Cells: []string{"delta", "img", "up"}, Tone: ToneGood, CellTones: []Tone{ToneBad, ToneBad, ToneBad, ToneWarn}},
		{Key: "e", Cells: []string{"echo", "img", "up"}, Tone: ToneWarn, CellTones: []Tone{ToneBad}},
	}
	tb := newTestTable(rows...)
	press(t, &tb, "G") // select e, so a to d are unselected
	lines := strings.Split(tb.View(), "\n")
	// render is st.Render, except that empty text renders as nothing, as
	// RenderSpans leaves it.
	render := func(st lipgloss.Style, s string) string {
		if s == "" {
			return ""
		}
		return st.Render(s)
	}

	line, s, e := cellRangeOf(&tb, rows[0].Cells, 1)
	want := map[string]string{
		// Only the IMAGE cell is in the error color; the rest of the row,
		// gaps included, keeps the row's color.
		"a": "  " + render(Styles.RowGood, line[:s]) + render(Styles.RowBad, line[s:e]) + render(Styles.RowGood, line[e:]),
	}
	line, s, e = cellRangeOf(&tb, rows[1].Cells, 2)
	// An untoned row: only the toned cell is styled.
	want["b"] = "  " + line[:s] + render(Styles.RowWarn, line[s:e]) + line[e:]
	line, _, _ = cellRangeOf(&tb, rows[2].Cells, 0)
	// Unknown and default cell tones fall back to the row's tone.
	want["c"] = "  " + Styles.RowDim.Render(line)
	line, _, e = cellRangeOf(&tb, rows[3].Cells, 2)
	// Every cell in its own tone, the gaps between them in the row's tone;
	// entries past the last column are ignored.
	_, s0, e0 := cellRangeOf(&tb, rows[3].Cells, 0)
	_, s1, e1 := cellRangeOf(&tb, rows[3].Cells, 1)
	_, s2, _ := cellRangeOf(&tb, rows[3].Cells, 2)
	want["d"] = "  " + render(Styles.RowBad, line[s0:e0]) + render(Styles.RowGood, line[e0:s1]) +
		render(Styles.RowBad, line[s1:e1]) + render(Styles.RowGood, line[e1:s2]) + render(Styles.RowBad, line[s2:e]) +
		render(Styles.RowGood, line[e:])

	for i, r := range rows[:4] {
		got := lines[i+1]
		if got != want[r.Key] {
			t.Errorf("row %q rendered %q, want %q", r.Key, got, want[r.Key])
		}
		plain, _ := tb.renderLine(r.Cells, tb.widths())
		if s := ansi.Strip(got); s != "  "+plain {
			t.Errorf("row %q stripped %q, want the plain line %q", r.Key, s, "  "+plain)
		}
	}
	// The styles must differ, or the comparisons above prove nothing.
	if Styles.RowBad.Render("x") == Styles.RowGood.Render("x") || Styles.RowBad.Render("x") == "x" {
		t.Fatal("RowBad does not stand out")
	}

	// The selected row is the plain selection of the untoned line: neither
	// its row tone nor its cell tones are drawn.
	plain, _ := tb.renderLine(rows[4].Cells, tb.widths())
	wantSel := Styles.SelectionMarker.Render(selectionMarker) + Styles.TableSelected.Render(" "+plain)
	if got := lines[5]; got != wantSel {
		t.Errorf("selected row rendered %q, want %q", got, wantSel)
	}
	open, _, _ := strings.Cut(Styles.RowBad.Render("x"), "x")
	if strings.Contains(lines[5], open) {
		t.Error("selected row carries a cell tone")
	}
	// Moving the selection away shows the cell tone again.
	press(t, &tb, "k")
	_, s, e = cellRangeOf(&tb, rows[4].Cells, 0)
	if got := strings.Split(tb.View(), "\n")[5]; !strings.Contains(got, Styles.RowBad.Render(plain[s:e])) {
		t.Errorf("unselected row e %q does not show its bad cell", got)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 60 {
			t.Errorf("line %d is %d cells wide, want 60", i, w)
		}
	}
}

// A line cut by the table width keeps every cell's bytes and closes every
// style it opens, whichever cell the cut falls in.
func TestTableCellTonesTruncated(t *testing.T) {
	cells := []string{"alpha", "a-very-long-image-name-é中-that-is-cut", "running"}
	for width := 1; width <= 60; width++ {
		for _, tones := range [][]Tone{
			{ToneDefault, ToneBad},
			{ToneDefault, ToneDefault, ToneBad},
			{ToneBad, ToneWarn, ToneBad},
		} {
			tb := NewTable([]Column{
				{Title: "NAME", Width: 10},
				{Title: "IMAGE", Width: 12},
				{Title: "STATUS", Width: 8},
			})
			tb.SetSize(width, 3)
			tb.SetRows([]Row{
				{Key: "sel", Cells: []string{"x"}},
				{Key: "a", Cells: cells, Tone: ToneGood, CellTones: tones},
			})
			lines := strings.Split(tb.View(), "\n")
			got := lines[2]
			plain, _ := tb.renderLine(cells, tb.widths())
			wantPlain := Fit(strings.Repeat(" ", gutterWidth)+plain, width)
			if width < gutterWidth {
				wantPlain = strings.Repeat(" ", width)
			}
			if s := ansi.Strip(got); s != wantPlain {
				t.Errorf("width %d tones %v: stripped %q, want %q", width, tones, s, wantPlain)
			}
			if w := ansi.StringWidth(got); w != width {
				t.Errorf("width %d tones %v: line is %d cells wide", width, tones, w)
			}
			// Every style opened is closed: the line ends unstyled.
			if strings.Contains(got, "\x1b[") && !strings.HasSuffix(strings.TrimRight(got, " "), "\x1b[m") {
				t.Errorf("width %d tones %v: line %q does not end with a reset", width, tones, got)
			}
			if n := strings.Count(got, "\x1b[m"); n != strings.Count(got, "\x1b[")-n {
				t.Errorf("width %d tones %v: %q opens and closes unevenly", width, tones, got)
			}
			if width < gutterWidth {
				continue
			}
			// Each bad cell still visible after the cut is drawn in the error
			// color, cut and ellipsis included; a fully cut one leaves no
			// trace of it.
			_, ranges := tb.renderLine(cells, tb.widths())
			badOpen, _, _ := strings.Cut(Styles.RowBad.Render("x"), "x")
			anyBad := false
			for c, tn := range tones {
				// RenderSpans moves an offset inside a character (here the
				// ellipsis) to that character's end.
				start, end := runeBoundary(plain, ranges[c].start), runeBoundary(plain, ranges[c].end)
				if tn != ToneBad || start >= end {
					continue
				}
				anyBad = true
				part := plain[start:end]
				if !strings.Contains(got, Styles.RowBad.Render(part)) {
					t.Errorf("width %d tones %v: bad cell %d %q not in the error color in %q", width, tones, c, part, got)
				}
			}
			if !anyBad && strings.Contains(got, badOpen) {
				t.Errorf("width %d tones %v: fully cut bad cells still color %q", width, tones, got)
			}
		}
	}
	// A cut inside a bad cell draws the ellipsis in that cell's color.
	tb := NewTable([]Column{{Title: "NAME", Width: 10}, {Title: "IMAGE", Width: 12}})
	tb.SetSize(2+10+2+6, 3) // the IMAGE cell is cut to 6 cells
	tb.SetRows([]Row{
		{Key: "sel", Cells: []string{"x"}},
		{Key: "a", Cells: []string{"alpha", "postgres-database"}, Tone: ToneGood, CellTones: []Tone{0, ToneBad}},
	})
	got := strings.Split(tb.View(), "\n")[2]
	if !strings.HasSuffix(got, Styles.RowBad.Render("postg…")) {
		t.Errorf("cut bad cell %q does not end with its text and ellipsis in the error color", got)
	}
}

func TestRowSpans(t *testing.T) {
	ranges := []cellRange{{0, 3}, {5, 8}, {10, 14}}
	for _, tc := range []struct {
		name    string
		r       Row
		lineLen int
		want    []Span
	}{
		{"no tones", Row{}, 14, []Span{{0, 14, KindNone}}},
		{"row tone only", Row{Tone: ToneGood}, 14, []Span{{0, 14, KindStateGood}}},
		{"one cell", Row{Tone: ToneGood, CellTones: []Tone{0, ToneBad}}, 14,
			[]Span{{0, 5, KindStateGood}, {5, 8, KindStateBad}, {8, 14, KindStateGood}}},
		{"last cell, padded line", Row{CellTones: []Tone{0, 0, ToneWarn}}, 20,
			[]Span{{0, 10, KindNone}, {10, 14, KindStateWarn}, {14, 20, KindNone}}},
		{"cut inside a cell", Row{Tone: ToneDim, CellTones: []Tone{0, ToneBad}}, 6,
			[]Span{{0, 5, KindStateDim}, {5, 6, KindStateBad}}},
		{"cut before a toned cell", Row{Tone: ToneDim, CellTones: []Tone{0, 0, ToneBad}}, 7,
			[]Span{{0, 7, KindStateDim}}},
		{"empty line", Row{Tone: ToneGood, CellTones: []Tone{ToneBad}}, 0, []Span{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rowSpans(tc.r, tc.lineLen, ranges); !slices.Equal(got, tc.want) {
				t.Errorf("rowSpans = %v, want %v", got, tc.want)
			}
		})
	}
}

// newMeterTable returns a table with a NAME column and a flex MEM meter
// column of minimum width 19, whose rendered width is width-10 (the gutter,
// NAME and the gap take the rest), with the first row selected.
func newMeterTable(width int, rows ...Row) Table {
	tb := NewTable([]Column{
		{Title: "NAME", Width: 6, Filter: true},
		{Title: "MEM", Width: 19, Flex: true, Filter: true, Meter: true},
	})
	tb.SetSize(width, 10)
	tb.SetRows(rows)
	return tb
}

func meterRow(key, mem string, level float64) Row {
	return Row{Key: key, Cells: []string{key, mem}, Meter: level, HasMeter: true}
}

// memCells returns the stripped MEM cell of every body line, trailing
// blanks removed.
func memCells(t *testing.T, tb *Table) []string {
	t.Helper()
	lines := strings.Split(ansi.Strip(tb.View()), "\n")[1:]
	var cells []string
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		cells = append(cells, strings.TrimRight(ansi.Cut(l, gutterWidth+6+columnGap, ansi.StringWidth(l)), " "))
	}
	return cells
}

func TestTableMeter(t *testing.T) {
	rows := []Row{
		meterRow("db", "999.9MiB / 2GiB", 0.25), // 15 cells, the widest
		meterRow("redis", "8.1MiB / 1GiB", 1),
		{Key: "old", Cells: []string{"old", "-"}},
		meterRow("short", "1B / 1GiB", 0),
	}
	for _, tc := range []struct {
		name  string
		width int
		want  []string
	}{
		{"ample width draws 10-cell bars", 60, []string{
			"999.9MiB / 2GiB  ██▌░░░░░░░",
			"8.1MiB / 1GiB    ██████████",
			"-",
			"1B / 1GiB        ░░░░░░░░░░",
		}},
		{"5 cells left draws 5-cell bars", 32, []string{
			"999.9MiB / 2GiB  █▎░░░",
			"8.1MiB / 1GiB    █████",
			"-",
			"1B / 1GiB        ░░░░░",
		}},
		{"4 cells left draws no bar", 31, []string{"999.9MiB / 2GiB", "8.1MiB / 1GiB", "-", "1B / 1GiB"}},
		{"bars in between are as wide as the spare width", 35, []string{
			"999.9MiB / 2GiB  ██░░░░░░",
			"8.1MiB / 1GiB    ████████",
			"-",
			"1B / 1GiB        ░░░░░░░░",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := newMeterTable(tc.width, rows...)
			if got := memCells(t, &tb); !slices.Equal(got, tc.want) {
				t.Errorf("MEM cells = %q, want %q", got, tc.want)
			}
			for i, l := range strings.Split(tb.View(), "\n") {
				if w := ansi.StringWidth(l); w != tc.width {
					t.Errorf("line %d is %d cells wide, want %d", i, w, tc.width)
				}
			}
		})
	}
}

// At its minimum width a meter column draws no bar, however much blank the
// text leaves.
func TestTableMeterAtMinimumWidth(t *testing.T) {
	// The widest text is 9 cells, in a 19-cell column.
	tb := newMeterTable(29, meterRow("a", "52KiB/1Gi", 0.5), meterRow("b", "9KiB / 1G", 0.9))
	if ws := tb.widths(); ws[1] != 19 {
		t.Fatalf("MEM is %d cells wide, want its minimum 19", ws[1])
	}
	if got, want := memCells(t, &tb), []string{"52KiB/1Gi", "9KiB / 1G"}; !slices.Equal(got, want) {
		t.Errorf("MEM cells = %q, want %q", got, want)
	}
	// One cell wider and the same text has room for a 20-9-2 = 9-cell bar.
	tb.SetSize(30, 10)
	if got, want := memCells(t, &tb)[0], "52KiB/1Gi  "+Bar(0.5, 9); got != want {
		t.Errorf("MEM cell at column width 20 = %q, want %q", got, want)
	}
}

func TestTableMeterRowsWithoutMeter(t *testing.T) {
	plain := []Row{
		{Key: "a", Cells: []string{"a", "1MiB / 1GiB"}, Tone: ToneGood},
		{Key: "b", Cells: []string{"b", "-"}, Meter: 0.5}, // Meter without HasMeter
		{Key: "c", Cells: []string{"c"}, HasMeter: true, Meter: 1},
	}
	tb := newMeterTable(60, plain...)
	// The same rows in a table without a meter column render identically.
	ref := NewTable([]Column{
		{Title: "NAME", Width: 6, Filter: true},
		{Title: "MEM", Width: 19, Flex: true, Filter: true},
	})
	ref.SetSize(60, 10)
	ref.SetRows(plain)
	if got, want := tb.View(), ref.View(); got != want {
		t.Errorf("rows without a meter changed:\n%q\nwant\n%q", got, want)
	}
	// A row that has no cell in the meter column gets no bar.
	if strings.Contains(ansi.Strip(tb.View()), barEmpty) || strings.Contains(ansi.Strip(tb.View()), barFull) {
		t.Error("a bar was drawn for a row without HasMeter or without a MEM cell")
	}
}

func TestTableMeterAlignmentFollowsFilter(t *testing.T) {
	tb := newMeterTable(60,
		meterRow("db", "999.9MiB / 2GiB", 0.25),
		meterRow("redis", "8.1MiB / 1GiB", 0.5),
		meterRow("rabbit", "12MiB / 1GiB", 0.5),
	)
	press(t, &tb, "/")
	typeText(&tb, "r")
	press(t, &tb, "enter")
	want := []string{
		"8.1MiB / 1GiB  █████░░░░░",
		"12MiB / 1GiB   █████░░░░░",
	}
	if got := memCells(t, &tb); !slices.Equal(got, want) {
		t.Errorf("filtered MEM cells = %q, want %q", got, want)
	}
	tb.ClearFilter()
	if got := memCells(t, &tb); got[1] != "8.1MiB / 1GiB    █████░░░░░" {
		t.Errorf("unfiltered MEM cell = %q, want the bar after the widest text again", got[1])
	}
}

func TestTableMeterToneAndSelection(t *testing.T) {
	warn := meterRow("api", "800MiB / 1GiB", 0.8)
	warn.Tone, warn.CellTones = ToneGood, []Tone{ToneDefault, ToneWarn}
	sel := meterRow("db", "999.9MiB / 2GiB", 0.95)
	sel.Tone, sel.CellTones = ToneGood, []Tone{ToneDefault, ToneBad}
	tb := newMeterTable(60, sel, warn)
	lines := strings.Split(tb.View(), "\n")

	// The bar is part of the cell, so it is drawn in the cell's tone.
	cell := Fit("800MiB / 1GiB", 15) + "  " + Bar(0.8, 10)
	if !strings.Contains(lines[2], Styles.RowWarn.Render(Fit(cell, 50))) {
		t.Errorf("warn row %q does not draw its text and bar in the warning color", lines[2])
	}
	// The selected row shows its bar, in the selection style alone.
	if !strings.Contains(ansi.Strip(lines[1]), Bar(0.95, 10)) {
		t.Errorf("selected row %q has no bar", ansi.Strip(lines[1]))
	}
	plain, _ := tb.renderLine(tb.meterCells(sel, tb.meterWidths(tb.widths())), tb.widths())
	if want := Styles.SelectionMarker.Render(selectionMarker) + Styles.TableSelected.Render(" "+plain); lines[1] != want {
		t.Errorf("selected row rendered %q, want %q", lines[1], want)
	}
	badOpen, _, _ := strings.Cut(Styles.RowBad.Render("x"), "x")
	if strings.Contains(lines[1], badOpen) {
		t.Error("selected row carries its cell tone")
	}
}

// The bar is added at render time: it changes neither the natural widths
// nor what the filter matches, and the caller's cells are left untouched.
func TestTableMeterLeavesWidthsAndFilterAlone(t *testing.T) {
	rows := []Row{meterRow("db", "999.9MiB / 2GiB", 1), meterRow("redis", "8.1MiB / 1GiB", 0.5)}
	cols := func(meter bool) []Column {
		return []Column{
			{Title: "NAME", Width: 6, Flex: true, Filter: true},
			{Title: "MEM", Width: 10, Flex: true, Filter: true, Meter: meter},
		}
	}
	with, without := NewTable(cols(true)), NewTable(cols(false))
	for _, tb := range []*Table{&with, &without} {
		tb.SetSize(80, 10)
		tb.SetRows(rows)
	}
	if !slices.Equal(with.natural, without.natural) || !slices.Equal(with.widths(), without.widths()) {
		t.Errorf("meter changed the widths: natural %v / %v, widths %v / %v",
			with.natural, without.natural, with.widths(), without.widths())
	}
	if !strings.Contains(with.View(), barFull) {
		t.Fatal("no bar drawn, so the checks below prove nothing")
	}
	if rows[0].Cells[1] != "999.9MiB / 2GiB" {
		t.Errorf("caller's cell modified to %q", rows[0].Cells[1])
	}
	for _, needle := range []string{barFull, barEmpty, "▌", "  █"} {
		with.ClearFilter()
		press(t, &with, "/")
		typeText(&with, needle)
		if _, listed, _, _ := with.Position(); listed != 0 {
			t.Errorf("filter %q matched %d rows through the bar", needle, listed)
		}
	}
}

// Replacing the columns or the rows recomputes where bars go, and untrusted
// MEM text stays cleaned when a bar is drawn after it.
func TestTableMeterAfterReplacingColumnsAndRows(t *testing.T) {
	tb := newMeterTable(60, meterRow("db", "999.9MiB / 2GiB", 0.5))
	tb.SetColumns([]Column{
		{Title: "NAME", Width: 6, Filter: true},
		{Title: "MEM", Width: 19, Flex: true, Meter: true},
	})
	if v := ansi.Strip(tb.View()); strings.Contains(v, barEmpty) || strings.Contains(v, barFull) {
		t.Errorf("bar drawn before SetRows: %q", v)
	}
	// Shorter text moves the bar left; wide runes count by cells.
	tb.SetRows([]Row{meterRow("db", "1MiB / 2GiB", 0.5), meterRow("cn", "中中", 0.5)})
	if got, want := memCells(t, &tb), []string{
		"1MiB / 2GiB  █████░░░░░",
		"中中         █████░░░░░",
	}; !slices.Equal(got, want) {
		t.Errorf("MEM cells = %q, want %q", got, want)
	}
	// Escape bytes in a cell are replaced, not passed to the terminal.
	tb.SetRows([]Row{meterRow("sel", "x", 0), meterRow("evil", "\x1b[31mX\x07", 0.5)})
	line := strings.Split(tb.View(), "\n")[2]
	if strings.ContainsAny(line, "\x1b\x07") {
		t.Errorf("unselected untoned row %q holds raw control bytes", line)
	}
	if !strings.Contains(line, Bar(0.5, 10)) {
		t.Errorf("row %q has no bar", line)
	}
}
