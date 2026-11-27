package dockerfake

import (
	"encoding/json"
	"net/netip"
	"slices"
	"sync"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
)

func mustAddNetwork(t *testing.T, f *Service, n network.Network) string {
	t.Helper()
	id, err := f.AddNetwork(n)
	if err != nil {
		t.Fatalf("AddNetwork(%q): %v", n.Name, err)
	}
	return id
}

func mustNetworkSettings(t *testing.T, f *Service, names ...string) *container.NetworkSettingsSummary {
	t.Helper()
	ns, err := f.NetworkSettings(names...)
	if err != nil {
		t.Fatalf("NetworkSettings(%v): %v", names, err)
	}
	return ns
}

func networkNames(t *testing.T, f *Service) []string {
	t.Helper()
	ns, err := f.NetworkList(ctxT(t))
	if err != nil {
		t.Fatalf("NetworkList: %v", err)
	}
	var out []string
	for _, n := range ns {
		out = append(out, n.Name)
	}
	return out
}

func TestPredefinedNetworks(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	ns, err := f.NetworkList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := networkNames(t, f); !slices.Equal(got, PredefinedNetworks) {
		t.Fatalf("networks of a new fake = %v, want %v", got, PredefinedNetworks)
	}
	wantDriver := map[string]string{BridgeNetwork: "bridge", HostNetwork: "host", NoneNetwork: "null"}
	for _, n := range ns {
		if n.ID != PredefinedNetworkID(n.Name) || docker.ValidateNetworkID(n.ID) != nil {
			t.Errorf("%s: ID = %q, want PredefinedNetworkID", n.Name, n.ID)
		}
		if n.Driver != wantDriver[n.Name] || n.Scope != "local" || n.Created.IsZero() {
			t.Errorf("%s = %+v", n.Name, n.Network)
		}
		if err := f.NetworkRemove(ctx, n.ID); !cerrdefs.IsPermissionDenied(err) {
			t.Errorf("remove %s: err = %v, want permission denied", n.Name, err)
		}
	}
	if got := ns[0].IPAM.Config; len(got) != 1 || got[0].Subnet != netip.MustParsePrefix("172.17.0.0/16") {
		t.Errorf("bridge IPAM config = %+v", got)
	}
	if PredefinedNetworkID("mynet") != "" {
		t.Error("PredefinedNetworkID of an ordinary name is not empty")
	}
	if New().networks[0].ID != ns[0].ID {
		t.Error("predefined IDs differ between fakes")
	}
}

