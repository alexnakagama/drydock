package dockerctx

import (
	"errors"
	"fmt"
)

// ErrNotFound means the context store has no context with the requested
// name.
var ErrNotFound = errors.New("context not found")

// ErrSSHUnsupported means the context's docker endpoint uses ssh://, which
// drydock cannot reach without starting a process.
var ErrSSHUnsupported = errors.New("ssh:// endpoints are not supported")

// FileError means a CLI config or context store file could not be read or
// parsed. Path is the file at fault.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return e.Path + ": " + e.Err.Error() }

// Unwrap returns the underlying error.
func (e *FileError) Unwrap() error { return e.Err }

// ContextError names the context whose resolution failed. It wraps
// ErrNotFound, ErrSSHUnsupported or a *FileError, so errors.Is and
// errors.As see through it. Errors from config.json, which are found before
// any context is known, are returned as a bare *FileError.
type ContextError struct {
	Name string
	Err  error
}

func (e *ContextError) Error() string { return fmt.Sprintf("context %q: %v", e.Name, e.Err) }

// Unwrap returns the underlying error.
func (e *ContextError) Unwrap() error { return e.Err }
