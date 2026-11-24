package store

import (
	"errors"
	"time"
)

// ErrNotConnected is returned by actions attempted while the store is not
// connected to the engine. No engine call is made in that case.
var ErrNotConnected = errors.New("not connected to the Docker engine")

// State is the store's connection state.
type State int32

const (
	// StateConnecting is the state before the first load completes.
	StateConnecting State = iota
	// StateConnected means the event stream is live and the snapshot is
	// current. Actions are allowed only in this state.
	StateConnected
	// StateDisconnected means the engine connection or the event stream was
	// lost; the store is retrying with backoff and the last snapshot may be
	// stale.
	StateDisconnected
)

// String returns a lower-case name for the state.
func (s State) String() string {
	switch s {
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}

// SnapshotMsg delivers a new snapshot to the UI. Snapshots arrive in
// increasing Seq order.
type SnapshotMsg struct {
	Snapshot Snapshot
}

// ConnectionMsg reports a change of the store's connection state, and each
// failed reconnection attempt while disconnected.
type ConnectionMsg struct {
	State State
	// Err is why the store is disconnected; nil unless State is
	// StateDisconnected. Its Error method returns Reason; errors.Is and
	// errors.As see the underlying error.
	Err error
	// Reason is Err's text made safe for display: credentials in the
	// endpoint are removed and terminal control characters are replaced.
	Reason string
	// RetryIn is the delay before the next reconnection attempt; zero
	// unless State is StateDisconnected.
	RetryIn time.Duration
}

// ActionResultMsg reports the outcome of a command built with
// Store.Action.
type ActionResultMsg struct {
	// Label is the label passed to Store.Action.
	Label string
	// Err is nil on success, ErrNotConnected if the action was refused
	// because the store was not connected, or the engine's error. Its Error
	// method returns Reason; errors.Is and errors.As see the underlying
	// error.
	Err error
	// Reason is Err's text made safe for display, or "" on success.
	Reason string
	// Detail describes a successful outcome (see Store.ActionWith); it is
	// "" on failure and for actions built with Store.Action. It is display
	// text built by drydock, but may quote engine data, so render it
	// cleaned.
	Detail string
}
