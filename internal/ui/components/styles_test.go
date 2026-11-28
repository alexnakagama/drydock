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
