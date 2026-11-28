package components

import (
	"math"
	"strings"
)

const (
	barFull  = "█"
	barEmpty = "░"
)

// barPartials are the partial blocks for 1 to 7 eighths of a cell.
var barPartials = [...]string{"▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// Bar draws fraction as a bar exactly width cells wide: full blocks, at most
// one partial block for the last eighth-cell (rounded to the nearest eighth,
// a half rounding up) and empty blocks for the rest. fraction is clamped to
// [0, 1]; NaN draws an empty bar. A width of 0 or less draws nothing. Every
// glyph is one cell wide.
func Bar(fraction float64, width int) string {
	if width <= 0 {
		return ""
	}
	switch {
	case !(fraction > 0): // also NaN
		fraction = 0
	case fraction > 1:
		fraction = 1
	}
	// fraction is in [0, 1], so eighths is in [0, width*8].
	eighths := int(math.Round(fraction * float64(width) * 8))
	full, part := eighths/8, eighths%8

	var b strings.Builder
	b.Grow(width * len(barFull))
	b.WriteString(strings.Repeat(barFull, full))
	empty := width - full
	if part > 0 {
		b.WriteString(barPartials[part-1])
		empty--
	}
	b.WriteString(strings.Repeat(barEmpty, empty))
	return b.String()
}
