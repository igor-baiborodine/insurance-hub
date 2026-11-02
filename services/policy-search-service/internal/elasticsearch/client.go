// Package elasticsearch owns the policy search service's Elasticsearch client configuration.
package elasticsearch

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	elasticsearchapi "github.com/elastic/go-elasticsearch/v8"
)

// Client owns the official API client and its reusable HTTP connection pool.
type Client struct {
	*elasticsearchapi.Client
	transport *http.Transport
}

// Config keeps endpoint credentials separate from the URL and requires bounded transport waits.
type Config struct {
	Endpoint string
	Username string
	Password string
	CACert   []byte
	Timeout  time.Duration
}

// New constructs the official 8.19 client without performing network or index operations.
// The Java gateway owns retries, so the Elasticsearch client's retry layer is disabled.
func New(settings Config) (*Client, error) {
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
	return &Client{Client: client, transport: transport}, nil
}

// Check verifies that the accepted index is visible without changing cluster state.
func (client *Client) Check(ctx context.Context) error {
	if client == nil || client.Client == nil {
		return errors.New("check elasticsearch: client is required")
	}
	response, err := client.Indices.Exists(
		[]string{PolicyIndex},
		client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return errors.New("check elasticsearch: request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return errors.New("check elasticsearch: invalid response")
	}
	if response.IsError() {
		return errors.New("check elasticsearch: unsuccessful response")
	}
	return nil
}

// CheckWrite verifies the projection identity's exact index permission without reading or
// mutating the index.
func (client *Client) CheckWrite(ctx context.Context) error {
	if client == nil || client.Client == nil {
		return errors.New("check elasticsearch writer: client is required")
	}
	payload := []byte(`{"index":[{"names":["` + PolicyIndex + `"],"privileges":["index"]}]}`)
	response, err := client.Security.HasPrivileges(
		bytes.NewReader(payload),
		client.Security.HasPrivileges.WithContext(ctx),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return errors.New("check elasticsearch writer: request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.IsError() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errors.New("check elasticsearch writer: unsuccessful response")
	}
	var privileges struct {
		HasAllRequested bool                       `json:"has_all_requested"`
		Index           map[string]map[string]bool `json:"index"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).
		Decode(&privileges); err != nil {
		return errors.New("check elasticsearch writer: invalid response")
	}
	if !privileges.HasAllRequested || !privileges.Index[PolicyIndex]["index"] {
		return errors.New("check elasticsearch writer: required privilege unavailable")
	}
	return nil
}

// CloseIdleConnections releases pooled connections during shutdown or startup rollback.
func (client *Client) CloseIdleConnections() {
	if client != nil && client.transport != nil {
		client.transport.CloseIdleConnections()
	}
}
