package components

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestContentStylesRenderStyled(t *testing.T) {
	for _, dark := range []bool{true, false} {
		s := NewStyles(dark)
		for name, st := range map[string]lipgloss.Style{
			"JSONKey":     s.JSONKey,
			"JSONString":  s.JSONString,
			"JSONNumber":  s.JSONNumber,
			"JSONPunct":   s.JSONPunct,
			"LogTime":     s.LogTime,
			"LogError":    s.LogError,
			"LogWarn":     s.LogWarn,
			"LogDebug":    s.LogDebug,
			"SearchMatch": s.SearchMatch,
		} {
			if got := st.Render("sample"); got == "sample" {
				t.Errorf("dark=%v: %s renders plain text", dark, name)
			}
		}
		if !s.SearchMatch.GetUnderline() {
			t.Errorf("dark=%v: SearchMatch is not underlined", dark)
		}
		// The underline must reach the terminal, not only the style.
		if s.SearchMatch.Render("x") == s.SearchMatch.Underline(false).Render("x") {
			t.Errorf("dark=%v: SearchMatch output has no underline", dark)
		}
		if !s.LogError.GetBold() {
			t.Errorf("dark=%v: LogError is not bold", dark)
		}
	}
	if NewStyles(true).SearchMatch.Render("x") == NewStyles(false).SearchMatch.Render("x") {
		t.Error("SearchMatch is the same on both palettes")
	}
}

// RowBad is the error color on both palettes, and stands apart from the
// other row tones, so a critical cell is visible inside a good or warn row.
func TestRowBadUsesErrorColor(t *testing.T) {
	for _, tc := range []struct {
		dark bool
		p    palette
	}{{true, darkPalette}, {false, lightPalette}} {
		s := NewStyles(tc.dark)
		if got := s.RowBad.GetForeground(); got != tc.p.error {
			t.Errorf("dark=%v: RowBad foreground %v, want the error color %v", tc.dark, got, tc.p.error)
		}
		if s.RowBad.GetBackground() != (lipgloss.NoColor{}) || s.RowBad.GetBold() {
			t.Errorf("dark=%v: RowBad sets more than its foreground", tc.dark)
		}
		bad := s.RowBad.Render("x")
		for name, other := range map[string]lipgloss.Style{"RowGood": s.RowGood, "RowWarn": s.RowWarn, "RowDim": s.RowDim} {
			if bad == other.Render("x") {
				t.Errorf("dark=%v: RowBad renders like %s", tc.dark, name)
			}
		}
		if bad == "x" {
			t.Errorf("dark=%v: RowBad renders plain text", tc.dark)
		}
	}
}
