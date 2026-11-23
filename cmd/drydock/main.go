// Command drydock is a terminal UI for managing a local Docker engine.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

// Exit statuses returned by run.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args and runs the program, writing to stdout and stderr, and
// returns the process exit status. It never calls os.Exit so that deferred
// cleanup in callers runs and so it can be tested.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("drydock", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: drydock [flags]\n\n"+
			"A terminal UI for managing a local Docker engine.\n"+
			"The engine endpoint is taken from DOCKER_HOST, DOCKER_TLS_VERIFY and\n"+
			"DOCKER_CERT_PATH, or the default local socket.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	showVersion := fs.Bool("version", false, "print version information and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "drydock: unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return exitUsage
	}

	if *showVersion {
		fmt.Fprintf(stdout, "drydock %s\n", version())
		return exitOK
	}

	// The engine connection and the TUI are wired in by later tasks.
	fmt.Fprintln(stderr, "drydock: the terminal UI is not implemented yet")
	return exitError
}

// version reports the module version embedded by the Go toolchain: a tagged
// version for `go install ...@vX.Y.Z`, a pseudo-version derived from VCS
// information for `go build` inside a checkout, or "(devel)" for `go run` and
// builds without VCS stamping.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(unknown)"
	}
	return info.Main.Version
}
