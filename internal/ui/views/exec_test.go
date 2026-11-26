package views

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
	"github.com/alexnakagama/drydock/internal/docker/dockerfake"
)

func execCmds(f *dockerfake.Service) [][]string {
	var out [][]string
	for _, c := range f.CallsTo(dockerfake.MethodExecCreate) {
		out = append(out, c.Args[1].([]string))
	}
	return out
}

func TestProbeShellOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		shells    []string
		want      string
		wantErr   error
		wantTried [][]string
	}{
		{"bash first", []string{"/bin/bash", "/bin/sh"}, "/bin/bash", nil,
			[][]string{{"/bin/bash", "-c", "true"}}},
		{"falls back to sh", []string{"/bin/sh"}, "/bin/sh", nil,
			[][]string{{"/bin/bash", "-c", "true"}, {"/bin/sh", "-c", "true"}}},
		{"no shell", nil, "", errNoShell,
			[][]string{{"/bin/bash", "-c", "true"}, {"/bin/sh", "-c", "true"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := dockerfake.New()
			id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
			f.SetShells(id, tt.shells...)
			got, err := probeShell(t.Context(), f, id, time.Millisecond)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("probeShell = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
			if tried := execCmds(f); !slices.EqualFunc(tried, tt.wantTried, slices.Equal) {
				t.Errorf("tried %v, want %v", tried, tt.wantTried)
			}
		})
	}
}

func TestProbeShellContainerError(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "old", "alpine", container.StateExited, "Exited"))
	if _, err := probeShell(t.Context(), f, id, time.Millisecond); err == nil || errors.Is(err, errNoShell) {
		t.Errorf("probe of a stopped container = %v, want the engine's error", err)
	}
}

func TestProbeShellHonoursContext(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := probeShell(ctx, f, id, time.Millisecond); !errors.Is(err, context.Canceled) {
		t.Errorf("probe with a cancelled context = %v", err)
	}
}

