package views

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/docker/go-units"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// viewIDs numbers view instances. Every non-key message reaches every view
// on the stack, and several views of one type can be stacked (a containers
// view and a drill-down containers view), so a view's asynchronous results
// carry its ID and other views ignore them.
var viewIDs atomic.Uint64

func nextViewID() uint64 { return viewIDs.Add(1) }

// containerColumns are the containers view's columns. NAME and IMAGE are
// searched by the filter.
var containerColumns = []components.Column{
	{Title: "NAME", Width: 20, Flex: true, Filter: true},
	{Title: "IMAGE", Width: 24, Flex: true, Filter: true},
	{Title: "STATUS", Width: 24}, // a state glyph, a space, then the status
	{Title: "PORTS", Width: 24, Flex: true},
}

// statsColumns replace PORTS with the live CPU and MEM columns while stats
// are shown. They take as many cells as containerColumns (98 with the
// gaps), so both layouts fit a 100-column terminal; MEM, the last flex
// column, gets any width left over.
var statsColumns = []components.Column{
	{Title: "NAME", Width: 20, Flex: true, Filter: true},
	{Title: "IMAGE", Width: 20, Flex: true, Filter: true},
	{Title: "STATUS", Width: 24},
	{Title: "CPU", Width: 7},
	{Title: "MEM", Width: 19, Flex: true},
}

// noStats is a stats cell's text when there is no value to show.
const noStats = "-"

var (
	_ ui.Positioner  = (*containersView)(nil)
	_ ui.Activatable = (*containersView)(nil)
)

// statsMsg delivers a stats batch to the containers view, and the
// subscription, that asked for it.
type statsMsg struct {
	view  uint64
	sub   *store.StatsSub
	stats map[string]store.ContainerStats
}

// containersView lists containers and acts on the selected one.
//
// The root containers view lists every container, running-only by default.
// A drill-down view (see NewFilteredContainers) lists only the containers
// matching a fixed predicate and starts in "all" mode.
type containersView struct {
	id   uint64
	st   *store.Store
	snap store.Snapshot

	crumbs []string
	// match restricts a drill-down view to some containers; nil lists all.
	match func(container.Summary) bool
	// emptyText is shown when no container matches (in "all" mode, or in
	// running-only mode with no stopped container hidden).
	emptyText string
	all       bool

	table components.Table
	// list holds the listed containers in display order, and shown maps
	// their IDs to them; both come from snap.
	list  []container.Summary
	shown map[string]container.Summary

	// prefs are the session's settings; prefs.Stats is what the user last
	// chose in any containers view. statsCols is the layout the table has,
	// which follows prefs.Stats whenever the view is on screen.
	prefs     *Prefs
	statsCols bool
	// active is set while the view is on screen (ui.Activatable), and
	// closed once Close has been called.
	active, closed bool
	// conn is the last connection state the view was told about. Stats
	// cells show noStats while it is not StateConnected.
	conn store.State
	// sub is the stats subscription, held only while the view is active
	// with stats on; subCtx is cancelled when sub is closed, which ends the
	// command waiting for its next batch. stats is sub's latest batch.
	sub       *store.StatsSub
	subCtx    context.Context
	subCancel context.CancelFunc
	stats     map[string]store.ContainerStats

	exec execEnv
	// probing is set while a shell probe started by this view runs.
	probing bool
	// newLogs creates the logs view for a container; tests replace it.
	newLogs func(containerID, name string) ui.View
}

// NewContainers returns the root containers view, listing running
// containers until `a` is pressed. prefs are the session's settings, shared
// with every other containers view; nil gives the view settings of its own.
func NewContainers(st *store.Store, snap store.Snapshot, prefs *Prefs) ui.View {
	return newContainersView(st, snap, prefs, []string{"containers"}, "No containers.", nil, false, defaultExecEnv())
}

// NewFilteredContainers returns a drill-down containers view that lists
// only the containers, running or stopped, for which match returns true.
// prefs are as for NewContainers. crumbs are its breadcrumb segments (for
// example {"nginx:1.27", "containers"}) and emptyText is shown when no
// container matches. The view starts in "all" mode; `a` toggles it as usual.
func NewFilteredContainers(st *store.Store, snap store.Snapshot, prefs *Prefs, crumbs []string, emptyText string,
	match func(container.Summary) bool) ui.View {
	if match == nil {
		panic("views: nil container predicate")
	}
	return newContainersView(st, snap, prefs, slices.Clone(crumbs), emptyText, match, true, defaultExecEnv())
}

