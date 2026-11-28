package store

import (
	"context"
	"errors"
	"iter"
	"math"
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

const mib = 1 << 20

// --- 2.1 formulas ---------------------------------------------------------

// cpuSample returns a sample whose CPU counters advanced by cpuDelta and
// sysDelta since a previous sample with non-zero counters.
func cpuSample(cpuDelta, sysDelta int64, online uint32) container.StatsResponse {
	const preTotal, preSys = 1_000_000, 50_000_000
	var s container.StatsResponse
	s.PreCPUStats.CPUUsage.TotalUsage = preTotal
	s.PreCPUStats.SystemUsage = preSys
	s.CPUStats.CPUUsage.TotalUsage = uint64(preTotal + cpuDelta)
	s.CPUStats.SystemUsage = uint64(preSys + sysDelta)
	s.CPUStats.OnlineCPUs = online
	return s
}

func TestCPUPercent(t *testing.T) {
	t.Parallel()
	// Over an interval, the host counter advances by interval*cores, so a
	// container using one full core of four advances by a quarter of it.
	const interval = 1_000_000_000
	first := container.StatsResponse{}
	first.CPUStats.CPUUsage.TotalUsage = 5_000
	first.CPUStats.SystemUsage = 9_000_000
	first.CPUStats.OnlineCPUs = 4
	percpu := cpuSample(interval, 4*interval, 0)
	percpu.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 2, 3, 4}
	noCPUs := cpuSample(interval, 4*interval, 0)

	// OnlineCPUs wins over the per-CPU list when both are reported.
	both := cpuSample(interval, 4*interval, 4)
	both.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 2}
	// The previous sample's CPU count is not the one that counts.
	preOnly := cpuSample(interval, 4*interval, 0)
	preOnly.PreCPUStats.OnlineCPUs = 4

	tests := []struct {
		name     string
		sample   container.StatsResponse
		want     float64
		wantCPUs int
		wantOK   bool
	}{
		{"one core of four", cpuSample(interval, 4*interval, 4), 100, 4, true},
		{"two cores of eight", cpuSample(2*interval, 8*interval, 8), 200, 8, true},
		{"single core", cpuSample(interval, interval, 1), 100, 1, true},
		{"idle", cpuSample(0, 4*interval, 4), 0, 4, true},
		{"online preferred over percpu", both, 100, 4, true},
		{"first stream record", first, 0, 0, false},
		{"no host delta", cpuSample(interval, 0, 4), 0, 0, false},
		{"negative cpu delta", cpuSample(-1, 4*interval, 4), 0, 0, false},
		{"online 0 falls back to percpu", percpu, 100, 4, true},
		{"no cpu count", noCPUs, 0, 0, false},
		{"only previous cpu count", preOnly, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, cpus, ok := cpuPercent(tt.sample)
			if ok != tt.wantOK || cpus != tt.wantCPUs || math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cpuPercent = %v, %d, %v; want %v, %d, %v", got, cpus, ok, tt.want, tt.wantCPUs, tt.wantOK)
			}
			// statsFromSample carries the same values, and a CPU count
			// only together with a CPU value.
			cs := statsFromSample(tt.sample)
			if cs.HasCPU != tt.wantOK || cs.CPUs != tt.wantCPUs || cs.CPUPercent != got {
				t.Errorf("statsFromSample CPU = %v, %d, %v; want %v, %d, %v",
					cs.CPUPercent, cs.CPUs, cs.HasCPU, got, tt.wantCPUs, tt.wantOK)
			}
			if !cs.HasCPU && cs.CPUs != 0 {
				t.Errorf("CPUs = %d without HasCPU", cs.CPUs)
			}
			if cs.HasCPU && cs.CPUs <= 0 {
				t.Errorf("CPUs = %d with HasCPU", cs.CPUs)
			}
		})
	}
}

func maxCPUsForIntSize() int {
	if strconv.IntSize == 32 {
		return 0
	}
	n := uint64(math.MaxUint32) // a variable, so 32-bit builds still compile
	return int(n)
}

