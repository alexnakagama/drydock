package docker

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/distribution/reference"
)

// The engine API addresses objects by path segment (/containers/{id},
// /volumes/{name}, /images/{name}), and the SDK builds request paths with
// path.Join, which resolves ".." segments. Identifiers are therefore checked
// before they reach the SDK, so a malformed or hostile value (for example a
// name typed into a prompt) can never address a different endpoint.

// objectNamePattern matches container IDs and names and exec IDs. It is the
// engine's own pattern for container names, which container and exec IDs
// (hex) also satisfy.
var objectNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// maxObjectNameLen bounds identifiers well above anything the engine
// generates (64-character IDs) or users reasonably choose.
const maxObjectNameLen = 255

// ValidateContainerID checks a container ID or name (without the leading
// "/" the engine adds to names).
func ValidateContainerID(id string) error { return validateObjectName("container ID", id) }

// ValidateVolumeName checks a volume name. The empty name, which asks the
// engine to generate one, is accepted only by VolumeCreate and is rejected
// here.
//
// Volume drivers other than "local" may accept names outside the local
// driver's pattern, and such volumes must stay inspectable and removable, so
// only what could change the meaning of the request path is rejected: path
// separators, "." and "..", URL metacharacters and control characters. The
// engine judges the rest.
func ValidateVolumeName(name string) error {
	const kind = "volume name"
	if name == "" {
		return cerrdefs.ErrInvalidArgument.WithMessage("empty " + kind)
	}
	if len(name) > maxObjectNameLen || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\?#%`) || !utf8.ValidString(name) ||
		strings.ContainsFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("invalid %s %q", kind, name))
	}
	return nil
}

// ValidateExecID checks an exec instance ID.
func ValidateExecID(id string) error { return validateObjectName("exec ID", id) }

func validateObjectName(kind, s string) error {
	if s == "" {
		return cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("empty %s", kind))
	}
	if len(s) > maxObjectNameLen || !objectNamePattern.MatchString(s) {
		return cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("invalid %s %q", kind, s))
	}
	return nil
}

// ValidateImageRef accepts image IDs (full, "sha256:"-prefixed, or short)
// and image references such as "nginx", "nginx:1.27",
// "ghcr.io/org/app:v1" or "nginx@sha256:...".
func ValidateImageRef(s string) error {
	if s == "" {
		return cerrdefs.ErrInvalidArgument.WithMessage("empty image reference")
	}
	if _, err := reference.ParseAnyReference(s); err != nil {
		return cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("invalid image reference %q: %v", s, err))
	}
	return nil
}