func TestAddAndRemoveNetwork(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	es := f.Events(ctx)

	subnet := netip.MustParsePrefix("172.20.0.0/16")
	id := mustAddNetwork(t, f, network.Network{
		Name: "myapp_default",
		IPAM: network.IPAM{Config: []network.IPAMConfig{{Subnet: subnet}}},
	})
	if err := docker.ValidateNetworkID(id); err != nil {
		t.Fatalf("generated ID %q: %v", id, err)
	}
	m := nextEvent(t, es)
	if m.Type != events.NetworkEventType || m.Action != events.ActionCreate || m.Actor.ID != id ||
		m.Actor.Attributes["name"] != "myapp_default" || m.Actor.Attributes["type"] != "bridge" {
		t.Errorf("create event = %+v", m)
	}
	if got, ok := f.NetworkID("myapp_default"); !ok || got != id {
		t.Errorf("NetworkID = %q, %v, want %q", got, ok, id)
	}

	ns, err := f.NetworkList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n := ns[len(ns)-1]
	if n.ID != id || n.Driver != "bridge" || n.Scope != "local" || n.Created.IsZero() || n.IPAM.Config[0].Subnet != subnet {
		t.Errorf("listed network = %+v", n.Network)
	}
	// Summaries are copies.
	n.IPAM.Config[0].Subnet = netip.Prefix{}
	n.Labels["x"] = "y"
	if again, _ := f.NetworkList(ctx); again[len(again)-1].IPAM.Config[0].Subnet != subnet || len(again[len(again)-1].Labels) != 0 {
		t.Error("changing a listed network changed the fake")
	}

	if _, err := f.AddNetwork(network.Network{Name: "myapp_default"}); !docker.IsConflict(err) {
		t.Errorf("add a duplicate name: err = %v, want conflict", err)
	}
	if _, err := f.AddNetwork(network.Network{}); !docker.IsInvalidArgument(err) {
		t.Errorf("add without a name: err = %v, want invalid argument", err)
	}
	if _, err := f.AddNetwork(network.Network{Name: "x", ID: "../x"}); !docker.IsInvalidArgument(err) {
		t.Errorf("add with an invalid ID: err = %v, want invalid argument", err)
	}
	// Replacing by ID keeps one network.
	mustAddNetwork(t, f, network.Network{ID: id, Name: "myapp_default", Driver: "macvlan"})
	_ = nextEvent(t, es)
	if got := networkNames(t, f); len(got) != 4 {
		t.Errorf("networks after replace = %v", got)
	}

	if !f.RemoveNetwork(id) {
		t.Fatal("RemoveNetwork reported a missing network")
	}
	if m := nextEvent(t, es); m.Action != events.ActionDestroy || m.Actor.ID != id {
		t.Errorf("destroy event = %+v", m)
	}
	if f.RemoveNetwork(id) {
		t.Error("RemoveNetwork of a removed network reported true")
	}
	if _, ok := f.NetworkID("myapp_default"); ok {
		t.Error("NetworkID still finds the removed network")
	}
}

func TestNetworkSettingsAttachContainers(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	netID := mustAddNetwork(t, f, network.Network{Name: "app"})

	ns := mustNetworkSettings(t, f, "app", BridgeNetwork)
	ep := ns.Networks["app"]
	if ep == nil || ep.NetworkID != netID || ep.EndpointID == "" {
		t.Fatalf("app endpoint = %+v", ep)
	}
	if ns.Networks[BridgeNetwork].NetworkID != PredefinedNetworkID(BridgeNetwork) {
		t.Errorf("bridge endpoint = %+v", ns.Networks[BridgeNetwork])
	}
	if _, err := f.NetworkSettings("missing"); !docker.IsNotFound(err) {
		t.Errorf("NetworkSettings of a missing network: err = %v, want not found", err)
	}

	created := WithoutNetworkIDs(ns)
	if e := created.Networks["app"]; e.NetworkID != "" || e.EndpointID != "" {
		t.Errorf("WithoutNetworkIDs endpoint = %+v", e)
	}
	if ep.NetworkID != netID {
		t.Error("WithoutNetworkIDs changed its argument")
	}
	if WithoutNetworkIDs(nil) != nil {
		t.Error("WithoutNetworkIDs(nil) is not nil")
	}

	c := running("web", "nginx", "sha256:a")
	c.NetworkSettings = ns
	f.AddContainer(c)
	ns.Networks["app"].NetworkID = "changed" // AddContainer copied it
	cs, err := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	got := cs[0].NetworkSettings.Networks["app"]
	if got.NetworkID != netID {
		t.Fatalf("listed endpoint NetworkID = %q, want %q", got.NetworkID, netID)
	}
	got.NetworkID = "changed" // and listing copies endpoints too
	if again, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true}); again[0].NetworkSettings.Networks["app"].NetworkID != netID {
		t.Error("changing a listed endpoint changed the fake")
	}
}