func TestOnlineCPUs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		online uint32
		percpu []uint64
		want   int
	}{
		{"online set", 8, nil, 8},
		{"online set with percpu", 8, []uint64{1, 2}, 8},
		{"percpu fallback", 0, []uint64{1, 2, 3}, 3},
		{"neither", 0, nil, 0},
		{"empty percpu", 0, []uint64{}, 0},
		// The largest count fits an int on 64-bit platforms; on 32-bit ones
		// it does not, and reads as unknown rather than wrapping negative.
		{"largest count", math.MaxUint32, nil, maxCPUsForIntSize()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var c container.CPUStats
			c.OnlineCPUs = tt.online
			c.CPUUsage.PercpuUsage = tt.percpu
			if got := onlineCPUs(&c); got != tt.want {
				t.Errorf("onlineCPUs = %d, want %d", got, tt.want)
			}
		})
	}
}

func memSample(usage, limit uint64, stats map[string]uint64) container.StatsResponse {
	var s container.StatsResponse
	s.MemoryStats.Usage = usage
	s.MemoryStats.Limit = limit
	s.MemoryStats.Stats = stats
	return s
}

func TestMemUsage(t *testing.T) {
	t.Parallel()
	const host = 64 << 30
	tests := []struct {
		name         string
		sample       container.StatsResponse
		usage, limit uint64
		wantOK       bool
	}{
		{"cgroup v2 inactive_file", memSample(900*mib, 2<<30, map[string]uint64{"inactive_file": 400 * mib}), 500 * mib, 2 << 30, true},
		{"cgroup v1 total_inactive_file", memSample(900*mib, 2<<30, map[string]uint64{"total_inactive_file": 400 * mib}), 500 * mib, 2 << 30, true},
		{"v1 key preferred", memSample(900*mib, 2<<30, map[string]uint64{"total_inactive_file": 400 * mib, "inactive_file": 100 * mib}), 500 * mib, 2 << 30, true},
		{"inactive equal to usage", memSample(400*mib, 2<<30, map[string]uint64{"inactive_file": 400 * mib}), 400 * mib, 2 << 30, true},
		{"inactive above usage", memSample(400*mib, 2<<30, map[string]uint64{"inactive_file": 500 * mib}), 400 * mib, 2 << 30, true},
		{"host-sized limit", memSample(100*mib, host, map[string]uint64{"inactive_file": 0}), 100 * mib, host, true},
		{"no inactive stat", memSample(100*mib, 2<<30, nil), 100 * mib, 2 << 30, true},
		{"empty stats", memSample(0, 0, nil), 0, 0, false},
		{"empty stats with limit", memSample(0, host, nil), 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, l, ok := memUsage(tt.sample)
			if u != tt.usage || l != tt.limit || ok != tt.wantOK {
				t.Errorf("memUsage = %d, %d, %v; want %d, %d, %v", u, l, ok, tt.usage, tt.limit, tt.wantOK)
			}
		})
	}
}

// --- 2.2 sampler ----------------------------------------------------------

// statsTestConfig is testConfig with the stats timings scaled down by the
// same factor (100) as its reconnection backoff base.
func statsTestConfig() config {
	cfg := testConfig()
	cfg.statsFlush = defaultStatsFlush / 100
	cfg.statsNotConnected = defaultStatsNotConnected / 100
	cfg.statsBackoffBase = defaultStatsBackoffBase / 100
	cfg.statsBackoffMax = defaultStatsBackoffMax / 100
	return cfg
}

