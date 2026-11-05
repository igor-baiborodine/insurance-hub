// Package elasticsearch owns the Dashboard service's read-only Elasticsearch adapter.
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
	"strings"
	"time"

	elasticsearchapi "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

const PolicyIndex = "policy_stats"

const maximumCheckResponseBytes = 1 << 20

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

// Check verifies exact-index compatibility without creating or changing cluster state.
func (client *Client) Check(ctx context.Context) error {
	if client == nil || client.Client == nil {
		return errors.New("check elasticsearch: client is required")
	}
	if err := client.checkExactOpenIndex(ctx); err != nil {
		return err
	}
	if err := client.checkMapping(ctx); err != nil {
		return err
	}
	if err := client.checkFieldCapabilities(ctx); err != nil {
		return err
	}
	return client.checkCompatibilityQuery(ctx)
}

func (client *Client) checkExactOpenIndex(ctx context.Context) error {
	response, err := client.Indices.ResolveIndex(
		[]string{client.index},
		client.Indices.ResolveIndex.WithContext(ctx),
		client.Indices.ResolveIndex.WithAllowNoIndices(false),
		client.Indices.ResolveIndex.WithIgnoreUnavailable(false),
		client.Indices.ResolveIndex.WithExpandWildcards("all"),
	)
	var resolved struct {
		Indices []struct {
			Name       string   `json:"name"`
			Attributes []string `json:"attributes"`
		} `json:"indices"`
		Aliases     []json.RawMessage `json:"aliases"`
		DataStreams []json.RawMessage `json:"data_streams"`
	}
	if err := decodeCheckResponse(ctx, response, err, &resolved); err != nil {
		return err
	}
	if len(resolved.Indices) != 1 || resolved.Indices[0].Name != client.index ||
		len(resolved.Aliases) != 0 || len(resolved.DataStreams) != 0 ||
		!contains(resolved.Indices[0].Attributes, "open") {
		return errors.New("check elasticsearch: exact open index is required")
	}
	return nil
}

type mappingField struct {
	Type   string                  `json:"type"`
	Format string                  `json:"format"`
	Fields map[string]mappingField `json:"fields"`
}

func (client *Client) checkMapping(ctx context.Context) error {
	response, err := client.Indices.GetMapping(
		client.Indices.GetMapping.WithContext(ctx),
		client.Indices.GetMapping.WithIndex(client.index),
		client.Indices.GetMapping.WithAllowNoIndices(false),
		client.Indices.GetMapping.WithIgnoreUnavailable(false),
		client.Indices.GetMapping.WithExpandWildcards("open"),
	)
	var mappings map[string]struct {
		Mappings struct {
			Properties map[string]mappingField `json:"properties"`
		} `json:"mappings"`
	}
	if err := decodeCheckResponse(ctx, response, err, &mappings); err != nil {
		return err
	}
	mapping, present := mappings[client.index]
	product := mapping.Mappings.Properties["productCode"]
	dateField := mapping.Mappings.Properties["from"]
	if !present || len(mappings) != 1 || product.Fields["keyword"].Type != "keyword" ||
		dateField.Type != "date" || !acceptsDateOnly(dateField.Format) ||
		!contains(
			[]string{"float", "double"},
			mapping.Mappings.Properties["totalPremium"].Type,
		) {
		return errors.New("check elasticsearch: incompatible index mapping")
	}
	return nil
}

func acceptsDateOnly(format string) bool {
	if format == "" {
		return true
	}
	for _, candidate := range strings.Split(format, "||") {
		if contains(
			[]string{
				"strict_date_optional_time",
				"date_optional_time",
				"strict_date",
				"yyyy-MM-dd",
			},
			candidate,
		) {
			return true
		}
	}
	return false
}

type fieldCapability struct {
	Aggregatable bool `json:"aggregatable"`
	Searchable   bool `json:"searchable"`
}

func (client *Client) checkFieldCapabilities(ctx context.Context) error {
	response, err := client.FieldCaps(
		client.FieldCaps.WithContext(ctx),
		client.FieldCaps.WithIndex(client.index),
		client.FieldCaps.WithFields("productCode.keyword", "from", "totalPremium"),
		client.FieldCaps.WithAllowNoIndices(false),
		client.FieldCaps.WithIgnoreUnavailable(false),
		client.FieldCaps.WithIncludeUnmapped(true),
		client.FieldCaps.WithExpandWildcards("open"),
	)
	var capabilities struct {
		Fields map[string]map[string]fieldCapability `json:"fields"`
	}
	if err := decodeCheckResponse(ctx, response, err, &capabilities); err != nil {
		return err
	}
	want := map[string][]string{
		"productCode.keyword": {"keyword"},
		"from":                {"date"},
		"totalPremium":        {"float", "double"},
	}
	for field, types := range want {
		byType := capabilities.Fields[field]
		compatible := false
		for _, fieldType := range types {
			capability, present := byType[fieldType]
			if present && capability.Aggregatable && capability.Searchable {
				compatible = true
			}
		}
		if !compatible || len(byType) != 1 {
			return errors.New("check elasticsearch: incompatible field capabilities")
		}
	}
	return nil
}

func (client *Client) checkCompatibilityQuery(ctx context.Context) error {
	body := []byte(
		`{"size":0,"query":{"range":{"from":{"gte":"2000-01-01","lt":"2000-01-02"}}},"aggs":{"products":{"terms":{"field":"productCode.keyword","size":1},"aggs":{"premium":{"sum":{"field":"totalPremium"}}}}}}`,
	)
	response, err := client.Search(
		client.Search.WithContext(ctx),
		client.Search.WithIndex(client.index),
		client.Search.WithBody(bytes.NewReader(body)),
		client.Search.WithSize(0),
	)
	var result struct {
		Aggregations map[string]json.RawMessage `json:"aggregations"`
	}
	if err := decodeCheckResponse(ctx, response, err, &result); err != nil {
		return err
	}
	if len(result.Aggregations["products"]) == 0 {
		return errors.New("check elasticsearch: compatibility aggregation is missing")
	}
	return nil
}

func decodeCheckResponse(
	ctx context.Context,
	response *esapi.Response,
	requestErr error,
	destination any,
) error {
	if requestErr != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return errors.New("check elasticsearch: request failed")
	}
	if response == nil || response.Body == nil {
		return errors.New("check elasticsearch: invalid response")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumCheckResponseBytes+1))
	if err != nil || len(data) > maximumCheckResponseBytes {
		return errors.New("check elasticsearch: invalid response")
	}
	if response.IsError() {
		return errors.New("check elasticsearch: unsuccessful response")
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return errors.New("check elasticsearch: invalid response")
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
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
