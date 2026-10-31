// Package elasticsearch owns the policy search service's Elasticsearch client configuration.
package elasticsearch

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
)

// Config keeps endpoint credentials separate from the URL and requires bounded transport waits.
type Config struct {
	Endpoint string
	Username string
	Password string
	CACert   []byte
	Timeout  time.Duration
}

// New constructs the official 8.19 client without performing network or index operations.
// The existing gateway owns retries, so the Elasticsearch client's retry layer is disabled.
func New(settings Config) (*elasticsearch.Client, error) {
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil || endpoint == nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Hostname() == "" ||
		endpoint.User != nil ||
		endpoint.Path != "" ||
		endpoint.RawQuery != "" ||
		endpoint.Fragment != "" {
		return nil, errors.New("configure elasticsearch: invalid endpoint URL")
	}
	if settings.Timeout <= 0 {
		return nil, errors.New("configure elasticsearch: timeout must be positive")
	}
	if (settings.Username == "") != (settings.Password == "") {
		return nil, errors.New(
			"configure elasticsearch: username and password must be paired",
		)
	}
	if len(settings.CACert) > 0 && endpoint.Scheme != "https" {
		return nil, errors.New("configure elasticsearch: custom CA requires HTTPS")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: settings.Timeout}).DialContext
	transport.TLSHandshakeTimeout = settings.Timeout
	transport.ResponseHeaderTimeout = settings.Timeout
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if len(settings.CACert) > 0 {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf(
				"configure elasticsearch: load system roots: %w",
				err,
			)
		}
		if !roots.AppendCertsFromPEM(settings.CACert) {
			return nil, errors.New("configure elasticsearch: invalid CA certificate")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses:    []string{endpoint.String()},
		Username:     settings.Username,
		Password:     settings.Password,
		Transport:    transport,
		DisableRetry: true,
	})
	if err != nil {
		return nil, errors.New("configure elasticsearch: client initialization failed")
	}
	return client, nil
}