func TestConnectDisconnectNetwork(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	netID := mustAddNetwork(t, f, network.Network{Name: "test-net"})
	webID := f.AddContainer(running("web", "nginx", "sha256:a"))
	es := f.Events(ctx)

	if err := f.ConnectNetwork("test-net", "web"); err != nil {
		t.Fatalf("ConnectNetwork: %v", err)
	}
	m := nextEvent(t, es)
	if m.Type != events.NetworkEventType || m.Action != events.ActionConnect || m.Actor.ID != netID ||
		m.Actor.Attributes["container"] != webID || m.Actor.Attributes["name"] != "test-net" {
		t.Errorf("connect event = %+v", m)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if ep := cs[0].NetworkSettings.Networks["test-net"]; ep == nil || ep.NetworkID != netID || ep.EndpointID == "" {
		t.Errorf("endpoint after connect = %+v", cs[0].NetworkSettings)
	}
	if err := f.ConnectNetwork(netID, webID); !cerrdefs.IsPermissionDenied(err) {
		t.Errorf("connect again: err = %v, want permission denied", err)
	}
	if err := f.ConnectNetwork("missing", "web"); !docker.IsNotFound(err) {
		t.Errorf("connect to a missing network: err = %v, want not found", err)
	}
	if err := f.ConnectNetwork("test-net", "missing"); !docker.IsNotFound(err) {
		t.Errorf("connect a missing container: err = %v, want not found", err)
	}

	// The fake's state is read by inspect too.
	insp, err := f.NetworkInspect(ctx, netID)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := insp.Network.Containers[webID]; !ok || r.Name != "web" {
		t.Errorf("inspect Containers = %+v", insp.Network.Containers)
	}

	if err := f.DisconnectNetwork(netID[:12], "web"); err != nil {
		t.Fatalf("DisconnectNetwork: %v", err)
	}
	if m := nextEvent(t, es); m.Action != events.ActionDisconnect || m.Actor.ID != netID || m.Actor.Attributes["container"] != webID {
		t.Errorf("disconnect event = %+v", m)
	}
	cs, _ = f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if _, ok := cs[0].NetworkSettings.Networks["test-net"]; ok {
		t.Error("endpoint still listed after disconnect")
	}
	if err := f.DisconnectNetwork("test-net", "web"); !cerrdefs.IsInternal(err) {
		t.Errorf("disconnect again: err = %v, want internal", err)
	}

	// Connect and disconnect emitted network events only.
	f.AddVolume(volume.Volume{Name: "sentinel"})
	if m := nextEvent(t, es); m.Type != events.VolumeEventType {
		t.Errorf("event after disconnect = %s/%s, want only network events before the sentinel", m.Type, m.Action)
	}
}

func TestConnectContainerWithoutNetworkSettings(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	mustAddNetwork(t, f, network.Network{Name: "n"})
	f.AddContainer(exited("db", "pg", "sha256:p")) // NetworkSettings is nil
	if err := f.ConnectNetwork("n", "db"); err != nil {
		t.Fatal(err)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if cs[0].NetworkSettings == nil || cs[0].NetworkSettings.Networks["n"] == nil {
		t.Errorf("NetworkSettings after connect = %+v", cs[0].NetworkSettings)
	}
}

func TestNetworkRemove(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	unused := mustAddNetwork(t, f, network.Network{Name: "old-net"})
	usedRunning := mustAddNetwork(t, f, network.Network{Name: "live"})
	usedStopped := mustAddNetwork(t, f, network.Network{Name: "myapp_default"})
	usedPaused := mustAddNetwork(t, f, network.Network{Name: "frozen"})

	web := running("web", "nginx", "sha256:a")
	web.NetworkSettings = mustNetworkSettings(t, f, "live")
	f.AddContainer(web)
	db := exited("db", "pg", "sha256:p")
	db.NetworkSettings = mustNetworkSettings(t, f, "myapp_default")
	dbID := f.AddContainer(db)
	p := running("p", "busybox", "sha256:b")
	p.State = container.StatePaused
	p.NetworkSettings = mustNetworkSettings(t, f, "frozen")
	f.AddContainer(p)
	es := f.Events(ctx)

	if err := f.NetworkRemove(ctx, usedRunning); !cerrdefs.IsPermissionDenied(err) {
		t.Errorf("remove a network with a running container: err = %v, want permission denied", err)
	}
	if err := f.NetworkRemove(ctx, usedPaused); !cerrdefs.IsPermissionDenied(err) {
		t.Errorf("remove a network with a paused container: err = %v, want permission denied", err)
	}
	if err := f.NetworkRemove(ctx, unused); err != nil {
		t.Errorf("remove an unused network: %v", err)
	}
	if m := nextEvent(t, es); m.Type != events.NetworkEventType || m.Action != events.ActionDestroy || m.Actor.ID != unused {
		t.Errorf("destroy event = %+v", m)
	}
	// Stopped containers hold no endpoint and do not block removal; like
	// the engine, their NetworkSettings are left as they were.
	if err := f.NetworkRemove(ctx, usedStopped); err != nil {
		t.Errorf("remove a network used only by a stopped container: %v", err)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	for _, c := range cs {
		if c.ID == dbID && c.NetworkSettings.Networks["myapp_default"].NetworkID != usedStopped {
			t.Errorf("stopped container's endpoint after removal = %+v", c.NetworkSettings.Networks)
		}
	}
	if err := f.NetworkRemove(ctx, unused); !docker.IsNotFound(err) {
		t.Errorf("second removal: err = %v, want not found", err)
	}
	if err := f.NetworkRemove(ctx, "../containers/x"); !docker.IsInvalidArgument(err) {
		t.Errorf("remove with traversal: err = %v, want invalid argument", err)
	}
	if got := networkNames(t, f); !slices.Equal(got, []string{BridgeNetwork, HostNetwork, NoneNetwork, "live", "frozen"}) {
		t.Errorf("networks after removals = %v", got)
	}
	if calls := f.CallsTo(MethodNetworkRemove); len(calls) != 6 || calls[0].Args[0] != usedRunning {
		t.Errorf("recorded NetworkRemove calls = %+v", calls)
	}
}

func TestRecreatedNetworkDoesNotInheritEndpoints(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	oldID := mustAddNetwork(t, f, network.Network{Name: "app"})
	db := exited("db", "pg", "sha256:p")
	db.NetworkSettings = mustNetworkSettings(t, f, "app")
	f.AddContainer(db)
	if err := f.NetworkRemove(ctx, oldID); err != nil {
		t.Fatal(err)
	}
	newID := mustAddNetwork(t, f, network.Network{Name: "app"})
	if newID == oldID {
		t.Fatal("recreated network reused the ID")
	}
	// db's endpoint still names the old ID: it is not attached to the new
	// network, which can be removed and is not reported as connected.
	if err := f.DisconnectNetwork(newID, "db"); !cerrdefs.IsInternal(err) {
		t.Errorf("disconnect from the recreated network: err = %v, want internal", err)
	}
	// Its stale endpoint keeps the name, and the fake refuses to overwrite
	// it rather than lose it.
	if err := f.ConnectNetwork(newID, "db"); !cerrdefs.IsPermissionDenied(err) {
		t.Errorf("connect over the stale endpoint: err = %v, want permission denied", err)
	}
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if got := cs[0].NetworkSettings.Networks["app"].NetworkID; got != oldID {
		t.Errorf("stale endpoint NetworkID = %q, want the old %q", got, oldID)
	}
}

func TestUnstartedContainerAttachedByName(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	netID := mustAddNetwork(t, f, network.Network{Name: "app"})
	c := exited("new", "nginx", "sha256:a")
	c.State, c.Status = container.StateCreated, "Created"
	c.NetworkSettings = WithoutNetworkIDs(mustNetworkSettings(t, f, "app"))
	f.AddContainer(c)
	cs, _ := f.ContainerList(ctx, docker.ContainerListOptions{All: true})
	if ep := cs[0].NetworkSettings.Networks["app"]; ep == nil || ep.NetworkID != "" {
		t.Fatalf("created container's endpoint = %+v", ep)
	}
	// Its endpoint has no ID; it belongs to the network of that name.
	if err := f.DisconnectNetwork(netID, "new"); err != nil {
		t.Errorf("disconnect the created container: %v", err)
	}
}

func TestNetworkInspect(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	id := mustAddNetwork(t, f, network.Network{Name: "app", Labels: map[string]string{"k": "v"}})
	web := running("web", "nginx", "sha256:a")
	web.NetworkSettings = mustNetworkSettings(t, f, "app")
	webID := f.AddContainer(web)
	db := exited("db", "pg", "sha256:p")
	db.NetworkSettings = mustNetworkSettings(t, f, "app")
	f.AddContainer(db)

	for _, ref := range []string{id, "app", id[:12]} {
		insp, err := f.NetworkInspect(ctx, ref)
		if err != nil {
			t.Fatalf("NetworkInspect(%q): %v", ref, err)
		}
		if insp.Network.ID != id || insp.Network.Name != "app" {
			t.Errorf("NetworkInspect(%q) = %+v", ref, insp.Network.Network)
		}
		// Only running containers have endpoints.
		if len(insp.Network.Containers) != 1 || insp.Network.Containers[webID].Name != "web" {
			t.Errorf("NetworkInspect(%q).Containers = %+v", ref, insp.Network.Containers)
		}
		var raw struct {
			ID         string `json:"Id"`
			Labels     map[string]string
			Containers map[string]json.RawMessage
		}
		if err := json.Unmarshal(insp.Raw, &raw); err != nil || raw.ID != id || raw.Labels["k"] != "v" || len(raw.Containers) != 1 {
			t.Errorf("raw inspect = %s (%v)", insp.Raw, err)
		}
	}
	if _, err := f.NetworkInspect(ctx, "missing"); !docker.IsNotFound(err) {
		t.Errorf("inspect missing: err = %v, want not found", err)
	}
	if _, err := f.NetworkInspect(ctx, "a/b"); !docker.IsInvalidArgument(err) {
		t.Errorf("inspect invalid: err = %v, want invalid argument", err)
	}
}

func TestNetworkMethodsErrorInjection(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	boom := cerrdefs.ErrUnavailable.WithMessage("boom")
	f.FailNext(MethodNetworkList, boom)
	if _, err := f.NetworkList(ctx); !cerrdefs.IsUnavailable(err) {
		t.Errorf("NetworkList: err = %v, want injected", err)
	}
	id := PredefinedNetworkID(BridgeNetwork)
	f.FailFor(MethodNetworkInspect, id, boom)
	if _, err := f.NetworkInspect(ctx, id); !cerrdefs.IsUnavailable(err) {
		t.Errorf("NetworkInspect: err = %v, want injected", err)
	}
	f.Disconnect(nil)
	if err := f.NetworkRemove(ctx, id); err == nil || cerrdefs.IsPermissionDenied(err) {
		t.Errorf("NetworkRemove while disconnected: err = %v, want the connection error", err)
	}
}

func TestNetworkConcurrentUse(t *testing.T) {
	t.Parallel()
	ctx := ctxT(t)
	f := New()
	netID := mustAddNetwork(t, f, network.Network{Name: "shared"})
	es := f.Events(ctx)
	go func() {
		for range es.Messages {
		}
	}()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				id := f.AddContainer(running("c", "img", "sha256:x"))
				_ = f.ConnectNetwork(netID, id)
				_, _ = f.NetworkList(ctx)
				_, _ = f.NetworkInspect(ctx, netID)
				_, _ = f.ContainerList(ctx, docker.ContainerListOptions{All: true})
				_ = f.DisconnectNetwork(netID, id)
				_ = f.NetworkRemove(ctx, netID)
			}
		})
	}
	wg.Wait()
}
