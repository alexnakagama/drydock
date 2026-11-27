package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

const waitTimeout = 3 * time.Second

func testConfig() config {
	return config{
		debounce:      40 * time.Millisecond,
		maxWait:       time.Second,
		backoffBase:   10 * time.Millisecond,
		backoffMax:    40 * time.Millisecond,
		stableAfter:   time.Nanosecond,
		pingTimeout:   time.Second,
		callTimeout:   time.Second,
		actionTimeout: time.Second,
	}
}

// recorder collects the messages a store sends, in order.
type recorder struct {
	mu     sync.Mutex
	msgs   []tea.Msg
	next   int
	notify chan struct{}
}

func newRecorder() *recorder { return &recorder{notify: make(chan struct{}, 1)} }

func (r *recorder) send(msg tea.Msg) {
	r.mu.Lock()
	r.msgs = append(r.msgs, msg)
	r.mu.Unlock()
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// waitFor consumes messages in order until one satisfies pred, and returns
// it. Messages skipped on the way are consumed too.
func (r *recorder) waitFor(t *testing.T, what string, pred func(tea.Msg) bool) tea.Msg {
	t.Helper()
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	for {
		r.mu.Lock()
		for r.next < len(r.msgs) {
			m := r.msgs[r.next]
			r.next++
			if pred(m) {
				r.mu.Unlock()
				return m
			}
		}
		r.mu.Unlock()
		select {
		case <-r.notify:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (r *recorder) snapshot(t *testing.T, what string, pred func(Snapshot) bool) Snapshot {
	t.Helper()
	m := r.waitFor(t, "snapshot: "+what, func(m tea.Msg) bool {
		sm, ok := m.(SnapshotMsg)
		return ok && (pred == nil || pred(sm.Snapshot))
	})
	return m.(SnapshotMsg).Snapshot
}

func (r *recorder) state(t *testing.T, want State) ConnectionMsg {
	t.Helper()
	m := r.waitFor(t, "state "+want.String(), func(m tea.Msg) bool {
		cm, ok := m.(ConnectionMsg)
		return ok && cm.State == want
	})
	return m.(ConnectionMsg)
}

// pending returns the messages not yet consumed.
func (r *recorder) pending() []tea.Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.msgs[r.next:])
}

// start runs a store on svc until the test ends, and waits for Run to
// return during cleanup.
func start(t *testing.T, svc docker.Service, cfg config) (*Store, *recorder) {
	t.Helper()
	rec := newRecorder()
	st := newStore(svc, rec.send, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(waitTimeout):
			t.Error("Run did not return after cancellation")
		}
	})
	return st, rec
}

func hasContainer(name string) func(Snapshot) bool {
	return func(s Snapshot) bool {
		return slices.ContainsFunc(s.Containers(), func(c container.Summary) bool { return ContainerName(c) == name })
	}
}

func hasVolume(name string) func(Snapshot) bool {
	return func(s Snapshot) bool {
		return slices.ContainsFunc(s.Volumes(), func(v volume.Volume) bool { return v.Name == name })
	}
}

func hasNetwork(name string) func(Snapshot) bool {
	return func(s Snapshot) bool {
		return slices.ContainsFunc(s.Networks(), func(n network.Summary) bool { return n.Name == name })
	}
}

func networkNames(ns []network.Summary) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, n.Name)
	}
	slices.Sort(out)
	return out
}

// mustAddNetwork adds a network named name to f and returns its ID.
func mustAddNetwork(t *testing.T, f *dockerfake.Service, name string) string {
	t.Helper()
	id, err := f.AddNetwork(network.Network{Name: name})
	if err != nil {
		t.Fatalf("AddNetwork(%s): %v", name, err)
	}
	return id
}

// populated returns a fake with images, a volume, a network, and a running
// and a stopped container. Its setup is fixed (no testing.T), so it panics
// if the fake refuses it.
func populated() *dockerfake.Service {
	f := dockerfake.New()
	img := f.AddImage(image.Summary{RepoTags: []string{"app:latest", "app:1.0"}, Size: 100})
	f.AddImage(image.Summary{Size: 5}) // dangling
	f.AddVolume(volume.Volume{Name: "pgdata"})
	if _, err := f.AddNetwork(network.Network{Name: "app_default"}); err != nil {
		panic(err)
	}
	ns, err := f.NetworkSettings("app_default")
	if err != nil {
		panic(err)
	}
	web := ctr("", "web", img, container.StateRunning, volMount("pgdata", "/data"))
	web.NetworkSettings = ns
	f.AddContainer(web)
	f.AddContainer(ctr("", "old", img, container.StateExited))
	return f
}

