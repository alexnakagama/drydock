package dockerctx

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// env returns a Getenv backed by m, so no test reads the real environment.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, dir, current string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"currentContext": current, "auths": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "config.json"), string(data))
}

func metaPath(dir, name string) string {
	return filepath.Join(dir, "contexts", "meta", storeDir(name), "meta.json")
}

func tlsDir(dir, name string) string {
	return filepath.Join(dir, "contexts", "tls", storeDir(name), "docker")
}

// writeContext writes a context's meta.json as `docker context create` does,
// and the given pem files into its TLS directory.
func writeContext(t *testing.T, dir, name, host string, skipVerify bool, pems ...string) {
	t.Helper()
	docker := map[string]any{"SkipTLSVerify": skipVerify}
	if host != "" {
		docker["Host"] = host
	}
	data, err := json.Marshal(map[string]any{
		"Name":      name,
		"Metadata":  map[string]any{},
		"Endpoints": map[string]any{"docker": docker},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, metaPath(dir, name), string(data))
	for _, p := range pems {
		writeFile(t, filepath.Join(tlsDir(dir, name), p), "-----BEGIN TEST-----\n")
	}
}

func TestEndpointIsDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		ep   Endpoint
		want bool
	}{
		{Endpoint{}, true},
		{DefaultEndpoint(), true},
		{Endpoint{Context: "default", Host: "ignored"}, true},
		{Endpoint{Context: "colima"}, false},
		{Endpoint{Context: "Default"}, false},
	} {
		if got := tc.ep.IsDefault(); got != tc.want {
			t.Errorf("%+v.IsDefault() = %v, want %v", tc.ep, got, tc.want)
		}
	}
	if got := DefaultEndpoint(); got != (Endpoint{Context: "default"}) {
		t.Errorf("DefaultEndpoint() = %+v", got)
	}
}

