package views

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// Resources returns the root views the command bar can switch to, backed
// by st.
//
// The volumes view is a placeholder until its tasks (7.x) replace it.
func Resources(st *store.Store) []ui.Resource {
	return []ui.Resource{
		{Name: "containers", Aliases: []string{"co"}, New: func(snap store.Snapshot) ui.View {
			return NewContainers(st, snap)
		}},
		{Name: "images", Aliases: []string{"im"}, New: func(snap store.Snapshot) ui.View {
			return NewImages(st, snap)
		}},
		{Name: "volumes", Aliases: []string{"vol"}, New: placeholderFactory("volumes")},
	}
}

func placeholderFactory(name string) func(store.Snapshot) ui.View {
	return func(store.Snapshot) ui.View { return &placeholder{name: name} }
}

// placeholder is a resource view that only states it is not implemented.
type placeholder struct {
	name          string
	width, height int
}

func (p *placeholder) Init() tea.Cmd          { return nil }
func (p *placeholder) Update(tea.Msg) tea.Cmd { return nil }
func (p *placeholder) SetSize(w, h int)       { p.width, p.height = w, h }
func (p *placeholder) Crumbs() []string       { return []string{p.name} }
func (p *placeholder) Indicators() []string   { return nil }
func (p *placeholder) KeyHelp() []key.Binding { return nil }
func (p *placeholder) InputFocused() bool     { return false }
func (p *placeholder) Back() bool             { return false }
func (p *placeholder) Close()                 {}
func (p *placeholder) View() string {
	msg := components.Styles.Empty.Render("The " + p.name + " view is not implemented yet.")
	return lipgloss.Place(p.width, p.height, lipgloss.Center, lipgloss.Center, msg)
}
