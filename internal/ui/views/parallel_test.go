package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubParallel returns a parallel action on string items whose act looks
// each item up in outcomes (nil, the default, means success) and records
// the items it was called with.
type stubParallel struct {
	mu       sync.Mutex
	calls    []string
	outcomes map[string]error
	// block, if set, is called by act before returning; tests use it to
	// hold calls open.
	block func(ctx context.Context, item string) error
	// inFlight and peak track concurrent calls.
	inFlight, peak atomic.Int32
}

func (s *stubParallel) action(limit int) parallelAction[string] {
	return parallelAction[string]{
		name: func(item string) string { return item },
		act: func(ctx context.Context, item string) error {
			n := s.inFlight.Add(1)
			defer s.inFlight.Add(-1)
			for {
				p := s.peak.Load()
				if n <= p || s.peak.CompareAndSwap(p, n) {
					break
				}
			}
			s.mu.Lock()
			s.calls = append(s.calls, item)
			s.mu.Unlock()
			if s.block != nil {
				if err := s.block(ctx, item); err != nil {
					return err
				}
			}
			return s.outcomes[item]
		},
		verb:  "stopped",
		limit: limit,
	}
}

func (s *stubParallel) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.calls)
	slices.Sort(out)
	return out
}

func items(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("c%02d", i)
	}
	return out
}

func TestParallelAllSucceed(t *testing.T) {
	for _, n := range []int{0, 1, 3, 20} {
		s := &stubParallel{}
		in := items(n)
		done, err := s.action(0).run(context.Background(), in)
		if err != nil || done != n {
			t.Errorf("run(%d items) = %d, %v; want %d, nil", n, done, err, n)
		}
		if got := s.called(); !slices.Equal(got, in) && n > 0 {
			t.Errorf("run(%d items) called %v", n, got)
		}
	}
}

// TestParallelBounded checks that at most limit calls run at once, and
// that calls do run concurrently up to that limit.
func TestParallelBounded(t *testing.T) {
	for _, limit := range []int{0, 1, 3} {
		want := int32(limit)
		if limit <= 0 {
			want = maxParallelActions
		}
		var arrived atomic.Int32
		release := make(chan struct{})
		s := &stubParallel{block: func(ctx context.Context, _ string) error {
			if arrived.Add(1) == want {
				close(release) // the limit is reached: let them all go
			}
			select {
			case <-release:
			case <-time.After(5 * time.Second):
				return errors.New("never released: fewer calls ran at once than the limit")
			}
			return nil
		}}
		done, err := s.action(limit).run(context.Background(), items(20))
		if err != nil || done != 20 {
			t.Errorf("limit %d: run = %d, %v", limit, done, err)
		}
		if p := s.peak.Load(); p != want {
			t.Errorf("limit %d: %d calls ran at once, want %d", limit, p, want)
		}
	}
}

func TestParallelFailures(t *testing.T) {
	s := &stubParallel{outcomes: map[string]error{
		"c05": errors.New("err five"),
		"c01": errors.New("err one"),
		"c03": errors.New("err three"),
		"c07": errors.New("err seven"),
	}}
	in := items(8)
	slices.Reverse(in) // the report follows name order, not item order
	done, err := s.action(0).run(context.Background(), in)
	if done != 4 {
		t.Errorf("done = %d, want 4", done)
	}
	// Every item is attempted despite the failures.
	if got := s.called(); !slices.Equal(got, items(8)) {
		t.Errorf("called %v", got)
	}
	want := "4 stopped, 4 failed (c01, c03, c05, 1 more): c01: err one"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if !errors.Is(err, s.outcomes["c01"]) {
		t.Error("the first error is not wrapped")
	}
}

func TestParallelOneFailure(t *testing.T) {
	refused := errors.New("refused")
	s := &stubParallel{outcomes: map[string]error{"myapp-db-1": refused}}
	done, err := s.action(0).run(context.Background(), []string{"myapp-web-1", "myapp-db-1"})
	if done != 1 || err == nil || err.Error() != "1 stopped, 1 failed (myapp-db-1): myapp-db-1: refused" {
		t.Errorf("run = %d, %v", done, err)
	}
}

// TestParallelDeterministic runs a failing action many times: the report
// must not depend on which call finished first.
func TestParallelDeterministic(t *testing.T) {
	outcomes := map[string]error{}
	for _, it := range items(16) {
		outcomes[it] = errors.New("err " + it)
	}
	var first string
	for range 50 {
		s := &stubParallel{outcomes: outcomes}
		_, err := s.action(0).run(context.Background(), items(16))
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("report changed between runs:\n  %s\n  %s", first, err)
		}
	}
	if !strings.HasSuffix(first, ": c00: err c00") {
		t.Errorf("report %q does not end with the first error by name", first)
	}
}

func TestParallelCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &stubParallel{}
	done, err := s.action(0).run(ctx, items(3))
	if done != 0 || len(s.called()) != 0 {
		t.Errorf("run = %d with calls %v, want nothing attempted", done, s.called())
	}
	if err == nil || err.Error() != "0 stopped, 3 not attempted: context canceled" || !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
}

// TestParallelCancelledMidway cancels while the first wave is in flight:
// those calls are interrupted and the rest are not attempted.
func TestParallelCancelledMidway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var arrived atomic.Int32
	s := &stubParallel{
		outcomes: map[string]error{"c00": errors.New("boom")},
		block: func(ctx context.Context, item string) error {
			// Every call of the first wave waits for the cancellation, so
			// no slot frees up before it.
			if arrived.Add(1) == 3 {
				cancel()
			}
			<-ctx.Done()
			if item == "c00" {
				return nil // then fails with its own outcome, not ctx's
			}
			return fmt.Errorf("stopping %s: %w", item, ctx.Err())
		},
	}
	done, err := s.action(3).run(ctx, items(10))
	if done != 0 {
		t.Errorf("done = %d", done)
	}
	if n := len(s.called()); n != 3 {
		t.Errorf("%d calls, want the first wave of 3", n)
	}
	want := "0 stopped, 1 failed (c00), 2 interrupted (c01, c02), 7 not attempted: context canceled; c00: boom"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
	if !errors.Is(err, context.Canceled) || !errors.Is(err, s.outcomes["c00"]) {
		t.Error("err does not wrap the cause and the first failure")
	}
}

// manualDeadline is a context whose deadline expires when the test says
// so, making a timeout deterministic: after expire, Done is closed and Err
// returns context.DeadlineExceeded, as for a real deadline.
type manualDeadline struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newManualDeadline() *manualDeadline {
	return &manualDeadline{Context: context.Background(), done: make(chan struct{})}
}

func (c *manualDeadline) expire()               { c.once.Do(func() { close(c.done) }) }
func (c *manualDeadline) Done() <-chan struct{} { return c.done }

func (c *manualDeadline) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// TestParallelTimeout lets the deadline expire while calls hang: they are
// reported as interrupted with the deadline as the cause.
func TestParallelTimeout(t *testing.T) {
	ctx := newManualDeadline()
	var hanging atomic.Int32
	s := &stubParallel{block: func(ctx context.Context, item string) error {
		if item == "c00" {
			return nil // frees its slot for c02
		}
		// The deadline expires once c01 and c02 both hang, so c03 can
		// only find the runner full and the deadline passed.
		if hanging.Add(1) == 2 {
			ctx.(*manualDeadline).expire()
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	done, err := s.action(2).run(ctx, items(4))
	if done != 1 {
		t.Errorf("done = %d, want 1", done)
	}
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a deadline error", err)
	}
	want := "1 stopped, 2 interrupted (c01, c02), 1 not attempted: context deadline exceeded"
	if err.Error() != want {
		t.Errorf("err = %q\nwant %q", err, want)
	}
	if got := s.called(); !slices.Equal(got, items(3)) {
		t.Errorf("called %v, want c00 to c02", got)
	}
}

// TestParallelErrorAfterCancel checks that a call failing for its own
// reason while ctx is done is still reported as failed.
func TestParallelErrorAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := errors.New("no such container")
	s := &stubParallel{block: func(context.Context, string) error {
		cancel()
		return engine
	}}
	_, err := s.action(1).run(ctx, items(2))
	want := "0 stopped, 1 failed (c00), 1 not attempted: context canceled; c00: no such container"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
}

func TestListNames(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"a"}, "a"},
		{[]string{"a", "b", "c"}, "a, b, c"},
		{[]string{"a", "b", "c", "d", "e"}, "a, b, c, 2 more"},
	} {
		in := slices.Clone(tc.in)
		if got := listNames(in); got != tc.want {
			t.Errorf("listNames(%v) = %q, want %q", tc.in, got, tc.want)
		}
		if !slices.Equal(in, tc.in) {
			t.Errorf("listNames modified its input: %v", in)
		}
	}
}

func TestProjectActionTimeout(t *testing.T) {
	if projectActionTimeout != 10*time.Minute {
		t.Errorf("projectActionTimeout = %v, want 10m", projectActionTimeout)
	}
}
