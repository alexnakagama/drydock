package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/volume"

	"github.com/alexnakagama/drydock/internal/docker"
)

// Default timings; see config.
const (
	defaultDebounce      = 150 * time.Millisecond
	defaultMaxWait       = time.Second
	defaultBackoffBase   = time.Second
	defaultBackoffMax    = 10 * time.Second
	defaultStableAfter   = 30 * time.Second
	defaultPingTimeout   = 5 * time.Second
	defaultCallTimeout   = 10 * time.Second
	defaultActionTimeout = 2 * time.Minute
)

// config holds the store's timings. Tests shorten them.
type config struct {
	// debounce is how long the store waits after the last event of a kind
	// before re-listing that kind.
	debounce time.Duration
	// maxWait bounds how long a continuous stream of events can postpone a
	// re-list, so that event storms cannot starve the UI of updates.
	maxWait time.Duration
	// backoffBase and backoffMax shape the reconnection delays: base,
	// 2*base, 4*base, ... capped at max.
	backoffBase, backoffMax time.Duration
	// stableAfter is how long a session must stay connected for the
	// backoff to restart from backoffBase when it fails.
	stableAfter time.Duration
	// pingTimeout bounds each reconnection ping.
	pingTimeout time.Duration
	// callTimeout bounds each list call and the event subscription.
	callTimeout time.Duration
	// actionTimeout bounds each command built with Action.
	actionTimeout time.Duration
}

func defaultConfig() config {
	return config{
		debounce:      defaultDebounce,
		maxWait:       defaultMaxWait,
		backoffBase:   defaultBackoffBase,
		backoffMax:    defaultBackoffMax,
		stableAfter:   defaultStableAfter,
		pingTimeout:   defaultPingTimeout,
		callTimeout:   defaultCallTimeout,
		actionTimeout: defaultActionTimeout,
	}
}

// kind is a resource kind the store lists.
type kind int

const (
	kindContainers kind = iota
	kindImages
	kindVolumes
	numKinds
)

func (k kind) String() string {
	switch k {
	case kindContainers:
		return "containers"
	case kindImages:
		return "images"
	case kindVolumes:
		return "volumes"
	default:
		return "unknown"
	}
}

// Store keeps the engine's containers, images and volumes as immutable
// snapshots and refreshes them from the engine's event stream.
//
// Run owns all engine state: it performs the initial load, re-lists a kind
// (debounced) when events for it arrive, and reconnects with capped backoff
// when the connection is lost. Results reach the UI only as messages
// (SnapshotMsg, ConnectionMsg) passed to the send function, so the UI never
// shares mutable state with the store.
//
// State, Do and Action are safe for concurrent use.
type Store struct {
	svc  docker.Service
	send func(tea.Msg)
	cfg  config

	state   atomic.Int32 // State
	started atomic.Bool

	mu      sync.Mutex
	baseCtx context.Context // Run's context, for actions; nil before Run

	// Owned by the Run goroutine.
	snap Snapshot
}

// New returns a store that reads from svc and delivers messages with send,
// typically (*tea.Program).Send. send is called only from the goroutine
// running Run, in order; it may block, and a blocked send delays further
// refreshes (events are not lost, they are coalesced). It must return once
// the receiver has shut down.
func New(svc docker.Service, send func(tea.Msg)) *Store {
	return newStore(svc, send, defaultConfig())
}

func newStore(svc docker.Service, send func(tea.Msg), cfg config) *Store {
	if svc == nil {
		panic("store: nil docker.Service")
	}
	if send == nil {
		panic("store: nil send function")
	}
	s := &Store{svc: svc, send: send, cfg: cfg}
	s.state.Store(int32(StateConnecting))
	return s
}

// State returns the current connection state.
//
// StateConnected is reported until the store notices the loss, which can
// lag while a refresh is in flight: an action attempted in that window
// reaches the engine and fails with the engine's own error rather than
// ErrNotConnected.
func (s *Store) State() State { return State(s.state.Load()) }

// Do calls fn with the engine service if the store is connected. Otherwise
// it returns ErrNotConnected without calling fn, so no engine call is
// attempted while the engine is known to be unreachable.
//
// The error is returned as is: it may embed the engine endpoint, including
// credentials, so display it only through ErrorText.
func (s *Store) Do(ctx context.Context, fn func(context.Context, docker.Service) error) error {
	if s.State() != StateConnected {
		return ErrNotConnected
	}
	return fn(ctx, s.svc)
}

// Action returns a command that runs fn through Do, bounded by a timeout
// and cancelled when Run returns, and reports the outcome as an
// ActionResultMsg carrying label. The connection state is checked when the
// command runs, not when it is built.
func (s *Store) Action(label string, fn func(context.Context, docker.Service) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(s.actionBase(), s.cfg.actionTimeout)
		defer cancel()
		err := s.Do(ctx, fn)
		return ActionResultMsg{Label: label, Err: s.displayError(err), Reason: s.ErrorText(err)}
	}
}

