package dockerctx

// DefaultName is the name of the context that stands for the environment
// (DOCKER_HOST, DOCKER_TLS_VERIFY, DOCKER_CERT_PATH) or the platform's
// default socket. It never has an entry in the context store.
const DefaultName = "default"

// Inputs are everything Resolve reads besides the files on disk.
type Inputs struct {
	// Flag is the value of the -context flag; "" means not given.
	Flag string
	// Getenv reads environment variables, including HOME (USERPROFILE
	// first on Windows) to locate ~/.docker, so tests can stay hermetic. A
	// nil Getenv means os.Getenv.
	Getenv func(string) string
	// ConfigDir overrides the CLI config directory; "" means $DOCKER_CONFIG,
	// then the home directory's .docker. Relative paths are made absolute.
	ConfigDir string
}

// Endpoint is the resolved engine endpoint of the active context.
type Endpoint struct {
	// Context is "default" or the context's name; "" is treated as "default".
	Context string
	// Host is the context's docker endpoint. "" means the platform's default
	// socket, and it is always "" for the default context, whose endpoint
	// comes from the environment.
	Host string
	// TLS reports whether the connection uses TLS: CertDir != "" ||
	// SkipTLSVerify.
	TLS bool
	// CertDir is the context's TLS directory, "" if it holds no TLS files.
	CertDir string
	// CA, Cert and Key are the paths of the pem files present in CertDir,
	// "" for each one that is absent. Cert and Key are either both set or
	// both empty.
	CA, Cert, Key string
	// SkipTLSVerify disables verification of the engine's certificate.
	SkipTLSVerify bool
	// ConfigDir is the absolute CLI config directory the context was read
	// from, so the docker CLI can be pointed at the same context store. It
	// is "" for the default context, which is not read from the store.
	ConfigDir string
}

// IsDefault reports whether e is the default context, whose endpoint comes
// from the environment rather than the context store.
func (e Endpoint) IsDefault() bool {
	return e.Context == "" || e.Context == DefaultName
}

// DefaultEndpoint returns the endpoint of the default context.
func DefaultEndpoint() Endpoint {
	return Endpoint{Context: DefaultName}
}