func newContainersView(st *store.Store, snap store.Snapshot, prefs *Prefs, crumbs []string, emptyText string,
	match func(container.Summary) bool, all bool, env execEnv) *containersView {
	if st == nil {
		panic("views: nil store")
	}
	prefs = orNewPrefs(prefs)
	cols := containerColumns
	if prefs.Stats {
		cols = statsColumns
	}
	v := &containersView{
		id:        nextViewID(),
		st:        st,
		snap:      snap,
		crumbs:    crumbs,
		match:     match,
		emptyText: emptyText,
		all:       all,
		table:     components.NewTable(cols),
		prefs:     prefs,
		statsCols: prefs.Stats,
		conn:      st.State(),
		exec:      env,
	}
	v.newLogs = func(id, name string) ui.View { return newLogsView(st, id, name, defaultLogsConfig()) }
	v.refresh()
	return v
}

func (v *containersView) Init() tea.Cmd { return nil }

func (v *containersView) SetSize(w, h int) { v.table.SetSize(w, h) }

func (v *containersView) View() string { return v.table.View() }

func (v *containersView) Crumbs() []string { return v.crumbs }

func (v *containersView) Indicators() []string {
	ind := []string{"running"}
	if v.all {
		ind[0] = "all"
	}
	if v.statsCols {
		ind = append(ind, "stats")
	}
	if f := v.table.Filter(); f != "" {
		ind = append(ind, "/"+f)
	}
	return ind
}

// KeyHelp returns the view's bindings in priority order, as copies:
// actions that do not apply to the selected container (or that need a
// selection when there is none) are disabled, and `s` and `t` are labelled
// with what they would do. `t` comes last, so it is the first hint the
// footer drops when it runs out of room. Key presses are still matched
// against keys.Containers, so a disabled hint never disables its key.
func (v *containersView) KeyHelp() []key.Binding {
	km := keys.Containers
	logs, startStop, shell, inspect, restart, del := km.Logs, km.StartStop, km.Shell, km.Inspect, km.Restart, km.Delete
	stats := km.Stats
	if v.statsCols {
		stats.SetHelp(km.Stats.Help().Key, "ports")
	}
	c, ok := v.selected()
	if !ok {
		disable(&logs, &startStop, &shell, &inspect, &restart, &del)
	} else {
		switch c.State {
		case container.StateRunning:
			startStop.SetHelp(km.StartStop.Help().Key, "stop")
		case container.StateCreated, container.StateExited:
			startStop.SetHelp(km.StartStop.Help().Key, "start")
		default:
			disable(&startStop)
		}
		if c.State != container.StateRunning {
			disable(&shell)
		}
	}
	return []key.Binding{logs, startStop, shell, inspect, restart, del, km.ToggleAll, keys.Table.Filter, stats}
}

// Position reports the table's selection and row counts (ui.Positioner).
func (v *containersView) Position() (selected, listed, total int, filtered bool) {
	return v.table.Position()
}

func (v *containersView) InputFocused() bool { return v.table.InputFocused() }

func (v *containersView) Back() bool { return v.table.Back() }

// Close stops reading stats. The view ignores SetActive afterwards.
func (v *containersView) Close() {
	v.closed = true
	v.active = false
	v.closeStats()
}

// SetActive starts reading stats when the view comes on screen with stats
// on, and stops when it leaves the screen (ui.Activatable). A view revealed
// again first takes the layout chosen meanwhile in another containers view.
func (v *containersView) SetActive(active bool) tea.Cmd {
	if v.closed {
		return nil
	}
	v.active = active
	if active && v.statsCols != v.prefs.Stats {
		v.applyLayout()
	}
	return v.syncStats()
}