// ErrorText returns err's text made safe for display (see
// docker.ErrorText), or "" for a nil err.
func (s *Store) ErrorText(err error) string {
	return docker.ErrorText(s.svc.Host(), err)
}

// displayError wraps err so that its Error method returns ErrorText(err),
// keeping errors.Is and errors.As working through Unwrap. Errors placed in
// messages are wrapped, so UI code that prints them cannot leak endpoint
// credentials or inject terminal escapes.
func (s *Store) displayError(err error) error {
	if err == nil {
		return nil
	}
	return &safeError{text: s.ErrorText(err), err: err}
}

type safeError struct {
	text string
	err  error
}

func (e *safeError) Error() string { return e.text }
func (e *safeError) Unwrap() error { return e.err }

func (s *Store) actionBase() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.baseCtx == nil {
		// Before Run, Do refuses every action, so this context is never
		// used for an engine call.
		return context.Background()
	}
	return s.baseCtx
}

// Run loads the initial snapshot, keeps it current from the event stream,
// and recovers from lost connections, until ctx is cancelled. It returns
// nil once ctx is cancelled and every goroutine and stream it started has
// ended. Run may be called only once.
func (s *Store) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errors.New("store: Run called more than once")
	}
	s.mu.Lock()
	s.baseCtx = ctx
	s.mu.Unlock()
	// Refuse actions once Run has returned; the UI is shutting down, so no
	// message is sent.
	defer s.state.Store(int32(StateDisconnected))

	s.publishState(ConnectionMsg{State: StateConnecting})

	attempt := 0
	for {
		connectedFor, err := s.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errSessionEnded) && connectedFor >= s.cfg.stableAfter {
			// The session was healthy for a while before it failed, so
			// retry from the shortest delay. A connection that drops right
			// after connecting keeps backing off instead of reconnecting
			// every backoffBase forever.
			attempt = 0
		}
		delay := backoff(s.cfg.backoffBase, s.cfg.backoffMax, attempt)
		attempt++
		s.publishState(ConnectionMsg{
			State:   StateDisconnected,
			Err:     s.displayError(err),
			Reason:  s.ErrorText(err),
			RetryIn: delay,
		})
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// errSessionEnded wraps the error that ended a session which had connected
// successfully.
var errSessionEnded = errors.New("connection lost")

// session connects (ping, subscribe, full load), publishes the snapshot and
// the connected state, and then serves events until the stream or a
// refresh fails. It returns how long the session was connected (zero if it
// never connected) and why it ended; errors after a successful connection
// wrap errSessionEnded.
func (s *Store) session(ctx context.Context) (time.Duration, error) {
	pctx, cancel := context.WithTimeout(ctx, s.cfg.pingTimeout)
	_, err := s.svc.Ping(pctx)
	cancel()
	if err != nil {
		return 0, err
	}

	// Subscribe before listing, so that no change between the list and the
	// subscription is missed.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	stream, err := s.subscribe(streamCtx)
	if err != nil {
		return 0, err
	}

	snap, err := s.loadAll(ctx)
	if err != nil {
		return 0, err
	}
	s.publishSnapshot(snap)
	s.publishState(ConnectionMsg{State: StateConnected})
	connectedAt := time.Now()

	if err := s.serve(ctx, stream); err != nil {
		return time.Since(connectedAt), fmt.Errorf("%w: %w", errSessionEnded, err)
	}
	return time.Since(connectedAt), nil
}

// subscribe opens the event stream on ctx. Service.Events blocks until the
// engine accepts the subscription, so it runs in a goroutine and is
// abandoned (by cancelling ctx) if the engine does not answer within
// callTimeout.
func (s *Store) subscribe(ctx context.Context) (docker.EventStream, error) {
	subCtx, cancel := context.WithCancel(ctx)
	done := make(chan docker.EventStream, 1)
	go func() { done <- s.svc.Events(subCtx) }()

	t := time.NewTimer(s.cfg.callTimeout)
	defer t.Stop()
	var stream docker.EventStream
	select {
	case stream = <-done:
	case <-t.C:
		cancel()
		<-done // Events returns promptly once its context is cancelled.
		return docker.EventStream{}, fmt.Errorf("subscribe to engine events: %w", context.DeadlineExceeded)
	case <-ctx.Done():
		cancel()
		<-done
		return docker.EventStream{}, ctx.Err()
	}
	// subCtx must outlive this function, for the stream's lifetime. It is
	// cancelled with its parent when the session ends; releasing cancel
	// then frees its resources (and satisfies the lostcancel check).
	context.AfterFunc(ctx, cancel)

	// A subscription the engine refused reports its error right away.
	select {
	case err, ok := <-stream.Err:
		if !ok || err == nil {
			err = errors.New("event stream closed")
		}
		return docker.EventStream{}, fmt.Errorf("subscribe to engine events: %w", err)
	default:
	}
	return stream, nil
}

