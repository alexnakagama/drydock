package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
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
	msg := ConnectErrorMessage(host, fmt.Errorf("dial: %w", syscall.ENOENT), 5*time.Second)
	for _, want := range []string{host, "may not be running", "no such file or directory"} {
		if !strings.Contains(msg, want) {
			t.Errorf("not-running message %q does not contain %q", msg, want)
		}
	}
	msg = ConnectErrorMessage(host, sdkError{"permission denied while trying to connect"}, 5*time.Second)
	if !strings.Contains(msg, "permission denied for the Docker engine at "+host) {
		t.Errorf("permission message = %q", msg)
	}
	msg = ConnectErrorMessage(host, context.DeadlineExceeded, 5*time.Second)
	if !strings.Contains(msg, "did not respond within 5s") {
		t.Errorf("timeout message = %q", msg)
	}

	// SDK errors repeat the endpoint verbatim; its credentials must not leak.
	const credHost = "tcp://user:secret@127.0.0.1:1"
	msg = ConnectErrorMessage(credHost, sdkError{
		"Cannot connect to the Docker daemon at " + credHost + ". Is the docker daemon running?"}, time.Second)
	if strings.Contains(msg, "secret") || strings.Contains(msg, "user:") {
		t.Errorf("message leaks credentials: %q", msg)
	}
	if !strings.Contains(msg, "at tcp://127.0.0.1:1") {
		t.Errorf("message does not name the redacted endpoint: %q", msg)
	}

	// Credentials in the endpoint and terminal escapes in errors are not printed.
	msg = ConnectErrorMessage("tcp://user:secret@10.0.0.1:2376", errors.New("bad \x1b[31mthing\u202e"), time.Second)
	if strings.Contains(msg, "secret") || strings.ContainsAny(msg, "\x1b\u202e") {
		t.Errorf("message leaks credentials or control characters: %q", msg)
	}
	if !strings.Contains(msg, "tcp://10.0.0.1:2376") {
		t.Errorf("message does not name the redacted endpoint: %q", msg)
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