func (v *containersView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case store.SnapshotMsg:
		v.snap = msg.Snapshot
		v.refresh()
		if v.sub != nil {
			v.sub.SetTargets(v.statsTargets())
		}
		return nil
	case store.ConnectionMsg:
		if msg.State == v.conn {
			return nil
		}
		v.conn = msg.State
		if v.statsCols {
			v.setRows()
		}
		return nil
	case statsMsg:
		return v.receiveStats(msg)
	case execProbeMsg:
		if msg.view != v.id {
			return nil
		}
		return v.startShell(msg)
	case execDoneMsg:
		if msg.view != v.id {
			return nil
		}
		return v.shellDone(msg)
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

func (v *containersView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if ok, cmd := v.table.Update(msg); ok {
		return cmd
	}
	km := keys.Containers
	if key.Matches(msg, km.ToggleAll) {
		v.all = !v.all
		v.refresh()
		return nil
	}
	if key.Matches(msg, km.Stats) {
		return v.toggleStats()
	}
	c, ok := v.selected()
	if !ok {
		return nil
	}
	switch {
	case key.Matches(msg, km.StartStop):
		return v.startStop(c)
	case key.Matches(msg, km.Restart):
		return v.restart(c)
	case key.Matches(msg, km.Delete):
		return v.confirmDelete(c)
	case key.Matches(msg, km.Inspect):
		return v.inspect(c)
	case key.Matches(msg, km.Logs):
		return ui.Push(v.newLogs(c.ID, store.ContainerName(c)))
	case key.Matches(msg, km.Shell):
		return v.shell(c)
	}
	return nil
}

// selected returns the selected container as of the current snapshot.
func (v *containersView) selected() (container.Summary, bool) {
	r, ok := v.table.Selected()
	if !ok {
		return container.Summary{}, false
	}
	c, ok := v.shown[r.Key]
	return c, ok
}

// toggleStats flips the session's stats setting and applies it to this
// view: other containers views take it when they are next shown.
func (v *containersView) toggleStats() tea.Cmd {
	v.prefs.Stats = !v.prefs.Stats
	v.applyLayout()
	return v.syncStats()
}

// applyLayout switches the table to the layout prefs.Stats selects,
// keeping the filter and the selection.
func (v *containersView) applyLayout() {
	v.statsCols = v.prefs.Stats
	if v.statsCols {
		v.table.SetColumns(statsColumns)
	} else {
		v.table.SetColumns(containerColumns)
	}
	v.setRows()
}

// syncStats holds a stats subscription exactly while the view is on screen
// with stats on. It returns the command waiting for the first batch of a
// new subscription.
func (v *containersView) syncStats() tea.Cmd {
	want := v.active && v.statsCols && !v.closed
	switch {
	case want && v.sub == nil:
		v.sub = v.st.SubscribeStats()
		v.subCtx, v.subCancel = context.WithCancel(context.Background())
		// A new subscription starts with no values, and the rows may still
		// show those of an earlier one if the view was hidden meanwhile.
		v.stats = nil
		v.setRows()
		v.sub.SetTargets(v.statsTargets())
		return v.waitStats()
	case !want && v.sub != nil:
		v.closeStats()
		// A hidden view is redrawn when it is shown again; one still on
		// screen (stats turned off) is redrawn by the layout switch.
	}
	return nil
}

// closeStats closes the subscription, if any, and forgets its values.
func (v *containersView) closeStats() {
	if v.sub == nil {
		return
	}
	v.sub.Close()
	v.subCancel()
	v.sub, v.subCtx, v.subCancel = nil, nil, nil
	v.stats = nil
}

// waitStats returns a command that delivers the subscription's next batch,
// or nothing once the subscription is closed.
func (v *containersView) waitStats() tea.Cmd {
	id, sub, ctx := v.id, v.sub, v.subCtx
	return func() tea.Msg {
		select {
		case b := <-sub.C():
			return statsMsg{view: id, sub: sub, stats: b}
		case <-ctx.Done():
			return nil
		}
	}
}

// receiveStats takes a batch from the current subscription and waits for
// the next one. Batches for another view, or from a subscription closed
// since, are dropped.
//
// A batch received while disconnected is kept but not shown: the cells show
// noStats until the connection is back. Each batch holds every current
// value, but one is sent only when a value changed, so a discarded batch
// may not be followed by another and the cells would show the older values
// once reconnected. A batch equal to the one shown changes nothing.
func (v *containersView) receiveStats(msg statsMsg) tea.Cmd {
	if msg.view != v.id || msg.sub == nil || msg.sub != v.sub {
		return nil
	}
	if maps.Equal(msg.stats, v.stats) {
		return v.waitStats()
	}
	v.stats = msg.stats
	if v.statsCols && v.conn == store.StateConnected {
		v.setRows()
	}
	return v.waitStats()
}

// statsTargets returns the IDs of the running containers the view lists,
// whatever the "all" mode and the filter: those it reads stats for.
func (v *containersView) statsTargets() []string {
	var ids []string
	for _, c := range v.snap.Containers() {
		if c.State != container.StateRunning || (v.match != nil && !v.match(c)) {
			continue
		}
		ids = append(ids, c.ID)
	}
	return ids
}

// refresh rebuilds the list from the snapshot and the current mode.
func (v *containersView) refresh() {
	var list []container.Summary
	hidden := 0
	for _, c := range v.snap.Containers() {
		if v.match != nil && !v.match(c) {
			continue
		}
		if !v.all && !isActive(c.State) {
			hidden++
			continue
		}
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b container.Summary) int {
		return cmp.Or(cmp.Compare(store.ContainerName(a), store.ContainerName(b)), cmp.Compare(a.ID, b.ID))
	})

	v.list = list
	v.shown = make(map[string]container.Summary, len(list))
	for _, c := range list {
		v.shown[c.ID] = c
	}

	switch {
	case !v.snap.Loaded():
		v.table.SetEmptyText("Loading containers…")
	case !v.all && hidden > 0:
		v.table.SetEmptyText(fmt.Sprintf("No running containers (%d stopped; press a to show all).", hidden))
	case !v.all && v.match == nil:
		v.table.SetEmptyText("No running containers.")
	default:
		v.table.SetEmptyText(v.emptyText)
	}
	v.setRows()
}

