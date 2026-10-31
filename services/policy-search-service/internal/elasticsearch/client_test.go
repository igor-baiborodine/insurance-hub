package elasticsearch

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew_RejectsUnsafeConfigurationWithoutEchoingSecrets(t *testing.T) {
	// given
	const privateMarker = "private-credential-marker"
	for _, input := range []Config{
		{Endpoint: "http://user:" + privateMarker + "@example.test:9200", Timeout: time.Second},
		{Endpoint: "http://example.test:9200/private", Timeout: time.Second},
		{Endpoint: "ftp://example.test:9200", Timeout: time.Second},
		{Endpoint: "http://example.test:9200", Timeout: 0},
		{Endpoint: "http://example.test:9200", Username: privateMarker, Timeout: time.Second},
		{Endpoint: "http://example.test:9200", CACert: []byte(privateMarker), Timeout: time.Second},
		{Endpoint: "https://example.test:9200", CACert: []byte(privateMarker), Timeout: time.Second},
	} {
		// when
		client, err := New(input)

		// then
		if err == nil || client != nil {
			t.Fatalf("unsafe configuration accepted: %#v", input)
		}
		if strings.Contains(err.Error(), privateMarker) {
			t.Fatalf("configuration error exposed a secret: %v", err)
		}
	}
}

func TestNew_DisablesClientRetryOnUpstreamFailure(t *testing.T) {
	// given
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	// when
	client, err := New(Config{Endpoint: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Info()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close Elasticsearch response: %v", err)
		}
	}()

	// then
	if !response.IsError() || requests.Load() != 1 {
		t.Errorf(
			"status=%d requests=%d, want one failed request",
			response.StatusCode,
			requests.Load(),
		)
	}
}

func TestNew_UsesVerifiedCustomCA(t *testing.T) {
	// given
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Elastic-Product", "Elasticsearch")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":{"number":"8.19.6"}}`))
		}),
	)
	defer server.Close()
	certificate := pem.EncodeToMemory(
		&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw},
	)

	// when
	client, err := New(Config{Endpoint: server.URL, CACert: certificate, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Info()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close Elasticsearch response: %v", err)
		}
	}()

	// then
	if response.IsError() {
		t.Errorf("verified TLS request returned %d", response.StatusCode)
	}
}
