package views

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/components"
)

// netCreated is the creation time of the sample networks, fixed so that
// inspection output is stable.
var netCreated = time.Date(2026, time.November, 20, 10, 0, 0, 0, time.UTC)

// subnets builds an IPAM configuration with the given subnets.
func subnets(prefixes ...string) network.IPAM {
	var ipam network.IPAM
	for _, p := range prefixes {
		ipam.Config = append(ipam.Config, network.IPAMConfig{Subnet: netip.MustParsePrefix(p)})
	}
	return ipam
}

// addNetwork adds a network to f, failing the test on error, and returns
// its ID.
func addNetwork(t *testing.T, f *dockerfake.Service, n network.Network) string {
	t.Helper()
	if n.Created.IsZero() {
		n.Created = netCreated
	}
	id, err := f.AddNetwork(n)
	if err != nil {
		t.Fatalf("AddNetwork(%s): %v", n.Name, err)
	}
	return id
}

// attached adds to f a container attached to the named networks and
// returns it.
func attached(t *testing.T, f *dockerfake.Service, n int, name string, state container.ContainerState, nets ...string) container.Summary {
	t.Helper()
	c := ctr(cid(n), name, "nginx", state, string(state))
	ns, err := f.NetworkSettings(nets...)
	if err != nil {
		t.Fatalf("NetworkSettings(%v): %v", nets, err)
	}
	c.NetworkSettings = ns
	f.AddContainer(c)
	return c
}

// networkSample returns a fake engine with, besides the built-in bridge,
// host and none:
//   - myapp_default (172.20.0.0/16), used by web (running) and db (exited);
//   - dual (172.21.0.0/16 and fd00::/64), used by web;
//   - batch_net, used only by the exited container job;
//   - old-net and tmp-net, unused.
func networkSample(t *testing.T) *dockerfake.Service {
	t.Helper()
	f := dockerfake.New()
	addNetwork(t, f, network.Network{Name: "myapp_default", IPAM: subnets("172.20.0.0/16")})
	addNetwork(t, f, network.Network{Name: "dual", IPAM: subnets("172.21.0.0/16", "fd00::/64")})
	addNetwork(t, f, network.Network{Name: "batch_net", IPAM: subnets("172.22.0.0/16")})
	addNetwork(t, f, network.Network{Name: "old-net", IPAM: subnets("172.23.0.0/16")})
	addNetwork(t, f, network.Network{Name: "tmp-net", IPAM: subnets("172.24.0.0/16")})
	attached(t, f, 0, "web", container.StateRunning, "myapp_default", "dual")
	attached(t, f, 1, "db", container.StateExited, "myapp_default")
	attached(t, f, 2, "job", container.StateExited, "batch_net")
	return f
}

// networksEnv starts an env on the given engine and switches to the
// networks view.
func networksEnv(t *testing.T, f *dockerfake.Service, width, height int) *env {
	t.Helper()
	e := newEnv(t, f, width, height)
	e.command("networks")
	if !strings.HasPrefix(e.header(), "drydock > networks") {
		t.Fatalf("networks view not shown; screen:\n%s", e.screen())
	}
	return e
}

func (e *env) networksView() *networksView { return e.networks[len(e.networks)-1] }

// selectNetwork moves the selection of the networks view to the network
// called name.
func (e *env) selectNetwork(name string) {
	e.t.Helper()
	v := e.networksView()
	for id, n := range v.shown {
		if n.Name == name && v.table.Select(id) {
			return
		}
	}
	e.t.Fatalf("no network row %s; screen:\n%s", name, e.screen())
}

// networkNames returns the names of the networks listed, in order.
func (e *env) networkNames() []string {
	var out []string
	for _, l := range e.content()[1:] {
		if f := rowFields(l); len(f) >= 5 {
			out = append(out, f[0])
		}
	}
	return out
}

// networkIDs returns the IDs of f's networks by name, to name the networks
// of later NetworkRemove calls once they are gone.
func networkIDs(t *testing.T, f *dockerfake.Service) map[string]string {
	t.Helper()
	nets, err := f.NetworkList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]string, len(nets))
	for _, n := range nets {
		byID[n.ID] = n.Name
	}
	return byID
}