func TestExecCommand(t *testing.T) {
	t.Parallel()
	// Every case starts from an environment that selects another engine.
	environ := []string{
		"PATH=/usr/bin",
		"DOCKER_HOST=tcp://elsewhere:2376",
		"DOCKER_CONTEXT=remote",
		"DOCKER_TLS=1",
		"DOCKER_TLS_VERIFY=1",
		"DOCKER_CERT_PATH=/home/u/.docker/elsewhere",
		"DOCKER_CONFIG=/home/u/.docker-other",
		"HOME=/home/u",
		"DOCKER_HOSTNAME=keep-me",
		"DOCKER_API_VERSION=1.45",
	}
	cfgDir := filepath.Join(t.TempDir(), ".docker")
	// The environment left for a named context: the CLI reads the same
	// context store as drydock.
	namedEnv := []string{
		"PATH=/usr/bin",
		"HOME=/home/u",
		"DOCKER_HOSTNAME=keep-me",
		"DOCKER_API_VERSION=1.45",
		"DOCKER_CONFIG=" + cfgDir,
	}
	// The default context's environment is passed through, with the host
	// drydock uses.
	defaultEnv := func(host string) []string {
		return []string{
			"PATH=/usr/bin",
			"DOCKER_TLS=1",
			"DOCKER_TLS_VERIFY=1",
			"DOCKER_CERT_PATH=/home/u/.docker/elsewhere",
			"DOCKER_CONFIG=/home/u/.docker-other",
			"HOME=/home/u",
			"DOCKER_HOSTNAME=keep-me",
			"DOCKER_API_VERSION=1.45",
			"DOCKER_HOST=" + host,
		}
	}
	tlsDir := filepath.Join(cfgDir, "contexts", "tls", "abc", "docker")
	pem := func(name string) string { return filepath.Join(tlsDir, name) }
	tests := []struct {
		name      string
		ep        dockerctx.Endpoint
		host      string
		wantFlags []string
		wantEnv   []string
	}{
		{
			name:    "default context",
			ep:      dockerctx.DefaultEndpoint(),
			host:    "unix:///var/run/docker.sock",
			wantEnv: defaultEnv("unix:///var/run/docker.sock"),
		},
		{
			name:    "zero endpoint is the default context",
			host:    "tcp://engine:2376",
			wantEnv: defaultEnv("tcp://engine:2376"),
		},
		{
			name:      "named, no TLS",
			ep:        dockerctx.Endpoint{Context: "colima", Host: "unix:///Users/me/.colima/default/docker.sock", ConfigDir: cfgDir},
			host:      "unix:///Users/me/.colima/default/docker.sock",
			wantFlags: []string{"--context=colima"},
			wantEnv:   namedEnv,
		},
		{
			name: "named, TLS, verifying",
			ep: dockerctx.Endpoint{
				Context: "remote", Host: "tcp://box:2376", TLS: true, CertDir: tlsDir,
				CA: pem("ca.pem"), Cert: pem("cert.pem"), Key: pem("key.pem"), ConfigDir: cfgDir,
			},
			host:      "tcp://box:2376",
			wantFlags: []string{"--context=remote"},
			wantEnv:   namedEnv,
		},
		{
			name: "named, TLS, CA only",
			ep: dockerctx.Endpoint{
				Context: "remote", Host: "tcp://box:2376", TLS: true, CertDir: tlsDir, CA: pem("ca.pem"), ConfigDir: cfgDir,
			},
			host:      "tcp://box:2376",
			wantFlags: []string{"--context=remote"},
			wantEnv:   namedEnv,
		},
		{
			name: "named, TLS, skip verify without files",
			ep: dockerctx.Endpoint{
				Context: "remote", Host: "tcp://box:2376", TLS: true, SkipTLSVerify: true, ConfigDir: cfgDir,
			},
			host:      "tcp://box:2376",
			wantFlags: []string{"--context=remote"},
			wantEnv:   namedEnv,
		},
		{
			name:      "named, empty Host",
			ep:        dockerctx.Endpoint{Context: "local", ConfigDir: cfgDir},
			host:      "unix:///var/run/docker.sock",
			wantFlags: []string{"--context=local"},
			wantEnv:   namedEnv,
		},
		{
			name:      "named, name that looks like a flag",
			ep:        dockerctx.Endpoint{Context: "--host=tcp://evil:1", Host: "unix:///x.sock", ConfigDir: cfgDir},
			host:      "unix:///x.sock",
			wantFlags: []string{"--context=--host=tcp://evil:1"},
			wantEnv:   namedEnv,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd, err := execCommand("/usr/bin/docker", tt.ep, tt.host, cid(0), "/bin/sh", slices.Clone(environ))
			if err != nil {
				t.Fatalf("execCommand: %v", err)
			}
			if cmd.Path != "/usr/bin/docker" {
				t.Errorf("Path = %q", cmd.Path)
			}
			want := append(append([]string{"/usr/bin/docker"}, tt.wantFlags...), "exec", "-it", cid(0), "/bin/sh")
			if !slices.Equal(cmd.Args, want) {
				t.Errorf("Args = %q, want %q", cmd.Args, want)
			}
			if !slices.Equal(cmd.Env, tt.wantEnv) {
				t.Errorf("Env = %q, want %q", cmd.Env, tt.wantEnv)
			}
		})
	}
}

func TestExecCommandRejectsNamedContextWithoutConfigDir(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"", "relative/.docker"} {
		ep := dockerctx.Endpoint{Context: "colima", Host: "unix:///x.sock", ConfigDir: dir}
		if cmd, err := execCommand("/usr/bin/docker", ep, "unix:///x.sock", cid(0), "/bin/sh", nil); err == nil {
			t.Errorf("ConfigDir %q: execCommand = %q, want an error", dir, cmd.Args)
		}
	}
}