// TestResolvePrecedence covers the "Active context selection" scenarios and
// the precedence edge cases around them.
func TestResolvePrecedence(t *testing.T) {
	t.Parallel()
	const (
		colimaHost = "unix:///Users/me/.colima/default/docker.sock"
		remoteHost = "tcp://remote.example:2375"
	)
	// store writes a config dir with colima as the current context and the
	// contexts colima and remote.
	store := func(t *testing.T) string {
		dir := t.TempDir()
		writeConfig(t, dir, "colima")
		writeContext(t, dir, "colima", colimaHost, false)
		writeContext(t, dir, "remote", remoteHost, false)
		return dir
	}

	tests := []struct {
		name     string
		flag     string
		env      map[string]string // DOCKER_CONFIG is set to the store unless given
		noConfig bool              // the config dir has no config.json
		badCfg   bool              // config.json is not valid JSON
		want     Endpoint
	}{
		{
			name: "current context from the CLI config",
			want: Endpoint{Context: "colima", Host: colimaHost},
		},
		{
			name: "DOCKER_CONTEXT selects a context",
			env:  map[string]string{"DOCKER_CONTEXT": "remote"},
			want: Endpoint{Context: "remote", Host: remoteHost},
		},
		{
			name: "DOCKER_CONTEXT takes precedence over DOCKER_HOST",
			env:  map[string]string{"DOCKER_CONTEXT": "colima", "DOCKER_HOST": "tcp://other:2375"},
			want: Endpoint{Context: "colima", Host: colimaHost},
		},
		{
			name: "flag takes precedence over the environment",
			flag: "remote",
			env:  map[string]string{"DOCKER_CONTEXT": "colima", "DOCKER_HOST": "tcp://other:2375"},
			want: Endpoint{Context: "remote", Host: remoteHost},
		},
		{
			name: "explicit default context",
			flag: "default",
			env:  map[string]string{"DOCKER_HOST": "tcp://other:2375"},
			want: DefaultEndpoint(),
		},
		{
			name: "explicit default context beats DOCKER_CONTEXT",
			flag: "default",
			env:  map[string]string{"DOCKER_CONTEXT": "colima"},
			want: DefaultEndpoint(),
		},
		{
			name: "DOCKER_CONTEXT=default",
			env:  map[string]string{"DOCKER_CONTEXT": "default"},
			want: DefaultEndpoint(),
		},
		{
			name: "DOCKER_HOST override ignores the current context",
			env:  map[string]string{"DOCKER_HOST": "unix:///run/user/1000/docker.sock"},
			want: DefaultEndpoint(),
		},
		{
			name:   "DOCKER_HOST set and config.json malformed",
			env:    map[string]string{"DOCKER_HOST": "tcp://other:2375"},
			badCfg: true,
			want:   DefaultEndpoint(),
		},
		{
			name: "empty flag counts as not given",
			flag: "",
			env:  map[string]string{"DOCKER_CONTEXT": "remote"},
			want: Endpoint{Context: "remote", Host: remoteHost},
		},
		{
			name:     "default socket: nothing selects a context",
			noConfig: true,
			want:     DefaultEndpoint(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := store(t)
			switch {
			case tc.noConfig:
				if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
					t.Fatal(err)
				}
			case tc.badCfg:
				writeFile(t, filepath.Join(dir, "config.json"), "{not json")
			}
			e := map[string]string{"DOCKER_CONFIG": dir, "HOME": t.TempDir()}
			for k, v := range tc.env {
				e[k] = v
			}
			got, err := Resolve(Inputs{Flag: tc.flag, Getenv: env(e)})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("Resolve = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveConfigDir(t *testing.T) {
	t.Parallel()

	t.Run("custom config directory", func(t *testing.T) {
		t.Parallel()
		custom, home := t.TempDir(), t.TempDir()
		writeConfig(t, custom, "colima")
		writeContext(t, custom, "colima", "unix:///custom.sock", false)
		// A different current context under HOME must not be used.
		writeConfig(t, filepath.Join(home, ".docker"), "other")
		got, err := Resolve(Inputs{Getenv: env(map[string]string{"DOCKER_CONFIG": custom, "HOME": home})})
		if err != nil {
			t.Fatal(err)
		}
		if got.Context != "colima" || got.Host != "unix:///custom.sock" {
			t.Errorf("Resolve = %+v, want colima under DOCKER_CONFIG", got)
		}
	})

	t.Run("HOME from Getenv", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		dir := filepath.Join(home, ".docker")
		writeConfig(t, dir, "colima")
		writeContext(t, dir, "colima", "unix:///home.sock", false)
		got, err := Resolve(Inputs{Getenv: env(map[string]string{"HOME": home, "USERPROFILE": home})})
		if err != nil {
			t.Fatal(err)
		}
		if got.Context != "colima" || got.Host != "unix:///home.sock" {
			t.Errorf("Resolve = %+v, want colima under HOME/.docker", got)
		}
	})

	t.Run("Inputs.ConfigDir beats DOCKER_CONFIG", func(t *testing.T) {
		t.Parallel()
		override, envDir := t.TempDir(), t.TempDir()
		writeConfig(t, override, "colima")
		writeContext(t, override, "colima", "unix:///override.sock", false)
		writeFile(t, filepath.Join(envDir, "config.json"), "{broken")
		got, err := Resolve(Inputs{ConfigDir: override, Getenv: env(map[string]string{"DOCKER_CONFIG": envDir})})
		if err != nil {
			t.Fatal(err)
		}
		if got.Host != "unix:///override.sock" {
			t.Errorf("Resolve = %+v, want the override directory", got)
		}
	})

	t.Run("home directory from the platform", func(t *testing.T) {
		t.Parallel()
		home, other := t.TempDir(), t.TempDir()
		dir := filepath.Join(home, ".docker")
		writeConfig(t, dir, "colima")
		writeContext(t, dir, "colima", "unix:///platform.sock", false)
		writeFile(t, filepath.Join(other, ".docker", "config.json"), "{broken")
		// The docker CLI uses USERPROFILE on Windows, even when a shell
		// such as Git Bash sets HOME; elsewhere it uses HOME.
		e := map[string]string{"HOME": home, "USERPROFILE": other}
		if runtime.GOOS == "windows" {
			e = map[string]string{"HOME": other, "USERPROFILE": home}
		}
		got, err := Resolve(Inputs{Getenv: env(e)})
		if err != nil || got.Host != "unix:///platform.sock" {
			t.Errorf("Resolve = %+v, %v; want colima under the platform's home", got, err)
		}
	})

	t.Run("relative DOCKER_CONFIG is made absolute", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeContext(t, dir, "remote", "tcp://h:2376", false, "ca.pem")
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(wd, dir)
		if err != nil {
			t.Skipf("no relative path from %s to %s: %v", wd, dir, err)
		}
		got, err := Resolve(Inputs{Flag: "remote", Getenv: env(map[string]string{"DOCKER_CONFIG": rel})})
		if err != nil {
			t.Fatal(err)
		}
		if !filepath.IsAbs(got.CA) || !filepath.IsAbs(got.CertDir) {
			t.Errorf("CA %q and CertDir %q, want absolute paths", got.CA, got.CertDir)
		}
	})

	t.Run("nonexistent DOCKER_CONFIG", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "absent")
		got, err := Resolve(Inputs{Getenv: env(map[string]string{"DOCKER_CONFIG": missing})})
		if err != nil || got != DefaultEndpoint() {
			t.Errorf("Resolve = %+v, %v; want default, nil", got, err)
		}
		_, err = Resolve(Inputs{Flag: "colima", Getenv: env(map[string]string{"DOCKER_CONFIG": missing})})
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("config directory is a file", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "notadir")
		writeFile(t, file, "x")
		_, err := Resolve(Inputs{ConfigDir: file, Getenv: env(nil)})
		var fe *FileError
		if !errors.As(err, &fe) {
			t.Errorf("err = %v, want *FileError", err)
		}
	})

	t.Run("config.json without currentContext", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.json"), `{"auths":{}}`)
		got, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		if err != nil || got != DefaultEndpoint() {
			t.Errorf("Resolve = %+v, %v; want default, nil", got, err)
		}
	})

	t.Run("currentContext is default", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeConfig(t, dir, "default")
		got, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		if err != nil || got != DefaultEndpoint() {
			t.Errorf("Resolve = %+v, %v; want default, nil", got, err)
		}
	})
}

