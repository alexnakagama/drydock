package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// maxListedFailures bounds the failed items a removal report names.
const maxListedFailures = 3

// removal removes a previewed list of items one by one and summarizes the
// outcome. name and remove must be non-nil.
type removal[T any] struct {
	// name names an item in the report. It should not return "": the
	// report would name the item by an empty string.
	name func(T) string
	// remove removes one item. It returns an error wrapping gone (with %w) if
	// the item no longer exists.
	remove func(ctx context.Context, item T) error
	// gone is the sentinel remove wraps for an item that is already gone. If
	// nil, no item is counted as already gone.
	gone error
	// noun is the item's noun, singular and plural, as in "volume", "volumes".
	noun [2]string
}

// run removes items in order, carrying on past failures, and returns a
// summary such as "2 things removed, 1 already gone". If any removal
// failed, or ctx ended before all were done, it returns an error
// summarizing the outcome instead, naming the failed items before the first
// error, for example "2 removed, 1 failed (x): x: <the error>". An item
// whose removal ctx cut off is reported as interrupted: the engine may or
// may not have removed it. Items left after that are reported as not
// attempted.
func (r removal[T]) run(ctx context.Context, items []T) (string, error) {
	var (
		removed, gone, notAttempted int
		failed                      []string
		interrupted                 string
		wasInterrupted              bool
		firstErr                    error
		stopped                     bool
	)
loop:
	for i, item := range items {
		if ctx.Err() != nil {
			stopped, notAttempted = true, len(items)-i
			break
		}
		err := r.remove(ctx, item)
		switch {
		case err == nil:
			removed++
		case r.gone != nil && errors.Is(err, r.gone):
			gone++
		case ctx.Err() != nil && errors.Is(err, ctx.Err()):
			stopped, wasInterrupted, interrupted, notAttempted = true, true, r.name(item), len(items)-i-1
			break loop
		default:
			name := r.name(item)
			failed = append(failed, name)
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", name, err)
			}
		}
	}

	parts := []string{fmt.Sprintf("%d removed", removed)}
	if gone > 0 {
		parts = append(parts, fmt.Sprintf("%d already gone", gone))
	}
	if len(failed) > 0 {
		listed := failed
		if len(listed) > maxListedFailures {
			listed = append(slices.Clip(listed[:maxListedFailures]), fmt.Sprintf("%d more", len(failed)-maxListedFailures))
		}
		parts = append(parts, fmt.Sprintf("%d failed (%s)", len(failed), strings.Join(listed, ", ")))
	}
	if wasInterrupted {
		parts = append(parts, "1 interrupted ("+interrupted+")")
	}
	if notAttempted > 0 {
		parts = append(parts, fmt.Sprintf("%d not attempted", notAttempted))
	}
	summary := strings.Join(parts, ", ")

	switch {
	case stopped && firstErr != nil:
		return "", fmt.Errorf("%s: %w; %w", summary, context.Cause(ctx), firstErr)
	case stopped:
		return "", fmt.Errorf("%s: %w", summary, context.Cause(ctx))
	case firstErr != nil:
		return "", fmt.Errorf("%s: %w", summary, firstErr)
	}
	done := plural(removed, r.noun[0], r.noun[1]) + " removed"
	if gone > 0 {
		return fmt.Sprintf("%s, %d already gone", done, gone), nil
	}
	return done, nil
}