// setRows rebuilds the table rows from the list, in the table's layout.
func (v *containersView) setRows() {
	rows := make([]components.Row, len(v.list))
	for i, c := range v.list {
		cells := []string{
			store.ContainerName(c),
			displayImage(c.Image),
			containerGlyph(c.State) + " " + displayStatus(c),
		}
		if v.statsCols {
			cpu, mem := v.statsCells(c)
			cells = append(cells, cpu, mem)
		} else {
			cells = append(cells, FormatPorts(c.Ports))
		}
		rows[i] = components.Row{Key: c.ID, Tone: containerTone(c.State), Cells: cells}
	}
	v.table.SetRows(rows)
}

// statsCells returns c's CPU and MEM cells: its latest values, or noStats
// for a container that is not running, for a value not reported yet, and
// for every container while the engine is not connected.
func (v *containersView) statsCells(c container.Summary) (cpu, mem string) {
	cpu, mem = noStats, noStats
	if c.State != container.StateRunning || v.conn != store.StateConnected {
		return cpu, mem
	}
	s, ok := v.stats[c.ID]
	if !ok {
		return cpu, mem
	}
	if s.HasCPU {
		cpu = formatCPU(s.CPUPercent)
	}
	if s.HasMem {
		mem = formatMem(s.MemUsage, s.MemLimit)
	}
	return cpu, mem
}

// formatCPU renders a CPU percentage with one decimal, as `docker stats`
// does: "100.0%".
func formatCPU(pct float64) string { return strconv.FormatFloat(pct, 'f', 1, 64) + "%" }

// formatMem renders memory usage and limit in binary units, as `docker
// stats` does: "512.3MiB / 2GiB".
func formatMem(usage, limit uint64) string {
	return units.BytesSize(float64(usage)) + " / " + units.BytesSize(float64(limit))
}

// containerTone colors a container row by its state: running is good,
// paused and restarting need attention, and exited, created and dead are
// dimmed. Any other state (for example removing) keeps the default color.
func containerTone(s container.ContainerState) components.Tone {
	switch s {
	case container.StateRunning:
		return components.ToneGood
	case container.StatePaused, container.StateRestarting:
		return components.ToneWarn
	case container.StateExited, container.StateCreated, container.StateDead:
		return components.ToneDim
	default:
		return components.ToneDefault
	}
}

// containerGlyph returns the glyph that begins a container's STATUS cell:
// ● for running, ◐ for paused and restarting, ○ for exited, created and
// dead, and a blank for any other state. It is derived from containerTone,
// so the glyph and the row color always agree. The glyph is part of the
// cell text, so the state stays readable on the selected row (which is not
// colored) and without color.
func containerGlyph(s container.ContainerState) string {
	switch containerTone(s) {
	case components.ToneGood:
		return "●"
	case components.ToneWarn:
		return "◐"
	case components.ToneDim:
		return "○"
	default:
		return " "
	}
}

// disable disables the given binding copies. They must be copies: the
// package-level keymaps are what key presses are matched against.
func disable(bs ...*key.Binding) {
	for _, b := range bs {
		b.SetEnabled(false)
	}
}

// isActive reports whether a container is running in the broad sense the
// engine and `docker ps` use: running, paused or restarting. These are the
// containers the running-only mode lists, and the ones the engine refuses
// to remove without force.
func isActive(s container.ContainerState) bool {
	return s == container.StateRunning || s == container.StatePaused || s == container.StateRestarting
}