// --- 3.1 initial load --------------------------------------------------

func TestInitialLoad(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())

	if first := rec.waitFor(t, "first message", func(tea.Msg) bool { return true }); first != (ConnectionMsg{State: StateConnecting}) {
		t.Errorf("first message = %#v, want connecting", first)
	}
	snap := rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	if snap.Seq() != 1 || !snap.Loaded() {
		t.Errorf("initial snapshot Seq = %d, Loaded = %v", snap.Seq(), snap.Loaded())
	}
	if got := containerNames(snap.Containers()); !slices.Equal(got, []string{"web", "old"}) {
		t.Errorf("containers = %v, want running and stopped", got)
	}
	if len(snap.Images()) != 2 || len(snap.Volumes()) != 1 {
		t.Errorf("images = %d, volumes = %d; want 2 and 1", len(snap.Images()), len(snap.Volumes()))
	}
	if got := networkNames(snap.Networks()); !slices.Equal(got, []string{"app_default", "bridge", "host", "none"}) {
		t.Errorf("networks = %v, want the predefined ones and app_default", got)
	}
	if id, _ := f.NetworkID("app_default"); snap.NetworkUseCount(id) != 1 {
		t.Errorf("IN USE of app_default = %d, want 1", snap.NetworkUseCount(id))
	}
	if n := len(f.CallsTo(dockerfake.MethodNetworkList)); n != 1 {
		t.Errorf("NetworkList calls = %d, want 1", n)
	}
	calls := f.CallsTo(dockerfake.MethodContainerList)
	if len(calls) != 1 || calls[0].Args[0] != (docker.ContainerListOptions{All: true}) {
		t.Errorf("ContainerList calls = %+v, want one with All", calls)
	}
	// The subscription is opened before listing, so no change is missed.
	all := f.Calls()
	ev := slices.IndexFunc(all, func(c dockerfake.Call) bool { return c.Method == dockerfake.MethodEvents })
	ls := slices.IndexFunc(all, func(c dockerfake.Call) bool { return c.Method == dockerfake.MethodContainerList })
	if ev < 0 || ev > ls {
		t.Errorf("Events (call %d) not subscribed before listing (call %d)", ev, ls)
	}
}

// --- 3.2 derived indexes through the store -----------------------------

func TestUsageCountsIncludeStoppedAndAllTags(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	snap := rec.snapshot(t, "initial", nil)

	var tagged image.Summary
	for _, img := range snap.Images() {
		if len(img.RepoTags) == 2 {
			tagged = img
		}
	}
	if tagged.ID == "" {
		t.Fatal("multi-tag image not in snapshot")
	}
	// Both tag rows look up the same ID; both containers count, including
	// the stopped one.
	if got := snap.ImageUseCount(tagged.ID); got != 2 {
		t.Errorf("IN USE of %v = %d, want 2", tagged.RepoTags, got)
	}
	if got := containerNames(snap.ImageUsers(tagged.ID)); !slices.Equal(got, []string{"old", "web"}) {
		t.Errorf("users = %v", got)
	}
	if got := snap.VolumeUseCount("pgdata"); got != 1 {
		t.Errorf("IN USE of pgdata = %d, want 1", got)
	}
}

// --- 3.3 events, debounce, usage refresh -------------------------------

func TestEventBurstYieldsOneRelist(t *testing.T) {
	t.Parallel()
	f := populated()
	cfg := testConfig()
	_, rec := start(t, f, cfg)
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	f.ResetCalls()

	for i := range 50 {
		f.Emit(events.Message{Type: events.ContainerEventType, Action: events.ActionStart,
			Actor: events.Actor{ID: fmt.Sprint(i)}})
	}
	snap := rec.snapshot(t, "after burst", nil)
	if snap.Seq() != 2 {
		t.Errorf("Seq after burst = %d, want 2", snap.Seq())
	}
	// A later event of another kind is a sentinel: once its refresh is
	// published, any extra container re-list would have happened.
	f.Emit(events.Message{Type: events.VolumeEventType, Action: events.ActionCreate})
	if got := rec.snapshot(t, "sentinel", nil).Seq(); got != 3 {
		t.Errorf("sentinel snapshot Seq = %d, want 3 (an extra snapshot was published)", got)
	}

	if n := len(f.CallsTo(dockerfake.MethodContainerList)); n != 1 {
		t.Errorf("ContainerList called %d times for a burst, want 1", n)
	}
	if n := len(f.CallsTo(dockerfake.MethodImageList)); n != 0 {
		t.Errorf("container events re-listed images (%d calls)", n)
	}
	if n := len(f.CallsTo(dockerfake.MethodVolumeList)); n != 1 {
		t.Errorf("VolumeList called %d times, want 1 (the sentinel)", n)
	}
	for _, m := range rec.pending() {
		if _, ok := m.(SnapshotMsg); ok {
			t.Errorf("extra snapshot after the burst: %#v", m)
		}
	}
}

