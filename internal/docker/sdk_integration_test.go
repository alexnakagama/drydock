//go:build integration

package docker

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// These tests run against the engine configured by the environment
// (DOCKER_HOST or the default socket): go test -tags integration ./...
// They only create volumes named "drydock-it-*" and containers labelled
// drydock-it=1 (with the docker CLI, from alpine:latest, which must be present
// or pullable), and remove them afterwards.

func newIntegrationService(t *testing.T) *SDKService {
	t.Helper()
	svc, err := NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatalf("NewSDKService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := svc.Ping(ctx); err != nil {
		t.Fatalf("Ping %s: %v", svc.Host(), err)
	}
	return svc
}

func testVolumeName(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "drydock-it-" + hex.EncodeToString(b)
}

func TestIntegrationPingNegotiatesVersion(t *testing.T) {
	svc, err := NewSDKService("drydock-integration-test", dockerctx.DefaultEndpoint())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	ping, err := svc.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping %s: %v", svc.Host(), err)
	}
	want := ping.APIVersion
	if versions.GreaterThan(want, client.MaxAPIVersion) {
		want = client.MaxAPIVersion // a newer engine still serves older clients
	}
	if got := svc.APIVersion(); got != want {
		t.Errorf("client API version = %s, want %s (engine %s)", got, want, ping.APIVersion)
	}
}

func TestIntegrationListContainers(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	all, err := svc.ContainerList(ctx, ContainerListOptions{All: true})
	if err != nil {
		t.Fatalf("ContainerList(all): %v", err)
	}
	runningOnly, err := svc.ContainerList(ctx, ContainerListOptions{})
	if err != nil {
		t.Fatalf("ContainerList: %v", err)
	}
	if len(runningOnly) > len(all) {
		t.Errorf("running-only list (%d) is longer than the full list (%d)", len(runningOnly), len(all))
	}
	if _, err := svc.ImageList(ctx); err != nil {
		t.Errorf("ImageList: %v", err)
	}
}

func TestIntegrationVolumeLifecycleAndEvents(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	name := testVolumeName(t)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		if err := svc.VolumeRemove(cctx, name, VolumeRemoveOptions{}); err != nil && !IsNotFound(err) {
			t.Errorf("cleanup: remove volume %s: %v", name, err)
		}
	})

	evctx, evcancel := context.WithCancel(ctx)
	defer evcancel()
	es := svc.Events(evctx)

	v, err := svc.VolumeCreate(ctx, name)
	if err != nil {
		t.Fatalf("VolumeCreate: %v", err)
	}
	if v.Name != name || v.Driver != localVolumeDriver {
		t.Errorf("created volume = %+v", v)
	}
	waitForEvent(t, es, events.VolumeEventType, events.ActionCreate, name)

	insp, err := svc.VolumeInspect(ctx, name)
	if err != nil {
		t.Fatalf("VolumeInspect: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(insp.Raw, &raw); err != nil || raw["Name"] != name {
		t.Errorf("raw inspect = %s (%v)", insp.Raw, err)
	}

	vols, err := svc.VolumeList(ctx)
	if err != nil {
		t.Fatalf("VolumeList: %v", err)
	}
	found := false
	for _, vol := range vols {
		found = found || vol.Name == name
	}
	if !found {
		t.Errorf("VolumeList does not include %s", name)
	}

	if err := svc.VolumeRemove(ctx, name, VolumeRemoveOptions{}); err != nil {
		t.Fatalf("VolumeRemove: %v", err)
	}
	waitForEvent(t, es, events.VolumeEventType, events.ActionDestroy, name)
	if _, err := svc.VolumeInspect(ctx, name); !IsNotFound(err) {
		t.Errorf("inspect removed volume: err = %v, want not found", err)
	}

	evcancel()
	select {
	case err := <-es.Err:
		if err == nil {
			t.Error("event stream ended without an error")
		}
	case <-time.After(5 * time.Second):
		t.Error("event stream did not end after cancel")
	}
}

