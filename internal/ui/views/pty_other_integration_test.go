//go:build integration && !linux

package views

import (
	"os"
	"testing"
)

// openPTY skips the test: pseudo-terminals are only opened on Linux, where
// the integration tests run in CI.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	t.Skip("pseudo-terminal helper is Linux-only")
	return nil, nil
}
