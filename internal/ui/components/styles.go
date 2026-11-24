package components

import "charm.land/lipgloss/v2"

// Palette (ANSI 256 colours, readable on dark and light backgrounds).
var (
	colorAccent  = lipgloss.Color("39")  // blue
	colorMuted   = lipgloss.Color("245") // grey
	colorSuccess = lipgloss.Color("42")  // green
	colorError   = lipgloss.Color("203") // red
	colorWarn    = lipgloss.Color("214") // orange
)

// StyleSet holds every style used by drydock's UI, so the look is defined
// in one place.
type StyleSet struct {
	// Header and footer.
	Header       lipgloss.Style
	Breadcrumb   lipgloss.Style
	Crumb        lipgloss.Style // the last (current) breadcrumb segment
	Indicator    lipgloss.Style
	Connected    lipgloss.Style
	Connecting   lipgloss.Style
	Disconnected lipgloss.Style
	FooterKey    lipgloss.Style
	FooterDesc   lipgloss.Style

	// Resource tab strip and list position.
	TabActive lipgloss.Style
	Tab       lipgloss.Style
	Position  lipgloss.Style

	// Status line.
	StatusSuccess  lipgloss.Style
	StatusError    lipgloss.Style
	StatusProgress lipgloss.Style

	// Table.
	TableHeader   lipgloss.Style
	TableSelected lipgloss.Style
	Empty         lipgloss.Style
	RowGood       lipgloss.Style // unselected row with ToneGood
	RowWarn       lipgloss.Style // unselected row with ToneWarn
	RowDim        lipgloss.Style // unselected row with ToneDim

	// Overlays and inputs.
	Dialog      lipgloss.Style
	DialogTitle lipgloss.Style
	DialogHint  lipgloss.Style
	HelpKey     lipgloss.Style
	HelpSection lipgloss.Style
	InputPrompt lipgloss.Style
}

// Styles is the style set used by every component and view.
var Styles = StyleSet{
	Header:       lipgloss.NewStyle().Bold(true),
	Breadcrumb:   lipgloss.NewStyle().Foreground(colorMuted),
	Crumb:        lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
	Indicator:    lipgloss.NewStyle().Foreground(colorWarn),
	Connected:    lipgloss.NewStyle().Foreground(colorSuccess),
	Connecting:   lipgloss.NewStyle().Foreground(colorWarn),
	Disconnected: lipgloss.NewStyle().Bold(true).Foreground(colorError),
	FooterKey:    lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
	FooterDesc:   lipgloss.NewStyle().Foreground(colorMuted),

	TabActive: lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
	Tab:       lipgloss.NewStyle().Foreground(colorMuted),
	Position:  lipgloss.NewStyle().Foreground(colorMuted),

	StatusSuccess:  lipgloss.NewStyle().Foreground(colorSuccess),
	StatusError:    lipgloss.NewStyle().Foreground(colorError),
	StatusProgress: lipgloss.NewStyle().Foreground(colorAccent),

	TableHeader:   lipgloss.NewStyle().Bold(true).Foreground(colorMuted),
	TableSelected: lipgloss.NewStyle().Reverse(true),
	Empty:         lipgloss.NewStyle().Foreground(colorMuted).Italic(true),
	RowGood:       lipgloss.NewStyle().Foreground(colorSuccess),
	RowWarn:       lipgloss.NewStyle().Foreground(colorWarn),
	RowDim:        lipgloss.NewStyle().Foreground(colorMuted),

	Dialog: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorAccent).
		Padding(0, 1),
	DialogTitle: lipgloss.NewStyle().Bold(true),
	DialogHint:  lipgloss.NewStyle().Foreground(colorMuted),
	HelpKey:     lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
	HelpSection: lipgloss.NewStyle().Bold(true).Underline(true),
	InputPrompt: lipgloss.NewStyle().Bold(true).Foreground(colorAccent),
}