func TestStoreDirHash(t *testing.T) {
	t.Parallel()
	// Expected values from `printf '<name>' | sha256sum`.
	for name, want := range map[string]string{
		"colima":   "f24fd3749c1368328e2b149bec149cb6795619f244c5b584e844961215dadd16",
		"my ctx ü": "dc7898c793186e26b49274c182f81540d45be1a9b6d49223ec5b30686e6f7d77",
	} {
		if got := storeDir(name); got != want {
			t.Errorf("storeDir(%q) = %s, want %s", name, got, want)
		}
	}
}

// TestResolveNamedContext covers reading a context's endpoint and TLS
// material from the store.
func TestResolveNamedContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ctx        string
		host       string
		skipVerify bool
		pems       []string
		// want is the expected endpoint; CertDir, CA, Cert and Key are
		// filled in from wantPems when the test runs.
		want     Endpoint
		wantPems []string
	}{
		{
			name: "unix context",
			ctx:  "colima", host: "unix:///Users/me/.colima/default/docker.sock",
			want: Endpoint{Host: "unix:///Users/me/.colima/default/docker.sock"},
		},
		{
			name: "tcp context with a full TLS directory",
			ctx:  "remote", host: "tcp://h:2376", pems: []string{"ca.pem", "cert.pem", "key.pem"},
			want:     Endpoint{Host: "tcp://h:2376", TLS: true},
			wantPems: []string{"ca.pem", "cert.pem", "key.pem"},
		},
		{
			name: "only ca.pem",
			ctx:  "ca-only", host: "tcp://h:2376", pems: []string{"ca.pem"},
			want:     Endpoint{Host: "tcp://h:2376", TLS: true},
			wantPems: []string{"ca.pem"},
		},
		{
			name: "client certificate without a CA",
			ctx:  "client", host: "tcp://h:2376", pems: []string{"cert.pem", "key.pem"},
			want:     Endpoint{Host: "tcp://h:2376", TLS: true},
			wantPems: []string{"cert.pem", "key.pem"},
		},
		{
			name: "skip verify with a TLS directory",
			ctx:  "skip-dir", host: "tcp://h:2376", skipVerify: true, pems: []string{"cert.pem", "key.pem"},
			want:     Endpoint{Host: "tcp://h:2376", TLS: true, SkipTLSVerify: true},
			wantPems: []string{"cert.pem", "key.pem"},
		},
		{
			// As written by `docker context create ts --docker
			// host=tcp://h:2376,skip-tls-verify=true`: no TLS directory.
			name: "skip verify without a TLS directory",
			ctx:  "skip", host: "tcp://h:2376", skipVerify: true,
			want: Endpoint{Host: "tcp://h:2376", TLS: true, SkipTLSVerify: true},
		},
		{
			// `docker context create e --docker host=` omits Host.
			name: "empty Host means the default socket",
			ctx:  "empty",
			want: Endpoint{},
		},
		{
			name: "name with spaces and non-ASCII characters",
			ctx:  "my ctx ü", host: "unix:///weird.sock",
			want: Endpoint{Host: "unix:///weird.sock"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeContext(t, dir, tc.ctx, tc.host, tc.skipVerify, tc.pems...)
			want := tc.want
			want.Context = tc.ctx
			if len(tc.wantPems) > 0 {
				want.CertDir = tlsDir(dir, tc.ctx)
			}
			for _, p := range tc.wantPems {
				path := filepath.Join(tlsDir(dir, tc.ctx), p)
				switch p {
				case "ca.pem":
					want.CA = path
				case "cert.pem":
					want.Cert = path
				case "key.pem":
					want.Key = path
				}
			}
			got, err := Resolve(Inputs{Flag: tc.ctx, ConfigDir: dir, Getenv: env(nil)})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != want {
				t.Errorf("Resolve =\n  %+v\nwant\n  %+v", got, want)
			}
			if got.TLS != (got.CertDir != "" || got.SkipTLSVerify) {
				t.Errorf("TLS = %v, inconsistent with CertDir %q and SkipTLSVerify %v", got.TLS, got.CertDir, got.SkipTLSVerify)
			}
		})
	}
}

