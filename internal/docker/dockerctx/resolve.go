package dockerctx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// maxFileSize caps how much of config.json or meta.json is read. Both are a
// few hundred bytes in practice; the cap keeps a corrupt or hostile file
// from exhausting memory.
const maxFileSize = 1 << 20

// Resolve selects the active context with the docker CLI's precedence and
// returns its endpoint:
//
//  1. in.Flag, when not "";
//  2. $DOCKER_CONTEXT, when not "";
//  3. "default", when $DOCKER_HOST is not "";
//  4. currentContext from config.json in the config directory;
//  5. "default".
//
// config.json is read only when step 4 is reached, and a missing file means
// no current context. The default context never touches the context store.
//
// A named context that does not exist yields an error wrapping ErrNotFound;
// an unreadable or malformed file yields a *FileError; an ssh:// endpoint
// yields an error wrapping ErrSSHUnsupported. Errors about a named context
// are wrapped in a *ContextError carrying its name. Resolve never falls back
// to another context on error.
func Resolve(in Inputs) (Endpoint, error) {
	getenv := in.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	name := in.Flag
	if name == "" {
		name = getenv("DOCKER_CONTEXT")
	}
	if name == "" && getenv("DOCKER_HOST") != "" {
		name = DefaultName
	}

	if name == DefaultName {
		return DefaultEndpoint(), nil
	}

	// Only now is a file needed: config.json when nothing named a context,
	// or the store for the named one.
	dir, err := configDir(in.ConfigDir, getenv)
	if err != nil {
		if name != "" {
			return Endpoint{}, &ContextError{Name: name, Err: err}
		}
		return Endpoint{}, err
	}
	if name == "" {
		current, err := currentContext(dir)
		if err != nil {
			return Endpoint{}, err
		}
		name = current
	}
	if name == "" || name == DefaultName {
		return DefaultEndpoint(), nil
	}

	ep, err := readContext(dir, name)
	if err != nil {
		return Endpoint{}, &ContextError{Name: name, Err: err}
	}
	return ep, nil
}

// errNoHome means no config directory could be found: no override, no
// DOCKER_CONFIG and no home directory.
var errNoHome = errors.New("cannot determine the home directory; set HOME or DOCKER_CONFIG")

// lookupHome finds the home directory when the environment does not name
// one, as the docker CLI does (from the user database). It is a variable so
// tests can stay hermetic.
var lookupHome = func() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.HomeDir
}

// configDir returns the absolute CLI config directory: override, then
// $DOCKER_CONFIG, then the home directory's .docker. The home directory is
// $USERPROFILE then $HOME on Windows, $HOME elsewhere, then the user
// database. Relative values are made absolute once, so every path derived
// from them (including the TLS paths later handed to the docker CLI) stays
// valid if the working directory changes.
//
// When no directory can be found, configDir returns a *FileError rather
// than treating the config as absent: silently using the default engine
// could reach a different engine than the CLI's current context.
func configDir(override string, getenv func(string) string) (string, error) {
	dir := override
	if dir == "" {
		dir = getenv("DOCKER_CONFIG")
	}
	if dir == "" {
		home := ""
		if runtime.GOOS == "windows" {
			home = getenv("USERPROFILE")
		}
		if home == "" {
			home = getenv("HOME")
		}
		if home == "" {
			home = lookupHome()
		}
		if home == "" {
			return "", &FileError{Path: filepath.Join("~", ".docker", "config.json"), Err: errNoHome}
		}
		dir = filepath.Join(home, ".docker")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", &FileError{Path: dir, Err: err}
	}
	return abs, nil
}

// currentContext returns the currentContext field of config.json in dir,
// or "" when the file (or dir) does not exist or sets none.
func currentContext(dir string) (string, error) {
	path := filepath.Join(dir, "config.json")
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", &FileError{Path: path, Err: err}
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", &FileError{Path: path, Err: err}
	}
	return cfg.CurrentContext, nil
}

