// Command drydock is a terminal UI for managing a local Docker engine.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/alexnakagama/drydock/internal/docker"
)

// Exit statuses returned by run.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitInterrupted = 130 // 128 + SIGINT; main reports the signal actually caught
)

// pingTimeout bounds the startup connection check.
const pingTimeout = 5 * time.Second

// deps holds what run needs from the outside world, so tests can replace it.
type deps struct {
	// newService creates the engine service. It must not connect.
	newService  func() (docker.Service, error)
	pingTimeout time.Duration
}

func defaultDeps() deps {
	return deps{
		newService: func() (docker.Service, error) {
			return docker.NewSDKService(userAgent())
		},
		pingTimeout: pingTimeout,
	}
}

func main() {
	// A signal cancels the context, so a slow startup check can be aborted
	// with Ctrl+C before the TUI owns the terminal.
	ctx, cancel := context.WithCancel(context.Background())
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	var caught atomic.Value // os.Signal
	go func() {
		if sig, ok := <-sigc; ok {
			caught.Store(sig)
			cancel()
		}
	}()

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, defaultDeps())

	signal.Stop(sigc)
	close(sigc)
	cancel()
	if code == exitInterrupted {
		sig, _ := caught.Load().(os.Signal)
		code = signalExitCode(sig)
	}
	os.Exit(code)
}

// signalExitCode returns the conventional exit status for a process ended
// by sig (128 + the signal number), as shells report it.
func signalExitCode(sig os.Signal) int {
	if s, ok := sig.(syscall.Signal); ok && s > 0 {
		return 128 + int(s)
	}
	return exitInterrupted
}

// run parses args and runs the program, writing to stdout and stderr, and
// returns the process exit status. It never calls os.Exit so that deferred
// cleanup runs and so it can be tested.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
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
		fmt.Fprintf(stderr, "drydock: unexpected argument %q\n", docker.SanitizeText(fs.Arg(0)))
		fs.Usage()
		return exitUsage
	}

	if *showVersion {
		fmt.Fprintf(stdout, "drydock %s\n", version())
		return exitOK
	}

	svc, err := d.newService()
	if err != nil {
		fmt.Fprintf(stderr, "drydock: %s\n", docker.SanitizeText(err.Error()))
		return exitError
	}
	defer svc.Close()

	if code, ok := checkConnection(ctx, svc, stderr, d.pingTimeout); !ok {
		return code
	}

	// The TUI is wired in by later tasks.
	fmt.Fprintln(stderr, "drydock: the terminal UI is not implemented yet")
	return exitError
}

// checkConnection pings the engine before the TUI takes over the terminal,
// so that failures are reported as plain text on stderr. It returns the exit
// status to use and false if the engine is unreachable.
func checkConnection(ctx context.Context, svc docker.Service, stderr io.Writer, timeout time.Duration) (int, bool) {
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := svc.Ping(pctx)
	if err == nil {
		return exitOK, true
	}
	if ctx.Err() != nil {
		// Interrupted by the user, not a connection problem.
		fmt.Fprintln(stderr, "drydock: interrupted")
		return exitInterrupted, false
	}
	if errors.Is(err, context.DeadlineExceeded) || pctx.Err() != nil {
		err = context.DeadlineExceeded
	}
	fmt.Fprintf(stderr, "drydock: %s\n", docker.ConnectErrorMessage(svc.Host(), err, timeout))
	return exitError, false
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

// userAgent identifies drydock to the engine, for example "drydock/v0.1.0".
func userAgent() string {
	v := version()
	if strings.HasPrefix(v, "(") {
		return "drydock"
	}
	return "drydock/" + v
}
