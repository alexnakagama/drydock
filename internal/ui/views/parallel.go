package views

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxParallelActions bounds how many engine calls a project action runs at
// once.
const maxParallelActions = 8

// projectActionTimeout bounds a project action. A stop or restart waits up
// to each container's own stop timeout, which Compose users often raise
// with stop_grace_period, so a project of more than maxParallelActions such
// containers needs several waves; the default action timeout would cut it
// off halfway.
const projectActionTimeout = 10 * time.Minute

// parallelAction acts on a list of items concurrently, at most limit at a
// time, and summarizes the outcome. name and act must be non-nil.
type parallelAction[T any] struct {
	// name names an item in the report. It should not return "".
	name func(T) string
	// act acts on one item. It is called from several goroutines at once.
	act func(ctx context.Context, item T) error
	// verb is the past tense of the action, as in "stopped".
	verb string
	// limit is the number of calls run at once; zero or negative means
	// maxParallelActions.
	limit int
}

// outcome is what happened to one item of a parallel action.
type outcome int

const (
	notAttempted outcome = iota // ctx ended before its call began
	succeeded
	failed
	interrupted // ctx ended during its call: it may or may not have happened
)

// run acts on every item, carrying on past failures, and returns the
// number of items acted on. If any call failed, or ctx ended before all
// were done, it also returns an error summarizing the outcome, such as
// "2 stopped, 1 failed (x): x: <the error>". The report lists items in name
// order, whatever order the calls finished in, so it does not depend on
// scheduling: the failed items are named (up to maxListedFailures) and the
// first error is that of the first failed item by name. An item whose call
// ctx cut off is reported as interrupted, and items whose call had not
// begun as not attempted.
func (a parallelAction[T]) run(ctx context.Context, items []T) (int, error) {
	limit := a.limit
	if limit <= 0 {
		limit = maxParallelActions
	}
	results := make([]outcome, len(items))
	errs := make([]error, len(items))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
launch:
	for i, item := range items {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break launch // this item and the rest stay notAttempted
		}
		// select picks at random when both cases are ready, so ctx may have
		// ended while the slot was acquired: give it back unused.
		if ctx.Err() != nil {
			<-sem
			break launch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := a.act(ctx, item)
			switch {
			case err == nil:
				results[i] = succeeded
			case ctx.Err() != nil && errors.Is(err, ctx.Err()):
				results[i] = interrupted
			default:
				results[i], errs[i] = failed, err
			}
		}()
	}
	wg.Wait()
	return a.summarize(ctx, items, results, errs)
}

// summarize reports the outcomes of run, in name order.
func (a parallelAction[T]) summarize(ctx context.Context, items []T, results []outcome, errs []error) (int, error) {
	names := make([]string, len(items))
	order := make([]int, len(items))
	for i, item := range items {
		names[i], order[i] = a.name(item), i
	}
	slices.SortStableFunc(order, func(x, y int) int { return cmp.Compare(names[x], names[y]) })

	var (
		done, skipped       int
		failedNames, cutOff []string
		firstErr            error
	)
	for _, i := range order {
		switch results[i] {
		case succeeded:
			done++
		case failed:
			failedNames = append(failedNames, names[i])
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", names[i], errs[i])
			}
		case interrupted:
			cutOff = append(cutOff, names[i])
		default:
			skipped++
		}
	}
	stopped := len(cutOff) > 0 || skipped > 0
	if !stopped && firstErr == nil {
		return done, nil
	}

	parts := []string{fmt.Sprintf("%d %s", done, a.verb)}
	if len(failedNames) > 0 {
		parts = append(parts, fmt.Sprintf("%d failed (%s)", len(failedNames), listNames(failedNames)))
	}
	if len(cutOff) > 0 {
		parts = append(parts, fmt.Sprintf("%d interrupted (%s)", len(cutOff), listNames(cutOff)))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d not attempted", skipped))
	}
	summary := strings.Join(parts, ", ")
	switch {
	case stopped && firstErr != nil:
		return done, fmt.Errorf("%s: %w; %w", summary, context.Cause(ctx), firstErr)
	case stopped:
		return done, fmt.Errorf("%s: %w", summary, context.Cause(ctx))
	default:
		return done, fmt.Errorf("%s: %w", summary, firstErr)
	}
}

// listNames joins names, listing at most maxListedFailures of them and
// counting the rest.
func listNames(names []string) string {
	if len(names) > maxListedFailures {
		names = append(slices.Clip(names[:maxListedFailures]), fmt.Sprintf("%d more", len(names)-maxListedFailures))
	}
	return strings.Join(names, ", ")
}
