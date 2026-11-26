package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker"
	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/store"
	"github.com/alexnakagama/drydock/internal/ui"
)

// shellCandidates are the shells tried, in order, when opening a shell in a
// container.
var shellCandidates = []string{"/bin/bash", "/bin/sh"}

// Exec timings.
const (
	// probeTimeout bounds the whole shell probe.
	probeTimeout = 15 * time.Second
	// recheckTimeout bounds the check made after a shell exits with an
	// error.
	recheckTimeout = 10 * time.Second
	// probePoll is the interval between checks of a probe's exit status.
	probePoll = 25 * time.Millisecond
)

// errNoShell means none of shellCandidates could be started.
var errNoShell = errors.New("no shell available")

// execEnv is what opening a shell needs from the outside world, so tests
// can replace it.
type execEnv struct {
	// lookPath finds the docker CLI (exec.LookPath).
	lookPath func(file string) (string, error)
	// environ returns the environment the CLI inherits (os.Environ).
	environ func() []string
	// run hands the terminal to cmd (tea.ExecProcess).
	run func(cmd *exec.Cmd, done tea.ExecCallback) tea.Cmd
	// poll is the interval between exit-status checks while probing.
	poll time.Duration
}

func defaultExecEnv() execEnv {
	return execEnv{lookPath: exec.LookPath, environ: os.Environ, run: tea.ExecProcess, poll: probePoll}
}

// execProbeMsg reports the outcome of a shell probe started by view.
type execProbeMsg struct {
	view        uint64
	name        string // container name, for messages
	containerID string
	dockerPath  string
	endpoint    dockerctx.Endpoint // resolved context drydock uses
	host        string             // engine endpoint drydock uses (Service.Host)
	shell       string             // the shell found; "" on error
	err         error
	reason      string // err's display text
}

// execDoneMsg reports that the interactive shell started by view exited.
type execDoneMsg struct {
	view        uint64
	name        string
	containerID string
	err         error
}

// shell opens an interactive shell in a running container: it checks for
// the docker CLI, probes for a shell with non-interactive execs, and only
// then suspends the TUI (see startShell).
func (v *containersView) shell(c container.Summary) tea.Cmd {
	name, id := store.ContainerName(c), c.ID
	if v.probing {
		return nil // a probe is already running; its result decides
	}
	if c.State != container.StateRunning {
		return ui.Error(name + " must be running to open a shell (it is " + stateName(c.State) + ")")
	}
	if err := docker.ValidateContainerID(id); err != nil {
		// IDs come from the engine; this guards the CLI's argument list.
		return ui.Error("cannot open a shell in " + name + ": invalid container ID")
	}
	dockerPath, err := v.exec.lookPath("docker")
	if err != nil {
		return ui.Error("cannot open a shell: the docker CLI was not found in PATH")
	}
	view, st, poll := v.id, v.st, v.exec.poll
	v.probing = true
	return ui.Track("looking for a shell in "+name, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
		defer cancel()
		var (
			ep          dockerctx.Endpoint
			host, shell string
		)
		err := st.Do(ctx, func(ctx context.Context, svc docker.Service) error {
			ep, host = svc.Endpoint(), svc.Host()
			var err error
			shell, err = probeShell(ctx, svc, id, poll)
			return err
		})
		return execProbeMsg{
			view: view, name: name, containerID: id, dockerPath: dockerPath,
			endpoint: ep, host: host, shell: shell, err: err, reason: st.ErrorText(err),
		}
	})
}

// startShell suspends the TUI and runs `docker exec -it` with the probed
// shell, or reports why no shell can be opened. The probe can take a while,
// so the terminal is handed over only if this view is still in front with
// no dialog open (see ui.Foreground).
func (v *containersView) startShell(msg execProbeMsg) tea.Cmd {
	v.probing = false
	switch {
	case errors.Is(msg.err, errNoShell):
		return ui.Error("no shell could be started in " + msg.name + " (tried " + strings.Join(shellCandidates, " and ") + ")")
	case msg.err != nil:
		return ui.Error("open shell in " + msg.name + ": " + msg.reason)
	}
	cmd, err := execCommand(msg.dockerPath, msg.endpoint, msg.host, msg.containerID, msg.shell, v.exec.environ())
	if err != nil {
		return ui.Error("cannot open a shell in " + msg.name + ": " + docker.SanitizeText(err.Error()))
	}
	view, name, id := v.id, msg.name, msg.containerID
	run := v.exec.run(cmd, func(err error) tea.Msg { return execDoneMsg{view: view, name: name, containerID: id, err: err} })
	return ui.Foreground(run, "shell in "+name+" not opened: another view or dialog is in front")
}

// shellDone reports a shell session that failed. The exit status of the
// docker CLI is either the shell's (the user's last command) or the CLI's
// own error status, and both use small codes such as 1, so a non-zero
// status alone is not reported: instead the container is checked, and the
// session is reported as failed if the container stopped or disappeared
// (the CLI's message was printed on the terminal the TUI just redrew).
func (v *containersView) shellDone(msg execDoneMsg) tea.Cmd {
	var exitErr *exec.ExitError
	switch {
	case msg.err == nil:
		return nil
	case !errors.As(msg.err, &exitErr):
		return ui.Error("shell in " + msg.name + ": " + v.st.ErrorText(msg.err))
	}
	st, name, id := v.st, msg.name, msg.containerID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), recheckTimeout)
		defer cancel()
		var state container.ContainerState
		err := st.Do(ctx, func(ctx context.Context, svc docker.Service) error {
			res, err := svc.ContainerInspect(ctx, id)
			if err == nil && res.Container.State != nil {
				state = res.Container.State.Status
			}
			return err
		})
		switch {
		case docker.IsNotFound(err):
			return ui.Error("shell in " + name + " ended: the container no longer exists")()
		case err == nil && state != "" && state != container.StateRunning:
			return ui.Error("shell in " + name + " ended: the container is " + stateName(state))()
		}
		return nil
	}
}

