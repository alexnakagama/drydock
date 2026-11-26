package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

var errStubGone = errors.New("stub item gone")

// stubRemoval returns a removal of string items whose remove looks each item
// up in outcomes (nil, the default, means removed) and records the items it
// was called with in calls. An outcome of context.Canceled cancels ctx
// first, as a deadline expiring mid-removal would.
func stubRemoval(cancel context.CancelFunc, outcomes map[string]error, calls *[]string) removal[string] {
	return removal[string]{
		name: func(s string) string { return s },
		remove: func(ctx context.Context, s string) error {
			*calls = append(*calls, s)
			err := outcomes[s]
			if errors.Is(err, context.Canceled) {
				cancel()
				return ctx.Err()
			}
			return err
		},
		gone: errStubGone,
		noun: [2]string{"thing", "things"},
	}
}

func TestRemovalAllRemoved(t *testing.T) {
	for _, tc := range []struct {
		items []string
		want  string
	}{
		{[]string{"a"}, "1 thing removed"},
		{[]string{"a", "b", "c"}, "3 things removed"},
		{nil, "0 things removed"},
	} {
		var calls []string
		got, err := stubRemoval(nil, nil, &calls).run(context.Background(), tc.items)
		if err != nil || got != tc.want {
			t.Errorf("run(%v) = %q, %v; want %q", tc.items, got, err, tc.want)
		}
		if !slices.Equal(calls, tc.items) {
			t.Errorf("run(%v) removed %v", tc.items, calls)
		}
	}
}

func TestRemovalAlreadyGone(t *testing.T) {
	var calls []string
	outcomes := map[string]error{"b": fmt.Errorf("wrapped: %w", errStubGone)}
	got, err := stubRemoval(nil, outcomes, &calls).run(context.Background(), []string{"a", "b"})
	if err != nil || got != "1 thing removed, 1 already gone" {
		t.Errorf("run = %q, %v; want a gone item counted as such, not as a failure", got, err)
	}

	calls = nil
	got, err = stubRemoval(nil, outcomes, &calls).run(context.Background(), []string{"b"})
	if err != nil || got != "0 things removed, 1 already gone" || !slices.Equal(calls, []string{"b"}) {
		t.Errorf("run = %q, %v (calls %v)", got, err, calls)
	}
}

func TestRemovalNilGoneSentinel(t *testing.T) {
	var calls []string
	r := stubRemoval(nil, map[string]error{"a": errStubGone}, &calls)
	r.gone = nil
	_, err := r.run(context.Background(), []string{"a"})
	if want := "0 removed, 1 failed (a): a: stub item gone"; err == nil || err.Error() != want {
		t.Errorf("error %v, want %q: with no sentinel nothing counts as gone", err, want)
	}
}

func TestRemovalGoneWhileCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []string
	r := stubRemoval(cancel, nil, &calls)
	r.remove = func(context.Context, string) error {
		// The item turns out gone, but ctx ends during the call.
		cancel()
		return errStubGone
	}
	_, err := r.run(ctx, []string{"a", "b"})
	if want := "0 removed, 1 already gone, 1 not attempted: context canceled"; err == nil || err.Error() != want {
		t.Errorf("error %v, want %q", err, want)
	}
}

func TestRemovalInterruptedCause(t *testing.T) {
	errDeadline := errors.New("prune took too long")
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	r := removal[string]{
		name: func(s string) string { return s },
		remove: func(ctx context.Context, _ string) error {
			cancel(errDeadline)
			return ctx.Err()
		},
		gone: errStubGone,
		noun: [2]string{"thing", "things"},
	}
	_, err := r.run(ctx, []string{"a", "b"})
	want := "0 removed, 1 interrupted (a), 1 not attempted: prune took too long"
	if err == nil || err.Error() != want || !errors.Is(err, errDeadline) {
		t.Errorf("error %v, want %q wrapping the context's cause", err, want)
	}
}

func TestRemovalInterruptedEmptyName(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []string
	r := stubRemoval(cancel, map[string]error{"a": context.Canceled}, &calls)
	r.name = func(string) string { return "" }
	_, err := r.run(ctx, []string{"a"})
	if want := "0 removed, 1 interrupted (): context canceled"; err == nil || err.Error() != want {
		t.Errorf("error %v, want %q: an unnamed interrupted item is still reported", err, want)
	}
}

func TestRemovalFailures(t *testing.T) {
	errBusy := errors.New("in use")
	outcomes := map[string]error{}
	for _, s := range []string{"f1", "f2", "f3", "f4", "f5"} {
		outcomes[s] = fmt.Errorf("remove %s: %w", s, errBusy)
	}
	outcomes["g"] = errStubGone
	var calls []string
	items := []string{"ok", "f1", "f2", "g", "f3", "f4", "f5"}
	got, err := stubRemoval(nil, outcomes, &calls).run(context.Background(), items)
	want := "1 removed, 1 already gone, 5 failed (f1, f2, f3, 2 more): f1: remove f1: in use"
	if got != "" || err == nil || err.Error() != want {
		t.Errorf("run = %q, %v; want error %q", got, err, want)
	}
	if !errors.Is(err, errBusy) {
		t.Errorf("error %v does not wrap the first failure", err)
	}
	if !slices.Equal(calls, items) {
		t.Errorf("removed %v, want every item tried despite failures", calls)
	}

	// Exactly maxListedFailures failures are all named.
	calls = nil
	got, err = stubRemoval(nil, outcomes, &calls).run(context.Background(), []string{"f1", "f2", "f3"})
	want = "0 removed, 3 failed (f1, f2, f3): f1: remove f1: in use"
	if err == nil || err.Error() != want {
		t.Errorf("run = %q, %v; want error %q", got, err, want)
	}
}

func TestRemovalInterrupted(t *testing.T) {
	errBusy := errors.New("in use")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes := map[string]error{"a": errBusy, "c": context.Canceled}
	var calls []string
	got, err := stubRemoval(cancel, outcomes, &calls).run(ctx, []string{"a", "b", "c", "d", "e"})
	// a fails, b is removed, c is cut off and d, e are never tried: neither
	// counts as failed.
	want := "1 removed, 1 failed (a), 1 interrupted (c), 2 not attempted: context canceled; a: in use"
	if got != "" || err == nil || err.Error() != want {
		t.Errorf("run = %q, %v; want error %q", got, err, want)
	}
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errBusy) {
		t.Errorf("error %v should wrap both the cancellation and the first failure", err)
	}
	if !slices.Equal(calls, []string{"a", "b", "c"}) {
		t.Errorf("removed %v, want nothing tried after the interruption", calls)
	}

	// Without an earlier failure, only the cancellation is reported.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls = nil
	_, err = stubRemoval(cancel, map[string]error{"a": context.Canceled}, &calls).run(ctx, []string{"a"})
	if err == nil || err.Error() != "0 removed, 1 interrupted (a): context canceled" {
		t.Errorf("error %v", err)
	}
}

func TestRemovalNotAttemptedAfterCancellation(t *testing.T) {
	errDeadline := errors.New("prune took too long")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errDeadline)
	var calls []string
	_, err := stubRemoval(nil, nil, &calls).run(ctx, []string{"a", "b"})
	want := "0 removed, 2 not attempted: prune took too long"
	if err == nil || err.Error() != want {
		t.Errorf("error %v, want %q", err, want)
	}
	if !errors.Is(err, errDeadline) {
		t.Errorf("error %v does not wrap the context's cause", err)
	}
	if len(calls) != 0 {
		t.Errorf("removed %v after cancellation", calls)
	}
}
