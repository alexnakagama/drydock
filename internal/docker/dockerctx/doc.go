// Package dockerctx resolves the active Docker context the way the docker
// CLI does, and reads a named context's docker endpoint from the CLI's
// context store.
//
// Resolution is a pure function of its Inputs: the -context flag, the
// environment (read through Inputs.Getenv) and the files under the CLI
// config directory. It never opens a network connection, never starts a
// process and never writes to the config directory or the context store.
//
// The store layout it reads is the one written by `docker context create`:
//
//	$DOCKER_CONFIG (default $HOME/.docker)
//	|-- config.json                          {"currentContext": "colima", ...}
//	`-- contexts/
//	    |-- meta/<hex(sha256(name))>/meta.json
//	    `-- tls/<hex(sha256(name))>/docker/{ca.pem, cert.pem, key.pem}
package dockerctx