func TestEventsRelistOnlyTheirKind(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	f.ResetCalls()

	f.AddVolume(volume.Volume{Name: "cache"})
	snap := rec.snapshot(t, "volume added", hasVolume("cache"))
	if n := len(f.CallsTo(dockerfake.MethodContainerList)) + len(f.CallsTo(dockerfake.MethodImageList)); n != 0 {
		t.Errorf("a volume event re-listed other kinds (%d calls)", n)
	}
	if len(snap.Containers()) != 2 || len(snap.Images()) != 2 {
		t.Error("unchanged kinds were not carried over to the new snapshot")
	}

	f.ResetCalls()
	f.AddImage(image.Summary{RepoTags: []string{"alpine:3.20"}})
	rec.snapshot(t, "image added", func(s Snapshot) bool { return len(s.Images()) == 3 })
	if n := len(f.CallsTo(dockerfake.MethodContainerList)) + len(f.CallsTo(dockerfake.MethodVolumeList)) +
		len(f.CallsTo(dockerfake.MethodNetworkList)); n != 0 {
		t.Errorf("an image event re-listed other kinds (%d calls)", n)
	}
}

func TestNetworkCreateAndDestroyEvents(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	f.ResetCalls()

	id := mustAddNetwork(t, f, "test-net")
	snap := rec.snapshot(t, "network created", hasNetwork("test-net"))
	if got := snap.NumNetworks(); got != 5 {
		t.Errorf("NumNetworks after create = %d, want 5", got)
	}
	if snap.NetworkUseCount(id) != 0 {
		t.Errorf("new network IN USE = %d, want 0", snap.NetworkUseCount(id))
	}
	if len(snap.Containers()) != 2 || len(snap.Images()) != 2 || len(snap.Volumes()) != 1 {
		t.Error("unchanged kinds were not carried over to the new snapshot")
	}
	if appID, _ := f.NetworkID("app_default"); snap.NetworkUseCount(appID) != 1 {
		t.Error("network usage was lost when only networks were re-listed")
	}
	if n := len(f.CallsTo(dockerfake.MethodContainerList)) + len(f.CallsTo(dockerfake.MethodImageList)) +
		len(f.CallsTo(dockerfake.MethodVolumeList)); n != 0 {
		t.Errorf("a network create event re-listed other kinds (%d calls)", n)
	}

	f.ResetCalls()
	if !f.RemoveNetwork(id) {
		t.Fatal("RemoveNetwork: network not found")
	}
	snap = rec.snapshot(t, "network destroyed", func(s Snapshot) bool { return !hasNetwork("test-net")(s) })
	if got := snap.NumNetworks(); got != 4 {
		t.Errorf("NumNetworks after destroy = %d, want 4", got)
	}
	if n := len(f.CallsTo(dockerfake.MethodContainerList)); n != 0 {
		t.Errorf("a network destroy event re-listed containers (%d calls)", n)
	}
}

// docker network connect emits only a network event; the store must still
// see the container's new attachment, with no other engine activity.
func TestNetworkConnectRefreshesUsers(t *testing.T) {
	t.Parallel()
	f := populated()
	cfg := testConfig()
	// A long maxWait separates a refresh driven by the debounce from one
	// that only maxWait would force.
	cfg.debounce = 50 * time.Millisecond
	cfg.maxWait = 5 * time.Second
	_, rec := start(t, f, cfg)
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	id := mustAddNetwork(t, f, "test-net")
	rec.snapshot(t, "network created", hasNetwork("test-net"))
	f.ResetCalls()

	connectedAt := time.Now()
	if err := f.ConnectNetwork("test-net", "web"); err != nil {
		t.Fatalf("ConnectNetwork: %v", err)
	}
	snap := rec.snapshot(t, "web connected", func(s Snapshot) bool { return s.NetworkUseCount(id) == 1 })
	if got := containerNames(snap.NetworkUsers(id)); !slices.Equal(got, []string{"web"}) {
		t.Errorf("NetworkUsers(test-net) = %v, want [web]", got)
	}
	// The debounce, not maxWait or a later unrelated event, brought it.
	if waited := time.Since(connectedAt); waited >= cfg.maxWait/2 {
		t.Errorf("connect took %v to show, want well under maxWait (%v); debounce is %v",
			waited, cfg.maxWait, cfg.debounce)
	}
	if n := len(f.CallsTo(dockerfake.MethodContainerList)); n != 1 {
		t.Errorf("ContainerList calls after connect = %d, want 1", n)
	}
	if n := len(f.CallsTo(dockerfake.MethodNetworkList)); n != 1 {
		t.Errorf("NetworkList calls after connect = %d, want 1", n)
	}

	if err := f.DisconnectNetwork("test-net", "web"); err != nil {
		t.Fatalf("DisconnectNetwork: %v", err)
	}
	rec.snapshot(t, "web disconnected", func(s Snapshot) bool { return s.NetworkUseCount(id) == 0 })
}

