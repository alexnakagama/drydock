package components

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// horizontalStep is how many cells one left/right key press scrolls.
const horizontalStep = 8

// Pager is the scroll state of a window over a list of text lines. It does
// not own the lines: the view that embeds it reports how many there are
// (SetTotal) and how wide the widest is (SetContentWidth), and supplies
// them when rendering, so a view can keep its lines in whatever structure
// suits it (a slice, a ring buffer, a filtered index). Lines must be plain
// and display-safe; styling is supplied as spans (see ViewStyled).
//
// The zero value is an empty pager of size 0x0.
type Pager struct {
	width, height int
	total         int // number of lines
	contentWidth  int // widest line, in cells
	top           int // index of the first visible line
	left          int // horizontal scroll, in cells
}

// SetSize sets the window size.
func (p *Pager) SetSize(width, height int) {
	p.width, p.height = max(width, 0), max(height, 0)
	p.clamp()
}

// Height returns the window height in lines.
func (p *Pager) Height() int { return p.height }

// SetTotal sets the number of lines, keeping the scroll position when
// possible.
func (p *Pager) SetTotal(n int) {
	p.total = max(n, 0)
	p.clamp()
}

// SetContentWidth sets the width of the widest line, which bounds
// horizontal scrolling.
func (p *Pager) SetContentWidth(w int) {
	p.contentWidth = max(w, 0)
	p.clamp()
}

// Top returns the index of the first visible line.
func (p *Pager) Top() int { return p.top }

// ScrollBy scrolls down by delta lines (up if negative).
func (p *Pager) ScrollBy(delta int) {
	p.top += delta
	p.clamp()
}

// GotoTop scrolls to the first line.
func (p *Pager) GotoTop() {
	p.top = 0
	p.clamp()
}

// GotoBottom scrolls so that the last line is at the bottom of the window.
func (p *Pager) GotoBottom() {
	p.top = p.maxTop()
	p.clamp()
}

// AtBottom reports whether the last line is visible.
func (p *Pager) AtBottom() bool { return p.top >= p.maxTop() }

// Update handles the scrolling keys (see keys.Pager) and reports whether
// msg was one of them.
func (p *Pager) Update(msg tea.KeyPressMsg) bool {
	km := keys.Pager
	page := max(p.height, 1)
	switch {
	case key.Matches(msg, km.Up):
		p.ScrollBy(-1)
	case key.Matches(msg, km.Down):
		p.ScrollBy(1)
	case key.Matches(msg, km.Top):
		p.GotoTop()
	case key.Matches(msg, km.Bottom):
		p.GotoBottom()
	case key.Matches(msg, km.PageUp):
		p.ScrollBy(-page)
	case key.Matches(msg, km.PageDown):
		p.ScrollBy(page)
	case key.Matches(msg, km.Left):
		p.left -= horizontalStep
		p.clamp()
	case key.Matches(msg, km.Right):
		p.left += horizontalStep
		p.clamp()
	default:
		return false
	}
	return true
}

func (p *Pager) maxTop() int { return max(p.total-p.height, 0) }

func (p *Pager) clamp() {
	p.top = min(max(p.top, 0), p.maxTop())
	p.left = min(max(p.left, 0), max(p.contentWidth-p.width, 0))
}

// View renders the visible lines into exactly the window size. line returns
// the line at an index in [0, total). Lines must be plain and display-safe
// (see Clean): they hold no ANSI sequences. Use ViewStyled to supply styling
// as spans.
func (p *Pager) View(line func(i int) string) string {
	return p.render(line)
}

// ViewStyled is View for styled lines: line returns the plain, display-safe
// text of a line and the spans that style it (see RenderSpans). Styling is
// applied before the horizontal cut, so a span that starts left of the
// window keeps its style in the visible part, and the text shown is exactly
// what View shows for the same plain lines. The content width set with
// SetContentWidth is still that of the plain text.
func (p *Pager) ViewStyled(line func(i int) (string, []Span)) string {
	return p.render(func(i int) string {
		text, spans := line(i)
		return RenderSpans(text, spans)
	})
}

// render draws the window from lines that may hold ANSI styling.
func (p *Pager) render(line func(i int) string) string {
	if p.width <= 0 || p.height <= 0 {
		return ""
	}
	out := make([]string, 0, p.height)
	for i := p.top; i < p.total && len(out) < p.height; i++ {
		l := line(i)
		if p.left > 0 {
			l = ansi.Cut(l, p.left, p.left+p.width)
		}
		out = append(out, Fit(l, p.width))
	}
	blank := strings.Repeat(" ", p.width)
	for len(out) < p.height {
		out = append(out, blank)
	}
	return strings.Join(out, "\n")
}