// displayImage shortens an image given by ID (a container whose image was
// untagged after it was created) as the docker CLI does.
func displayImage(img string) string {
	if strings.HasPrefix(img, "sha256:") {
		return store.ShortID(img)
	}
	return img
}

func displayStatus(c container.Summary) string {
	if c.Status != "" {
		return c.Status
	}
	return string(c.State)
}

// FormatPorts renders a container's ports as the PORTS column shows them:
// published ports as "8080->80/tcp" and exposed but unpublished ones as
// "5432/tcp", sorted by container port, with the duplicates the engine
// reports for each host address (IPv4 and IPv6) removed. An exposed port
// that is also published is shown only as published.
func FormatPorts(ports []container.PortSummary) string {
	type port struct {
		private, public uint16
		proto           string
	}
	published := make(map[port]bool)
	seen := make(map[port]bool)
	var list []port
	for _, p := range ports {
		proto := strings.ToLower(p.Type)
		if proto == "" {
			proto = "tcp"
		}
		k := port{private: p.PrivatePort, public: p.PublicPort, proto: proto}
		if seen[k] {
			continue
		}
		seen[k] = true
		list = append(list, k)
		if p.PublicPort != 0 {
			published[port{private: p.PrivatePort, proto: proto}] = true
		}
	}
	list = slices.DeleteFunc(list, func(p port) bool {
		return p.public == 0 && published[port{private: p.private, proto: p.proto}]
	})
	slices.SortFunc(list, func(a, b port) int {
		return cmp.Or(cmp.Compare(a.private, b.private), cmp.Compare(a.proto, b.proto), cmp.Compare(a.public, b.public))
	})
	parts := make([]string, len(list))
	for i, p := range list {
		s := strconv.Itoa(int(p.private)) + "/" + p.proto
		if p.public != 0 {
			s = strconv.Itoa(int(p.public)) + "->" + s
		}
		parts[i] = s
	}
	return strings.Join(parts, ", ")
}

// startStop stops a running container and starts a created or exited one.
// Any other state is refused without calling the engine.
func (v *containersView) startStop(c container.Summary) tea.Cmd {
	name, id := store.ContainerName(c), c.ID
	switch c.State {
	case container.StateRunning:
		return ui.Track("stopping "+name, v.st.Action("stop "+name, func(ctx context.Context, svc docker.Service) error {
			return svc.ContainerStop(ctx, id)
		}))
	case container.StateCreated, container.StateExited:
		return ui.Track("starting "+name, v.st.Action("start "+name, func(ctx context.Context, svc docker.Service) error {
			return svc.ContainerStart(ctx, id)
		}))
	default:
		return ui.Error(fmt.Sprintf("start/stop is unavailable for a %s container (%s)", stateName(c.State), name))
	}
}

func (v *containersView) restart(c container.Summary) tea.Cmd {
	name, id := store.ContainerName(c), c.ID
	return ui.Track("restarting "+name, v.st.Action("restart "+name, func(ctx context.Context, svc docker.Service) error {
		return svc.ContainerRestart(ctx, id)
	}))
}

// confirmDelete asks before removing a container. Running, paused and
// restarting containers are removed with force, which the engine requires
// for them, and the dialog says so; volumes are always kept.
func (v *containersView) confirmDelete(c container.Summary) tea.Cmd {
	name, id := store.ContainerName(c), c.ID
	force := isActive(c.State)
	var body []string
	if force {
		body = append(body, fmt.Sprintf("%s is %s and will be force-removed (killed, then removed).", name, stateName(c.State)))
	} else {
		body = append(body, fmt.Sprintf("%s (%s) will be removed.", name, stateName(c.State)))
	}
	body = append(body, "Its volumes are kept.")
	opts := docker.ContainerRemoveOptions{Force: force, RemoveVolumes: false}
	return ui.Confirm("Delete container "+name+"?", body,
		ui.Track("deleting "+name, v.st.Action("delete "+name, func(ctx context.Context, svc docker.Service) error {
			return svc.ContainerRemove(ctx, id, opts)
		})))
}

func (v *containersView) inspect(c container.Summary) tea.Cmd {
	id := c.ID
	return ui.Push(newInspectView(v.st, store.ContainerName(c), func(ctx context.Context, svc docker.Service) (json.RawMessage, error) {
		res, err := svc.ContainerInspect(ctx, id)
		return res.Raw, err
	}))
}

// stateName returns a container state for display.
func stateName(s container.ContainerState) string {
	if s == "" {
		return "unknown"
	}
	return components.Clean(string(s))
}
