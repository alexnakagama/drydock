package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

func TestValidateContainerID(t *testing.T) {
	t.Parallel()
	valid := []string{
		"web", "my_app-1.2", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "a",
	}
	invalid := []string{
		"", "/web", "../images/x", "a/b", "web?x=1", "web#x", "-web", ".hidden", "a b", "a\x00b", "ü",
		strings.Repeat("a", 256),
	}
	for _, s := range valid {
		if err := ValidateContainerID(s); err != nil {
			t.Errorf("ValidateContainerID(%q) = %v, want nil", s, err)
		}
		if err := ValidateExecID(s); err != nil {
			t.Errorf("ValidateExecID(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range invalid {
		if err := ValidateContainerID(s); !IsInvalidArgument(err) {
			t.Errorf("ValidateContainerID(%q) = %v, want invalid argument", s, err)
		}
	}
}

func TestValidateVolumeName(t *testing.T) {
	t.Parallel()
	// Plugin drivers may accept names the local driver would not.
	valid := []string{
		"pgdata", "my_vol-1.2", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"a", "-dash", ".hidden", "ünï", "name:with:colons", "user@host",
	}
	invalid := []string{
		"", ".", "..", "../images/x", "a/b", `a\b`, "v?x=1", "v#x", "v%2F", "a b", "a\tb", "a\x00b",
		"esc\x1b", "\xff\xfe", strings.Repeat("a", 256),
	}
	for _, s := range valid {
		if err := ValidateVolumeName(s); err != nil {
			t.Errorf("ValidateVolumeName(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range invalid {
		if err := ValidateVolumeName(s); !IsInvalidArgument(err) {
			t.Errorf("ValidateVolumeName(%q) = %v, want invalid argument", s, err)
		}
	}
}

func TestValidateImageRef(t *testing.T) {
	t.Parallel()
	valid := []string{
		"nginx", "nginx:1.27", "library/nginx:latest", "ghcr.io/org/app:v1", "localhost:5000/app",
		"nginx@sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), "abc123def456",
	}
	invalid := []string{"", "../containers/x", "nginx:../../x", "UPPER", "a b", "nginx:tag?x=1", "/abs"}
	for _, s := range valid {
		if err := ValidateImageRef(s); err != nil {
			t.Errorf("ValidateImageRef(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range invalid {
		if err := ValidateImageRef(s); !IsInvalidArgument(err) {
			t.Errorf("ValidateImageRef(%q) = %v, want invalid argument", s, err)
		}
	}
}

func TestNormalizePullRef(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	ok := map[string]string{
		"redis":                     "redis:latest",
		"redis:7":                   "redis:7",
		"docker.io/library/redis:7": "redis:7",
		"ghcr.io/org/app":           "ghcr.io/org/app:latest",
		"localhost:5000/app:v1":     "localhost:5000/app:v1",
		"nginx@" + digest:           "nginx@" + digest,
	}
	for in, want := range ok {
		if got, err := NormalizePullRef(in); err != nil || got != want {
			t.Errorf("NormalizePullRef(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "UPPER", "a b", "../x", "nginx:../../x", "nginx:tag?x=1",
		strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), "sha256:0123456789ab", "redis\x1b[2J"} {
		if _, err := NormalizePullRef(in); !IsInvalidArgument(err) {
			t.Errorf("NormalizePullRef(%q) = %v, want invalid argument", in, err)
		}
	}
}

func TestRepositoryName(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	for in, want := range map[string]string{
		"nginx":                 "docker.io/library/nginx",
		"nginx:1.27":            "docker.io/library/nginx",
		"nginx@" + digest:       "docker.io/library/nginx",
		"ghcr.io/org/app:v1":    "ghcr.io/org/app",
		"localhost:5000/app:v1": "localhost:5000/app",
		"UPPER":                 "",
		"<none>:<none>":         "",
	} {
		if got := RepositoryName(in); got != want {
			t.Errorf("RepositoryName(%q) = %q, want %q", in, got, want)
		}
	}
}

// sdkError mimics an SDK error that does not wrap its cause.
type sdkError struct{ msg string }

func (e sdkError) Error() string { return e.msg }

func TestClassifyConnectError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want ConnectFailure
	}{
		{"nil", nil, FailureOther},
		{"deadline", fmt.Errorf("ping: %w", context.DeadlineExceeded), FailureTimeout},
		{"wrapped ENOENT", fmt.Errorf("failed to connect: %w", &os.PathError{Op: "dial", Path: "/x.sock", Err: syscall.ENOENT}), FailureNotRunning},
		{"wrapped ECONNREFUSED", fmt.Errorf("x: %w", syscall.ECONNREFUSED), FailureNotRunning},
		{"sdk refused text", sdkError{"Cannot connect to the Docker daemon at unix:///x.sock. Is the docker daemon running?"}, FailureNotRunning},
		{"wrapped EACCES", fmt.Errorf("x: %w", syscall.EACCES), FailurePermission},
		{"sdk permission text", sdkError{"permission denied while trying to connect to the docker API at unix:///var/run/docker.sock"}, FailurePermission},
		{"other", errors.New("tls: handshake failure"), FailureOther},
	}
	for _, tt := range tests {
		if got := ClassifyConnectError(tt.err); got != tt.want {
			t.Errorf("%s: ClassifyConnectError(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestConnectErrorMessage(t *testing.T) {
	t.Parallel()
	const host = "unix:///nonexistent.sock"
	msg := ConnectErrorMessage(dockerctx.DefaultEndpoint(), host, fmt.Errorf("dial: %w", syscall.ENOENT), 5*time.Second)
	for _, want := range []string{host, "may not be running", "no such file or directory"} {
		if !strings.Contains(msg, want) {
			t.Errorf("not-running message %q does not contain %q", msg, want)
		}
	}
	msg = ConnectErrorMessage(dockerctx.DefaultEndpoint(), host, sdkError{"permission denied while trying to connect"}, 5*time.Second)
	if !strings.Contains(msg, "permission denied for the Docker engine at "+host) {
		t.Errorf("permission message = %q", msg)
	}
	msg = ConnectErrorMessage(dockerctx.DefaultEndpoint(), host, context.DeadlineExceeded, 5*time.Second)
	if !strings.Contains(msg, "did not respond within 5s") {
		t.Errorf("timeout message = %q", msg)
	}

	// SDK errors repeat the endpoint verbatim; its credentials must not leak.
	const credHost = "tcp://user:secret@127.0.0.1:1"
	msg = ConnectErrorMessage(dockerctx.DefaultEndpoint(), credHost, sdkError{
		"Cannot connect to the Docker daemon at " + credHost + ". Is the docker daemon running?"}, time.Second)
	if strings.Contains(msg, "secret") || strings.Contains(msg, "user:") {
		t.Errorf("message leaks credentials: %q", msg)
	}
	if !strings.Contains(msg, "at tcp://127.0.0.1:1") {
		t.Errorf("message does not name the redacted endpoint: %q", msg)
	}

	// Credentials in the endpoint and terminal escapes in errors are not printed.
	msg = ConnectErrorMessage(dockerctx.DefaultEndpoint(), "tcp://user:secret@10.0.0.1:2376", errors.New("bad \x1b[31mthing\u202e"), time.Second)
	if strings.Contains(msg, "secret") || strings.ContainsAny(msg, "\x1b\u202e") {
		t.Errorf("message leaks credentials or control characters: %q", msg)
	}
	if !strings.Contains(msg, "tcp://10.0.0.1:2376") {
		t.Errorf("message does not name the redacted endpoint: %q", msg)
	}
}

func TestConnectErrorMessageDefaultContextWording(t *testing.T) {
	t.Parallel()
	// The default context's messages do not change, except for the
	// permission hint, which now mentions Docker contexts.
	const host = "unix:///var/run/docker.sock"
	tests := []struct {
		name string
		ep   dockerctx.Endpoint
		err  error
		want string
	}{
		{"not running", dockerctx.DefaultEndpoint(), fmt.Errorf("dial: %w", syscall.ENOENT),
			"cannot connect to the Docker engine at unix:///var/run/docker.sock: " +
				"the engine may not be running (dial: no such file or directory)"},
		{"timeout", dockerctx.DefaultEndpoint(), context.DeadlineExceeded,
			"the Docker engine at unix:///var/run/docker.sock did not respond within 5s"},
		{"other", dockerctx.DefaultEndpoint(), errors.New("tls: handshake failure"),
			"cannot connect to the Docker engine at unix:///var/run/docker.sock: tls: handshake failure"},
		{"zero endpoint", dockerctx.Endpoint{}, errors.New("tls: handshake failure"),
			"cannot connect to the Docker engine at unix:///var/run/docker.sock: tls: handshake failure"},
		{"permission", dockerctx.DefaultEndpoint(), sdkError{"permission denied while trying to connect"},
			"permission denied for the Docker engine at unix:///var/run/docker.sock: your user cannot access it; " +
				"add your user to the docker group, or point DOCKER_HOST or a Docker context at a socket " +
				"you can access (permission denied while trying to connect)"},
	}
	for _, tt := range tests {
		if got := ConnectErrorMessage(tt.ep, host, tt.err, 5*time.Second); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestConnectErrorMessageNamedContext(t *testing.T) {
	t.Parallel()
	ep := dockerctx.Endpoint{Context: "colima", Host: "unix:///Users/me/.colima/default/docker.sock"}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"not running", fmt.Errorf("dial: %w", syscall.ECONNREFUSED),
			`cannot connect to the Docker engine for context "colima" (unix:///Users/me/.colima/default/docker.sock): ` +
				"the engine may not be running (dial: connection refused)"},
		{"permission", fmt.Errorf("dial: %w", syscall.EACCES),
			`permission denied for the Docker engine for context "colima" (unix:///Users/me/.colima/default/docker.sock): ` +
				"your user cannot access it; add your user to the docker group, or point DOCKER_HOST or a Docker " +
				"context at a socket you can access (dial: permission denied)"},
		{"timeout", context.DeadlineExceeded,
			`the Docker engine for context "colima" (unix:///Users/me/.colima/default/docker.sock) did not respond within 2s`},
	}
	for _, tt := range tests {
		if got := ConnectErrorMessage(ep, ep.Host, tt.err, 2*time.Second); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}

	// Names and endpoints come from files: escapes are replaced and
	// credentials removed.
	evil := dockerctx.Endpoint{Context: "box\x1b]0;pwned\a\u202e", Host: "tcp://u:secret@10.0.0.1:2376"}
	msg := ConnectErrorMessage(evil, evil.Host, errors.New("refused"), time.Second)
	if strings.ContainsAny(msg, "\x1b\a\u202e") || strings.Contains(msg, "secret") {
		t.Errorf("message is not sanitized: %q", msg)
	}
	if !strings.Contains(msg, "tcp://10.0.0.1:2376") || !strings.Contains(msg, "box") {
		t.Errorf("message does not name the context and endpoint: %q", msg)
	}
}

func TestContextErrorMessage(t *testing.T) {
	t.Parallel()
	const (
		meta = "/home/me/.docker/contexts/meta/abc/meta.json"
		cfg  = "/home/me/.docker/config.json"
	)
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"not found", &dockerctx.ContextError{Name: "nope", Err: dockerctx.ErrNotFound},
			`Docker context "nope" was not found; run "docker context ls" to list the available contexts`},
		{"ssh", &dockerctx.ContextError{Name: "box", Err: dockerctx.ErrSSHUnsupported},
			`context "box" uses an ssh:// endpoint, which drydock does not support yet; ` +
				"forward the remote socket (for example with ssh -L) and set DOCKER_HOST to it"},
		{"context file", &dockerctx.ContextError{Name: "colima", Err: &dockerctx.FileError{
			Path: meta, Err: errors.New("unexpected end of JSON input")}},
			`cannot read Docker context "colima": ` + meta + ": unexpected end of JSON input"},
		{"context file path error", &dockerctx.ContextError{Name: "colima", Err: &dockerctx.FileError{
			Path: meta, Err: &fs.PathError{Op: "open", Path: meta, Err: syscall.EACCES}}},
			`cannot read Docker context "colima": ` + meta + ": permission denied"},
		{"config file", &dockerctx.FileError{Path: cfg, Err: errors.New("invalid character 'x' looking for beginning of value")},
			"cannot read the Docker CLI config " + cfg + ": invalid character 'x' looking for beginning of value"},
		{"wrapped", fmt.Errorf("resolve: %w", &dockerctx.ContextError{Name: "nope", Err: dockerctx.ErrNotFound}),
			`Docker context "nope" was not found; run "docker context ls" to list the available contexts`},
		{"other", errors.New("boom"), "cannot resolve the Docker context: boom"},
	}
	for _, tt := range tests {
		if got := ContextErrorMessage(tt.err); got != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func TestContextErrorMessageSanitizes(t *testing.T) {
	t.Parallel()
	const name = "evil\x1b[2J\u009b31m\u202e"
	const path = "/tmp/\x1b[31mcfg/config.json"
	for _, err := range []error{
		&dockerctx.ContextError{Name: name, Err: dockerctx.ErrNotFound},
		&dockerctx.ContextError{Name: name, Err: dockerctx.ErrSSHUnsupported},
		&dockerctx.ContextError{Name: name, Err: &dockerctx.FileError{Path: path, Err: errors.New("bad\x1b[0m")}},
		&dockerctx.FileError{Path: path, Err: errors.New("bad\x07")},
	} {
		msg := ContextErrorMessage(err)
		if strings.ContainsAny(msg, "\x1b\u009b\u202e\x07") {
			t.Errorf("message for %T is not sanitized: %q", err, msg)
		}
		if !strings.Contains(msg, "evil") && !strings.Contains(msg, "cfg/config.json") {
			t.Errorf("message lost the name or path: %q", msg)
		}
	}
}

func TestRedactHost(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"unix:///var/run/docker.sock":     "unix:///var/run/docker.sock",
		"tcp://10.0.0.1:2376":             "tcp://10.0.0.1:2376",
		"tcp://user:secret@10.0.0.1:2376": "tcp://10.0.0.1:2376",
		"tcp://user:p%zz@host:2376/base":  "tcp://host:2376/base", // does not parse as a URL
		"ssh://me@host":                   "ssh://host",
		"tcp://u:p@ss@host":               "tcp://host",
		"tcp://host/path@with-at":         "tcp://host/path@with-at",
		"not a url":                       "not a url",
	}
	for in, want := range tests {
		if got := RedactHost(in); got != want {
			t.Errorf("RedactHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeText(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"plain text":          "plain text",
		"tab\tand\nnewline":   "tab\tand\nnewline",
		"esc\x1b[2Jclear":     "esc�[2Jclear",
		"c1 \u009b csi":       "c1 � csi",
		"bell\a del\x7f":      "bell� del�",
		"rtl \u202eoverride":  "rtl �override",
		"cr\rcarriage":        "cr�carriage",
		"unicode ünïcödé 日本語": "unicode ünïcödé 日本語",
	}
	for in, want := range tests {
		if got := SanitizeText(in); got != want {
			t.Errorf("SanitizeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	const host = "tcp://user:secret@10.0.0.1:2376"
	err := fmt.Errorf("error during connect: Get \"%s/v1.57/events\": dial tcp: \x1b[31mrefused", host)
	got := ErrorText(host, err)
	if strings.Contains(got, "secret") || strings.Contains(got, "user:") {
		t.Errorf("ErrorText leaks credentials: %q", got)
	}
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("ErrorText keeps an escape character: %q", got)
	}
	if !strings.Contains(got, "tcp://10.0.0.1:2376") {
		t.Errorf("ErrorText lost the redacted endpoint: %q", got)
	}
	if got := ErrorText(host, nil); got != "" {
		t.Errorf("ErrorText(nil) = %q, want empty", got)
	}
}

// setStrayDockerEnv sets the environment variables FromEnv reads to values
// that would break or redirect a client built from them, so a test can show
// that a named context ignores them.
func setStrayDockerEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DOCKER_HOST", "tcp://stray.invalid:2376")
	t.Setenv("DOCKER_TLS_VERIFY", "1")
	t.Setenv("DOCKER_CERT_PATH", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("DOCKER_API_VERSION", "")
}

func newTestService(t *testing.T, ep dockerctx.Endpoint) *SDKService {
	t.Helper()
	svc, err := NewSDKService("drydock-test", ep)
	if err != nil {
		t.Fatalf("NewSDKService(%+v): %v", ep, err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc
}

func TestNewSDKServiceNamedContextIgnoresEnvironment(t *testing.T) {
	setStrayDockerEnv(t)
	ep := dockerctx.Endpoint{Context: "colima", Host: "unix:///Users/me/.colima/default/docker.sock"}
	svc := newTestService(t, ep)
	if got := svc.Host(); got != ep.Host {
		t.Errorf("Host() = %q, want %q", got, ep.Host)
	}
	if got := svc.Endpoint(); got != ep {
		t.Errorf("Endpoint() = %+v, want %+v", got, ep)
	}
}

func TestNewSDKServiceNamedContextEmptyHost(t *testing.T) {
	setStrayDockerEnv(t)
	svc := newTestService(t, dockerctx.Endpoint{Context: "local"})
	if got := svc.Host(); got != client.DefaultDockerHost {
		t.Errorf("Host() = %q, want the platform default %q", got, client.DefaultDockerHost)
	}

}

// TestNewSDKServiceTLSEmptyHostReachesDefaultSocket checks that a TLS
// context without a host still dials the default socket: the TLS transport
// replaces the one the SDK configured for it.
func TestNewSDKServiceTLSEmptyHostReachesDefaultSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the default endpoint is a named pipe on Windows")
	}
	setStrayDockerEnv(t)
	// A short directory keeps the socket path under the unix limit.
	dir, err := os.MkdirTemp("", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	var sawClientCert atomic.Bool
	srv := httptest.NewUnstartedServer(pingHandler(&sawClientCert))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.StartTLS()
	t.Cleanup(srv.Close)

	old := defaultSocket
	defaultSocket = "unix://" + sock
	t.Cleanup(func() { defaultSocket = old })

	svc := newTestService(t, dockerctx.Endpoint{Context: "local-tls", TLS: true, SkipTLSVerify: true})
	if got := svc.Host(); got != "unix://"+sock {
		t.Errorf("Host() = %q, want the default socket %q", got, "unix://"+sock)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := svc.Ping(ctx); err != nil {
		t.Fatalf("Ping over TLS on the default socket: %v", err)
	}
}

func TestNewSDKServiceZeroEndpointIsDefault(t *testing.T) {
	const host = "unix:///run/user/1000/docker.sock"
	t.Setenv("DOCKER_HOST", host)
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	t.Setenv("DOCKER_API_VERSION", "")
	for _, ep := range []dockerctx.Endpoint{{}, dockerctx.DefaultEndpoint()} {
		svc := newTestService(t, ep)
		if got := svc.Host(); got != host {
			t.Errorf("%+v: Host() = %q, want DOCKER_HOST %q", ep, got, host)
		}
		if got := svc.Endpoint(); got != ep {
			t.Errorf("Endpoint() = %+v, want %+v", got, ep)
		}
	}
}

func TestNewSDKServiceAPIVersionFromEnv(t *testing.T) {
	setStrayDockerEnv(t)
	t.Setenv("DOCKER_API_VERSION", "1.44")
	svc := newTestService(t, dockerctx.Endpoint{Context: "colima", Host: "unix:///tmp/colima.sock"})
	if got := svc.APIVersion(); got != "1.44" {
		t.Errorf("APIVersion() = %q, want the pinned 1.44", got)
	}
}

func TestNewSDKServicePEMFiles(t *testing.T) {
	setStrayDockerEnv(t)
	dir := t.TempDir()
	ca := newTestCA(t, "ca")
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	goodCA := write("ca.pem", ca.certPEM)
	certPEM, keyPEM := ca.issue(t, "client", nil, x509.ExtKeyUsageClientAuth)
	goodCert := write("cert.pem", certPEM)
	goodKey := write("key.pem", keyPEM)
	garbage := write("garbage.pem", []byte("not a pem file"))
	missing := filepath.Join(dir, "missing.pem")
	unreadable := write("unreadable.pem", ca.certPEM)
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}

	named := func(ca, cert, key string) dockerctx.Endpoint {
		return dockerctx.Endpoint{Context: "remote", Host: "tcp://127.0.0.1:2376", TLS: true,
			CertDir: dir, CA: ca, Cert: cert, Key: key}
	}
	bad := []struct {
		name string
		ep   dockerctx.Endpoint
		path string
	}{
		{"missing CA", named(missing, "", ""), missing},
		{"invalid CA", named(garbage, "", ""), garbage},
		{"missing cert", named(goodCA, missing, goodKey), missing},
		{"missing key", named(goodCA, goodCert, missing), missing},
		{"invalid cert", named(goodCA, garbage, goodKey), garbage},
		{"invalid key", named(goodCA, goodCert, garbage), garbage},
		{"cert without key", named(goodCA, goodCert, ""), goodCert},
		{"key without cert", named(goodCA, "", goodKey), goodKey},
	}
	if os.Geteuid() != 0 { // root reads files regardless of mode
		bad = append(bad, struct {
			name string
			ep   dockerctx.Endpoint
			path string
		}{"unreadable CA", named(unreadable, "", ""), unreadable})
	}
	for _, tt := range bad {
		_, err := NewSDKService("", tt.ep)
		if err == nil {
			t.Errorf("%s: NewSDKService succeeded, want an error", tt.name)
			continue
		}
		if !strings.Contains(err.Error(), tt.path) {
			t.Errorf("%s: error %q does not name %s", tt.name, err, tt.path)
		}
		if strings.Contains(err.Error(), "PRIVATE KEY") {
			t.Errorf("%s: error leaks key material: %q", tt.name, err)
		}
	}

	// Absent files are skipped: a CA alone, or skip-verify with no files.
	for _, ep := range []dockerctx.Endpoint{
		named(goodCA, "", ""),
		named("", goodCert, goodKey),
		{Context: "insecure", Host: "tcp://127.0.0.1:2376", TLS: true, SkipTLSVerify: true},
	} {
		newTestService(t, ep)
	}
}

func TestNewSDKServiceTLS(t *testing.T) {
	setStrayDockerEnv(t)
	serverCA := newTestCA(t, "server-ca")
	otherCA := newTestCA(t, "other-ca")
	clientCA := newTestCA(t, "client-ca")
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	serverCAFile := write("server-ca.pem", serverCA.certPEM)
	otherCAFile := write("other-ca.pem", otherCA.certPEM)
	clientCertPEM, clientKeyPEM := clientCA.issue(t, "client", nil, x509.ExtKeyUsageClientAuth)
	clientCert := write("cert.pem", clientCertPEM)
	clientKey := write("key.pem", clientKeyPEM)

	serverCertPEM, serverKeyPEM := serverCA.issue(t, "127.0.0.1", []net.IP{net.IPv4(127, 0, 0, 1)}, x509.ExtKeyUsageServerAuth)
	serverPair, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	var sawClientCert atomic.Bool
	startServer := func(requireClientCert bool) string {
		t.Helper()
		srv := httptest.NewUnstartedServer(pingHandler(&sawClientCert))
		srv.Config.ErrorLog = log.New(io.Discard, "", 0) // expected handshake failures
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{serverPair}, MinVersion: tls.VersionTLS12}
		if requireClientCert {
			pool := x509.NewCertPool()
			pool.AddCert(clientCA.cert)
			srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
			srv.TLS.ClientCAs = pool
		}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return "tcp://" + srv.Listener.Addr().String()
	}
	plain := startServer(false)
	mtls := startServer(true)

	const (
		ok         = ""
		verifyFail = "verify"      // the client rejects the engine's certificate
		clientFail = "client-cert" // the engine rejects the missing client certificate
	)
	tests := []struct {
		name           string
		ep             dockerctx.Endpoint
		want           string
		wantClientCert bool
	}{
		{"server CA verifies", dockerctx.Endpoint{Host: plain, CA: serverCAFile}, ok, false},
		{"other CA fails", dockerctx.Endpoint{Host: plain, CA: otherCAFile}, verifyFail, false},
		{"no CA, system roots fail", dockerctx.Endpoint{Host: plain}, verifyFail, false},
		{"no CA, skip verify", dockerctx.Endpoint{Host: plain, SkipTLSVerify: true}, ok, false},
		{"mTLS without client cert", dockerctx.Endpoint{Host: mtls, CA: serverCAFile}, clientFail, false},
		{"mTLS with client cert", dockerctx.Endpoint{Host: mtls, CA: serverCAFile, Cert: clientCert, Key: clientKey}, ok, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sawClientCert.Store(false)
			ep := tt.ep
			ep.Context = "remote"
			ep.TLS = true
			if ep.CA != "" || ep.Cert != "" {
				ep.CertDir = dir
			}
			svc := newTestService(t, ep)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			res, err := svc.Ping(ctx)
			switch tt.want {
			case ok:
				if err != nil {
					t.Fatalf("Ping: %v", err)
				}
				if res.OSType != "linux" {
					t.Errorf("Ping OSType = %q, want linux", res.OSType)
				}
				if got := sawClientCert.Load(); got != tt.wantClientCert {
					t.Errorf("engine saw a verified client certificate = %v, want %v", got, tt.wantClientCert)
				}
			case verifyFail:
				var verr *tls.CertificateVerificationError
				if !errors.As(err, &verr) {
					t.Fatalf("Ping error = %v, want a certificate verification error", err)
				}
			case clientFail:
				msg := fmt.Sprint(err)
				if err == nil || (!strings.Contains(msg, "certificate required") && !strings.Contains(msg, "bad certificate")) {
					t.Fatalf("Ping error = %v, want the engine to reject the missing client certificate", err)
				}
			}
		})
	}
}

// pingHandler answers the engine's /_ping like an engine does, and records
// whether the request came with a verified client certificate.
func pingHandler(sawClientCert *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			http.NotFound(w, r)
			return
		}
		if r.TLS != nil && len(r.TLS.VerifiedChains) > 0 {
			sawClientCert.Store(true)
		}
		w.Header().Set("Api-Version", "1.47")
		w.Header().Set("Ostype", "linux")
		_, _ = io.WriteString(w, "OK")
	})
}

// testCA is a self-signed certificate authority for TLS tests.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue returns a certificate and key signed by the CA, as PEM.
func (ca *testCA) issue(t *testing.T, name string, ips []net.IP, usage x509.ExtKeyUsage) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}
