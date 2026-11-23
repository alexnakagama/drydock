// Package docker defines drydock's narrow interface to the Docker engine
// (Service), its production implementation backed by the Engine Go SDK, and
// an in-memory fake for tests. Every SDK call lives in this package so that
// changes in the pre-v1 SDK stay contained here.
package docker
