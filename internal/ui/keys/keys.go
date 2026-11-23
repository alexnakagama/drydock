package keys

import "charm.land/bubbles/v2/key"

// GlobalMap holds the keys handled by the root model whenever no overlay is
// open and no text input is focused.
type GlobalMap struct {
	Command key.Binding
	Help    key.Binding
	Back    key.Binding
	Quit    key.Binding
	// ForceQuit quits from anywhere, even with an overlay or a text input
	// focused. It is not shown in the footer.
	ForceQuit key.Binding
}

// Global is the global keymap.
var Global = GlobalMap{
	Command:   key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
}

// Bindings returns the global bindings in display order.
func (m GlobalMap) Bindings() []key.Binding {
	return []key.Binding{m.Command, m.Help, m.Back, m.Quit, m.ForceQuit}
}

// TableMap holds the navigation and filter keys shared by every list view.
type TableMap struct {
	Up       key.Binding
	Down     key.Binding
	Top      key.Binding
	Bottom   key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Filter   key.Binding
}

// Table is the list-navigation keymap.
var Table = TableMap{
	Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
	Bottom:   key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
	PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup", "page up")),
	PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn", "page down")),
	Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
}

// Bindings returns the table bindings in display order.
func (m TableMap) Bindings() []key.Binding {
	return []key.Binding{m.Up, m.Down, m.Top, m.Bottom, m.PageUp, m.PageDown, m.Filter}
}

// InputMap holds the keys that end a single-line text input (command bar,
// prompt, filter).
type InputMap struct {
	Submit key.Binding
	Cancel key.Binding
}

// Input is the text-input keymap.
var Input = InputMap{
	Submit: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply")),
	Cancel: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
}

// ConfirmMap holds the confirmation dialog keys. Only Yes confirms; every
// other key cancels.
type ConfirmMap struct {
	Yes key.Binding
}

// Confirm is the confirmation-dialog keymap.
var Confirm = ConfirmMap{
	Yes: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
}