// sample returns a stream sample with a CPU value and memUsage MiB of
// memory.
func sample(memUsage uint64) container.StatsResponse {
	s := cpuSample(1_000, 4_000, 4)
	s.MemoryStats.Usage = memUsage * mib
	s.MemoryStats.Limit = 2 << 30
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// batch receives batches until one satisfies pred.
func batch(t *testing.T, sub *StatsSub, what string, pred func(map[string]ContainerStats) bool) map[string]ContainerStats {
	t.Helper()
	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	for {
		select {
		case b := <-sub.C():
			if pred(b) {
				return b
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for batch: %s", what)
			return nil
		}
	}
}

func has(ids ...string) func(map[string]ContainerStats) bool {
	return func(b map[string]ContainerStats) bool {
		for _, id := range ids {
			if v, ok := b[id]; !ok || !v.HasMem {
				return false
			}
		}
		return true
	}
}

// statsFixture starts a connected store on a fake with running containers
// a and b and a paused container p.
func statsFixture(t *testing.T, cfg config) (*dockerfake.Service, *Store, *recorder) {
	t.Helper()
	f := dockerfake.New()
	f.AddContainer(ctr("a", "web", "", container.StateRunning))
	f.AddContainer(ctr("b", "db", "", container.StateRunning))
	f.AddContainer(ctr("p", "paused", "", container.StatePaused))
	st, rec := start(t, f, cfg)
	rec.state(t, StateConnected)
	return f, st, rec
}

func subscribe(t *testing.T, st *Store) *StatsSub {
	t.Helper()
	sub := st.SubscribeStats()
	t.Cleanup(sub.Close)
	return sub
}

func streams(f *dockerfake.Service, n int) func() bool {
	return func() bool { return f.StatsStreams() == n }
}

func TestStatsTargetsAndBatches(t *testing.T) {
	t.Parallel()
	f, st, _ := statsFixture(t, statsTestConfig())
	sub := subscribe(t, st)

	sub.SetTargets([]string{"a", "b"})
	eventually(t, "2 streams", streams(f, 2))
	f.SetStats("a", sample(10))
	f.SetStats("b", sample(20))
	b := batch(t, sub, "a and b", has("a", "b"))
	if b["a"].MemUsage != 10*mib || b["b"].MemUsage != 20*mib || !b["a"].HasCPU {
		t.Errorf("batch = %+v", b)
	}

	// Dropping b closes its stream and drops it from the next batch.
	sub.SetTargets([]string{"a"})
	eventually(t, "1 stream", streams(f, 1))
	b = batch(t, sub, "without b", func(b map[string]ContainerStats) bool { _, ok := b["b"]; return !ok })
	if _, ok := b["a"]; !ok {
		t.Errorf("batch lost a: %+v", b)
	}

	// Nothing changed, so no batch is sent; the same values again are no
	// change either.
	f.SetStats("a", sample(10))
	time.Sleep(10 * st.cfg.statsFlush)
	select {
	case b := <-sub.C():
		t.Errorf("unexpected batch %+v", b)
	default:
	}

	// A changed value is sent.
	f.SetStats("a", sample(11))
	batch(t, sub, "a changed", func(b map[string]ContainerStats) bool { return b["a"].MemUsage == 11*mib })
}

func TestStatsIgnoresInvalidAndDuplicateTargets(t *testing.T) {
	t.Parallel()
	f, st, _ := statsFixture(t, statsTestConfig())
	sub := subscribe(t, st)
	sub.SetTargets([]string{"a", "a", "", "../x", "b"})
	eventually(t, "2 streams", streams(f, 2))
	time.Sleep(5 * st.cfg.statsFlush)
	if n := len(f.CallsTo(dockerfake.MethodContainerStats)); n != 2 {
		t.Errorf("ContainerStats calls = %d, want 2", n)
	}
}

func TestStatsRestartsFailedStream(t *testing.T) {
	t.Parallel()
	f, st, _ := statsFixture(t, statsTestConfig())
	sub := subscribe(t, st)
	f.FailNext(dockerfake.MethodContainerStats, errors.New("boom"))
	sub.SetTargets([]string{"a"})
	eventually(t, "reopened stream", streams(f, 1))
	if n := len(f.CallsTo(dockerfake.MethodContainerStats)); n < 2 {
		t.Errorf("ContainerStats calls = %d, want a retry", n)
	}
	f.SetStats("a", sample(1))
	batch(t, sub, "a", has("a"))
}

func TestStatsStoppedContainerLeavesBatch(t *testing.T) {
	t.Parallel()
	f, st, _ := statsFixture(t, statsTestConfig())
	sub := subscribe(t, st)
	sub.SetTargets([]string{"a"})
	eventually(t, "stream", streams(f, 1))
	f.SetStats("a", sample(1))
	batch(t, sub, "a", has("a"))

	f.AddContainer(ctr("a", "web", "", container.StateExited))
	batch(t, sub, "a dropped", func(b map[string]ContainerStats) bool { _, ok := b["a"]; return !ok })
	eventually(t, "no stream", streams(f, 0))
}

func TestStatsRecoverAfterReconnect(t *testing.T) {
	t.Parallel()
	f, st, rec := statsFixture(t, statsTestConfig())
	sub := subscribe(t, st)
	sub.SetTargets([]string{"a", "b"})
	eventually(t, "2 streams", streams(f, 2))
	f.SetStats("a", sample(1))
	f.SetStats("b", sample(2))
	batch(t, sub, "a and b", has("a", "b"))

	f.Disconnect(nil)
	rec.state(t, StateDisconnected)
	batch(t, sub, "values dropped", func(b map[string]ContainerStats) bool { return len(b) == 0 })
	start := time.Now()
	f.Reconnect()
	rec.snapshot(t, "after reconnect", nil)
	// The view sets its targets again from that snapshot.
	sub.SetTargets([]string{"a", "b"})
	eventually(t, "2 streams again", streams(f, 2))
	f.SetStats("a", sample(3))
	f.SetStats("b", sample(4))
	batch(t, sub, "a and b again", func(b map[string]ContainerStats) bool {
		return b["a"].MemUsage == 3*mib && b["b"].MemUsage == 4*mib
	})
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("recovery took %v, want under 3s", d)
	}
}

// After an outage, a stream refused with ErrNotConnected retries after the
// short notConnected delay, without waiting for a SetTargets, while other
// failures back off.
func TestStatsNotConnectedRetriesQuickly(t *testing.T) {
	t.Parallel()
	cfg := statsTestConfig()
	cfg.statsNotConnected = 5 * time.Millisecond
	cfg.statsBackoffBase, cfg.statsBackoffMax = 5*time.Second, 5*time.Second
	f, st, rec := statsFixture(t, cfg)
	sub := subscribe(t, st)

	f.Disconnect(nil)
	rec.state(t, StateDisconnected)
	sub.SetTargets([]string{"a"})
	time.Sleep(20 * time.Millisecond) // refused by Do at least once
	if n := len(f.CallsTo(dockerfake.MethodContainerStats)); n != 0 {
		t.Fatalf("ContainerStats called %d times while disconnected", n)
	}
	f.Reconnect()
	rec.state(t, StateConnected)
	deadline := time.Now().Add(time.Second)
	for f.StatsStreams() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("stream not reopened within 1s of reconnecting")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Any other failure backs off for the base delay.
	f.FailNext(dockerfake.MethodContainerStats, errors.New("boom"))
	sub.SetTargets([]string{"a", "b"})
	eventually(t, "b's failed attempt", func() bool {
		return len(f.CallsTo(dockerfake.MethodContainerStats)) == 2
	})
	time.Sleep(300 * time.Millisecond)
	if n := len(f.CallsTo(dockerfake.MethodContainerStats)); n != 2 {
		t.Errorf("ContainerStats calls = %d, want no retry within the backoff", n)
	}
}

func TestStatsSetTargetsWakesBackoff(t *testing.T) {
	t.Parallel()
	cfg := statsTestConfig()
	cfg.statsBackoffBase, cfg.statsBackoffMax = 5*time.Second, 5*time.Second
	f, st, _ := statsFixture(t, cfg)
	sub := subscribe(t, st)
	f.FailNext(dockerfake.MethodContainerStats, errors.New("boom"))
	sub.SetTargets([]string{"a"})
	eventually(t, "failed attempt", func() bool { return len(f.CallsTo(dockerfake.MethodContainerStats)) == 1 })
	time.Sleep(20 * time.Millisecond) // let it fall asleep
	if n := f.StatsStreams(); n != 0 {
		t.Fatalf("StatsStreams = %d before the wake", n)
	}

	woken := time.Now()
	sub.SetTargets([]string{"a"})
	eventually(t, "restarted stream", streams(f, 1))
	if d := time.Since(woken); d > time.Second {
		t.Errorf("restart took %v, want immediate", d)
	}
}

func TestStatsCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	f, st, _ := statsFixture(t, statsTestConfig())
	sub := st.SubscribeStats()
	sub.SetTargets([]string{"a", "b"})
	eventually(t, "2 streams", streams(f, 2))
	sub.Close()
	eventually(t, "0 streams", streams(f, 0))
	sub.Close()
	sub.SetTargets([]string{"a"})
	time.Sleep(5 * st.cfg.statsFlush)
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("StatsStreams after Close = %d", n)
	}
}

