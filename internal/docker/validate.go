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
// /volumes/{name}, /images/{name}, /networks/{id}), and the SDK builds
// request paths with path.Join, which resolves ".." segments. Identifiers
// are therefore checked before they reach the SDK, so a malformed or hostile
// value (for example a name typed into a prompt) can never address a
// different endpoint.

// objectNamePattern matches container IDs and names, network IDs and exec
// IDs. It is the engine's own pattern for container names, which container,
// network and exec IDs also satisfy.
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

// ValidateNetworkID checks a network ID. The engine generates them (64 hex
// characters for local networks, 25 lowercase alphanumerics for Swarm
// ones), and both satisfy the container name pattern. Network names may
// contain characters outside it, so drydock addresses networks only by ID.
func ValidateNetworkID(id string) error { return validateObjectName("network ID", id) }

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

// fullImageID matches a full image ID, with or without its algorithm.
var fullImageID = regexp.MustCompile(`^(sha256:)?[0-9a-f]{64}$`)

// NormalizePullRef checks a reference typed by the user for a pull and
// returns it in familiar form with the default tag made explicit: "redis"
// becomes "redis:latest" and "docker.io/library/redis:7" becomes "redis:7".
// A reference with a digest is kept as given (normalized). Anything that is
// not a valid name is rejected, as are image IDs: a full ID, and anything
// starting with "sha256:", which the reference grammar would otherwise read
// as the repository "sha256". A short hex string is a valid repository name
// and is accepted; callers that know the local image IDs should reject
// those themselves.
func NormalizePullRef(s string) (string, error) {
	if s == "" {
		return "", cerrdefs.ErrInvalidArgument.WithMessage("empty image reference")
	}
	if fullImageID.MatchString(s) || strings.HasPrefix(s, "sha256:") {
		return "", cerrdefs.ErrInvalidArgument.WithMessage(
			"that is an image ID; pull takes a reference such as redis:7")
	}
	named, err := reference.ParseNormalizedNamed(s)
	if err != nil {
		return "", cerrdefs.ErrInvalidArgument.WithMessage(fmt.Sprintf("invalid image reference %q: %v", s, err))
	}
	return reference.FamiliarString(reference.TagNameOnly(named)), nil
}

// RepositoryName returns the fully qualified repository of an image
// reference ("nginx:1.27" and "nginx@sha256:..." both give
// "docker.io/library/nginx"), or "" if ref is not a valid reference.
func RepositoryName(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ""
	}
	return named.Name()
}