func TestResolveUnpairedClientCert(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ present, missing string }{
		{"cert.pem", "key.pem"},
		{"key.pem", "cert.pem"},
	} {
		t.Run(tc.present+" without "+tc.missing, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeContext(t, dir, "half", "tcp://h:2376", false, "ca.pem", tc.present)
			_, err := Resolve(Inputs{Flag: "half", ConfigDir: dir, Getenv: env(nil)})
			var fe *FileError
			if !errors.As(err, &fe) {
				t.Fatalf("err = %v, want *FileError", err)
			}
			if want := filepath.Join(tlsDir(dir, "half"), tc.present); fe.Path != want {
				t.Errorf("FileError.Path = %q, want %q", fe.Path, want)
			}
			if !strings.Contains(fe.Err.Error(), tc.missing) {
				t.Errorf("FileError.Err = %q, want it to name %s", fe.Err, tc.missing)
			}
			assertContextName(t, err, "half")
		})
	}
}

// assertContextName checks that err is a *ContextError naming the context.
func assertContextName(t *testing.T, err error, name string) {
	t.Helper()
	var ce *ContextError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v, want a *ContextError", err)
	}
	if ce.Name != name {
		t.Errorf("ContextError.Name = %q, want %q", ce.Name, name)
	}
}

// TestResolveFailures covers the "Context resolution failures" scenarios.
func TestResolveFailures(t *testing.T) {
	t.Parallel()

	t.Run("unknown context", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeContext(t, dir, "colima", "unix:///c.sock", false)
		for _, in := range []Inputs{
			{Flag: "nope", ConfigDir: dir, Getenv: env(nil)},
			{ConfigDir: dir, Getenv: env(map[string]string{"DOCKER_CONTEXT": "nope"})},
		} {
			got, err := Resolve(in)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want ErrNotFound", err)
			}
			if got != (Endpoint{}) {
				t.Errorf("endpoint on error = %+v, want zero", got)
			}
			assertContextName(t, err, "nope")
		}
	})

	t.Run("unknown current context", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeConfig(t, dir, "gone")
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		assertContextName(t, err, "gone")
	})

	fileErr := func(t *testing.T, err error, path string) {
		t.Helper()
		var fe *FileError
		if !errors.As(err, &fe) {
			t.Fatalf("err = %v, want *FileError", err)
		}
		if fe.Path != path {
			t.Errorf("FileError.Path = %q, want %q", fe.Path, path)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("err %q does not name %s", err, path)
		}
	}

	t.Run("malformed config.json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"currentContext": `)
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
		var ce *ContextError
		if errors.As(err, &ce) {
			t.Errorf("config.json error wrapped in ContextError %q; no context is known yet", ce.Name)
		}
	})

	t.Run("config.json with a non-string currentContext", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"currentContext": 7}`)
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
	})

	t.Run("config.json is a directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
	})

	t.Run("oversized config.json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		writeFile(t, path, `{"x":"`+strings.Repeat("a", maxFileSize)+`"}`)
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
	})

	t.Run("unreadable config.json", func(t *testing.T) {
		t.Parallel()
		skipIfPermissionsIgnored(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")
		writeConfig(t, dir, "colima")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
	})

	t.Run("malformed meta.json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := metaPath(dir, "colima")
		writeFile(t, path, "not json")
		_, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
		assertContextName(t, err, "colima")
	})

	t.Run("unreadable meta.json", func(t *testing.T) {
		t.Parallel()
		skipIfPermissionsIgnored(t)
		dir := t.TempDir()
		writeContext(t, dir, "colima", "unix:///c.sock", false)
		path := metaPath(dir, "colima")
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
		assertContextName(t, err, "colima")
	})

	for name, body := range map[string]string{
		"no Endpoints":         `{"Name":"colima","Metadata":{}}`,
		"no docker endpoint":   `{"Name":"colima","Endpoints":{"kubernetes":{"Host":"https://k"}}}`,
		"null docker endpoint": `{"Name":"colima","Endpoints":{"docker":null}}`,
	} {
		t.Run("meta.json with "+name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := metaPath(dir, "colima")
			writeFile(t, path, body)
			_, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)})
			fileErr(t, err, path)
			assertContextName(t, err, "colima")
		})
	}

	for _, host := range []string{"ssh://me@build-box", "SSH://me@build-box:2222"} {
		t.Run("ssh endpoint "+host, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeContext(t, dir, "box", host, false)
			_, err := Resolve(Inputs{Flag: "box", ConfigDir: dir, Getenv: env(nil)})
			if !errors.Is(err, ErrSSHUnsupported) {
				t.Fatalf("err = %v, want ErrSSHUnsupported", err)
			}
			assertContextName(t, err, "box")
		})
	}

	t.Run("pem path is not a regular file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeContext(t, dir, "remote", "tcp://h:2376", false)
		path := filepath.Join(tlsDir(dir, "remote"), "ca.pem")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{Flag: "remote", ConfigDir: dir, Getenv: env(nil)})
		fileErr(t, err, path)
	})

	t.Run("explicit default never reads the store", func(t *testing.T) {
		t.Parallel()
		skipIfPermissionsIgnored(t)
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.json"), "{broken")
		if err := os.Chmod(dir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		for _, in := range []Inputs{
			{Flag: "default", ConfigDir: dir, Getenv: env(nil)},
			{ConfigDir: dir, Getenv: env(map[string]string{"DOCKER_CONTEXT": "default"})},
			{ConfigDir: dir, Getenv: env(map[string]string{"DOCKER_HOST": "tcp://h:2375"})},
		} {
			got, err := Resolve(in)
			if err != nil || got != DefaultEndpoint() {
				t.Errorf("Resolve(%+v) = %+v, %v; want default, nil", in, got, err)
			}
		}
	})
}

func TestErrorStrings(t *testing.T) {
	t.Parallel()
	fe := &FileError{Path: "/c/config.json", Err: errors.New("boom")}
	if got, want := fe.Error(), "/c/config.json: boom"; got != want {
		t.Errorf("FileError.Error() = %q, want %q", got, want)
	}
	ce := &ContextError{Name: "box", Err: ErrSSHUnsupported}
	if got, want := ce.Error(), `context "box": ssh:// endpoints are not supported`; got != want {
		t.Errorf("ContextError.Error() = %q, want %q", got, want)
	}
}

