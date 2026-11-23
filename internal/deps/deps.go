//go:build deps

// Package deps pins the project's direct dependencies in go.mod before code
// that imports them exists. The "deps" build tag keeps it out of every normal
// build, but `go mod tidy` evaluates all build tags, so the requirements are
// retained.
//
// Delete each import once real code imports the module, and delete this
// package once it is empty.
package deps

import (
	_ "charm.land/bubbles/v2/table"
	_ "charm.land/lipgloss/v2"
)
