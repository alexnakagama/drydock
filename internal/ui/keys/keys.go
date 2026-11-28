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

	// Tab1 to Tab5 switch to the resource with that number, and NextTab
	// and PrevTab cycle through the resources. They are used only for
	// matching and have no help: SwitchHelp documents them all.
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Tab4    key.Binding
	Tab5    key.Binding
	NextTab key.Binding
	PrevTab key.Binding
	// SwitchHelp is display-only: it lists the resource-switching keys as a
	// single help entry. It carries their keys only because a binding
	// without keys reports itself disabled; the root matches the individual
	// bindings above, never this one.
	SwitchHelp key.Binding
}

// The resource-switching keys, shared by the bindings that match them and
// by SwitchHelp, so the help cannot drift from what is matched. They are
// reserved: no view may bind them, since the root takes them first.
var (
	tabKeys    = [...]string{"1", "2", "3", "4", "5"}
	nextTabKey = "tab"
	prevTabKey = "shift+tab"
	switchKeys = append(tabKeys[:], nextTabKey, prevTabKey)
)

// Global is the global keymap.
var Global = GlobalMap{
	Command:   key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
	Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),

	Tab1:       key.NewBinding(key.WithKeys(tabKeys[0])),
	Tab2:       key.NewBinding(key.WithKeys(tabKeys[1])),
	Tab3:       key.NewBinding(key.WithKeys(tabKeys[2])),
	Tab4:       key.NewBinding(key.WithKeys(tabKeys[3])),
	Tab5:       key.NewBinding(key.WithKeys(tabKeys[4])),
	NextTab:    key.NewBinding(key.WithKeys(nextTabKey)),
	PrevTab:    key.NewBinding(key.WithKeys(prevTabKey)),
	SwitchHelp: key.NewBinding(key.WithKeys(switchKeys...), key.WithHelp("1-5/tab", "switch resource")),
}

// Bindings returns the global bindings in display order. The
// resource-switching keys appear once, as SwitchHelp.
func (m GlobalMap) Bindings() []key.Binding {
	return []key.Binding{m.Command, m.SwitchHelp, m.Help, m.Back, m.Quit, m.ForceQuit}
}

// TabKeys returns the keys that jump to a resource, in resource order: the
// first selects resource 0.
func (m GlobalMap) TabKeys() []key.Binding {
	return []key.Binding{m.Tab1, m.Tab2, m.Tab3, m.Tab4, m.Tab5}
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
	// Pause pauses a running container and unpauses a paused one. The
	// view relabels its help to name the action for the selected row.
	Pause     key.Binding
	Restart   key.Binding
	Delete    key.Binding
	Logs      key.Binding
	Inspect   key.Binding
	Shell     key.Binding
	ToggleAll key.Binding
	// Prune removes the stopped containers in the view's scope.
	Prune key.Binding
	// Stats switches the PORTS column for the live CPU and MEM columns.
	Stats key.Binding
	// Sort cycles the row order through name, CPU and MEM. The view
	// relabels its help to name the order the next press would choose.
	Sort key.Binding
}

// Containers is the containers-view keymap.
var Containers = ContainersMap{
	StartStop: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop")),
	Pause:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pause/unpause")),
	Restart:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
	Delete:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Logs:      key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
	Inspect:   key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
	Shell:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "shell")),
	ToggleAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/running")),
	Prune:     key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "prune stopped")),
	Stats:     key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "stats")),
	Sort:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "sort cpu")),
}

// Bindings returns the containers-view bindings in display order.
func (m ContainersMap) Bindings() []key.Binding {
	return []key.Binding{m.StartStop, m.Pause, m.Restart, m.Delete, m.Logs, m.Inspect, m.Shell, m.ToggleAll, m.Prune, m.Stats, m.Sort}
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

// NetworksMap holds the networks view's action keys.
type NetworksMap struct {
	// Containers opens the containers attached to the selected network.
	Containers key.Binding
	Inspect    key.Binding
	// Delete removes the selected network. Built-in networks cannot be
	// removed, and the view hides its hint on them.
	Delete key.Binding
	// Prune removes the networks no container, running or stopped, uses.
	Prune key.Binding
}

// Networks is the networks-view keymap.
var Networks = NetworksMap{
	Containers: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "containers")),
	Inspect:    key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "inspect")),
	Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	Prune:      key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "prune unused")),
}

// Bindings returns the networks-view bindings in display order.
func (m NetworksMap) Bindings() []key.Binding {
	return []key.Binding{m.Containers, m.Inspect, m.Delete, m.Prune}
}

// ProjectsMap holds the projects view's action keys.
type ProjectsMap struct {
	// Containers opens the containers of the selected project.
	Containers key.Binding
	// StartStop stops the project's running service containers, or starts
	// its created and exited ones when none is running.
	StartStop key.Binding
	// Restart restarts the project's service containers that are not
	// paused.
	Restart key.Binding
}

// Projects is the projects-view keymap.
var Projects = ProjectsMap{
	Containers: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "containers")),
	StartStop:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "start/stop")),
	Restart:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
}

// Bindings returns the projects-view bindings in display order.
func (m ProjectsMap) Bindings() []key.Binding {
	return []key.Binding{m.Containers, m.StartStop, m.Restart}
}