// skipIfPermissionsIgnored skips tests that rely on file modes denying
// access, which root and Windows do not enforce.
func skipIfPermissionsIgnored(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file permissions are not enforced for this user or platform")
	}
}

// TestResolveNoHomeDirectory is not parallel: it replaces lookupHome, and
// top-level parallel tests only start once the sequential ones are done.
func TestResolveNoHomeDirectory(t *testing.T) {
	saved := lookupHome
	lookupHome = func() string { return "" }
	t.Cleanup(func() { lookupHome = saved })

	// Nothing names a context: the current context cannot be read, so this
	// must fail rather than silently use the default engine.
	_, err := Resolve(Inputs{Getenv: env(nil)})
	var fe *FileError
	if !errors.As(err, &fe) || !errors.Is(err, errNoHome) {
		t.Errorf("err = %v, want *FileError wrapping errNoHome", err)
	}
	_, err = Resolve(Inputs{Flag: "colima", Getenv: env(nil)})
	if !errors.Is(err, errNoHome) {
		t.Errorf("named context: err = %v, want errNoHome", err)
	}
	assertContextName(t, err, "colima")

	// The default context and DOCKER_HOST need no config directory.
	for _, in := range []Inputs{
		{Flag: "default", Getenv: env(nil)},
		{Getenv: env(map[string]string{"DOCKER_HOST": "tcp://h:2375"})},
	} {
		if got, err := Resolve(in); err != nil || got != DefaultEndpoint() {
			t.Errorf("Resolve(%+v) = %+v, %v; want default, nil", in, got, err)
		}
	}

	// The user database is the last resort before failing.
	home := t.TempDir()
	writeConfig(t, filepath.Join(home, ".docker"), "colima")
	writeContext(t, filepath.Join(home, ".docker"), "colima", "unix:///db.sock", false)
	lookupHome = func() string { return home }
	if got, err := Resolve(Inputs{Getenv: env(nil)}); err != nil || got.Host != "unix:///db.sock" {
		t.Errorf("Resolve = %+v, %v; want colima under the looked-up home", got, err)
	}
}