func TestNetworkConnectBurstReloadsEachKindOnce(t *testing.T) {
	t.Parallel()
	const n = 20
	f := populated()
	for i := range n {
		f.AddContainer(ctr("", fmt.Sprintf("c%02d", i), "", container.StateRunning))
	}
	// A debounce long enough that a scheduling pause on a loaded machine
	// cannot split the burst into two refreshes; maxWait stays above it.
	cfg := testConfig()
	cfg.debounce = 300 * time.Millisecond
	cfg.maxWait = 5 * time.Second
	_, rec := start(t, f, cfg)
	rec.snapshot(t, "initial", func(s Snapshot) bool { return s.NumContainers() == n+2 })
	rec.state(t, StateConnected)
	id := mustAddNetwork(t, f, "burst")
	rec.snapshot(t, "network created", hasNetwork("burst"))
	f.ResetCalls()

	for i := range n {
		if err := f.ConnectNetwork("burst", fmt.Sprintf("c%02d", i)); err != nil {
			t.Fatalf("ConnectNetwork: %v", err)
		}
	}
	rec.snapshot(t, "burst connected", func(s Snapshot) bool { return s.NetworkUseCount(id) == n })
	// Image events refreshed one after the other are sentinels: by the
	// second refresh, any extra re-list the burst had scheduled would have
	// run.
	for range 2 {
		f.Emit(events.Message{Type: events.ImageEventType, Action: events.ActionTag})
		rec.snapshot(t, "sentinel", nil)
	}

	if got := len(f.CallsTo(dockerfake.MethodContainerList)); got != 1 {
		t.Errorf("ContainerList called %d times for a burst of connects, want 1", got)
	}
	if got := len(f.CallsTo(dockerfake.MethodNetworkList)); got != 1 {
		t.Errorf("NetworkList called %d times for a burst of connects, want 1", got)
	}
	if got := len(f.CallsTo(dockerfake.MethodVolumeList)); got != 0 {
		t.Errorf("connect events re-listed volumes (%d calls)", got)
	}
}

func TestEventKinds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		m    events.Message
		want []kind
	}{
		{events.Message{Type: events.ContainerEventType, Action: events.ActionStart}, []kind{kindContainers}},
		{events.Message{Type: events.ContainerEventType, Action: events.ActionExecStart}, nil},
		{events.Message{Type: events.ContainerEventType, Action: events.ActionAttach}, nil},
		{events.Message{Type: events.ImageEventType, Action: events.ActionPull}, []kind{kindImages}},
		{events.Message{Type: events.VolumeEventType, Action: events.ActionCreate}, []kind{kindVolumes}},
		{events.Message{Type: events.VolumeEventType, Action: events.ActionMount}, nil},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionCreate}, []kind{kindNetworks}},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionDestroy}, []kind{kindNetworks}},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionRemove}, []kind{kindNetworks}},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionPrune}, []kind{kindNetworks}},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionConnect}, []kind{kindContainers, kindNetworks}},
		{events.Message{Type: events.NetworkEventType, Action: events.ActionDisconnect}, []kind{kindContainers, kindNetworks}},
		{events.Message{Type: events.PluginEventType, Action: events.ActionEnable}, nil},
		{events.Message{}, nil},
	}
	for _, tt := range tests {
		ks := eventKinds(tt.m)
		var got []kind
		for k := range numKinds {
			if ks.has(k) {
				got = append(got, k)
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("eventKinds(%s %s) = %v, want %v", tt.m.Type, tt.m.Action, got, tt.want)
		}
	}
}