func TestIntegrationRejectsInvalidIdentifiers(t *testing.T) {
	svc := newIntegrationService(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := svc.VolumeRemove(ctx, "../containers/x", VolumeRemoveOptions{}); !IsInvalidArgument(err) {
		t.Errorf("VolumeRemove with traversal: err = %v, want invalid argument", err)
	}
	if _, err := svc.ImageRemove(ctx, "../../containers/x", ImageRemoveOptions{}); !IsInvalidArgument(err) {
		t.Errorf("ImageRemove with traversal: err = %v, want invalid argument", err)
	}
	if st, err := svc.ContainerStats(ctx, "../images/x"); !IsInvalidArgument(err) {
		if st != nil {
			_ = st.Close()
		}
		t.Errorf("ContainerStats with traversal: err = %v, want invalid argument", err)
	}
	if err := svc.ContainerPause(ctx, "../images/x"); !IsInvalidArgument(err) {
		t.Errorf("ContainerPause with traversal: err = %v, want invalid argument", err)
	}
	if err := svc.ContainerUnpause(ctx, "../images/x"); !IsInvalidArgument(err) {
		t.Errorf("ContainerUnpause with traversal: err = %v, want invalid argument", err)
	}
}

// skipOutsideCI skips the test, except in CI, where the docker CLI and a
// local engine socket are expected and their absence must not pass silently.
func skipOutsideCI(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// contextCLI runs the docker CLI against the config directory cfgDir, with
// HOME pointing at an empty directory and the variables that would select
// another engine, context or config directory removed, and fails the test
// if the command fails or takes over 30 seconds.
func contextCLI(t *testing.T, dockerPath, cfgDir string, args ...string) {
	t.Helper()
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "HOME", "DOCKER_CONFIG", "DOCKER_CONTEXT", "DOCKER_HOST", "DOCKER_TLS", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH":
			return true
		}
		return false
	})
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, dockerPath, args...)
	c.Env = append(env, "HOME="+t.TempDir(), "DOCKER_CONFIG="+cfgDir)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// contextStoreHash is the context store's directory name for a context,
