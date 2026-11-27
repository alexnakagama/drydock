package views

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// projectColumns are the projects view's columns. PROJECT and WORKING DIR
// are searched by the filter, as displayed (with the home directory shown
// as ~).
var projectColumns = []components.Column{
	{Title: "PROJECT", Width: 20, Flex: true, Filter: true},
	{Title: "RUNNING", Width: 7},
	{Title: "STATE", Width: 9}, // a state glyph, a space, then the state
	{Title: "WORKING DIR", Width: 30, Flex: true, Filter: true},
}

// noWorkingDir is the WORKING DIR cell of a project whose containers record
// no working directory.
const noWorkingDir = "-"

// projectsConfig holds what the projects view takes from its environment.
// Tests replace it.
type projectsConfig struct {
	// home is the user's home directory, shown as ~ in WORKING DIR, or ""
	// to show every path unchanged.
	home string
}

// defaultProjectsConfig reads the user's home directory once. If it cannot
// be read, paths are shown unchanged.
func defaultProjectsConfig() projectsConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return projectsConfig{home: home}
}

var _ ui.Positioner = (*projectsView)(nil)

// projectsView lists the Compose projects derived from the containers'
// labels (see store.Snapshot.Projects) and acts on the selected one.
type projectsView struct {
	st    *store.Store
	snap  store.Snapshot
	cfg   projectsConfig
	prefs *Prefs // shared with the containers views it opens

	table components.Table
	// shown maps the names of the listed projects to their summaries in
	// snap.
	shown map[string]store.Project
}

// NewProjects returns the projects view. prefs are the session's settings,
// passed on to the containers views it opens; nil gives the view settings
// of its own.
func NewProjects(st *store.Store, snap store.Snapshot, prefs *Prefs) ui.View {
	return newProjectsView(st, snap, prefs, defaultProjectsConfig())
}

func newProjectsView(st *store.Store, snap store.Snapshot, prefs *Prefs, cfg projectsConfig) *projectsView {
	if st == nil {
		panic("views: nil store")
	}
	v := &projectsView{
		st:    st,
		snap:  snap,
		cfg:   cfg,
		prefs: orNewPrefs(prefs),
		table: components.NewTable(projectColumns),
	}
	v.refresh()
	return v
}

func (v *projectsView) Init() tea.Cmd { return nil }

func (v *projectsView) SetSize(w, h int) { v.table.SetSize(w, h) }

func (v *projectsView) View() string { return v.table.View() }

func (v *projectsView) Crumbs() []string { return []string{"projects"} }

func (v *projectsView) Indicators() []string {
	if f := v.table.Filter(); f != "" {
		return []string{"/" + f}
	}
	return nil
}

// KeyHelp returns the view's bindings in priority order, as copies: the
// row actions are disabled when no row is selected. Key presses are still
// matched against keys.Projects, so a disabled hint never disables its key.
func (v *projectsView) KeyHelp() []key.Binding {
	km := keys.Projects
	ctrs := km.Containers
	if _, ok := v.selected(); !ok {
		disable(&ctrs)
	}
	return []key.Binding{ctrs, keys.Table.Filter}
}

// Position reports the table's selection and row counts (ui.Positioner).
func (v *projectsView) Position() (selected, listed, total int, filtered bool) {
	return v.table.Position()
}

func (v *projectsView) InputFocused() bool { return v.table.InputFocused() }

func (v *projectsView) Back() bool { return v.table.Back() }

func (v *projectsView) Close() {}

func (v *projectsView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case store.SnapshotMsg:
		v.snap = msg.Snapshot
		v.refresh()
		return nil
	case tea.PasteMsg:
		_, cmd := v.table.Update(msg)
		return cmd
	case tea.BackgroundColorMsg:
		// The palette changed: the filter input restyles itself.
		_, cmd := v.table.Update(msg)
		return cmd
	case tea.KeyPressMsg:
		return v.handleKey(msg)
	default:
		// The filter input's own messages (cursor) while it is focused.
		if v.table.InputFocused() {
			_, cmd := v.table.Update(msg)
			return cmd
		}
		return nil
	}
}

