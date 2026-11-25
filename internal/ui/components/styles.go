package components

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// palette is the set of colors a StyleSet is built from (ANSI 256 colors).
type palette struct {
	accent  color.Color // blue
	muted   color.Color // grey
	success color.Color // green
	error   color.Color // red
	warn    color.Color // orange

	onAccent  color.Color // text drawn on an accent background
	bar       color.Color // header bar background
	selection color.Color // selected-row background, a subtle accent tint
}

// darkPalette is used on dark terminal backgrounds, and until the terminal
// reports its background.
var darkPalette = palette{
	accent:    lipgloss.Color("39"),
	muted:     lipgloss.Color("245"),
	success:   lipgloss.Color("42"),
	error:     lipgloss.Color("203"),
	warn:      lipgloss.Color("214"),
	onAccent:  lipgloss.Color("16"),
	bar:       lipgloss.Color("235"),
	selection: lipgloss.Color("24"),
}

// lightPalette uses darker shades of the same hues, so text stays readable
// on light terminal backgrounds, the header bar included.
var lightPalette = palette{
	accent:    lipgloss.Color("25"),
	muted:     lipgloss.Color("240"),
	success:   lipgloss.Color("28"),
	error:     lipgloss.Color("160"),
	warn:      lipgloss.Color("130"),
	onAccent:  lipgloss.Color("231"),
	bar:       lipgloss.Color("255"),
	selection: lipgloss.Color("153"),
}

// StyleSet holds every style used by drydock's UI, so the look is defined
// in one place.
type StyleSet struct {
	// Dark reports whether the set was built for a dark terminal
	// background.
	Dark bool

	// Header and footer.
	Header       lipgloss.Style
	HeaderBar    lipgloss.Style // the header line's background, alone
	Breadcrumb   lipgloss.Style
	Crumb        lipgloss.Style // the last (current) breadcrumb segment
	Indicator    lipgloss.Style
	Connected    lipgloss.Style
	Connecting   lipgloss.Style
	Disconnected lipgloss.Style
	FooterKey    lipgloss.Style
	FooterDesc   lipgloss.Style

	// Resource tab strip, the rule below it, and list position.
	TabActive     lipgloss.Style
	Tab           lipgloss.Style
	TabPillActive lipgloss.Style // the active tab, as a filled pill
	TabPill       lipgloss.Style // an inactive tab, padded like TabPillActive
	Rule          lipgloss.Style
	Position      lipgloss.Style

	// Status line.
	StatusSuccess  lipgloss.Style
	StatusError    lipgloss.Style
	StatusProgress lipgloss.Style

	// Table.
	TableHeader     lipgloss.Style
	TableSelected   lipgloss.Style // the selected row's background bar
	SelectionMarker lipgloss.Style // the selected row's gutter marker
	Empty           lipgloss.Style
	RowGood         lipgloss.Style // unselected row with ToneGood
	RowWarn         lipgloss.Style // unselected row with ToneWarn
	RowDim          lipgloss.Style // unselected row with ToneDim

	// Overlays and inputs.
	Dialog        lipgloss.Style
	ConfirmDialog lipgloss.Style // Dialog for a destructive action
	DialogTitle   lipgloss.Style
	DialogHint    lipgloss.Style
	HelpKey       lipgloss.Style
	HelpSection   lipgloss.Style
	InputPrompt   lipgloss.Style
}

// NewStyles returns the style set for a dark (dark true) or light terminal
// background. Only colors differ between the two; text attributes, padding
// and borders are the same.
func NewStyles(dark bool) StyleSet {
	p := lightPalette
	if dark {
		p = darkPalette
	}
	dialog := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.accent).
		Padding(0, 1)
	return StyleSet{
		Dark: dark,

		Header:       lipgloss.NewStyle().Bold(true),
		HeaderBar:    lipgloss.NewStyle().Background(p.bar),
		Breadcrumb:   lipgloss.NewStyle().Foreground(p.muted),
		Crumb:        lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		Indicator:    lipgloss.NewStyle().Foreground(p.warn),
		Connected:    lipgloss.NewStyle().Foreground(p.success),
		Connecting:   lipgloss.NewStyle().Foreground(p.warn),
		Disconnected: lipgloss.NewStyle().Bold(true).Foreground(p.error),
		FooterKey:    lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		FooterDesc:   lipgloss.NewStyle().Foreground(p.muted),

		TabActive:     lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		Tab:           lipgloss.NewStyle().Foreground(p.muted),
		TabPillActive: lipgloss.NewStyle().Bold(true).Foreground(p.onAccent).Background(p.accent).Padding(0, 1),
		TabPill:       lipgloss.NewStyle().Foreground(p.muted).Padding(0, 1),
		Rule:          lipgloss.NewStyle().Foreground(p.muted),
		Position:      lipgloss.NewStyle().Foreground(p.muted),

		StatusSuccess:  lipgloss.NewStyle().Foreground(p.success),
		StatusError:    lipgloss.NewStyle().Foreground(p.error),
		StatusProgress: lipgloss.NewStyle().Foreground(p.accent),

		TableHeader:     lipgloss.NewStyle().Bold(true).Foreground(p.muted),
		TableSelected:   lipgloss.NewStyle().Bold(true).Background(p.selection),
		SelectionMarker: lipgloss.NewStyle().Foreground(p.accent),
		Empty:           lipgloss.NewStyle().Foreground(p.muted).Italic(true),
		RowGood:         lipgloss.NewStyle().Foreground(p.success),
		RowWarn:         lipgloss.NewStyle().Foreground(p.warn),
		RowDim:          lipgloss.NewStyle().Foreground(p.muted),

		Dialog:        dialog,
		ConfirmDialog: dialog.BorderForeground(p.error),
		DialogTitle:   lipgloss.NewStyle().Bold(true),
		DialogHint:    lipgloss.NewStyle().Foreground(p.muted),
		HelpKey:       lipgloss.NewStyle().Bold(true).Foreground(p.accent),
		HelpSection:   lipgloss.NewStyle().Bold(true).Underline(true),
		InputPrompt:   lipgloss.NewStyle().Bold(true).Foreground(p.accent),
	}
}

// Styles is the style set used by every component and view. It starts as
// the dark set; the root model replaces it when the terminal reports its
// background color. Like the rest of the UI state, it is only read and
// written on the Bubble Tea event loop.
var Styles = NewStyles(true)