func TestContainerEventsRefreshUsageCounts(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	img := f.AddImage(image.Summary{RepoTags: []string{"redis:7"}})
	f.AddVolume(volume.Volume{Name: "cache"})
	snap := rec.snapshot(t, "image and volume", func(s Snapshot) bool {
		return hasVolume("cache")(s) && len(s.Images()) == 3
	})
	if snap.ImageUseCount(img) != 0 || snap.VolumeUseCount("cache") != 0 {
		t.Fatal("new image or volume already in use")
	}
	f.ResetCalls()

	id := f.AddContainer(ctr("", "redis", img, container.StateExited, volMount("cache", "/data")))
	snap = rec.snapshot(t, "redis container", hasContainer("redis"))
	if got := snap.ImageUseCount(img); got != 1 {
		t.Errorf("image IN USE after container create = %d, want 1", got)
	}
	if got := snap.VolumeUseCount("cache"); got != 1 {
		t.Errorf("volume IN USE after container create = %d, want 1", got)
	}
	if n := len(f.CallsTo(dockerfake.MethodImageList)) + len(f.CallsTo(dockerfake.MethodVolumeList)); n != 0 {
		t.Errorf("usage refresh re-listed images or volumes (%d calls)", n)
	}

	f.RemoveContainer(id)
	snap = rec.snapshot(t, "redis removed", func(s Snapshot) bool { return !hasContainer("redis")(s) })
	if snap.ImageUseCount(img) != 0 || snap.VolumeUseCount("cache") != 0 {
		t.Error("usage counts not refreshed after container removal")
	}
}

func TestIrrelevantEventsAreIgnored(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	f.ResetCalls()

	for _, m := range []events.Message{
		{Type: events.ContainerEventType, Action: events.ActionExecCreate},
		{Type: events.ContainerEventType, Action: events.ActionExecDie},
		{Type: events.ContainerEventType, Action: events.ActionResize},
		{Type: events.VolumeEventType, Action: events.ActionMount},
		{Type: events.VolumeEventType, Action: events.ActionUnmount},
		{Type: events.PluginEventType, Action: events.ActionCreate},
	} {
		f.Emit(m)
	}
	// Two image events refreshed one after the other act as sentinels: by
	// the second refresh, any re-list the ignored events had scheduled (due
	// one debounce after them) would have run.
	for range 2 {
		f.Emit(events.Message{Type: events.ImageEventType, Action: events.ActionTag})
		rec.snapshot(t, "sentinel", nil)
	}
	for _, c := range f.Calls() {
		if c.Method != dockerfake.MethodImageList {
			t.Errorf("irrelevant events caused an engine call: %s", c.Method)
		}
	}
}

// A continuous stream of events must not postpone a refresh beyond
// maxWait.
func TestEventStormRefreshesWithinMaxWait(t *testing.T) {
	t.Parallel()
	f := populated()
	cfg := testConfig()
	cfg.debounce = 50 * time.Millisecond
	cfg.maxWait = 150 * time.Millisecond
	_, rec := start(t, f, cfg)
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	stop := make(chan struct{})
	stormDone := make(chan struct{})
	go func() {
		defer close(stormDone)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				f.Emit(events.Message{Type: events.ContainerEventType, Action: events.ActionStart})
			}
		}
	}()
	defer func() { close(stop); <-stormDone }()

	rec.snapshot(t, "refresh during the storm", func(s Snapshot) bool { return s.Seq() >= 2 })
}

func TestEventDebounceTiming(t *testing.T) {
	t.Parallel()
	if defaultDebounce != 150*time.Millisecond {
		t.Errorf("default debounce = %v, want 150ms", defaultDebounce)
	}
}

// --- 3.4 disconnect and recovery ---------------------------------------

func TestBackoff(t *testing.T) {
	t.Parallel()
	want := []time.Duration{1, 2, 4, 8, 10, 10, 10}
	for n, w := range want {
		if got := backoff(defaultBackoffBase, defaultBackoffMax, n); got != w*time.Second {
			t.Errorf("backoff(%d) = %v, want %v", n, got, w*time.Second)
		}
	}
	if got := backoff(time.Second, 10*time.Second, 1000); got != 10*time.Second {
		t.Errorf("backoff does not stay capped for large attempts: %v", got)
	}
}

