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

	tea "charm.land/bubbletea/v2"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
	"github.com/alexnakagama/drydock/internal/ui/views"
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
	// programOptions are appended to the TUI program's options; tests use
	// them to replace the terminal.
	programOptions []tea.ProgramOption
}

func defaultDeps() deps {
	return deps{
		newService: func() (docker.Service, error) {
			return docker.NewSDKService(userAgent(), dockerctx.DefaultEndpoint())
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
		_, _ = fmt.Fprint(fs.Output(), "Usage: drydock [flags]\n\n"+
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
		_, _ = fmt.Fprintf(stderr, "drydock: unexpected argument %q\n", docker.SanitizeText(fs.Arg(0)))
		fs.Usage()
		return exitUsage
	}

	if *showVersion {
		if _, err := fmt.Fprintf(stdout, "drydock %s\n", version()); err != nil {
			// stdout is the requested output; failing to write it (for
			// example a closed pipe or a full disk) is an error.
			_, _ = fmt.Fprintf(stderr, "drydock: write version: %s\n", docker.SanitizeText(err.Error()))
			return exitError
		}
		return exitOK
	}

	svc, err := d.newService()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "drydock: %s\n", docker.SanitizeText(err.Error()))
		return exitError
	}
	// Closing only releases idle connections on the way out; there is
	// nothing useful to do with a failure at that point.
	defer func() { _ = svc.Close() }()

	if code, ok := checkConnection(ctx, svc, stderr, d.pingTimeout); !ok {
		return code
	}

	err = runTUI(ctx, svc, d.programOptions)
	switch {
	case err == nil:
		return exitOK
	case ctx.Err() != nil:
		return exitInterrupted
	default:
		_, _ = fmt.Fprintf(stderr, "drydock: %s\n", docker.ErrorText(svc.Host(), err))
		return exitError
	}
}

// initialResource is the view drydock starts on.
const initialResource = "containers"

// runTUI runs the terminal UI until the user quits or ctx is cancelled.
// The store runs alongside the program; its goroutine is stopped, and
// waited for, before runTUI returns. Commands still in flight when the
// program exits (for example an engine action) are not waited for: they are
// bounded by their own timeouts, and the service tolerates concurrent use
// while it is being closed.
func runTUI(ctx context.Context, svc docker.Service, opts []tea.ProgramOption) error {
	// The store delivers messages through the program. p is assigned before
	// the store starts, and Send returns once the program has exited, so
	// the store never blocks shutdown.
	var p *tea.Program
	st := store.New(svc, func(msg tea.Msg) { p.Send(msg) })
	model := ui.New(views.Resources(st), initialResource)
	defer model.Close()

	// main handles SIGINT and SIGTERM by cancelling ctx, which kills the
	// program and restores the terminal; Ctrl+C typed in the terminal is
	// a key press and quits normally.
	opts = append([]tea.ProgramOption{tea.WithContext(ctx), tea.WithoutSignalHandler()}, opts...)
	p = tea.NewProgram(model, opts...)

	storeCtx, stopStore := context.WithCancel(ctx)
	storeDone := make(chan struct{})
	go func() {
		defer close(storeDone)
		_ = st.Run(storeCtx) // returns nil once storeCtx is cancelled
	}()

	_, err := p.Run()
	stopStore()
	<-storeDone
	return err
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
		_, _ = fmt.Fprintln(stderr, "drydock: interrupted")
		return exitInterrupted, false
	}
	if errors.Is(err, context.DeadlineExceeded) || pctx.Err() != nil {
		err = context.DeadlineExceeded
	}
	_, _ = fmt.Fprintf(stderr, "drydock: %s\n", docker.ConnectErrorMessage(svc.Endpoint(), svc.Host(), err, timeout))
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
