package store

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
)

// ContainerStats is a container's latest resource usage, computed the way
// `docker stats` computes it. A field is meaningful only when its Has flag
// is set.
type ContainerStats struct {
	// CPUPercent is the CPU usage over the latest sampling interval, summed
	// across cores: a container using two full cores reports 200.
	CPUPercent float64
	HasCPU     bool
	// MemUsage is the memory usage in bytes, excluding the inactive page
	// cache; MemLimit is the limit the engine reports (host memory when the
	// container has none).
	MemUsage, MemLimit uint64
	HasMem             bool
}

// statsFromSample computes a ContainerStats from one stream sample.
func statsFromSample(s container.StatsResponse) ContainerStats {
	var cs ContainerStats
	cs.CPUPercent, cs.HasCPU = cpuPercent(s)
	cs.MemUsage, cs.MemLimit, cs.HasMem = memUsage(s)
	return cs
}

// cpuPercent returns the CPU usage between s's previous and current
// counters, as `docker stats` computes it: the container's CPU time delta
// over the host's, times the number of online CPUs, times 100. It reports
// no value for the first record of a stream (whose previous counters are
// empty; the CLI shows a meaningless cumulative figure there), when the
// host counter did not advance, when the container counter went backwards,
// and when the CPU count is unknown.
func cpuPercent(s container.StatsResponse) (float64, bool) {
	cur, pre := &s.CPUStats, &s.PreCPUStats
	if pre.SystemUsage == 0 {
		return 0, false
	}
	if cur.SystemUsage <= pre.SystemUsage || cur.CPUUsage.TotalUsage < pre.CPUUsage.TotalUsage {
		return 0, false
	}
	online := uint64(cur.OnlineCPUs)
	if online == 0 {
		online = uint64(len(cur.CPUUsage.PercpuUsage))
	}
	if online == 0 {
		return 0, false
	}
	cpuDelta := float64(cur.CPUUsage.TotalUsage - pre.CPUUsage.TotalUsage)
	sysDelta := float64(cur.SystemUsage - pre.SystemUsage)
	return cpuDelta / sysDelta * float64(online) * 100, true
}

// memUsage returns s's memory usage without the inactive page cache, and
// the memory limit, as `docker stats` computes them: total_inactive_file
// (cgroup v1) or else inactive_file (cgroup v2) is subtracted when it is
// smaller than the raw usage. It reports no value when the engine sent no
// memory figures, for example on a host without the memory controller.
func memUsage(s container.StatsResponse) (usage, limit uint64, ok bool) {
	m := &s.MemoryStats
	if m.Usage == 0 && len(m.Stats) == 0 {
		return 0, 0, false
	}
	usage = m.Usage
	if v, v1 := m.Stats["total_inactive_file"]; v1 && v < usage {
		usage -= v
	} else if v, v2 := m.Stats["inactive_file"]; v2 && v < usage {
		usage -= v
	}
	return usage, m.Limit, true
}

// StatsSub is a live stats subscription made with Store.SubscribeStats. It
// keeps one engine stats stream open per target container, restarting
// streams that end while their container is still targeted, and hands the
// latest values to the subscriber in batches.
//
// SetTargets and Close are safe for concurrent use; C may be read from any
// goroutine.
type StatsSub struct {
	st     *Store
	ctx    context.Context
	cancel context.CancelFunc
	out    chan map[string]ContainerStats // capacity 1; only flush sends

	mu      sync.Mutex
	closed  bool
	targets map[string]*statsTarget
	values  map[string]ContainerStats // latest values of current targets
	dirty   bool                      // values changed since the last batch
	// wake is closed and replaced by SetTargets, waking every stream
	// asleep before a restart.
	wake chan struct{}
}

// statsTarget is the stream goroutine of one targeted container.
type statsTarget struct {
	cancel context.CancelFunc
}

// SubscribeStats returns a subscription that reads no stats until its
// targets are set with SetTargets. Its streams run through Do, so none is
// attempted while the store is not connected, and they end when the
// subscription is closed or when Run's context is cancelled. Close it when
// it is no longer needed.
func (s *Store) SubscribeStats() *StatsSub {
	s.mu.Lock()
	parent := s.baseCtx
	if parent == nil {
		parent = s.life
	}
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	sub := &StatsSub{
		st:      s,
		ctx:     ctx,
		cancel:  cancel,
		out:     make(chan map[string]ContainerStats, 1),
		targets: map[string]*statsTarget{},
		values:  map[string]ContainerStats{},
		wake:    make(chan struct{}),
	}
	if !s.goStats(sub.flush) {
		cancel()
		sub.closed = true
	}
	return sub
}

// goStats runs fn on a new goroutine counted by statsWG, unless Run has
// already returned. It reports whether fn was started.
func (s *Store) goStats(fn func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.statsDone {
		return false
	}
	s.statsWG.Add(1)
	go func() {
		defer s.statsWG.Done()
		fn()
	}()
	return true
}