func TestRecoversAfterEngineLoss(t *testing.T) {
	t.Parallel()
	f := populated()
	f.SetHost("tcp://admin:hunter2@10.0.0.1:2376")
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	f.Disconnect(errors.New("dial tcp://admin:hunter2@10.0.0.1:2376: connection refused \x1b[2J"))
	down := rec.state(t, StateDisconnected)
	if down.Err == nil || down.RetryIn != 10*time.Millisecond {
		t.Errorf("disconnected msg = %+v, want an error and the first backoff step", down)
	}
	if down.Reason == "" || containsAny(down.Reason, "hunter2", "\x1b") {
		t.Errorf("Reason is not safe to display: %q", down.Reason)
	}
	// While the engine is down, retries back off: 20ms, 40ms, then capped.
	var delays []time.Duration
	for range 3 {
		delays = append(delays, rec.state(t, StateDisconnected).RetryIn)
	}
	if !slices.Equal(delays, []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}) {
		t.Errorf("retry delays = %v, want [20ms 40ms 40ms]", delays)
	}

	// Changes made while disconnected show up after the reload.
	f.AddContainer(ctr("", "late", "", container.StateRunning))
	f.Reconnect()
	snap := rec.snapshot(t, "reload after reconnect", hasContainer("late"))
	rec.state(t, StateConnected)
	if snap.Seq() < 2 {
		t.Errorf("reload Seq = %d, want > 1", snap.Seq())
	}
	if n := f.Subscribers(); n != 1 {
		t.Errorf("event subscribers after reconnect = %d, want 1", n)
	}

	// Live updates resume on the new subscription.
	f.AddVolume(volume.Volume{Name: "after"})
	rec.snapshot(t, "event after reconnect", hasVolume("after"))
}

func TestRecoversAfterEventStreamDrop(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	first := rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)
	f.ResetCalls()

	f.DropEvents(nil)
	rec.state(t, StateDisconnected)
	snap := rec.snapshot(t, "full reload", nil)
	rec.state(t, StateConnected)
	if snap.Seq() <= first.Seq() {
		t.Errorf("reload Seq %d not newer than %d", snap.Seq(), first.Seq())
	}
	for _, m := range []dockerfake.Method{
		dockerfake.MethodPing, dockerfake.MethodEvents,
		dockerfake.MethodContainerList, dockerfake.MethodImageList, dockerfake.MethodVolumeList,
		dockerfake.MethodNetworkList,
	} {
		if n := len(f.CallsTo(m)); n != 1 {
			t.Errorf("%s called %d times during recovery, want 1", m, n)
		}
	}
	if n := f.Subscribers(); n != 1 {
		t.Errorf("event subscribers = %d, want 1", n)
	}
}

func TestStartsDisconnectedAndRecovers(t *testing.T) {
	t.Parallel()
	f := populated()
	f.Disconnect(nil)
	st, rec := start(t, f, testConfig())
	rec.state(t, StateDisconnected)
	if st.State() != StateDisconnected {
		t.Errorf("State = %v", st.State())
	}
	f.Reconnect()
	rec.snapshot(t, "initial after recovery", hasContainer("web"))
	rec.state(t, StateConnected)
}

func TestFailedRefreshReconnects(t *testing.T) {
	t.Parallel()
	f := populated()
	_, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	f.FailNext(dockerfake.MethodContainerList, errors.New("engine hiccup"))
	f.AddContainer(ctr("", "new", "", container.StateRunning))
	down := rec.state(t, StateDisconnected)
	if down.Err == nil || !containsAny(down.Reason, "engine hiccup") {
		t.Errorf("disconnected msg = %+v, want the list error", down)
	}
	rec.snapshot(t, "recovered", hasContainer("new"))
	rec.state(t, StateConnected)
}

func TestFailedSubscriptionReconnects(t *testing.T) {
	t.Parallel()
	f := populated()
	f.FailNext(dockerfake.MethodEvents, errors.New("events refused"))
	_, rec := start(t, f, testConfig())
	down := rec.state(t, StateDisconnected)
	if !containsAny(down.Reason, "events refused") {
		t.Errorf("Reason = %q", down.Reason)
	}
	rec.snapshot(t, "recovered", nil)
	rec.state(t, StateConnected)
	if n := f.Subscribers(); n != 1 {
		t.Errorf("event subscribers = %d, want 1", n)
	}
}

// hangingEvents is a service whose event subscription never gets an answer
// from the engine.
type hangingEvents struct {
	*dockerfake.Service
}

func (h hangingEvents) Events(ctx context.Context) docker.EventStream {
	<-ctx.Done()
	errc := make(chan error, 1)
	errc <- ctx.Err()
	close(errc)
	return docker.EventStream{Messages: make(chan events.Message), Err: errc}
}

func TestSubscriptionTimeout(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.callTimeout = 50 * time.Millisecond
	_, rec := start(t, hangingEvents{populated()}, cfg)
	down := rec.state(t, StateDisconnected)
	if !errors.Is(down.Err, context.DeadlineExceeded) {
		t.Errorf("Err = %v, want a deadline error", down.Err)
	}
}