// execCommand builds the command that opens an interactive shell in a
// container with the docker CLI at dockerPath. The CLI is pointed at the
// engine drydock itself uses, described by ep (the service's resolved
// context) and host (the service's Host), because the CLI resolves contexts
// and the environment on its own and could otherwise reach another engine:
//
//   - default context: DOCKER_CONTEXT is removed and DOCKER_HOST is set to
//     host; the TLS environment variables are passed through, as the SDK
//     reads the same ones.
//   - named context: the CLI is given --context=<name> before "exec", with
//     DOCKER_CONFIG set to the config directory drydock read the context
//     from, so it loads the same context store entry, TLS material and
//     verification setting. DOCKER_HOST, DOCKER_CONTEXT and the TLS
//     environment variables are removed so they cannot interfere.
//
// The CLI's own TLS flags are not used for named contexts: in TLS mode the
// CLI always loads a CA file, falling back to the config directory's
// ca.pem, so a context with no CA (verifying against the system roots, or
// skipping verification) could not be expressed with flags.
//
// No shell is involved, so the arguments are passed verbatim. The context
// name is passed as --context=<name>, so it can never be read as another
// flag; containerID must have passed docker.ValidateContainerID, which
// rules out values the CLI would parse as flags. A named ep without an
// absolute ConfigDir is an error rather than a guess at the CLI's store.
func execCommand(dockerPath string, ep dockerctx.Endpoint, host, containerID, shell string, environ []string) (*exec.Cmd, error) {
	var args []string
	if !ep.IsDefault() {
		if ep.ConfigDir == "" || !filepath.IsAbs(ep.ConfigDir) {
			return nil, fmt.Errorf("context %q has no absolute config directory", ep.Context)
		}
		args = append(args, "--context="+ep.Context)
	}
	args = append(args, "exec", "-it", containerID, shell)
	cmd := exec.Command(dockerPath, args...)
	cmd.Env = execEnviron(environ, ep, host)
	return cmd, nil
}

// execEnviron returns the environment for the CLI: environ without the
// variables that would select an engine other than ep's. For the default
// context, DOCKER_HOST and DOCKER_CONTEXT are removed and DOCKER_HOST is
// set to host (unless host is empty). For a named context, DOCKER_TLS,
// DOCKER_TLS_VERIFY, DOCKER_CERT_PATH and DOCKER_CONFIG are removed too,
// and DOCKER_CONFIG is set to ep.ConfigDir; the context itself is selected
// by a flag (see execCommand).
func execEnviron(environ []string, ep dockerctx.Endpoint, host string) []string {
	drop := map[string]bool{"DOCKER_HOST": true, "DOCKER_CONTEXT": true}
	named := !ep.IsDefault()
	if named {
		for _, k := range []string{"DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "DOCKER_CONFIG"} {
			drop[k] = true
		}
	}
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if drop[envKey(k)] {
			continue
		}
		out = append(out, kv)
	}
	switch {
	case named:
		out = append(out, "DOCKER_CONFIG="+ep.ConfigDir)
	case host != "":
		out = append(out, "DOCKER_HOST="+host)
	}
	return out
}

// envKey normalizes an environment variable name for comparison: names are
// case-insensitive on Windows.
func envKey(k string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(k)
	}
	return k
}

// probeShell returns the first of shellCandidates that runs in the
// container, by executing `<shell> -c true` non-interactively. It returns
// errNoShell if none does, and the engine's error if the container cannot
// run commands at all (for example because it stopped).
func probeShell(ctx context.Context, svc docker.Service, containerID string, poll time.Duration) (string, error) {
	for _, sh := range shellCandidates {
		ok, err := tryShell(ctx, svc, containerID, sh, poll)
		if err != nil {
			return "", err
		}
		if ok {
			return sh, nil
		}
	}
	return "", errNoShell
}

// tryShell reports whether `<shell> -c true` runs successfully in the
// container. The runtime refusing to start the process (the outcome when
// the shell does not exist or cannot be executed), or the process failing,
// is a "no"; any other failure (the engine, the connection, the container
// state) is an error.
func tryShell(ctx context.Context, svc docker.Service, containerID, shell string, poll time.Duration) (bool, error) {
	execID, err := svc.ExecCreate(ctx, containerID, []string{shell, "-c", "true"})
	if err != nil {
		return false, err
	}
	if err := svc.ExecStart(ctx, execID); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if isExecStartFailure(err) {
			return false, nil
		}
		return false, err
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		res, err := svc.ExecInspect(ctx, execID)
		if err != nil {
			return false, err
		}
		if !res.Running {
			return res.ExitCode == 0, nil
		}
		t.Reset(poll)
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-t.C:
		}
	}
}

// isExecStartFailure reports whether err is the runtime refusing to start
// an exec's process, as opposed to an engine or connection error. The
// engine reports it as an untyped error whose text comes from the OCI
// runtime, for example `OCI runtime exec failed: exec failed: unable to
// start container process: exec: "/bin/bash": stat /bin/bash: no such file
// or directory: unknown`.
func isExecStartFailure(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"oci runtime exec failed",
		"unable to start container process",
		"no such file or directory",
		"executable file not found",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
