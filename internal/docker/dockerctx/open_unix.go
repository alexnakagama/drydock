//go:build unix

package dockerctx

import (
	"os"
	"syscall"
)

// openNoBlock opens path read-only with O_NONBLOCK, so opening a FIFO that
// has no writer returns at once instead of blocking. Reads of a regular
// file are unaffected by the flag.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