func TestExecEnvironWindowsNamesAreCaseInsensitive(t *testing.T) {
	t.Parallel()
	environ := []string{"Path=C:\\bin", "Docker_Host=tcp://elsewhere:2376", "docker_context=remote", "Docker_Config=C:\\other", "Docker_Tls_Verify=1"}
	ep := dockerctx.Endpoint{Context: "colima", ConfigDir: `C:\Users\me\.docker`}
	want := []string{"Path=C:\\bin", `DOCKER_CONFIG=C:\Users\me\.docker`}
	if got := execEnvironFor("windows", environ, ep, "npipe:////./pipe/docker_engine"); !slices.Equal(got, want) {
		t.Errorf("windows env = %q, want %q", got, want)
	}
	// Elsewhere names are case-sensitive: only exact names are removed.
	if got := execEnvironFor("linux", environ, ep, ""); !slices.Equal(got, append(slices.Clone(environ), `DOCKER_CONFIG=C:\Users\me\.docker`)) {
		t.Errorf("linux env = %q", got)
	}
}

func TestRecheckContext(t *testing.T) {
	t.Parallel()
	ep := dockerctx.Endpoint{Context: "colima", Host: "unix:///a.sock", ConfigDir: "/home/u/.docker"}
	same := func(in dockerctx.Inputs) (dockerctx.Endpoint, error) {
		if in.Flag != ep.Context || in.ConfigDir != ep.ConfigDir || in.Getenv("HOME") != "" {
			t.Errorf("resolve called with %+v", in)
		}
		return ep, nil
	}
	if reason, err := recheckContext(same, ep); err != nil {
		t.Errorf("unchanged context: %q, %v", reason, err)
	}
	moved := func(dockerctx.Inputs) (dockerctx.Endpoint, error) {
		got := ep
		got.Host = "tcp://other:2376"
		return got, nil
	}
	if reason, err := recheckContext(moved, ep); !errors.Is(err, errContextChanged) || !strings.Contains(reason, `Docker context "colima" changed since startup`) {
		t.Errorf("moved context: %q, %v", reason, err)
	}
	gone := func(dockerctx.Inputs) (dockerctx.Endpoint, error) {
		return dockerctx.Endpoint{}, &dockerctx.ContextError{Name: "colima\x1b[31m", Err: dockerctx.ErrNotFound}
	}
	if reason, err := recheckContext(gone, ep); !errors.Is(err, dockerctx.ErrNotFound) || !strings.Contains(reason, "was not found") || strings.Contains(reason, "\x1b") {
		t.Errorf("removed context: %q, %v", reason, err)
	}
	never := func(dockerctx.Inputs) (dockerctx.Endpoint, error) {
		t.Error("resolve called for the default context or a context without a config directory")
		return dockerctx.Endpoint{}, nil
	}
	for _, e := range []dockerctx.Endpoint{dockerctx.DefaultEndpoint(), {}, {Context: "colima"}} {
		if _, err := recheckContext(never, e); err != nil {
			t.Errorf("recheckContext(%+v) = %v", e, err)
		}
	}
}

func TestShellRefusedWhenContextChanged(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	ep := dockerctx.Endpoint{Context: "colima", Host: dockerfake.DefaultHost, ConfigDir: t.TempDir()}
	f.SetEndpoint(ep)
	e := newEnv(t, f, 100, 12)
	e.root().exec.resolve = func(dockerctx.Inputs) (dockerctx.Endpoint, error) {
		moved := ep
		moved.Host = "tcp://other:2376"
		return moved, nil
	}
	e.selectRow("web")
	e.press("x")
	e.waitStatus(`open shell in web: Docker context "colima" changed since startup; restart drydock`)
	e.settle()
	if len(e.shells()) != 0 {
		t.Error("the TUI was suspended for a context that changed")
	}
}

func TestShellUsesNamedContext(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	cfgDir := t.TempDir()
	f.SetEndpoint(dockerctx.Endpoint{Context: "colima", Host: dockerfake.DefaultHost, ConfigDir: cfgDir})
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("x")
	e.until("shell started", func() bool { return len(e.shells()) == 1 })
	e.settle()

	cmd := e.shells()[0]
	if want := []string{"/usr/bin/docker", "--context=colima", "exec", "-it", ids["web"], "/bin/sh"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("Args = %q, want %q", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "DOCKER_CONFIG="+cfgDir) || slices.ContainsFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "DOCKER_HOST=") || strings.HasPrefix(kv, "DOCKER_CONTEXT=")
	}) {
		t.Errorf("Env = %q", cmd.Env)
	}
}