// wantNetworkRemoves checks that NetworkRemove was called, by ID, for
// exactly the networks named, in order. names maps IDs to names, as
// networkIDs returned them before the removals.
func wantNetworkRemoves(t *testing.T, f *dockerfake.Service, names map[string]string, want ...string) {
	t.Helper()
	var got []string
	for _, c := range f.CallsTo(dockerfake.MethodNetworkRemove) {
		id := c.Args[0].(string)
		name, ok := names[id]
		if !ok {
			name = "unknown ID " + id
		}
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("NetworkRemove calls for %v, want %v", got, want)
	}
}

func TestNetworkSubnet(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  []network.IPAMConfig
		want string
	}{
		{"none", nil, "-"},
		{"one IPv4", subnets("172.20.0.0/16").Config, "172.20.0.0/16"},
		{"IPv4 then IPv6", subnets("172.21.0.0/16", "fd00::/64").Config, "172.21.0.0/16 +1"},
		{"IPv6 first, IPv4 shown", subnets("fd00::/64", "10.1.0.0/24", "10.2.0.0/24").Config, "10.1.0.0/24 +2"},
		{"IPv6 only", subnets("fd00::/64", "fd01::/64").Config, "fd00::/64 +1"},
		{"invalid entries ignored", []network.IPAMConfig{{}, {Subnet: netip.MustParsePrefix("10.0.0.0/8")}, {}}, "10.0.0.0/8"},
		{"only invalid entries", []network.IPAMConfig{{}, {}}, "-"},
	} {
		n := network.Summary{Network: network.Network{Name: "n", IPAM: network.IPAM{Config: tc.cfg}}}
		if got := networkSubnet(n); got != tc.want {
			t.Errorf("%s: SUBNET %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestIsBuiltinNetwork(t *testing.T) {
	for _, tc := range []struct {
		n    network.Network
		want bool
	}{
		{network.Network{Name: "bridge"}, true},
		{network.Network{Name: "host"}, true},
		{network.Network{Name: "none"}, true},
		{network.Network{Name: "ingress", Driver: "overlay", Ingress: true}, true},
		{network.Network{Name: "my-ingress", Ingress: true}, true},
		{network.Network{Name: "ingress", Driver: "overlay"}, false},
		{network.Network{Name: "docker_gwbridge"}, false},
		{network.Network{Name: "Bridge"}, false},
		{network.Network{Name: "myapp_default"}, false},
	} {
		if got := isBuiltinNetwork(network.Summary{Network: tc.n}); got != tc.want {
			t.Errorf("isBuiltinNetwork(%q, ingress=%v) = %v, want %v", tc.n.Name, tc.n.Ingress, got, tc.want)
		}
	}
}

func TestNetworksGolden(t *testing.T) {
	e := networksEnv(t, networkSample(t), 100, 12)
	checkGolden(t, "networks_100x12.golden", e.screen())
}

func TestNetworksList(t *testing.T) {
	e := networksEnv(t, networkSample(t), 100, 16)
	want := []string{"batch_net", "bridge", "dual", "host", "myapp_default", "none", "old-net", "tmp-net"}
	if got := e.networkNames(); !slices.Equal(got, want) {
		t.Fatalf("rows %v, want %v", got, want)
	}
	for name, cells := range map[string][]string{
		"myapp_default": {"myapp_default", "bridge", "local", "172.20.0.0/16", "2"},
		"dual":          {"dual", "bridge", "local", "172.21.0.0/16", "+1", "1"},
		"batch_net":     {"batch_net", "bridge", "local", "172.22.0.0/16", "1"},
		"old-net":       {"old-net", "bridge", "local", "172.23.0.0/16", "0"},
		"bridge":        {"bridge", "bridge", "local", "172.17.0.0/16", "0"},
		"host":          {"host", "host", "local", "-", "0"},
		"none":          {"none", "null", "local", "-", "0"},
	} {
		if got := rowFields(e.rowOf(name)); !slices.Equal(got, cells) {
			t.Errorf("%s row %q, want %q", name, got, cells)
		}
	}
	if tabs := e.tabsLine(); !strings.Contains(tabs, "5 networks (8)") {
		t.Errorf("tabs %q, want 5 networks (8)", tabs)
	}
}

func TestNetworkRowTones(t *testing.T) {
	e := networksEnv(t, networkSample(t), 100, 16)
	v := e.networksView()
	want := map[string]components.Tone{
		"myapp_default": components.ToneDefault,
		"dual":          components.ToneDefault,
		"batch_net":     components.ToneDefault, // used by a stopped container
		"old-net":       components.ToneDim,
		"tmp-net":       components.ToneDim,
		"bridge":        components.ToneDim,
		"none":          components.ToneDim,
	}
	for id, n := range v.shown {
		w, ok := want[n.Name]
		if !ok {
			continue
		}
		if got := rowTone(t, &v.table, id); got != w {
			t.Errorf("network %s: tone %d, want %d", n.Name, got, w)
		}
	}
}

func TestNetworksFilterAndPosition(t *testing.T) {
	e := networksEnv(t, networkSample(t), 100, 16)
	var p ui.Positioner = e.networksView()
	if s, l, tot, filt := p.Position(); s != 1 || l != 8 || tot != 8 || filt {
		t.Errorf("Position = %d,%d,%d,%v, want 1,8,8,false", s, l, tot, filt)
	}
	e.press("/")
	e.typeText("net")
	e.press("enter")
	if got, want := e.networkNames(), []string{"batch_net", "old-net", "tmp-net"}; !slices.Equal(got, want) {
		t.Errorf("filtered rows %v, want %v", got, want)
	}
	if ind := e.networksView().Indicators(); !slices.Equal(ind, []string{"/net"}) {
		t.Errorf("indicators %q", ind)
	}
	if s, l, tot, filt := p.Position(); s != 1 || l != 3 || tot != 8 || !filt {
		t.Errorf("filtered Position = %d,%d,%d,%v, want 1,3,8,true", s, l, tot, filt)
	}
	// The filter matches NAME only: no network is named after its driver.
	e.press("esc")
	e.press("/")
	e.typeText("null")
	e.press("enter")
	if _, l, _, _ := p.Position(); l != 0 {
		t.Errorf("filter on a DRIVER value lists %d rows:\n%s", l, e.screen())
	}
}

func TestNetworksSelectionSurvivesRefresh(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 16)
	e.selectNetwork("old-net")
	addNetwork(t, f, network.Network{Name: "aaa"})
	e.until("new network listed", func() bool { return e.rowOf("aaa") != "" })
	if n, _ := e.networksView().selected(); n.Name != "old-net" {
		t.Errorf("selection after refresh %q, want old-net", n.Name)
	}
}

func TestNetworksUpdateLive(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 16)
	if f := rowFields(e.rowOf("old-net")); len(f) != 5 || f[4] != "0" {
		t.Fatalf("old-net row %q, want IN USE 0", e.rowOf("old-net"))
	}
	if err := f.ConnectNetwork("old-net", "web"); err != nil {
		t.Fatal(err)
	}
	e.until("IN USE updated", func() bool {
		f := rowFields(e.rowOf("old-net"))
		return len(f) == 5 && f[4] == "1"
	})
}