func TestStatsEndWithRun(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	f.AddContainer(ctr("a", "web", "", container.StateRunning))
	rec := newRecorder()
	st := newStore(f, rec.send, statsTestConfig())
	early := st.SubscribeStats() // before Run: derives from Run's lifetime
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx) }()
	rec.state(t, StateConnected)

	sub := st.SubscribeStats()
	sub.SetTargets([]string{"a"})
	early.SetTargets([]string{"a"})
	eventually(t, "2 streams", streams(f, 2))

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("Run did not return")
	}
	// Run waits for the subscriptions' goroutines, so the streams are closed.
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("StatsStreams after Run = %d, want 0", n)
	}
	// A subscription made after Run returned starts nothing.
	late := st.SubscribeStats()
	late.SetTargets([]string{"a"})
	late.Close()
	sub.Close()
	early.Close()
	if n := f.StatsStreams(); n != 0 {
		t.Errorf("StatsStreams = %d, want 0", n)
	}
}

func TestNewOptions(t *testing.T) {
	t.Parallel()
	send := func(tea.Msg) {}
	st := New(dockerfake.New(), send, nil, WithStatsTimings(1, 2, 3, 4))
	if c := st.cfg; c.statsFlush != 1 || c.statsNotConnected != 2 || c.statsBackoffBase != 3 || c.statsBackoffMax != 4 {
		t.Errorf("config = %+v", c)
	}
	st = New(dockerfake.New(), send, WithStatsTimings(0, -1, 0, 0))
	if c, d := st.cfg, defaultConfig(); c.statsFlush != d.statsFlush || c.statsNotConnected != d.statsNotConnected ||
		c.statsBackoffBase != d.statsBackoffBase || c.statsBackoffMax != d.statsBackoffMax {
		t.Errorf("unset timings = %+v, want defaults", c)
	}
	if st.cfg.debounce != defaultDebounce {
		t.Errorf("debounce = %v, want default", st.cfg.debounce)
	}
}