func TestShellNamedContextWithoutConfigDirIsReported(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	f.SetEndpoint(dockerctx.Endpoint{Context: "colima", Host: dockerfake.DefaultHost})
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("x")
	e.waitStatus(`cannot open a shell in web: context "colima" has no absolute config directory`)
	e.settle()
	if len(e.shells()) != 0 {
		t.Error("the TUI was suspended without a usable context store")
	}
}

func TestShellOpensAndRestoresSelection(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("x")
	e.until("shell started", func() bool { return len(e.shells()) == 1 })
	e.settle()

	cmd := e.shells()[0]
	if want := []string{"/usr/bin/docker", "exec", "-it", ids["web"], "/bin/sh"}; !slices.Equal(cmd.Args, want) {
		t.Errorf("Args = %q, want %q", cmd.Args, want)
	}
	if !slices.Contains(cmd.Env, "DOCKER_HOST="+dockerfake.DefaultHost) || slices.ContainsFunc(cmd.Env, func(kv string) bool {
		return strings.HasPrefix(kv, "DOCKER_CONTEXT=")
	}) {
		t.Errorf("Env = %q", cmd.Env)
	}
	if r, _ := e.root().table.Selected(); r.Cells[0] != "web" {
		t.Errorf("selection after the shell = %v, want web", r.Cells)
	}
	if s := e.statusLine(); strings.Contains(s, "✗") {
		t.Errorf("status after the shell = %q", s)
	}
}

func TestShellExitStatusIsNotReported(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 3").Run()
	var ee *exec.ExitError
	if !errors.As(exitErr, &ee) {
		t.Skipf("cannot produce an exit error: %v", exitErr)
	}
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/bash")
	e := newEnv(t, f, 100, 12)
	e.execResult = exitErr
	e.selectRow("web")
	e.press("x")
	e.until("shell started", func() bool { return len(e.shells()) == 1 })
	e.settle()
	if s := e.statusLine(); strings.Contains(s, "✗") {
		t.Errorf("status = %q, want no error", s)
	}
}

func TestShellRunFailureIsReported(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/bash")
	e := newEnv(t, f, 100, 12)
	e.execResult = errors.New("fork/exec /usr/bin/docker: permission denied")
	e.selectRow("web")
	e.press("x")
	e.waitStatus("shell in web: fork/exec /usr/bin/docker: permission denied")
}

func TestShellRequiresRunningContainer(t *testing.T) {
	f, _ := sample()
	e := newEnv(t, f, 100, 12)
	e.press("a")
	for _, name := range []string{"old", "frozen"} {
		e.selectRow(name)
		e.press("x")
		e.waitStatus(name + " must be running to open a shell")
	}
	e.settle()
	if n := len(f.CallsTo(dockerfake.MethodExecCreate)); n != 0 || len(e.shells()) != 0 {
		t.Errorf("exec attempted: %d probes, %d shells", n, len(e.shells()))
	}
}

func TestShellMissingCLI(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	e := newEnv(t, f, 100, 12)
	e.root().exec.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	e.selectRow("web")
	e.press("x")
	e.waitStatus("the docker CLI was not found in PATH")
	e.settle()
	if n := len(f.CallsTo(dockerfake.MethodExecCreate)); n != 0 || len(e.shells()) != 0 {
		t.Errorf("exec attempted: %d probes, %d shells", n, len(e.shells()))
	}
}

func TestShellNoShellAvailable(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"]) // neither bash nor sh
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("x")
	e.waitStatus("no shell could be started in web")
	e.settle()
	if len(e.shells()) != 0 {
		t.Error("the TUI was suspended although no shell exists")
	}
}

