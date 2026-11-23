package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode"

	cerrdefs "github.com/containerd/errdefs"
)

// IsNotFound reports whether err means the requested object does not exist.
func IsNotFound(err error) bool { return cerrdefs.IsNotFound(err) }

// IsConflict reports whether err means the engine refused the operation
// because of the object's state (for example, removing an in-use volume).
func IsConflict(err error) bool { return cerrdefs.IsConflict(err) }

// IsInvalidArgument reports whether err means a request was malformed, for
// example an identifier rejected by validation.
func IsInvalidArgument(err error) bool { return cerrdefs.IsInvalidArgument(err) }

// ConnectFailure classifies why the engine could not be reached.
type ConnectFailure int

const (
	// FailureOther is any failure not covered by a more specific kind.
	FailureOther ConnectFailure = iota
	// FailureNotRunning means nothing is listening on the endpoint: the
	// connection was refused or the socket does not exist.
	FailureNotRunning
	// FailurePermission means the user may not access the endpoint.
	FailurePermission
	// FailureTimeout means the engine did not answer in time.
	FailureTimeout
)

// ClassifyConnectError classifies an error returned by Ping.
//
// The SDK does not always wrap the underlying syscall error (a refused
// connection and a permission error are both reported with a fresh message),
// so matching on the wrapped error is complemented by matching the SDK's
// message text.
func ClassifyConnectError(err error) ConnectFailure {
	if err == nil {
		return FailureOther
	}
	msg := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return FailureTimeout
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM),
		strings.Contains(msg, "permission denied"):
		return FailurePermission
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOENT),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such file or directory"),
		strings.Contains(msg, "cannot connect to the docker daemon"):
		return FailureNotRunning
	default:
		return FailureOther
	}
}

// ConnectErrorMessage builds the human-readable message printed when the
// engine at host cannot be reached at startup. timeout is the ping timeout
// that was used. The result is safe to print to a terminal.
func ConnectErrorMessage(host string, err error, timeout time.Duration) string {
	safeHost := RedactHost(host)
	// SDK errors embed the endpoint verbatim, so credentials in it must be
	// scrubbed from the error text too.
	errText := ""
	if err != nil {
		errText = redactCredentials(err.Error(), host)
	}
	var msg string
	switch ClassifyConnectError(err) {
	case FailureNotRunning:
		msg = fmt.Sprintf("cannot connect to the Docker engine at %s: the engine may not be running (%s)", safeHost, errText)
	case FailurePermission:
		msg = fmt.Sprintf("permission denied for the Docker engine at %s: your user cannot access it; "+
			"add it to the docker group or point DOCKER_HOST at a socket you can access (%s)", safeHost, errText)
	case FailureTimeout:
		msg = fmt.Sprintf("the Docker engine at %s did not respond within %s", safeHost, timeout)
	default:
		msg = fmt.Sprintf("cannot connect to the Docker engine at %s: %s", safeHost, errText)
	}
	return SanitizeText(msg)
}

// ErrorText returns err's message made safe for display: the credentials of
// host (the endpoint the error may embed) are removed and terminal control
// characters are replaced. It returns "" for a nil err.
func ErrorText(host string, err error) string {
	if err == nil {
		return ""
	}
	return SanitizeText(redactCredentials(err.Error(), host))
}

// RedactHost removes any user credentials ("user:password@") from an
// endpoint URL so that it can be displayed. It does not rely on URL parsing,
// so malformed endpoints are redacted too.
func RedactHost(host string) string {
	if userinfo := hostUserinfo(host); userinfo != "" {
		return strings.Replace(host, userinfo+"@", "", 1)
	}
	return host
}

// hostUserinfo returns the userinfo part of an endpoint URL (between "://"
// and the last "@" of the authority), or "" if there is none.
func hostUserinfo(host string) string {
	_, rest, ok := strings.Cut(host, "://")
	if !ok {
		return ""
	}
	authority, _, _ := strings.Cut(rest, "/")
	i := strings.LastIndex(authority, "@")
	if i < 0 {
		return ""
	}
	return authority[:i]
}

// redactCredentials removes host's credentials from text, wherever they
// appear.
func redactCredentials(text, host string) string {
	userinfo := hostUserinfo(host)
	if userinfo == "" {
		return text
	}
	text = strings.ReplaceAll(text, host, RedactHost(host))
	return strings.ReplaceAll(text, userinfo+"@", "")
}

// SanitizeText replaces control characters (including ANSI escape
// introducers) with U+FFFD, except newlines and tabs, so that text that came
// from the engine or the environment cannot inject terminal escape
// sequences when printed.
func SanitizeText(s string) string {
	clean := true
	for _, r := range s {
		if isUnsafeRune(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isUnsafeRune(r) {
			return unicode.ReplacementChar
		}
		return r
	}, s)
}

func isUnsafeRune(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	// unicode.IsControl covers C0, DEL and C1 (which includes the 8-bit CSI).
	// Bidi controls can make displayed text read differently from its bytes.
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r)
}
