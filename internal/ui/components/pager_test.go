package components

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/uitest"
)

func TestPagerScrolling(t *testing.T) {
	var p Pager
	p.SetSize(10, 3)
	p.SetTotal(10)
	line := func(i int) string { return fmt.Sprintf("line %d", i) }
	first := func() string { return strings.TrimSpace(strings.SplitN(p.View(line), "\n", 2)[0]) }

	if first() != "line 0" || p.AtBottom() {
		t.Fatalf("initial first line %q, at bottom %v", first(), p.AtBottom())
	}
	for _, step := range []struct {
		key  string
		want string
	}{
		{"j", "line 1"}, {"G", "line 7"}, {"j", "line 7"}, {"k", "line 6"},
		{"g", "line 0"}, {"k", "line 0"}, {"pgdown", "line 3"}, {"pgup", "line 0"},
	} {
		if !p.Update(uitest.KeyPress(step.key)) {
			t.Fatalf("%s not handled", step.key)
		}
		if got := first(); got != step.want {
			t.Errorf("after %s first line = %q, want %q", step.key, got, step.want)
		}
	}
	if p.Update(uitest.KeyPress("x")) {
		t.Error("x handled")
	}
	p.GotoBottom()
	if !p.AtBottom() {
		t.Error("GotoBottom not at bottom")
	}
	// Fewer lines than the window: always at the bottom, padded.
	p.SetTotal(1)
	if !p.AtBottom() || p.Top() != 0 {
		t.Errorf("short content: top %d", p.Top())
	}
	if got := p.View(line); len(strings.Split(got, "\n")) != 3 {
		t.Errorf("View has %d lines, want 3", len(strings.Split(got, "\n")))
	}
}

func TestPagerHorizontalScroll(t *testing.T) {
	var p Pager
	p.SetSize(5, 1)
	p.SetTotal(1)
	p.SetContentWidth(20)
	line := func(int) string { return "abcdefghijklmnopqrst" }
	p.Update(uitest.KeyPress("right"))
	if got := p.View(line); got != "ijklm" {
		t.Errorf("after right = %q", got)
	}
	for range 5 {
		p.Update(uitest.KeyPress("right"))
	}
	if got := p.View(line); got != "pqrst" {
		t.Errorf("scrolled past the end: %q", got)
	}
	p.Update(uitest.KeyPress("left"))
	p.Update(uitest.KeyPress("left"))
	p.Update(uitest.KeyPress("left"))
	if got := p.View(line); got != "abcd…" { // cut lines end with an ellipsis
		t.Errorf("after left = %q", got)
	}
}

func TestPagerZeroSize(t *testing.T) {
	var p Pager
	p.SetTotal(5)
	if p.View(func(int) string { return "x" }) != "" {
		t.Error("zero-size pager rendered something")
	}
}

func TestPagerViewStyled(t *testing.T) {
	// Each line has a string span crossing both window edges once scrolled,
	// and wide characters that the cut can split.
	lines := []string{
		`  "Image": "nginx:1.27-alpine-long-value",`,
		`  "Name": "中文中文中文中文中文",`,
		`  "x": 1`,
	}
	spans := func(i int) []Span {
		l := lines[i]
		colon := strings.Index(l, ":")
		end := len(l)
		if strings.HasSuffix(l, ",") {
			end--
		}
		return []Span{
			{2, colon, KindJSONKey},
			{colon, colon + 1, KindJSONPunct},
			{colon + 2, end, KindJSONString},
		}
	}
	plain := func(i int) string { return lines[i] }
	styled := func(i int) (string, []Span) { return lines[i], spans(i) }
	sgr := func(k SpanKind) string {
		s, _, _ := strings.Cut(k.style().Render("x"), "x")
		return s
	}

	for _, size := range []struct{ width, left int }{
		{80, 0}, {12, 0}, {12, 13}, {12, 14}, {12, 15}, {7, 18}, {5, 30},
	} {
		var p Pager
		p.SetSize(size.width, len(lines))
		p.SetTotal(len(lines))
		p.SetContentWidth(ansi.StringWidth(lines[0]))
		p.left = size.left
		p.clamp()

		want := p.View(plain)
		got := p.ViewStyled(styled)
		if s := ansi.Strip(got); s != want {
			t.Errorf("width %d left %d: stripped\n%q\nwant\n%q", size.width, p.left, s, want)
		}
		for i, l := range strings.Split(got, "\n") {
			if w := ansi.StringWidth(l); w != size.width {
				t.Errorf("width %d left %d line %d: %d cells", size.width, p.left, i, w)
			}
			// A line cut inside the string span starts in that span's style.
			if sp := spans(i)[2]; p.left > ansi.StringWidth(lines[i][:sp.Start]) && p.left < ansi.StringWidth(lines[i][:sp.End]) {
				if got := leadingStyle(l); got != sgr(KindJSONString) {
					t.Errorf("width %d left %d line %d: %q starts in style %q, want the string style", size.width, p.left, i, l, got)
				}
			}
			// Styling never leaks past the end of a line.
			if last := strings.LastIndex(l, "\x1b["); last >= 0 && !strings.HasPrefix(l[last:], "\x1b[m") {
				t.Errorf("width %d left %d line %d: %q leaves a style open", size.width, p.left, i, l)
			}
		}
	}
}

// leadingStyle returns the last escape sequence before the first printable
// character of l: the style that character is drawn in. ansi.Cut keeps the
// sequences of spans left of the cut, which are already closed.
func leadingStyle(l string) string {
	last := ""
	for strings.HasPrefix(l, "\x1b[") {
		end := strings.IndexByte(l, 'm')
		if end < 0 {
			break
		}
		last, l = l[:end+1], l[end+1:]
	}
	return last
}