func TestProbeShellReportsEngineErrors(t *testing.T) {
	t.Parallel()
	f := dockerfake.New()
	id := f.AddContainer(ctr(cid(0), "web", "nginx", container.StateRunning, "Up"))
	f.SetShells(id, "/bin/sh")
	f.FailNext(dockerfake.MethodExecStart, errors.New("connection reset by peer"))
	_, err := probeShell(t.Context(), f, id, time.Millisecond)
	if err == nil || errors.Is(err, errNoShell) || !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("probeShell = %v, want the engine error", err)
	}
	if n := len(f.CallsTo(dockerfake.MethodExecCreate)); n != 1 {
		t.Errorf("%d probes after an engine error, want 1", n)
	}
}

func TestIsExecStartFailure(t *testing.T) {
	t.Parallel()
	for msg, want := range map[string]bool{
		`OCI runtime exec failed: exec failed: unable to start container process: exec: "/bin/bash": stat /bin/bash: no such file or directory: unknown`: true,
		`exec: "sh": executable file not found in $PATH`: true,
		"container 123 is not running":                   false,
		"connection reset by peer":                       false,
	} {
		if got := isExecStartFailure(errors.New(msg)); got != want {
			t.Errorf("isExecStartFailure(%q) = %v", msg, got)
		}
	}
}

func TestShellNotOpenedWhenAnotherViewIsInFront(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	v := e.root()
	// The user opens the logs view while a probe is still running; the
	// probe's result then arrives.
	e.openLogs()
	e.deliver(execProbeMsg{
		view: v.id, name: "web", containerID: ids["web"], dockerPath: "/usr/bin/docker",
		host: dockerfake.DefaultHost, shell: "/bin/sh",
	})
	e.waitStatus("shell in web not opened: another view or dialog is in front")
	if len(e.shells()) != 0 {
		t.Error("the TUI was suspended behind the logs view")
	}
	if !strings.Contains(e.header(), "web > logs") {
		t.Errorf("header = %q, want the logs view still in front", e.header())
	}
}

func TestShellNotOpenedWhileDialogIsOpen(t *testing.T) {
	f, ids := sample()
	e := newEnv(t, f, 100, 16)
	e.selectRow("web")
	e.openConfirm()
	e.deliver(execProbeMsg{
		view: e.root().id, name: "web", containerID: ids["web"], dockerPath: "/usr/bin/docker",
		host: dockerfake.DefaultHost, shell: "/bin/sh",
	})
	e.settle()
	if len(e.shells()) != 0 {
		t.Error("the TUI was suspended over the confirmation dialog")
	}
	if !strings.Contains(e.screen(), "Delete container web?") {
		t.Error("the confirmation dialog was closed")
	}
}

func TestShellIgnoresRepeatedKeyWhileProbing(t *testing.T) {
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	e.press("x", "x", "x")
	e.until("shell started", func() bool { return len(e.shells()) >= 1 })
	e.settle()
	if len(e.shells()) != 1 {
		t.Errorf("%d shells started, want 1", len(e.shells()))
	}
}

func TestShellEndReportsStoppedContainer(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	e := newEnv(t, f, 100, 12)
	e.selectRow("web")
	// The container stops while the shell runs: the CLI exits 1.
	e.root().exec.run = func(cmd *exec.Cmd, done tea.ExecCallback) tea.Cmd {
		return func() tea.Msg {
			e.recordExec(cmd)
			_ = f.ContainerStop(context.Background(), ids["web"])
			return done(exitErr)
		}
	}
	e.press("x")
	e.waitStatus("shell in web ended: the container is exited")
}

func TestShellEndWithRunningContainerIsSilent(t *testing.T) {
	exitErr := exec.Command("sh", "-c", "exit 1").Run()
	f, ids := sample()
	f.SetShells(ids["web"], "/bin/sh")
	e := newEnv(t, f, 100, 12)
	e.execResult = exitErr
	e.selectRow("web")
	f.ResetCalls()
	e.press("x")
	e.until("re-checked", func() bool { return len(f.CallsTo(dockerfake.MethodContainerInspect)) == 1 })
	e.settle()
	if s := e.statusLine(); strings.Contains(s, "✗") {
		t.Errorf("status = %q, want no error", s)
	}
}
