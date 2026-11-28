package components

import (
	"math"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBar(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fraction float64
		width    int
		want     string
	}{
		{"zero", 0, 10, "░░░░░░░░░░"},
		{"quarter", 0.25, 10, "██▌░░░░░░░"},
		{"rounds down to the nearest eighth", 1.4 / 80, 10, "▏░░░░░░░░░"},
		{"rounds up to the nearest eighth", 1.6 / 80, 10, "▎░░░░░░░░░"},
		// Exact binary fractions, so the half is not lost to rounding error.
		{"exact half rounds up", 2.5 / 80, 10, "▍░░░░░░░░░"},
		{"exact half of the first eighth rounds up", 0.5 / 80, 10, "▏░░░░░░░░░"},
		{"just under half an eighth", 0.49 / 80, 10, "░░░░░░░░░░"},
		{"five eighths", 5.0 / 80, 10, "▋░░░░░░░░░"},
		{"six eighths", 6.0 / 80, 10, "▊░░░░░░░░░"},
		{"seven eighths", 7.0 / 80, 10, "▉░░░░░░░░░"},
		{"full cells only", 0.4, 10, "████░░░░░░"},
		{"almost full", 0.99, 10, "█████████▉"}, // 79.2 eighths
		{"rounds up to full", 79.5 / 80, 10, "██████████"},
		{"exactly full", 1, 10, "██████████"},
		{"over full", 1.7, 10, "██████████"},
		{"infinite", math.Inf(1), 5, "█████"},
		{"negative", -0.3, 10, "░░░░░░░░░░"},
		{"negative infinite", math.Inf(-1), 5, "░░░░░"},
		{"NaN", math.NaN(), 5, "░░░░░"},
		{"width 5 zero", 0, 5, "░░░░░"},
		{"width 5 quarter", 0.25, 5, "█▎░░░"}, // 10 eighths
		{"width 5 full", 1, 5, "█████"},
		{"width 1", 0.5, 1, "▌"},
		{"width 0", 0.5, 0, ""},
		{"negative width", 0.5, -3, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Bar(tc.fraction, tc.width)
			if got != tc.want {
				t.Errorf("Bar(%v, %d) = %q, want %q", tc.fraction, tc.width, got, tc.want)
			}
			if w := ansi.StringWidth(got); w != max(tc.width, 0) {
				t.Errorf("Bar(%v, %d) is %d cells wide, want %d", tc.fraction, tc.width, w, max(tc.width, 0))
			}
		})
	}
}

// Every fraction draws exactly width cells, and more usage never draws a
// shorter filled part.
func TestBarWidthAndMonotonic(t *testing.T) {
	for _, width := range []int{1, 5, 7, 10} {
		prev := -1
		for i := -10; i <= 1100; i++ {
			f := float64(i) / 1000
			got := Bar(f, width)
			if w := ansi.StringWidth(got); w != width {
				t.Fatalf("Bar(%v, %d) = %q is %d cells wide", f, width, got, w)
			}
			filled := barEighths(got)
			if filled < prev {
				t.Fatalf("Bar(%v, %d) = %q fills less than the previous fraction", f, width, got)
			}
			prev = filled
		}
		if prev != width*8 {
			t.Errorf("width %d: a full fraction fills %d eighths, want %d", width, prev, width*8)
		}
	}
}

// barEighths counts the eighths a bar fills.
func barEighths(bar string) int {
	n := 0
	for _, r := range bar {
		switch s := string(r); s {
		case barFull:
			n += 8
		case barEmpty:
		default:
			for i, p := range barPartials {
				if p == s {
					n += i + 1
				}
			}
		}
	}
	return n
}
