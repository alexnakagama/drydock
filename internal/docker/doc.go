// Package docker defines drydock's narrow interface to the Docker engine
// (Service), its production implementation backed by the Engine Go SDK, and
// an in-memory fake for tests. Every SDK call lives in this package so that
// changes in the pre-v1 SDK stay contained here.
//
// The engine a Service talks to is chosen by the resolved Docker context
// (see package dockerctx): the default context is configured by the
// environment, as the docker CLI does, and a named context by its endpoint
// and TLS material in the CLI's context store.
package docker