func TestNetworksBeforeLoad(t *testing.T) {
	v := newNetworksView(store.New(dockerfake.New(), func(tea.Msg) {}), store.Snapshot{}, nil)
	v.SetSize(80, 6)
	if !strings.Contains(ansi.Strip(v.View()), "Loading networks…") {
		t.Errorf("view before load:\n%s", v.View())
	}
	if msg := fmt.Sprintf("%+v", v.confirmPrune()()); !strings.Contains(msg, "prune: networks are still loading") {
		t.Errorf("prune before load: %s", msg)
	}
}

func TestInspectNetwork(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 80, 14)
	e.selectNetwork("myapp_default")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"IPAM"`) })
	if raw := e.m.View().Content; !strings.Contains(raw, components.Styles.JSONKey.Render(`"Name"`)) {
		t.Errorf("network inspection not colored:\n%q", raw)
	}
	if h := e.header(); !strings.HasPrefix(h, "drydock > networks > myapp_default > inspect") {
		t.Errorf("header %q", h)
	}
	checkGolden(t, "inspect_network_80x14.golden", e.screen())
	id, _ := f.NetworkID("myapp_default")
	if calls := f.CallsTo(dockerfake.MethodNetworkInspect); len(calls) != 1 || calls[0].Args[0] != id {
		t.Errorf("NetworkInspect calls %v, want one by ID %s", calls, id)
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > networks") || strings.Contains(h, "inspect") {
		t.Errorf("header after esc %q", h)
	}
}

func TestDeleteUnusedNetwork(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 20)
	ids := networkIDs(t, f)
	e.selectNetwork("old-net")
	e.press("d")
	e.settle()
	if d := e.dialogText(); !strings.Contains(d, "Delete network old-net?") || !strings.Contains(d, "Network old-net (bridge driver) will be removed.") {
		t.Fatalf("confirmation:\n%s", e.screen())
	}
	e.press("y")
	e.waitStatus("delete network old-net: done")
	e.until("row removed", func() bool { return e.rowOf("old-net") == "" })
	wantNetworkRemoves(t, f, ids, "old-net")
}

func TestDeleteNetworkInUseRefused(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 200, 20)
	ids := networkIDs(t, f)
	e.selectNetwork("myapp_default")
	e.press("d")
	e.settle()
	d := e.dialogText()
	for _, want := range []string{"myapp_default is attached to 2 containers;", "the engine will refuse to remove it while a running one is attached:", "db (exited)", "web (running)"} {
		if !strings.Contains(d, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, e.screen())
		}
	}
	e.press("y")
	e.waitStatus("delete network myapp_default: error while removing network: network myapp_default has active endpoints")
	e.settle()
	if e.rowOf("myapp_default") == "" {
		t.Errorf("network no longer listed:\n%s", e.screen())
	}
	wantNetworkRemoves(t, f, ids, "myapp_default")
}

func TestDeleteNetworkOfStoppedContainers(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 20)
	ids := networkIDs(t, f)
	e.selectNetwork("batch_net")
	e.press("d")
	e.settle()
	d := e.dialogText()
	for _, want := range []string{"batch_net is attached to 1 container, all stopped:", "job (exited)", "The network will be removed and it will not start until reconnected."} {
		if !strings.Contains(d, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, e.screen())
		}
	}
	if strings.Contains(d, "refuse") {
		t.Errorf("confirmation warns of a refusal with only stopped users:\n%s", e.screen())
	}
	e.press("y")
	e.waitStatus("delete network batch_net: done")
	e.until("row removed", func() bool { return e.rowOf("batch_net") == "" })
	wantNetworkRemoves(t, f, ids, "batch_net")
}

func TestDeleteNetworkCancelled(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 20)
	e.selectNetwork("tmp-net")
	e.press("d")
	e.settle()
	e.press("esc")
	e.settle()
	if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 || e.rowOf("tmp-net") == "" {
		t.Errorf("cancelled delete removed the network: %v", c)
	}
}

func TestDeleteNetworkAlreadyGone(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 20)
	e.selectNetwork("tmp-net")
	e.press("d")
	e.settle()
	id, _ := f.NetworkID("tmp-net")
	f.RemoveNetwork(id)
	e.press("y")
	e.waitStatus("delete network tmp-net: already removed")
}

func TestDeleteBuiltinNetworkRefused(t *testing.T) {
	f := networkSample(t)
	addNetwork(t, f, network.Network{Name: "ingress", Driver: "overlay", Scope: "swarm", Ingress: true})
	for _, name := range []string{"bridge", "host", "none", "ingress"} {
		e := networksEnv(t, f, 200, 20)
		e.selectNetwork(name)
		if b := findHelp(t, e.networksView().KeyHelp(), "d"); b.Enabled() {
			t.Errorf("%s: d hint enabled", name)
		}
		if strings.Contains(e.footer(), "d delete") {
			t.Errorf("%s: footer %q shows d", name, e.footer())
		}
		f.ResetCalls()
		e.press("d")
		e.waitStatus("delete: " + name + " is a built-in network and cannot be removed")
		e.settle()
		if strings.Contains(e.screen(), "Delete network") {
			t.Errorf("%s: confirmation shown:\n%s", name, e.screen())
		}
		if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 {
			t.Errorf("%s: NetworkRemove called: %v", name, c)
		}
	}
}

func TestPruneUnusedNetworks(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 24)
	ids := networkIDs(t, f)
	e.press("P")
	e.settle()
	s := e.dialog()
	d := e.dialogText()
	if !strings.Contains(d, "Prune unused networks?") ||
		!strings.Contains(d, "2 networks not used by any container, running or stopped, will be removed:") ||
		!strings.Contains(d, "Networks used only by stopped containers are kept.") {
		t.Fatalf("confirmation:\n%s", s)
	}
	for _, want := range []string{"old-net", "tmp-net"} {
		if !strings.Contains(s, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, s)
		}
	}
	// Built-in networks are never pruned, and networks attached to any
	// container, even a stopped one, are kept.
	for _, kept := range []string{"bridge", "host", "none", "batch_net", "myapp_default", "dual"} {
		if slices.Contains(strings.Fields(s), kept) {
			t.Errorf("confirmation lists %s:\n%s", kept, s)
		}
	}
	e.press("y")
	e.waitStatus("prune unused networks: 2 networks removed")
	wantNetworkRemoves(t, f, ids, "old-net", "tmp-net")
	e.until("pruned networks gone", func() bool {
		return slices.Equal(e.networkNames(), []string{"batch_net", "bridge", "dual", "host", "myapp_default", "none"})
	})
}

func TestPruneNetworksKeepsStoppedComposeNetwork(t *testing.T) {
	f := dockerfake.New()
	addNetwork(t, f, network.Network{Name: "myapp_default"})
	addNetwork(t, f, network.Network{Name: "old-net"})
	for i, svc := range []string{"web", "db"} {
		c := composeCtr(i, "myapp-"+svc+"-1", "nginx", container.StateExited, "Exited (0)", dockerfake.Compose{Project: "myapp", Service: svc})
		ns, err := f.NetworkSettings("myapp_default")
		if err != nil {
			t.Fatal(err)
		}
		c.NetworkSettings = ns
		f.AddContainer(c)
	}
	e := networksEnv(t, f, 100, 20)
	ids := networkIDs(t, f)
	e.press("P")
	e.settle()
	if s := e.dialog(); strings.Contains(s, "myapp_default") || !strings.Contains(s, "old-net") {
		t.Fatalf("confirmation:\n%s", s)
	}
	e.press("y")
	e.waitStatus("prune unused networks: 1 network removed")
	wantNetworkRemoves(t, f, ids, "old-net")
}

func TestPruneNetworksNothingToPrune(t *testing.T) {
	f := dockerfake.New()
	addNetwork(t, f, network.Network{Name: "myapp_default"})
	attached(t, f, 0, "db", container.StateExited, "myapp_default")
	e := networksEnv(t, f, 100, 20)
	e.press("P")
	e.settle()
	if strings.Contains(e.screen(), "Prune unused networks?") {
		t.Fatalf("confirmation shown with nothing to prune:\n%s", e.screen())
	}
	e.waitStatus("nothing to prune")
	if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 {
		t.Errorf("NetworkRemove called: %v", c)
	}
}

func TestPruneNetworksPartialFailure(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 200, 24)
	ids := networkIDs(t, f)
	e.press("P")
	e.settle()
	// After the preview, a running container joins tmp-net: the engine
	// refuses to remove it and old-net is still removed.
	if err := f.ConnectNetwork("tmp-net", "web"); err != nil {
		t.Fatal(err)
	}
	e.press("y")
	e.waitStatus("prune unused networks: 1 removed, 1 failed (tmp-net): tmp-net: error while removing network: network tmp-net has active endpoints")
	wantNetworkRemoves(t, f, ids, "old-net", "tmp-net")
	// old-net disappears, and tmp-net stays, now in use.
	e.until("list refreshed", func() bool {
		f := rowFields(e.rowOf("tmp-net"))
		return e.rowOf("old-net") == "" && len(f) == 5 && f[4] == "1"
	})
}

func TestPruneNetworksCancelled(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	e.press("n")
	e.settle()
	if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 {
		t.Errorf("NetworkRemove called after cancel: %v", c)
	}
}

func TestPruneNetworksAlreadyGone(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 24)
	e.press("P")
	e.settle()
	id, _ := f.NetworkID("old-net")
	f.RemoveNetwork(id)
	e.press("y")
	e.waitStatus("prune unused networks: 1 network removed, 1 already gone")
}

func TestNetworksViewNeutralizesHostileText(t *testing.T) {
	f := dockerfake.New()
	hostile := "n1\x1b]52;c;cHduZWQ=\a\u202e\x1b[2J"
	addNetwork(t, f, network.Network{Name: hostile, Driver: "evil\x1b[2J", Scope: "x\u202e"})
	attached(t, f, 0, "db\x1b[2J", container.StateExited, hostile)
	addNetwork(t, f, network.Network{Name: "n2\x1b[2J"})
	e := networksEnv(t, f, 100, 20)
	check := func(step string) {
		t.Helper()
		raw := e.m.View().Content
		if strings.Contains(raw, "\x1b]52") || strings.Contains(raw, "\x1b[2J") || strings.ContainsRune(raw, 0x202e) {
			t.Errorf("%s: injected escape sequence reached the screen", step)
		}
	}
	check("list")
	e.selectNetwork(hostile)
	e.press("d")
	e.settle()
	check("delete dialog")
	e.press("y")
	e.waitStatus("delete network")
	check("delete status")
	e.selectNetwork("n2\x1b[2J")
	e.press("i")
	e.until("inspect loaded", func() bool { return strings.Contains(e.screen(), `"IPAM"`) })
	check("inspect")
	e.press("esc")
	e.press("P")
	e.settle()
	check("prune")
	e.press("n")
}

func TestNetworkActionsRefusedWhileDisconnected(t *testing.T) {
	f := networkSample(t)
	e := networksEnv(t, f, 100, 20)
	f.Disconnect(errors.New("connection refused"))
	e.until("disconnected", func() bool { return e.st.State() == store.StateDisconnected })
	f.ResetCalls()

	e.selectNetwork("old-net")
	e.press("d")
	e.settle()
	e.press("y")
	e.waitStatus("delete network old-net: not connected")

	e.press("P")
	e.settle()
	e.press("y")
	e.waitStatus("prune unused networks: not connected")
	if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 {
		t.Errorf("engine called while disconnected: %v", c)
	}
}

// The refusal warning follows the engine: a paused or restarting container
// holds an endpoint and blocks removal like a running one, while a created
// or exited one does not.
func TestDeleteNetworkWarningByUserState(t *testing.T) {
	for _, tc := range []struct {
		state  container.ContainerState
		refuse bool
	}{
		{container.StateRunning, true},
		{container.StatePaused, true},
		{container.StateRestarting, true},
		{container.StateCreated, false},
		{container.StateExited, false},
		{container.StateDead, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			f := dockerfake.New()
			addNetwork(t, f, network.Network{Name: "app-net"})
			attached(t, f, 0, "svc", tc.state, "app-net")
			e := networksEnv(t, f, 200, 20)
			ids := networkIDs(t, f)
			e.selectNetwork("app-net")
			e.press("d")
			e.settle()
			d := e.dialogText()
			if got := strings.Contains(d, "the engine will refuse to remove it"); got != tc.refuse {
				t.Errorf("refusal warning shown = %v, want %v:\n%s", got, tc.refuse, e.screen())
			}
			if got := strings.Contains(d, "all stopped"); got == tc.refuse {
				t.Errorf("stopped wording shown = %v, want %v:\n%s", got, !tc.refuse, e.screen())
			}
			e.press("y")
			if tc.refuse {
				e.waitStatus("delete network app-net: error while removing network")
			} else {
				e.waitStatus("delete network app-net: done")
			}
			wantNetworkRemoves(t, f, ids, "app-net")
		})
	}
}

func TestPruneNetworksNeverRemovesIngress(t *testing.T) {
	f := networkSample(t)
	addNetwork(t, f, network.Network{Name: "my-ingress", Driver: "overlay", Scope: "swarm", Ingress: true})
	e := networksEnv(t, f, 100, 24)
	ids := networkIDs(t, f)
	e.press("P")
	e.settle()
	if s := e.dialog(); strings.Contains(s, "my-ingress") || !strings.Contains(s, "old-net") {
		t.Fatalf("confirmation:\n%s", s)
	}
	e.press("y")
	e.waitStatus("prune unused networks: 2 networks removed")
	wantNetworkRemoves(t, f, ids, "old-net", "tmp-net")
	if e.rowOf("my-ingress") == "" {
		t.Errorf("ingress network no longer listed:\n%s", e.screen())
	}
}

func TestNetworkName(t *testing.T) {
	id := strings.Repeat("ab", 32)
	for _, tc := range []struct {
		n    network.Network
		want string
	}{
		{network.Network{Name: "app", ID: id}, "app"},
		{network.Network{ID: id}, "abababababab"},
		{network.Network{Name: "a\x1b[2Jb", ID: id}, "a\ufffd[2Jb"},
	} {
		if got := networkName(network.Summary{Network: tc.n}); got != tc.want {
			t.Errorf("networkName(%q) = %q, want %q", tc.n.Name, got, tc.want)
		}
	}
}

// An ID the engine could not have issued is refused before any request,
// for delete and prune alike.
func TestRemoveNetworkRejectsInvalidID(t *testing.T) {
	f := dockerfake.New()
	for _, id := range []string{"", "../containers/x", "a b", "x\x1b[2J"} {
		n := network.Summary{Network: network.Network{Name: "n", ID: id}}
		if err := removeNetwork(context.Background(), f, n); err == nil || errors.Is(err, errNetworkGone) {
			t.Errorf("removeNetwork(%q) = %v, want an invalid-ID error", id, err)
		}
	}
	if c := f.CallsTo(dockerfake.MethodNetworkRemove); len(c) != 0 {
		t.Errorf("NetworkRemove called: %v", c)
	}
}

func TestNetworkDrillDownToContainers(t *testing.T) {
	f := networkSample(t)
	attached(t, f, 3, "other", container.StateExited, "batch_net")
	e := networksEnv(t, f, 100, 20)
	e.selectNetwork("myapp_default")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "myapp_default > containers") })
	// The drill-down starts in all mode, so the exited db is listed too.
	if h := e.header(); !strings.HasPrefix(h, "drydock > networks > myapp_default > containers [all]") {
		t.Fatalf("header %q", h)
	}
	if e.rowOf("web") == "" || e.rowOf("db") == "" || e.rowOf("job") != "" || e.rowOf("other") != "" {
		t.Errorf("drill-down lists the wrong containers:\n%s", e.screen())
	}
	e.press("esc")
	if h := e.header(); !strings.HasPrefix(h, "drydock > networks") || strings.Contains(h, "containers") {
		t.Errorf("header after esc %q", h)
	}
	if n, _ := e.networksView().selected(); n.Name != "myapp_default" {
		t.Errorf("selection after esc %q", n.Name)
	}
}

func TestNetworkDrillDownPruneScope(t *testing.T) {
	f := networkSample(t)
	attached(t, f, 3, "other", container.StateExited, "batch_net")
	e := networksEnv(t, f, 100, 24)
	e.selectNetwork("myapp_default")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "myapp_default > containers") })
	e.openPrune()
	if got, want := e.pruneListed(), []string{"db exited"}; !slices.Equal(got, want) {
		t.Errorf("listed %v, want %v", got, want)
	}
	e.press("n")
	e.settle()
	if c := f.CallsTo(dockerfake.MethodContainerRemove); len(c) != 0 {
		t.Errorf("ContainerRemove called after cancel: %v", c)
	}
}

func TestNetworkDrillDownListsCreatedContainer(t *testing.T) {
	f := networkSample(t)
	// A container created but never started: the engine lists its
	// endpoint without a NetworkID, so only the name identifies the
	// network.
	c := ctr(cid(3), "fresh", "nginx", container.StateCreated, "Created")
	ns, err := f.NetworkSettings("old-net")
	if err != nil {
		t.Fatal(err)
	}
	c.NetworkSettings = dockerfake.WithoutNetworkIDs(ns)
	f.AddContainer(c)
	e := networksEnv(t, f, 100, 20)
	e.selectNetwork("old-net")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "old-net > containers") })
	if e.rowOf("fresh") == "" || e.rowOf("web") != "" || e.rowOf("db") != "" {
		t.Errorf("drill-down lists the wrong containers:\n%s", e.screen())
	}
}

func TestNetworkDrillDownRecreatedNetwork(t *testing.T) {
	// A container attached by ID to a network that was removed and
	// recreated under the same name does not belong to the new network.
	f := dockerfake.New()
	addNetwork(t, f, network.Network{Name: "app"})
	attached(t, f, 0, "stale", container.StateExited, "app")
	ids := networkIDs(t, f)
	for id, name := range ids {
		if name == "app" {
			if !f.RemoveNetwork(id) {
				t.Fatalf("RemoveNetwork(%s) found no network", id)
			}
		}
	}
	addNetwork(t, f, network.Network{Name: "app"})
	e := networksEnv(t, f, 100, 20)
	e.selectNetwork("app")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "app > containers") })
	e.until("empty drill-down", func() bool { return strings.Contains(e.screen(), "No containers are attached to app.") })
	if e.rowOf("stale") != "" {
		t.Errorf("container of the removed network listed:\n%s", e.screen())
	}
}

func TestNetworkDrillDownFollowsStatsSetting(t *testing.T) {
	f := networkSample(t)
	e := newEnv(t, f, 120, 16)
	e.press("t")
	e.waitStreams(1)
	e.command("networks")
	e.waitStreams(0)
	e.selectNetwork("myapp_default")
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), "myapp_default > containers") })
	if got := e.headerTitles(); !slices.Contains(got, "CPU") || !slices.Contains(got, "MEM") {
		t.Errorf("drill-down columns %q, want CPU and MEM", got)
	}
	// Only the running web is streamed.
	e.waitStreams(1)
	e.press("esc")
	e.waitStreams(0)
}

func TestNetworkDrillDownHostileName(t *testing.T) {
	// The engine's network names are untrusted: the breadcrumb and empty
	// text show them sanitized, while matching uses the raw name, so a
	// created container known only by that name is still listed.
	const hostile = "evil\x1b[31mnet\x07"
	const unused = "void\x1b]0;pwned\x07net"
	f := dockerfake.New()
	addNetwork(t, f, network.Network{Name: hostile})
	addNetwork(t, f, network.Network{Name: unused})
	c := ctr(cid(0), "fresh", "nginx", container.StateCreated, "Created")
	ns, err := f.NetworkSettings(hostile)
	if err != nil {
		t.Fatal(err)
	}
	c.NetworkSettings = dockerfake.WithoutNetworkIDs(ns)
	f.AddContainer(c)
	e := networksEnv(t, f, 120, 20)

	noControl := func(what string) {
		t.Helper()
		raw := e.m.View().Content
		for _, bad := range []string{"\x1b[31mnet", "\x07", "\x1b]0;"} {
			if strings.Contains(raw, bad) {
				t.Errorf("%s: %q reached the terminal:\n%q", what, bad, raw)
			}
		}
	}

	e.selectNetwork(hostile)
	e.press("enter")
	e.until("drill-down", func() bool { return strings.Contains(e.header(), " > containers") })
	if h, want := e.header(), "drydock > networks > "+networkName(network.Summary{Name: hostile})+" > containers"; !strings.HasPrefix(h, want) {
		t.Errorf("header %q, want prefix %q", h, want)
	}
	if e.rowOf("fresh") == "" {
		t.Errorf("container attached by the raw name not listed:\n%s", e.screen())
	}
	noControl("drill-down")
	e.press("esc")

	e.selectNetwork(unused)
	e.press("enter")
	want := "No containers are attached to " + networkName(network.Summary{Name: unused}) + "."
	e.until("empty drill-down", func() bool { return strings.Contains(e.screen(), want) })
	noControl("empty drill-down")
}
