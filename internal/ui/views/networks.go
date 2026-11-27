package views

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
	"github.com/alexnakagama/drydock/internal/ui/keys"
)

// networkPruneTimeout bounds a network prune, which removes the previewed
// networks one by one.
const networkPruneTimeout = 5 * time.Minute

// networkColumns are the networks view's columns. NAME is searched by the
// filter.
var networkColumns = []components.Column{
	{Title: "NAME", Width: 30, Flex: true, Filter: true},
	{Title: "DRIVER", Width: 10},
	{Title: "SCOPE", Width: 6},
	{Title: "SUBNET", Width: 18, Flex: true},
	{Title: "IN USE", Width: 6},
}

// builtinNetworks are the names of the engine's predefined networks, which
// it creates itself and refuses to remove.
var builtinNetworks = []string{"bridge", "host", "none"}

// isBuiltinNetwork reports whether n is built-in: one of the engine's
// predefined networks or the Swarm ingress network. The API has no
// "predefined" field, so the predefined set is matched by name. Built-in
// networks are never deleted or pruned by drydock.
func isBuiltinNetwork(n network.Summary) bool {
	return n.Ingress || slices.Contains(builtinNetworks, n.Name)
}

// networkSubnet formats the SUBNET cell: the network's first IPv4 subnet,
// or its first subnet of any family when it has no IPv4 one, followed by
// "+N" when it has N more subnets, and "-" when it has none. IPAM entries
// without a valid subnet (the zero prefix) are ignored.
func networkSubnet(n network.Summary) string {
	var subnets []string
	first := -1
	for _, c := range n.IPAM.Config {
		p := c.Subnet
		if !p.IsValid() {
			continue
		}
		if first < 0 && p.Addr().Is4() {
			first = len(subnets)
		}
		subnets = append(subnets, p.String())
	}
	if len(subnets) == 0 {
		return "-"
	}
	if first < 0 {
		first = 0
	}
	if len(subnets) == 1 {
		return subnets[first]
	}
	return fmt.Sprintf("%s +%d", subnets[first], len(subnets)-1)
}

var _ ui.Positioner = (*networksView)(nil)

// networksView lists networks and acts on the selected one.
type networksView struct {
	st    *store.Store
	snap  store.Snapshot
	prefs *Prefs // shared with the containers views it opens

	table components.Table
	// shown maps the IDs of the listed networks to their data in snap.
	shown map[string]network.Summary
}

// NewNetworks returns the networks view. prefs are the session's settings,
// passed on to the containers views it opens; nil gives the view settings
// of its own.
func NewNetworks(st *store.Store, snap store.Snapshot, prefs *Prefs) ui.View {
	return newNetworksView(st, snap, prefs)
}

func newNetworksView(st *store.Store, snap store.Snapshot, prefs *Prefs) *networksView {
	v := &networksView{
		st:    st,
		snap:  snap,
		prefs: orNewPrefs(prefs),
		table: components.NewTable(networkColumns),
	}
	v.refresh()
	return v
}

func (v *networksView) Init() tea.Cmd { return nil }

func (v *networksView) SetSize(w, h int) { v.table.SetSize(w, h) }

func (v *networksView) View() string { return v.table.View() }

func (v *networksView) Crumbs() []string { return []string{"networks"} }

func (v *networksView) Indicators() []string {
	if f := v.table.Filter(); f != "" {
		return []string{"/" + f}
	}
	return nil
}

// KeyHelp returns the view's bindings in priority order, as copies: the
// row actions are disabled when no row is selected, `enter` when no
// container uses the selected network, and `d` on a built-in network. Key
// presses are still matched against keys.Networks, so a disabled hint
// never disables its key.
func (v *networksView) KeyHelp() []key.Binding {
	km := keys.Networks
	ctrs, inspect, del := km.Containers, km.Inspect, km.Delete
	n, ok := v.selected()
	switch {
	case !ok:
		disable(&ctrs, &inspect, &del)
	default:
		if v.snap.NetworkUseCount(n.ID) == 0 {
			disable(&ctrs)
		}
		if isBuiltinNetwork(n) {
			disable(&del)
		}
	}
	return []key.Binding{ctrs, inspect, del, keys.Table.Filter, km.Prune}
}

// Position reports the table's selection and row counts (ui.Positioner).
func (v *networksView) Position() (selected, listed, total int, filtered bool) {
	return v.table.Position()
}

func (v *networksView) InputFocused() bool { return v.table.InputFocused() }