// computed here independently of dockerctx so a change on either side shows.
func contextStoreHash(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// checkStoredMeta checks that the CLI wrote the context's metadata where
// dockerctx reads it, with the given docker endpoint host.
func checkStoredMeta(t *testing.T, cfgDir, name, host string) {
	t.Helper()
	path := filepath.Join(cfgDir, "contexts", "meta", contextStoreHash(name), "meta.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("context store layout changed: %v", err)
	}
	var meta struct {
		Name      string `json:"Name"`
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if meta.Name != name || meta.Endpoints["docker"].Host != host {
		t.Errorf("%s = %s, want Name %q and Endpoints.docker.Host %q", path, data, name, host)
	}
}

// writeSelfSignedPEM writes a self-signed ECDSA certificate as ca.pem and
// cert.pem, and its key as key.pem, in a new temporary directory.
func writeSelfSignedPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "drydock-it"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"ca.pem":   certPEM,
		"cert.pem": certPEM,
		"key.pem":  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestIntegrationCurrentContextFromStore creates a context with
// `docker context create` in a temporary config directory, makes it current
// with `docker context use`, and checks that Resolve finds it through
// currentContext alone, that the store layout the CLI wrote is the one
// dockerctx reads, and that the resolved endpoint reaches the engine.
//
// The context points at a symbolic link to the engine's socket that exists
// only for this test, and DOCKER_HOST is unreachable, so the ping succeeds
// only if the client dials the context's host; once the link is removed, a
// new client for the same endpoint must fail to ping.
func TestIntegrationCurrentContextFromStore(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		skipOutsideCI(t, "docker CLI not found")
	}
	def := newIntegrationService(t)
	engineSock, ok := strings.CutPrefix(def.Host(), "unix://")
	if !ok {
		skipOutsideCI(t, "engine at %s is not a local socket", def.Host())
	}

	// os.MkdirTemp keeps the socket path short: unix socket paths are
	// limited to about 104 bytes, which t.TempDir() can exceed.
	sockDir, err := os.MkdirTemp("", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	link := filepath.Join(sockDir, "engine.sock")
	if err := os.Symlink(engineSock, link); err != nil {
		t.Fatal(err)
	}
	host := "unix://" + link
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	const name = "drydock-it"
	cfgDir := t.TempDir()
	contextCLI(t, dockerPath, cfgDir, "context", "create", name, "--docker", "host="+host)
	contextCLI(t, dockerPath, cfgDir, "context", "use", name)
	checkStoredMeta(t, cfgDir, name, host)

	// Resolve sees only DOCKER_CONFIG and an empty HOME, so the context
	// comes from config.json's currentContext and the real ~/.docker is
	// never read.
	env := map[string]string{"DOCKER_CONFIG": cfgDir, "HOME": t.TempDir()}
	ep, err := dockerctx.Resolve(dockerctx.Inputs{Getenv: func(k string) string { return env[k] }})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := dockerctx.Endpoint{Context: name, Host: host, ConfigDir: cfgDir}
	if ep != want {
		t.Fatalf("Resolve = %+v, want %+v", ep, want)
	}

	svc, err := NewSDKService("drydock-integration-test", ep)
	if err != nil {
		t.Fatalf("NewSDKService: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if got := svc.Endpoint(); got != ep {
		t.Errorf("Endpoint() = %+v, want %+v", got, ep)
	}
	if got := svc.Host(); got != host {
		t.Errorf("Host() = %q, want %q", got, host)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := svc.Ping(ctx); err != nil {
		t.Fatalf("Ping %s through context %q: %v", svc.Host(), name, err)
	}

	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	gone, err := NewSDKService("drydock-integration-test", ep)
	if err != nil {
		t.Fatalf("NewSDKService: %v", err)
	}
	defer func() { _ = gone.Close() }()
	if _, err := gone.Ping(ctx); err == nil {
		t.Errorf("Ping through context %q succeeded with its socket removed; the client did not dial %s", name, host)
	}
}

// TestIntegrationTLSContextStoreLayout checks that dockerctx finds the TLS
// material and skip-verify setting of contexts written by
// `docker context create`. The endpoints are never dialled.
func TestIntegrationTLSContextStoreLayout(t *testing.T) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		skipOutsideCI(t, "docker CLI not found")
	}
	const host = "tcp://127.0.0.1:1"
	pemDir := writeSelfSignedPEM(t)
	cfgDir := t.TempDir()
	tlsDir := func(name string) string {
		return filepath.Join(cfgDir, "contexts", "tls", contextStoreHash(name), "docker")
	}

	tests := []struct {
		name, docker string
		want         func(name string) dockerctx.Endpoint
	}{
		{
			name: "drydock-it-tls",
			docker: "host=" + host +
				",ca=" + filepath.Join(pemDir, "ca.pem") +
				",cert=" + filepath.Join(pemDir, "cert.pem") +
				",key=" + filepath.Join(pemDir, "key.pem"),
			want: func(name string) dockerctx.Endpoint {
				d := tlsDir(name)
				return dockerctx.Endpoint{
					Context: name, Host: host, TLS: true, CertDir: d, ConfigDir: cfgDir,
					CA:   filepath.Join(d, "ca.pem"),
					Cert: filepath.Join(d, "cert.pem"),
					Key:  filepath.Join(d, "key.pem"),
				}
			},
		},
		{
			name:   "drydock-it-ca",
			docker: "host=" + host + ",ca=" + filepath.Join(pemDir, "ca.pem"),
			want: func(name string) dockerctx.Endpoint {
				d := tlsDir(name)
				return dockerctx.Endpoint{
					Context: name, Host: host, TLS: true, CertDir: d, ConfigDir: cfgDir,
					CA: filepath.Join(d, "ca.pem"),
				}
			},
		},
		{
			name:   "drydock-it-skip",
			docker: "host=" + host + ",skip-tls-verify=true",
			want: func(name string) dockerctx.Endpoint {
				return dockerctx.Endpoint{Context: name, Host: host, TLS: true, SkipTLSVerify: true, ConfigDir: cfgDir}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contextCLI(t, dockerPath, cfgDir, "context", "create", tt.name, "--docker", tt.docker)
			checkStoredMeta(t, cfgDir, tt.name, host)
			ep, err := dockerctx.Resolve(dockerctx.Inputs{
				Flag:      tt.name,
				ConfigDir: cfgDir,
				Getenv:    func(string) string { return "" },
			})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if want := tt.want(tt.name); ep != want {
				t.Fatalf("Resolve = %+v, want %+v", ep, want)
			}
			// The stored files are the ones given to the CLI.
			for _, f := range []struct{ stored, given string }{
				{ep.CA, "ca.pem"}, {ep.Cert, "cert.pem"}, {ep.Key, "key.pem"},
			} {
				if f.stored == "" {
					continue
				}
				got, err := dockerctx.ReadFile(f.stored)
				if err != nil {
					t.Fatal(err)
				}
				given, err := os.ReadFile(filepath.Join(pemDir, f.given))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, given) {
					t.Errorf("%s differs from the %s given to docker context create", f.stored, f.given)
				}
			}
			// The client accepts the material: a TLS setup error would
			// surface here, not as a connection failure.
			svc, err := NewSDKService("drydock-integration-test", ep)
			if err != nil {
				t.Fatalf("NewSDKService: %v", err)
			}
			_ = svc.Close()
		})
	}
}

// runITContainer starts a labelled alpine:latest container running cmd and
// removes it when the test ends. Only stdout carries the container ID: when
// the image has to be pulled first, docker reports the pull on stderr.
func runITContainer(t *testing.T, cmd ...string) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	args := append([]string{"run", "-d", "--rm", "--label", "drydock-it=1", "alpine:latest"}, cmd...)
	c := exec.Command("docker", args...)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	return id
}

