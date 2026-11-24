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

// PagerMap holds the scrolling keys of text views (inspect, logs).
type PagerMap struct {
	Up       key.Binding
	Down     key.Binding
	Top      key.Binding
	Bottom   key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Left     key.Binding
	Right    key.Binding
}

// Pager is the text-view scrolling keymap. Vertical keys match the list
// keys, so moving around feels the same in every view.
var Pager = PagerMap{
	Up:       Table.Up,
	Down:     Table.Down,
	Top:      Table.Top,
	Bottom:   Table.Bottom,
	PageUp:   Table.PageUp,
	PageDown: Table.PageDown,
	Left:     key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "scroll left")),
	Right:    key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "scroll right")),
}

// Bindings returns the pager bindings in display order.
func (m PagerMap) Bindings() []key.Binding {
	return []key.Binding{m.Up, m.Down, m.Top, m.Bottom, m.PageUp, m.PageDown, m.Left, m.Right}
}

// ContainersMap holds the containers view's action keys.
type ContainersMap struct {
	StartStop key.Binding
	Restart   key.Binding
	Delete    key.Binding
	Logs      key.Binding
	Inspect   key.Binding
	Shell     key.Binding
	ToggleAll key.Binding
}

// Containers is the containers-view keymap.
var Containers = ContainersMap{
	StartStop: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop")),
	Restart:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
	Delete:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Logs:      key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
	Inspect:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
	Shell:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "shell")),
	ToggleAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/running")),
}

// Bindings returns the containers-view bindings in display order.
func (m ContainersMap) Bindings() []key.Binding {
	return []key.Binding{m.StartStop, m.Restart, m.Delete, m.Logs, m.Inspect, m.Shell, m.ToggleAll}
}

// LogsMap holds the logs view's own keys (scrolling uses Pager).
type LogsMap struct {
	// Follow jumps to the newest line and resumes auto-follow.
	Follow key.Binding
	Search key.Binding
	// Reopen restarts a stream that has ended (for example after the
	// container was restarted).
	Reopen key.Binding
}

// Logs is the logs-view keymap.
var Logs = LogsMap{
	Follow: key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "follow")),
	Search: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	Reopen: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reopen")),
}

// ImagesMap holds the images view's action keys.
type ImagesMap struct {
	Containers key.Binding
	Delete     key.Binding
	Inspect    key.Binding
	Pull       key.Binding
	Prune      key.Binding
	ToggleAll  key.Binding
}

// Images is the images-view keymap.
var Images = ImagesMap{
	Containers: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "containers")),
	Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Inspect:    key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
	Pull:       key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pull")),
	Prune:      key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "prune dangling")),
	ToggleAll:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/tagged")),
}

// Bindings returns the images-view bindings in display order.
func (m ImagesMap) Bindings() []key.Binding {
	return []key.Binding{m.Containers, m.Delete, m.Inspect, m.Pull, m.Prune, m.ToggleAll}
}

// VolumesMap holds the volumes view's action keys.
type VolumesMap struct {
	Containers key.Binding
	Create     key.Binding
	Delete     key.Binding
	Inspect    key.Binding
	Prune      key.Binding
}

// Volumes is the volumes-view keymap.
var Volumes = VolumesMap{
	Containers: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "containers")),
	Create:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "create")),
	Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Inspect:    key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
	Prune:      key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "prune unused")),
}

// Bindings returns the volumes-view bindings in display order.
func (m VolumesMap) Bindings() []key.Binding {
	return []key.Binding{m.Containers, m.Create, m.Delete, m.Inspect, m.Prune}
}