func (v *networksView) Back() bool { return v.table.Back() }

func (v *networksView) Close() {}

func (v *networksView) Update(msg tea.Msg) tea.Cmd {
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

func (v *networksView) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if ok, cmd := v.table.Update(msg); ok {
		return cmd
	}
	km := keys.Networks
	if key.Matches(msg, km.Prune) {
		return v.confirmPrune()
	}
	n, ok := v.selected()
	if !ok {
		return nil
	}
	switch {
	case key.Matches(msg, km.Containers):
		return v.showContainers(n)
	case key.Matches(msg, km.Delete):
		return v.confirmDelete(n)
	case key.Matches(msg, km.Inspect):
		return v.inspect(n)
	}
	return nil
}

// selected returns the selected network as of the current snapshot.
func (v *networksView) selected() (network.Summary, bool) {
	r, ok := v.table.Selected()
	if !ok {
		return network.Summary{}, false
	}
	n, ok := v.shown[r.Key]
	return n, ok
}

// refresh rebuilds the rows from the snapshot, sorted by name. Rows are
// keyed by network ID, which the engine never reuses, so the selection
// follows a network across snapshots and never moves to a network
// recreated under the same name. A network without an ID cannot be
// addressed and is not listed.
func (v *networksView) refresh() {
	nets := v.snap.Networks()
	slices.SortFunc(nets, func(a, b network.Summary) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	v.shown = make(map[string]network.Summary, len(nets))
	rows := make([]components.Row, 0, len(nets))
	for _, n := range nets {
		if _, dup := v.shown[n.ID]; dup || n.ID == "" {
			continue
		}
		v.shown[n.ID] = n
		users := v.snap.NetworkUseCount(n.ID)
		tone := components.ToneDefault
		if users == 0 {
			tone = components.ToneDim
		}
		rows = append(rows, components.Row{Key: n.ID, Tone: tone, Cells: []string{
			networkName(n),
			docker.SanitizeText(n.Driver),
			docker.SanitizeText(n.Scope),
			networkSubnet(n),
			fmt.Sprint(users),
		}})
	}
	if v.snap.Loaded() {
		v.table.SetEmptyText("No networks.")
	} else {
		v.table.SetEmptyText("Loading networks…")
	}
	v.table.SetRows(rows)
}

// networkName returns the network's name, safe to display, or its short
// ID if it has no name.
func networkName(n network.Summary) string {
	if n.Name != "" {
		return docker.SanitizeText(n.Name)
	}
	return docker.SanitizeText(store.ShortID(n.ID))
}

// errNetworkGone reports that a network no longer exists.
var errNetworkGone = errors.New("network no longer exists")

// removeNetwork removes the network by ID, without force: the engine
// refuses to remove a network a running container is attached to. Network
// IDs are never reused, so unlike volumes no identity check is needed. An
// ID that docker.ValidateNetworkID rejects is refused without a request,
// whatever the Service implementation. It returns errNetworkGone (wrapped)
// if the network does not exist any more.
func removeNetwork(ctx context.Context, svc docker.Service, n network.Summary) error {
	if err := docker.ValidateNetworkID(n.ID); err != nil {
		return err
	}
	err := svc.NetworkRemove(ctx, n.ID)
	if docker.IsNotFound(err) {
		return fmt.Errorf("%w: %w", errNetworkGone, err)
	}
	return err
}

// confirmDelete asks before removing the network. Built-in networks are
// refused without a request. When containers are attached, the
// confirmation names them and says what the engine will do: it refuses
// removal while a running container is attached, and otherwise removes the
// network, leaving the stopped containers unable to start until they are
// reconnected.
func (v *networksView) confirmDelete(n network.Summary) tea.Cmd {
	name := networkName(n)
	if isBuiltinNetwork(n) {
		return ui.Error("delete: " + name + " is a built-in network and cannot be removed")
	}
	if err := docker.ValidateNetworkID(n.ID); err != nil {
		return ui.Error("delete " + name + ": invalid network ID")
	}
	var body []string
	switch users := v.snap.NetworkUsers(n.ID); {
	case len(users) == 0:
		body = []string{fmt.Sprintf("Network %s (%s driver) will be removed.", name, docker.SanitizeText(n.Driver))}
	case slices.ContainsFunc(users, func(c container.Summary) bool { return isActive(c.State) }):
		body = append(body, fmt.Sprintf("%s is attached to %s;", name, plural(len(users), "container", "containers")),
			"the engine will refuse to remove it while a running one is attached:")
		body = append(body, containerLines(users)...)
	default:
		they := "they"
		if len(users) == 1 {
			they = "it"
		}
		body = append(body, fmt.Sprintf("%s is attached to %s, all stopped:", name, plural(len(users), "container", "containers")))
		body = append(body, containerLines(users)...)
		body = append(body, fmt.Sprintf("The network will be removed and %s will not start until reconnected.", they))
	}
	return ui.Confirm("Delete network "+name+"?", body,
		ui.Track("deleting network "+name, v.st.ActionWith("delete network "+name, 0,
			func(ctx context.Context, svc docker.Service) (string, error) {
				switch err := removeNetwork(ctx, svc, n); {
				case errors.Is(err, errNetworkGone):
					return "already removed", nil
				case err != nil:
					return "", err
				}
				return "", nil
			})))
}

// inspect pushes the inspect view for the network. It inspects by ID, so a
// network recreated under the same name meanwhile is not shown instead.
func (v *networksView) inspect(n network.Summary) tea.Cmd {
	name, id := networkName(n), n.ID
	if err := docker.ValidateNetworkID(id); err != nil {
		return ui.Error("inspect " + name + ": invalid network ID")
	}
	return ui.Push(newInspectView(v.st, name, func(ctx context.Context, svc docker.Service) (json.RawMessage, error) {
		res, err := svc.NetworkInspect(ctx, id)
		return res.Raw, err
	}))
}

// showContainers pushes a containers view listing the containers, running
// or stopped, attached to the network. The predicate captures the
// network's ID and raw name and applies store.AttachedToNetwork, the rule
// behind Snapshot.NetworkUsers, so the drill-down lists exactly the
// containers counted in IN USE: by ID, or by name for an endpoint without
// a NetworkID (a container created but never started). The breadcrumb
// uses the sanitized name.
func (v *networksView) showContainers(n network.Summary) tea.Cmd {
	name, id, raw := networkName(n), n.ID, n.Name
	return ui.Push(NewFilteredContainers(v.st, v.snap, v.prefs, []string{name, "containers"},
		"No containers are attached to "+name+".", func(c container.Summary) bool {
			return store.AttachedToNetwork(c, id, raw)
		}))
}

// confirmPrune previews the networks that are not built-in and that no
// container uses, running or stopped, and asks before removing them.
//
// The engine's own prune removes every network without active endpoints,
// which includes the networks of stopped containers (for example a stopped
// Compose project's default network), so those containers could not start
// again. Instead exactly the previewed networks are removed, one by one by
// ID and without force: one that a container joined meanwhile is refused
// by the engine and reported.
func (v *networksView) confirmPrune() tea.Cmd {
	if !v.snap.Loaded() {
		return ui.Error("prune: networks are still loading")
	}
	var cands []network.Summary
	for id, n := range v.shown {
		if isBuiltinNetwork(n) || v.snap.NetworkUseCount(id) > 0 {
			continue
		}
		cands = append(cands, n)
	}
	if len(cands) == 0 {
		return ui.Success("nothing to prune: every network that is not built-in is in use")
	}
	slices.SortFunc(cands, func(a, b network.Summary) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	what := plural(len(cands), "network", "networks") + " not used by any container, running or stopped, will be removed:"
	body := []string{what}
	for _, n := range cands {
		body = append(body, "  "+networkName(n))
	}
	body = append(body, "Networks used only by stopped containers are kept.")
	return ui.Confirm("Prune unused networks?", body,
		ui.Track("pruning unused networks", v.st.ActionWith("prune unused networks", networkPruneTimeout,
			func(ctx context.Context, svc docker.Service) (string, error) {
				return removeNetworks(ctx, svc, cands)
			})))
}

// networkRemoval removes networks one by one with removeNetwork.
func networkRemoval(svc docker.Service) removal[network.Summary] {
	return removal[network.Summary]{
		name: networkName,
		remove: func(ctx context.Context, n network.Summary) error {
			return removeNetwork(ctx, svc, n)
		},
		gone: errNetworkGone,
		noun: [2]string{"network", "networks"},
	}
}

// removeNetworks removes the given networks one by one (see
// removeNetwork), carrying on past failures, and returns a summary such as
// "2 networks removed, 1 already gone". If any removal failed, or ctx ended
// before all were done, it returns an error summarizing the outcome instead
// (see removal.run).
func removeNetworks(ctx context.Context, svc docker.Service, nets []network.Summary) (string, error) {
	return networkRemoval(svc).run(ctx, nets)
}
