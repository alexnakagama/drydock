package components

import (
	"fmt"
	"strings"
	"testing"

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