// loadAll lists every kind and builds a fresh snapshot.
func (s *Store) loadAll(ctx context.Context) (Snapshot, error) {
	next := Snapshot{}
	for k := range numKinds {
		var err error
		if next, err = s.reload(ctx, next, k); err != nil {
			return Snapshot{}, err
		}
	}
	return next, nil
}

// reload returns a copy of base with kind k re-listed from the engine (and
// the usage indexes recomputed if k is containers). base is not modified.
func (s *Store) reload(ctx context.Context, base Snapshot, k kind) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.callTimeout)
	defer cancel()
	var err error
	switch k {
	case kindContainers:
		var cs []container.Summary
		if cs, err = s.svc.ContainerList(ctx, docker.ContainerListOptions{All: true}); err == nil {
			base.containers = cs
			base.imageUsers, base.volumeUsers = buildIndexes(cs)
		}
	case kindImages:
		var imgs []image.Summary
		if imgs, err = s.svc.ImageList(ctx); err == nil {
			base.images = imgs
		}
	case kindVolumes:
		var vols []volume.Volume
		if vols, err = s.svc.VolumeList(ctx); err == nil {
			base.volumes = vols
		}
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("list %s: %w", k, err)
	}
	return base, nil
}

// serve handles events until ctx is cancelled (returning nil) or the stream
// or a refresh fails (returning the error). Events of each kind are
// debounced: a kind is re-listed once its events have been quiet for
// debounce, or maxWait after the first pending event, whichever is sooner.
func (s *Store) serve(ctx context.Context, stream docker.EventStream) error {
	var (
		timers  [numKinds]*time.Timer
		fire    [numKinds]<-chan time.Time // nil while nothing is pending
		firstAt [numKinds]time.Time
	)
	for k := range numKinds {
		timers[k] = time.NewTimer(time.Hour)
		timers[k].Stop()
	}
	defer func() {
		for _, t := range timers {
			t.Stop()
		}
	}()

	schedule := func(k kind) {
		now := time.Now()
		if fire[k] == nil {
			firstAt[k] = now
		}
		delay := min(s.cfg.debounce, firstAt[k].Add(s.cfg.maxWait).Sub(now))
		timers[k].Reset(max(delay, 0))
		fire[k] = timers[k].C
	}
	refresh := func(k kind) error {
		fire[k] = nil
		next, err := s.reload(ctx, s.snap, k)
		if err != nil {
			return err
		}
		s.publishSnapshot(next)
		return nil
	}

	msgs, errc := stream.Messages, stream.Err
	for {
		var err error
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-errc:
			if !ok || e == nil {
				e = errors.New("event stream closed")
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("event stream: %w", e)
		case m := <-msgs:
			if k, ok := eventKind(m); ok {
				schedule(k)
			}
		case <-fire[kindContainers]:
			err = refresh(kindContainers)
		case <-fire[kindImages]:
			err = refresh(kindImages)
		case <-fire[kindVolumes]:
			err = refresh(kindVolumes)
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// eventKind maps an engine event to the kind it invalidates. Events that
// cannot change any listed field are ignored.
func eventKind(m events.Message) (kind, bool) {
	switch m.Type {
	case events.ContainerEventType:
		a := string(m.Action)
		if strings.HasPrefix(a, "exec_") {
			return 0, false
		}
		switch events.Action(a) {
		case events.ActionAttach, events.ActionDetach, events.ActionResize, events.ActionTop,
			events.ActionExport, events.ActionCopy, events.ActionArchivePath, events.ActionExtractToDir:
			return 0, false
		}
		return kindContainers, true
	case events.ImageEventType:
		return kindImages, true
	case events.VolumeEventType:
		switch m.Action {
		case events.ActionMount, events.ActionUnmount:
			// Usage comes from the container list, which container events
			// already refresh.
			return 0, false
		}
		return kindVolumes, true
	default:
		return 0, false
	}
}

// publishSnapshot stamps snap with the next sequence number, records it as
// current and sends it.
func (s *Store) publishSnapshot(snap Snapshot) {
	snap.seq = s.snap.seq + 1
	s.snap = snap
	s.send(SnapshotMsg{Snapshot: snap})
}

func (s *Store) publishState(msg ConnectionMsg) {
	s.state.Store(int32(msg.State))
	s.send(msg)
}

// backoff returns the delay before reconnection attempt n (from 0): base
// doubled n times, capped at limit.
func backoff(base, limit time.Duration, n int) time.Duration {
	if base <= 0 || base >= limit {
		return limit
	}
	d := base
	for range n {
		if d >= limit/2 {
			return limit
		}
		d *= 2
	}
	return d
}