// storeDir returns the name hash the context store uses as a directory
// name.
func storeDir(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// readContext reads the named context's docker endpoint and TLS material.
func readContext(dir, name string) (Endpoint, error) {
	hash := storeDir(name)
	metaDir := filepath.Join(dir, "contexts", "meta", hash)
	path := filepath.Join(metaDir, "meta.json")
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A meta directory without its meta.json is a broken store, not
		// an unknown name.
		if _, dirErr := os.Lstat(metaDir); dirErr == nil {
			return Endpoint{}, &FileError{Path: path, Err: err}
		}
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, &FileError{Path: path, Err: err}
	}

	var meta struct {
		Endpoints map[string]*struct {
			Host          string `json:"Host"`
			SkipTLSVerify bool   `json:"SkipTLSVerify"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return Endpoint{}, &FileError{Path: path, Err: err}
	}
	docker := meta.Endpoints["docker"]
	if docker == nil {
		return Endpoint{}, &FileError{Path: path, Err: errors.New("no docker endpoint")}
	}
	if hasScheme(docker.Host, "ssh") {
		return Endpoint{}, ErrSSHUnsupported
	}

	ep := Endpoint{
		Context:       name,
		Host:          docker.Host,
		SkipTLSVerify: docker.SkipTLSVerify,
		ConfigDir:     dir,
	}
	if err := readTLS(&ep, filepath.Join(dir, "contexts", "tls", hash, "docker")); err != nil {
		return Endpoint{}, err
	}
	ep.TLS = ep.CertDir != "" || ep.SkipTLSVerify
	return ep, nil
}

// readTLS records which pem files of the context's TLS directory exist. It
// does not read them: the client loads and validates their contents.
func readTLS(ep *Endpoint, certDir string) error {
	var found bool
	for _, f := range []struct {
		name string
		dst  *string
	}{
		{"ca.pem", &ep.CA},
		{"cert.pem", &ep.Cert},
		{"key.pem", &ep.Key},
	} {
		path := filepath.Join(certDir, f.name)
		ok, err := isFile(path)
		if err != nil {
			return &FileError{Path: path, Err: err}
		}
		if ok {
			*f.dst = path
			found = true
		}
	}
	switch {
	case ep.Cert != "" && ep.Key == "":
		return &FileError{Path: ep.Cert, Err: errors.New("certificate has no matching key.pem")}
	case ep.Key != "" && ep.Cert == "":
		return &FileError{Path: ep.Key, Err: errors.New("key has no matching cert.pem")}
	}
	if found {
		ep.CertDir = certDir
	}
	return nil
}

// isFile reports whether path exists. A missing file (or a missing parent
// directory) is (false, nil); a dangling symbolic link, anything that is not
// a regular file, or a path that cannot be inspected is an error, so a
// broken store is reported instead of being read as absent.
func isFile(path string) (bool, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lerr := os.Lstat(path); lerr == nil {
			return false, errors.New("broken symbolic link")
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, errors.New("not a regular file")
	}
	return true, nil
}

// ReadFile reads a file of the context store, such as one of an Endpoint's
// pem files, with the same checks Resolve applies: at most 1 MiB, regular
// files only, and opened without blocking so a FIFO cannot hang the caller.
// A missing file is an error matching fs.ErrNotExist.
func ReadFile(path string) ([]byte, error) { return readFile(path) }

// readFile reads at most maxFileSize bytes of a regular file. The file is
// opened without blocking (see openNoBlock) and its type is checked on the
// open descriptor, so a FIFO or device swapped in for the file is rejected
// instead of hanging startup.
func readFile(path string) ([]byte, error) {
	if ok, err := isFile(path); err != nil {
		return nil, err
	} else if !ok {
		return nil, fs.ErrNotExist
	}
	f, err := openNoBlock(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only; a close error loses nothing
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileSize {
		return nil, fmt.Errorf("larger than %d bytes", maxFileSize)
	}
	return data, nil
}

// hasScheme reports whether host is a URL with the given scheme, ignoring
// case as URL schemes do.
func hasScheme(host, scheme string) bool {
	prefix := scheme + "://"
	return len(host) >= len(prefix) && strings.EqualFold(host[:len(prefix)], prefix)
}
