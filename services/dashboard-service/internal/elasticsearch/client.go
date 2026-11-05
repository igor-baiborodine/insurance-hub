// Package elasticsearch owns the Dashboard service's read-only Elasticsearch adapter.
package elasticsearch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	elasticsearchapi "github.com/elastic/go-elasticsearch/v8"
)

const PolicyIndex = "policy_stats"

// Client owns the official API client and its reusable HTTP connection pool.
type Client struct {
	*elasticsearchapi.Client
	transport *http.Transport
	index     string
}

// Config keeps credentials outside the endpoint and requires bounded transport waits.
type Config struct {
	Endpoint string
	Index    string
	Username string
	Password string
	CACert   []byte
	Timeout  time.Duration
}

// New constructs the official v8 client without performing an index or cluster operation.
func New(settings Config) (*Client, error) {
	if settings.Index == "" {
		settings.Index = PolicyIndex
	}
	if !validIndexName(settings.Index) || settings.Index != PolicyIndex {
		return nil, errors.New("configure elasticsearch: invalid index name")
	}
	endpoint, err := url.Parse(settings.Endpoint)
	if err != nil || endpoint == nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Path != "" ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
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
	client, err := elasticsearchapi.NewClient(elasticsearchapi.Config{
		Addresses:    []string{endpoint.String()},
		Username:     settings.Username,
		Password:     settings.Password,
		Transport:    transport,
		DisableRetry: true,
	})
	if err != nil {
		return nil, errors.New("configure elasticsearch: client initialization failed")
	}
	return &Client{Client: client, transport: transport, index: settings.Index}, nil
}

// Check verifies exact-index visibility without creating or changing cluster state.
func (client *Client) Check(ctx context.Context) error {
	if client == nil || client.Client == nil {
		return errors.New("check elasticsearch: client is required")
	}
	response, err := client.Indices.Exists(
		[]string{client.index},
		client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return errors.New("check elasticsearch: request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return errors.New("check elasticsearch: invalid response")
	}
	if response.IsError() {
		return errors.New("check elasticsearch: unsuccessful response")
	}
	return nil
}

func validIndexName(value string) bool {
	if len(value) == 0 || len(value) > 255 || value == "." || value == ".." {
		return false
	}
	for index, char := range value {
		if index == 0 && (char == '-' || char == '_' || char == '+') {
			return false
		}
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') &&
			char != '-' && char != '_' && char != '.' {
			return false
		}
	}
	return true
}

// CloseIdleConnections releases pooled connections during shutdown or startup rollback.
func (client *Client) CloseIdleConnections() {
	if client != nil && client.transport != nil {
		client.transport.CloseIdleConnections()
	}
}
