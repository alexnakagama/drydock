package docker

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"

	"github.com/alexnakagama/drydock/internal/docker/dockerctx"
)

// tlsClientConfig builds the TLS configuration of a named context from the
// pem files ep names. An absent file (an empty path) is skipped: without a
// CA the system roots verify the engine, and without a certificate and key
// no client certificate is sent. A file that is named but cannot be read or
// parsed is an error naming it.
func tlsClientConfig(ep dockerctx.Endpoint) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Skipping verification is the context's explicit setting
		// (docker context create --docker skip-tls-verify=true).
		InsecureSkipVerify: ep.SkipTLSVerify,
	}
	if ep.CA != "" {
		data, err := dockerctx.ReadFile(ep.CA)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate %s: %w", ep.CA, withoutPath(err, ep.CA))
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("read CA certificate %s: no PEM certificate found", ep.CA)
		}
		cfg.RootCAs = pool
	}
	switch {
	case ep.Cert != "" && ep.Key != "":
		certPEM, err := dockerctx.ReadFile(ep.Cert)
		if err != nil {
			return nil, fmt.Errorf("read client certificate %s: %w", ep.Cert, withoutPath(err, ep.Cert))
		}
		keyPEM, err := dockerctx.ReadFile(ep.Key)
		if err != nil {
			return nil, fmt.Errorf("read client key %s: %w", ep.Key, withoutPath(err, ep.Key))
		}
		// The parse error never contains the key material.
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("load client certificate %s and key %s: %w", ep.Cert, ep.Key, err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	case ep.Cert != "":
		return nil, fmt.Errorf("client certificate %s has no matching key", ep.Cert)
	case ep.Key != "":
		return nil, fmt.Errorf("client key %s has no matching certificate", ep.Key)
	}
	return cfg, nil
}

// withoutPath returns err without the *fs.PathError layer naming path, so a
// message that already names the file does not repeat it.
func withoutPath(err error, path string) error {
	var pe *fs.PathError
	if errors.As(err, &pe) && pe.Path == path {
		return pe.Err
	}
	return err
}