// C returns the channel on which batches arrive: a map from container ID to
// the latest values of every targeted container that has reported a sample
// since its stream (re)started. A batch is sent only when a value changed
// since the previous one, at most once per flush interval, and replaces a
// batch still waiting to be received, so the receiver never sees a backlog.
// Each batch is the receiver's to keep. The channel is never closed: select
// on it together with your own cancellation. A batch queued before Close
// may still be received after it; receivers that closed the subscription
// should discard it.
func (sub *StatsSub) C() <-chan map[string]ContainerStats { return sub.out }

// SetTargets sets the containers to read stats for, by ID. Streams start
// for new IDs and stop for IDs no longer listed, whose values leave the
// next batch. Empty and invalid IDs are ignored. Every stream waiting to
// restart is woken and restarts at once, with its restart delay reset.
func (sub *StatsSub) SetTargets(ids []string) {
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if docker.ValidateContainerID(id) == nil {
			want[id] = struct{}{}
		}
	}

	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	for id, t := range sub.targets {
		if _, ok := want[id]; ok {
			continue
		}
		t.cancel()
		delete(sub.targets, id)
		if _, ok := sub.values[id]; ok {
			delete(sub.values, id)
			sub.dirty = true
		}
	}
	for id := range want {
		if _, ok := sub.targets[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(sub.ctx)
		t := &statsTarget{cancel: cancel}
		if !sub.st.goStats(func() { sub.sample(ctx, id, t) }) {
			cancel()
			continue
		}
		sub.targets[id] = t
	}
	close(sub.wake)
	sub.wake = make(chan struct{})
}

// Close stops every stream and the batching. It returns without waiting for
// the streams to close, which they do promptly. Later calls do nothing.
func (sub *StatsSub) Close() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	sub.closed = true
	sub.cancel()
	sub.targets = nil
	sub.values = nil
	sub.dirty = false
}

// sample keeps a stats stream open for container id until ctx is
// cancelled, restarting it after a delay whenever it ends.
func (sub *StatsSub) sample(ctx context.Context, id string, t *statsTarget) {
	defer t.cancel() // release the context's resources
	attempt := 0
	for {
		got := false
		err := sub.st.Do(ctx, func(ctx context.Context, svc docker.Service) error {
			stream, err := svc.ContainerStats(ctx, id)
			if err != nil {
				return err
			}
			defer func() { _ = stream.Close() }() // Close never fails usefully here
			for s, err := range stream.Samples(ctx) {
				if err != nil {
					return err
				}
				got = true
				sub.record(id, t, statsFromSample(s))
			}
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		// The stream ended while still targeted. Its values are no longer
		// current, so they leave the next batch until it delivers again.
		sub.forget(id, t)
		// Only a SetTargets from now on cuts the delay short: one made
		// while the stream was healthy must not skip the backoff.
		wake := sub.currentWake()

		var delay time.Duration
		switch {
		case errors.Is(err, ErrNotConnected):
			delay = sub.st.cfg.statsNotConnected
		default:
			if got {
				attempt = 0 // the stream worked; this is a fresh failure
			}
			delay = backoff(sub.st.cfg.statsBackoffBase, sub.st.cfg.statsBackoffMax, attempt)
			attempt++
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
			attempt = 0
		case <-timer.C:
		}
	}
}

func (sub *StatsSub) currentWake() <-chan struct{} {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return sub.wake
}

// record stores v as id's latest value if t is still id's stream.
func (sub *StatsSub) record(id string, t *statsTarget, v ContainerStats) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || sub.targets[id] != t {
		return
	}
	if old, ok := sub.values[id]; ok && old == v {
		return
	}
	sub.values[id] = v
	sub.dirty = true
}

// forget drops id's value if t is still id's stream.
func (sub *StatsSub) forget(id string, t *statsTarget) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || sub.targets[id] != t {
		return
	}
	if _, ok := sub.values[id]; ok {
		delete(sub.values, id)
		sub.dirty = true
	}
}

// flush hands the values to the subscriber every flush interval when they
// changed, until the subscription ends.
func (sub *StatsSub) flush() {
	ticker := time.NewTicker(sub.st.cfg.statsFlush)
	defer ticker.Stop()
	for {
		select {
		case <-sub.ctx.Done():
			return
		case <-ticker.C:
		}
		batch, ok := sub.takeBatch()
		if !ok {
			continue
		}
		// Replace a batch the subscriber has not taken yet. This goroutine
		// is the only sender, so after the drain the send cannot block.
		select {
		case <-sub.out:
		default:
		}
		sub.out <- batch
	}
}

// takeBatch returns a copy of the values if they changed since the last
// batch.
func (sub *StatsSub) takeBatch() (map[string]ContainerStats, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || !sub.dirty {
		return nil, false
	}
	sub.dirty = false
	return maps.Clone(sub.values), true
}