func (v *projectsView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if ok, cmd := v.table.Update(msg); ok {
		return cmd
	}
	p, ok := v.selected()
	if !ok {
		return nil
	}
	if key.Matches(msg, keys.Projects.Containers) {
		return v.showContainers(p.Name)
	}
	return nil
}

// selected returns the selected project as of the current snapshot.
func (v *projectsView) selected() (store.Project, bool) {
	r, ok := v.table.Selected()
	if !ok {
		return store.Project{}, false
	}
	p, ok := v.shown[r.Key]
	return p, ok
}

// refresh rebuilds the rows from the snapshot. Rows are keyed by project
// name, which the snapshot keeps unique, so the selection follows its
// project across snapshots.
func (v *projectsView) refresh() {
	projects := v.snap.Projects() // sorted by name
	v.shown = make(map[string]store.Project, len(projects))
	rows := make([]components.Row, 0, len(projects))
	for _, p := range projects {
		v.shown[p.Name] = p
		tone := projectTone(p.State)
		rows = append(rows, components.Row{Key: p.Name, Tone: tone, Cells: []string{
			p.Name,
			strconv.Itoa(p.Running) + "/" + strconv.Itoa(p.Total()),
			toneGlyph(tone) + " " + p.State.String(),
			v.workingDir(p.WorkingDir),
		}})
	}
	if v.snap.Loaded() {
		v.table.SetEmptyText("No Compose projects.")
	} else {
		v.table.SetEmptyText("Loading projects…")
	}
	v.table.SetRows(rows)
}

// workingDir returns the WORKING DIR cell for dir: noWorkingDir if it is
// empty, otherwise dir with the home directory shown as ~.
func (v *projectsView) workingDir(dir string) string {
	if dir == "" {
		return noWorkingDir
	}
	return shortenHome(dir, v.cfg.home)
}

// shortenHome shows path as ~ when it is the home directory and replaces
// the home directory with ~ when path lies inside it: home followed by a
// path separator, so /home/meow is not shortened for the home /home/me.
// Both are compared cleaned (see filepath.Clean), so /home/me/../other is
// not inside /home/me; a shortened path is shown cleaned. Any other path,
// and every path when home is empty, relative or the root directory, is
// returned unchanged.
func shortenHome(path, home string) string {
	if home == "" || !filepath.IsAbs(home) || !filepath.IsAbs(path) {
		return path
	}
	home = filepath.Clean(home)
	if home == filepath.VolumeName(home)+string(filepath.Separator) {
		return path // the root directory: every path would be inside it
	}
	clean := filepath.Clean(path)
	if clean == home {
		return "~"
	}
	rest, ok := strings.CutPrefix(clean, home)
	if !ok || rest == "" || (rest[0] != '/' && rest[0] != filepath.Separator) {
		return path
	}
	return "~" + rest
}

// projectTone colors a project row by its state: running is good, partial
// needs attention and exited is dimmed.
func projectTone(s store.ProjectState) components.Tone {
	switch s {
	case store.ProjectRunning:
		return components.ToneGood
	case store.ProjectPartial:
		return components.ToneWarn
	default:
		return components.ToneDim
	}
}

// showContainers pushes a containers view listing every container of the
// named project, running or stopped, one-off containers included. Its
// scoped prune (P) is limited to the project's stopped containers.
func (v *projectsView) showContainers(name string) tea.Cmd {
	return ui.Push(NewFilteredContainers(v.st, v.snap, v.prefs, []string{name, "containers"},
		"No containers in project "+name+".", func(c container.Summary) bool { return inProject(c, name) }))
}

// inProject reports whether c belongs to the named project, following the
// same rule as the snapshot's project index: its project label is exactly
// name, which is never empty.
func inProject(c container.Summary, name string) bool {
	return name != "" && c.Labels[store.ComposeProjectLabel] == name
}
