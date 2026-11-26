//go:build unix

package dockerctx

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestResolveRejectsFIFO checks that a FIFO in place of config.json or
// meta.json is reported as a *FileError instead of blocking Resolve.
func TestResolveRejectsFIFO(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path func(dir string) string
		in   func(dir string) Inputs
	}{
		{
			name: "config.json",
			path: func(dir string) string { return filepath.Join(dir, "config.json") },
			in:   func(dir string) Inputs { return Inputs{ConfigDir: dir, Getenv: env(nil)} },
		},
		{
			name: "meta.json",
			path: func(dir string) string { return metaPath(dir, "colima") },
			in:   func(dir string) Inputs { return Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := tc.path(dir)
			writeFile(t, path, "")
			if err := removeAndMkfifo(path); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := Resolve(tc.in(dir))
				done <- err
			}()
			select {
			case err := <-done:
				var fe *FileError
				if !errors.As(err, &fe) || fe.Path != path {
					t.Errorf("err = %v, want *FileError for %s", err, path)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Resolve blocked on a FIFO")
			}
		})
	}
}

func removeAndMkfifo(path string) error {
	if err := syscall.Unlink(path); err != nil {
		return err
	}
	return syscall.Mkfifo(path, 0o600)
}
