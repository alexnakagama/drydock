//go:build !unix

package dockerctx

import "os"

// openNoBlock opens path read-only. Platforms outside unix have no FIFOs
// that block on open in a config directory.
func openNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}