func TestShutdownReleasesStream(t *testing.T) {
	t.Parallel()
	f := populated()
	rec := newRecorder()
	st := newStore(f, rec.send, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx) }()
	rec.state(t, StateConnected)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run did not return")
	}
	deadline := time.Now().Add(waitTimeout)
	for f.Subscribers() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("event stream still open after Run returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if st.State() == StateConnected {
		t.Error("store still reports connected after Run returned")
	}
	if err := st.Run(context.Background()); err == nil {
		t.Error("second Run succeeded")
	}
}

// --- 3.5 action gating -------------------------------------------------

func TestActionsRefusedWhileDisconnected(t *testing.T) {
	t.Parallel()
	f := populated()
	id := f.AddContainer(ctr("", "stopped", "", container.StateExited))
	st, rec := start(t, f, testConfig())
	rec.snapshot(t, "initial", nil)
	rec.state(t, StateConnected)

	f.Disconnect(nil)
	rec.state(t, StateDisconnected)
	f.ResetCalls()

	called := false
	start := func(ctx context.Context, svc docker.Service) error {
		called = true
		return svc.ContainerStart(ctx, id)
	}
	if err := st.Do(context.Background(), start); !errors.Is(err, ErrNotConnected) {
		t.Errorf("Do while disconnected = %v, want ErrNotConnected", err)
	}
	msg := st.Action("start stopped", start)()
	res, ok := msg.(ActionResultMsg)
	if !ok || !errors.Is(res.Err, ErrNotConnected) || res.Label != "start stopped" || res.Reason != ErrNotConnected.Error() {
		t.Errorf("Action while disconnected = %#v", msg)
	}
	if called {
		t.Error("action function ran while disconnected")
	}
	for _, c := range f.Calls() {
		if c.Method != dockerfake.MethodPing {
			t.Errorf("engine called while disconnected: %s", c.Method)
		}
	}

	// Once reconnected, the same action runs.
	f.Reconnect()
	rec.state(t, StateConnected)
	res, _ = st.Action("start stopped", start)().(ActionResultMsg)
	if res.Err != nil || res.Reason != "" || !called {
		t.Errorf("Action while connected = %+v (called %v)", res, called)
	}
	if n := len(f.CallsTo(dockerfake.MethodContainerStart)); n != 1 {
		t.Errorf("ContainerStart calls = %d, want 1", n)
	}
}

func TestActionsRefusedBeforeRun(t *testing.T) {
	t.Parallel()
	f := populated()
	st := newStore(f, func(tea.Msg) {}, testConfig())
	if st.State() != StateConnecting {
		t.Errorf("initial State = %v", st.State())
	}
	err := st.Do(context.Background(), func(context.Context, docker.Service) error {
		t.Error("action ran before Run")
		return nil
	})
	if !errors.Is(err, ErrNotConnected) {
		t.Errorf("Do before Run = %v", err)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Errorf("engine called: %+v", calls)
	}
}

func TestActionReportsEngineError(t *testing.T) {
	t.Parallel()
	f := populated()
	st, rec := start(t, f, testConfig())
	rec.state(t, StateConnected)
	res := st.Action("start", func(ctx context.Context, svc docker.Service) error {
		return svc.ContainerStart(ctx, "nope")
	})().(ActionResultMsg)
	if !docker.IsNotFound(res.Err) || res.Reason == "" {
		t.Errorf("result = %+v, want the engine's not-found error", res)
	}
}

func containsAny(s string, subs ...string) bool {
	return slices.ContainsFunc(subs, func(sub string) bool { return strings.Contains(s, sub) })
}

// flappingEvents is a service whose event streams end shortly after the
// engine accepts them, like a proxy that drops long-lived connections.
type flappingEvents struct {
	*dockerfake.Service
}

func (fl flappingEvents) Events(ctx context.Context) docker.EventStream {
	errc := make(chan error, 1)
	go func() {
		defer close(errc)
		select {
		case <-time.After(5 * time.Millisecond):
			errc <- io.ErrUnexpectedEOF
		case <-ctx.Done():
			errc <- ctx.Err()
		}
	}()
	return docker.EventStream{Messages: make(chan events.Message), Err: errc}
}

func TestFlappingStreamKeepsBackingOff(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	cfg.stableAfter = time.Hour
	_, rec := start(t, flappingEvents{populated()}, cfg)
	var delays []time.Duration
	for range 4 {
		delays = append(delays, rec.state(t, StateDisconnected).RetryIn)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}
	if !slices.Equal(delays, want) {
		t.Errorf("retry delays for a flapping stream = %v, want %v", delays, want)
	}
}