// runExitedITContainer creates a labelled alpine:latest container that has
// run `true` and exited, and removes it when the test ends.
func runExitedITContainer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not found")
	}
	c := exec.Command("docker", "run", "-d", "--label", "drydock-it=1", "alpine:latest", "true")
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		t.Fatalf("docker run: %v: %s", err, stderr.Bytes())
	}
	id := strings.TrimSpace(string(out))
	if err := ValidateContainerID(id); err != nil {
		t.Fatalf("docker run printed %q, want a container ID: %v", out, err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	if out, err := exec.Command("docker", "wait", id).CombinedOutput(); err != nil {
		t.Fatalf("docker wait: %v: %s", err, out)
	}
	return id
}

func TestIntegrationContainerPauseUnpause(t *testing.T) {
	svc := newIntegrationService(t)
	id := runITContainer(t, "sleep", "300")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	inspectState := func(when string) *container.State {
		t.Helper()
		insp, err := svc.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatalf("%s: ContainerInspect: %v", when, err)
		}
		if insp.Container.State == nil {
			t.Fatalf("%s: inspect has no State", when)
		}
		return insp.Container.State
	}

	if err := svc.ContainerPause(ctx, id); err != nil {
		t.Fatalf("ContainerPause: %v", err)
	}
	if st := inspectState("after pause"); !st.Paused || !st.Running {
		t.Errorf("after pause: Running = %v, Paused = %v, want true, true (status %q)", st.Running, st.Paused, st.Status)
	}
	if err := svc.ContainerPause(ctx, id); !IsConflict(err) {
		t.Errorf("pause a paused container: err = %v, want conflict", err)
	}

	if err := svc.ContainerUnpause(ctx, id); err != nil {
		t.Fatalf("ContainerUnpause: %v", err)
	}
	if st := inspectState("after unpause"); !st.Running || st.Paused {
		t.Errorf("after unpause: Running = %v, Paused = %v, want true, false", st.Running, st.Paused)
	}
	// The engine answers this refusal with a 500, not a 409.
	if err := svc.ContainerUnpause(ctx, id); !cerrdefs.IsInternal(err) {
		t.Errorf("unpause a running container: err = %v, want internal", err)
	}

	exitedID := runExitedITContainer(t)
	if err := svc.ContainerPause(ctx, exitedID); !IsConflict(err) {
		t.Errorf("pause an exited container: err = %v, want conflict", err)
	}
}

func TestIntegrationContainerStats(t *testing.T) {
	svc := newIntegrationService(t)
	id := runITContainer(t, "sleep", "300")

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	st, err := svc.ContainerStats(ctx, id)
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	defer func() { _ = st.Close() }()

	type result struct {
		samples []container.StatsResponse
		err     error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		for sample, err := range st.Samples(ctx) {
			if err != nil {
				r.err = err
				break
			}
			r.samples = append(r.samples, sample)
			if len(r.samples) == 2 {
				cancel()
			}
		}
		done <- r
	}()

	var r result
	select {
	case r = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stats iteration did not end after ctx was cancelled")
	}
	if len(r.samples) < 2 {
		t.Fatalf("got %d samples within 3s (err %v), want at least 2", len(r.samples), r.err)
	}
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("iteration ended with %v, want context.Canceled", r.err)
	}
	if got := r.samples[0].PreCPUStats.SystemUsage; got != 0 {
		t.Errorf("first sample PreCPUStats.SystemUsage = %d, want 0", got)
	}
	if r.samples[1].PreCPUStats.SystemUsage == 0 {
		t.Error("second sample PreCPUStats.SystemUsage = 0, want non-zero")
	}
}

func waitForEvent(t *testing.T, es EventStream, typ events.Type, action events.Action, actorID string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-es.Messages:
			if m.Type == typ && m.Action == action && m.Actor.ID == actorID {
				return
			}
		case err := <-es.Err:
			t.Fatalf("event stream ended while waiting for %s/%s %s: %v", typ, action, actorID, err)
		case <-deadline:
			t.Fatalf("timed out waiting for %s/%s %s", typ, action, actorID)
		}
	}
}