func TestResolveBrokenStore(t *testing.T) {
	t.Parallel()

	t.Run("meta directory without meta.json", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := metaPath(dir, "colima")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)})
		var fe *FileError
		if errors.Is(err, ErrNotFound) || !errors.As(err, &fe) || fe.Path != path {
			t.Errorf("err = %v, want *FileError for %s", err, path)
		}
	})

	t.Run("meta.json is a directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := metaPath(dir, "colima")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)})
		var fe *FileError
		if !errors.As(err, &fe) || fe.Path != path {
			t.Errorf("err = %v, want *FileError for %s", err, path)
		}
	})

	t.Run("symbolic links", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("creating symbolic links needs privileges on Windows")
		}
		dir := t.TempDir()
		real := filepath.Join(t.TempDir(), "config.json")
		writeFile(t, real, `{"currentContext":"default"}`)
		cfg := filepath.Join(dir, "config.json")
		if err := os.Symlink(real, cfg); err != nil {
			t.Fatal(err)
		}
		if got, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)}); err != nil || got != DefaultEndpoint() {
			t.Errorf("symlinked config.json: Resolve = %+v, %v; want default, nil", got, err)
		}

		// Dangling links are a broken store, not a missing file.
		if err := os.Remove(real); err != nil {
			t.Fatal(err)
		}
		var fe *FileError
		if _, err := Resolve(Inputs{ConfigDir: dir, Getenv: env(nil)}); !errors.As(err, &fe) || fe.Path != cfg {
			t.Errorf("dangling config.json: err = %v, want *FileError for %s", err, cfg)
		}
		meta := metaPath(dir, "colima")
		if err := os.MkdirAll(filepath.Dir(meta), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "gone"), meta); err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(Inputs{Flag: "colima", ConfigDir: dir, Getenv: env(nil)}); errors.Is(err, ErrNotFound) || !errors.As(err, &fe) {
			t.Errorf("dangling meta.json: err = %v, want *FileError", err)
		}
		writeContext(t, dir, "remote", "tcp://h:2376", false)
		ca := filepath.Join(tlsDir(dir, "remote"), "ca.pem")
		if err := os.MkdirAll(filepath.Dir(ca), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "gone"), ca); err != nil {
			t.Fatal(err)
		}
		if _, err := Resolve(Inputs{Flag: "remote", ConfigDir: dir, Getenv: env(nil)}); !errors.As(err, &fe) || fe.Path != ca {
			t.Errorf("dangling ca.pem: err = %v, want *FileError for %s", err, ca)
		}
	})
}