func TestMessageErrorsAreSafeToDisplay(t *testing.T) {
	t.Parallel()
	f := populated()
	f.SetHost("tcp://admin:hunter2@10.0.0.1:2376")
	engineErr := errors.New("dial tcp://admin:hunter2@10.0.0.1:2376: refused \x1b]0;pwned\a")
	st, rec := start(t, f, testConfig())
	rec.state(t, StateConnected)

	res := st.Action("x", func(context.Context, docker.Service) error { return engineErr })().(ActionResultMsg)
	f.Disconnect(engineErr)
	down := rec.state(t, StateDisconnected)

	for name, err := range map[string]error{"ActionResultMsg.Err": res.Err, "ConnectionMsg.Err": down.Err} {
		if err == nil {
			t.Fatalf("%s is nil", name)
		}
		if containsAny(err.Error(), "hunter2", "\x1b", "\a") {
			t.Errorf("%s.Error() is not safe to display: %q", name, err.Error())
		}
		if !errors.Is(err, engineErr) {
			t.Errorf("%s does not unwrap to the engine error", name)
		}
	}
	if res.Err.Error() != res.Reason {
		t.Errorf("Err text %q differs from Reason %q", res.Err.Error(), res.Reason)
	}
}

func TestActionWithDetailAndTimeout(t *testing.T) {
	t.Parallel()
	f := populated()
	st, rec := start(t, f, testConfig())
	rec.state(t, StateConnected)

	res := st.ActionWith("prune", 0, func(context.Context, docker.Service) (string, error) {
		return "reclaimed 1MB", nil
	})().(ActionResultMsg)
	if res.Err != nil || res.Label != "prune" || res.Detail != "reclaimed 1MB" {
		t.Errorf("success = %+v", res)
	}

	// The detail is dropped on failure.
	res = st.ActionWith("prune", 0, func(context.Context, docker.Service) (string, error) {
		return "ignored", errors.New("boom")
	})().(ActionResultMsg)
	if res.Err == nil || res.Reason != "boom" || res.Detail != "" {
		t.Errorf("failure = %+v", res)
	}

	// A custom timeout bounds the action's context.
	res = st.ActionWith("slow", 10*time.Millisecond, func(ctx context.Context, _ docker.Service) (string, error) {
		dl, ok := ctx.Deadline()
		if !ok || time.Until(dl) > 10*time.Millisecond {
			t.Errorf("deadline %v (set %v), want within 10ms", dl, ok)
		}
		<-ctx.Done()
		return "", ctx.Err()
	})().(ActionResultMsg)
	if !errors.Is(res.Err, context.DeadlineExceeded) {
		t.Errorf("timed-out action = %+v", res)
	}
}

// --- Compose projects through the store --------------------------------

func TestSnapshotProjectsFromFake(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	add := func(name, project string, state container.ContainerState, oneoff bool) string {
		return f.AddContainer(container.Summary{
			Names: []string{"/" + name}, State: state,
			Labels: dockerfake.Compose{Project: project, Service: "svc", OneOff: oneoff, WorkingDir: "/srv/" + project}.Labels(),
		})
	}
	add("myapp-web-1", "myapp", container.StateRunning, false)
	add("myapp-db-1", "myapp", container.StateExited, false)
	add("myapp-run-1", "myapp", container.StateRunning, true)
	f.AddContainer(ctr("", "redis", "", container.StateRunning))
	_, rec := start(t, f, testConfig())

	snap := rec.snapshot(t, "initial", nil)
	ps := snap.Projects()
	if snap.NumProjects() != 1 || len(ps) != 1 {
		t.Fatalf("projects = %v, want [myapp]", projectNames(ps))
	}
	p := ps[0]
	if p.Name != "myapp" || p.Running != 1 || p.Total() != 2 || p.State != ProjectPartial ||
		len(p.OneOffs) != 1 || p.WorkingDir != "/srv/myapp" {
		t.Errorf("myapp = %+v, want 1/2 partial with one one-off in /srv/myapp", p)
	}

	// A new project appears with its first container ...
	blog := add("blog-web-1", "blog", container.StateExited, false)
	snap = rec.snapshot(t, "blog created", func(s Snapshot) bool { return s.NumProjects() == 2 })
	if got := projectNames(snap.Projects()); !slices.Equal(got, []string{"blog", "myapp"}) {
		t.Errorf("projects = %v, want [blog myapp]", got)
	}
	// ... and disappears with its last one.
	f.RemoveContainer(blog)
	snap = rec.snapshot(t, "blog removed", func(s Snapshot) bool { return s.NumProjects() == 1 })
	if got := projectNames(snap.Projects()); !slices.Equal(got, []string{"myapp"}) {
		t.Errorf("projects = %v, want [myapp]", got)
	}
}