// --- 2.3 benchmark --------------------------------------------------------

// benchService is a fake whose stats streams each yield a new sample every
// time tick is called, without the fake's per-sample bookkeeping, so the
// benchmark measures the sampler.
type benchService struct {
	*dockerfake.Service
	gen chan chan struct{} // holds the channel closed by the next tick
}

func newBenchService() *benchService {
	b := &benchService{Service: dockerfake.New(), gen: make(chan chan struct{}, 1)}
	b.gen <- make(chan struct{})
	return b
}

func (b *benchService) current() chan struct{} {
	c := <-b.gen
	b.gen <- c
	return c
}

func (b *benchService) tick() {
	c := <-b.gen
	close(c)
	b.gen <- make(chan struct{})
}

func (b *benchService) ContainerStats(ctx context.Context, id string) (docker.StatsStream, error) {
	return &benchStream{svc: b}, nil
}

type benchStream struct{ svc *benchService }

func (s *benchStream) Close() error { return nil }

func (s *benchStream) Samples(ctx context.Context) iter.Seq2[container.StatsResponse, error] {
	return func(yield func(container.StatsResponse, error) bool) {
		smp := sample(1)
		for n := uint64(0); ; n++ {
			c := s.svc.current()
			smp.MemoryStats.Usage = n * mib
			if !yield(smp, nil) {
				return
			}
			select {
			case <-c:
			case <-ctx.Done():
				yield(container.StatsResponse{}, ctx.Err())
				return
			}
		}
	}
}

// allAtLeast reports whether every value uses at least mem bytes, without
// allocating, so the benchmark counts only the sampler's allocations.
func allAtLeast(b map[string]ContainerStats, mem uint64) bool {
	for _, v := range b {
		if v.MemUsage < mem {
			return false
		}
	}
	return true
}

func BenchmarkStatsSampler200(b *testing.B) {
	const targets = 200
	svc := newBenchService()
	cfg := statsTestConfig()
	cfg.statsFlush = time.Millisecond
	st := newStore(svc, func(tea.Msg) {}, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			b.Errorf("Run = %v", err)
		}
	}()
	deadline := time.Now().Add(waitTimeout)
	for st.State() != StateConnected {
		if time.Now().After(deadline) {
			b.Fatal("store did not connect")
		}
		time.Sleep(time.Millisecond)
	}

	ids := make([]string, targets)
	for i := range ids {
		ids[i] = "c" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	sub := st.SubscribeStats()
	defer sub.Close()
	sub.SetTargets(ids)
	// full waits for a batch in which every target reports at least mem
	// MiB, that is a sample from the latest tick.
	full := func(mem uint64) {
		timeout := time.NewTimer(waitTimeout)
		defer timeout.Stop()
		for {
			select {
			case got := <-sub.C():
				if len(got) == targets && allAtLeast(got, mem*mib) {
					return
				}
			case <-timeout.C:
				b.Fatalf("no batch with all %d targets at %d MiB", targets, mem)
			}
		}
	}
	full(0)

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		svc.tick()          // every target yields its next sample
		full(uint64(i) + 1) // flushes until all of them are in a batch
	}
	b.StopTimer()
}
