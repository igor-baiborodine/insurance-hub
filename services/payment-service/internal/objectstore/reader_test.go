package objectstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

func TestReader_CheckReadAccess_EmptyBucketSucceedsWithBoundedList(t *testing.T) {
	tests := []struct {
		name  string
		start func(http.Handler) *httptest.Server
	}{
		{"local HTTP", httptest.NewServer},
		{"verified HTTPS", httptest.NewTLSServer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			fake := &fakeMinIO{}
			server := test.start(fake)
			t.Cleanup(server.Close)
			reader := newTestReader(t, settingsFor(t, server.URL, server))

			// when
			err := reader.CheckReadAccess(context.Background())
			// then
			if err != nil {
				t.Fatalf("CheckReadAccess: %v", err)
			}
			listed := false
			for _, request := range fake.requests() {
				if request.method != http.MethodGet ||
					!strings.HasPrefix(request.path, "/payments-import") {
					t.Errorf("request %s %s, want GET on payments-import only",
						request.method, request.path)
				}
				if request.query.Get("list-type") == "2" {
					listed = true
					if request.query.Get("max-keys") != "1" {
						t.Errorf(
							"max-keys = %q, want 1",
							request.query.Get("max-keys"),
						)
					}
				}
			}
			if !listed {
				t.Error("no bounded list request reached the bucket")
			}
		})
	}
}

func TestReader_CheckReadAccess_UntrustedTLSFailsClosed(t *testing.T) {
	// given
	fake := &fakeMinIO{}
	server := httptest.NewTLSServer(fake)
	t.Cleanup(server.Close)
	untrusted := settingsFor(t, server.URL, nil)
	untrusted.CAFile = writeUnrelatedCA(t)
	wrongHost := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	tests := []struct {
		name     string
		settings config.ObjectStore
	}{
		{"untrusted CA", untrusted},
		{"mismatched hostname", settingsFor(t, wrongHost, server)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := newTestReader(t, test.settings)

			// when
			err := reader.CheckReadAccess(context.Background())

			// then
			if err == nil {
				t.Fatal("CheckReadAccess trusted an unverified server")
			}
			if err.Error() != "check object-store read access: unavailable" {
				t.Errorf("error = %q, want the safe dependency failure", err)
			}
		})
	}
	if requests := fake.requests(); len(requests) != 0 {
		t.Errorf("%d requests reached the server over unverified TLS", len(requests))
	}
}

func TestReader_CheckReadAccess_StalledServerFailsAtCallerDeadline(t *testing.T) {
	// given
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	reader := newTestReader(t, settingsFor(t, server.URL, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()

	// when
	err := reader.CheckReadAccess(ctx)

	// then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("check took %v, want within the caller deadline", elapsed)
	}
}

func TestReader_ReadStatement_CapturesOneBoundedSnapshotAndDigest(t *testing.T) {
	// given
	content := []byte("header\r\naccepted,bytes\r\n")
	fake := &fakeMinIO{content: content}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	reader := newTestReader(t, settingsFor(t, server.URL, nil))
	const key = "bankStatements_2018_8_2.csv"

	// when
	snapshot, err := reader.ReadStatement(t.Context(), key)
	// then
	if err != nil {
		t.Fatalf("ReadStatement: %v; requests: %+v", err, fake.requests())
	}
	wantDigest := sha256.Sum256(content)
	if snapshot.SourceName != key || snapshot.SHA256 != fmt.Sprintf("%x", wantDigest) ||
		string(snapshot.Content) != string(content) {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	objectReads := 0
	for _, request := range fake.requests() {
		if request.method == http.MethodGet && request.path == "/payments-import/"+key {
			objectReads++
		}
	}
	if objectReads != 1 {
		t.Fatalf("object reads = %d, want one snapshot read", objectReads)
	}
}

func TestReader_ReadStatement_RejectsOversizedSnapshot(t *testing.T) {
	// given
	fake := &fakeMinIO{content: []byte("12345")}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	settings := settingsFor(t, server.URL, nil)
	settings.MaximumStatementBytes = 4
	reader := newTestReader(t, settings)

	// when
	snapshot, err := reader.ReadStatement(t.Context(), "bankStatements_2018_8_2.csv")

	// then
	if !errors.Is(err, application.ErrStatementLimit) {
		t.Fatalf("error = %v, want statement limit; requests: %+v", err, fake.requests())
	}
	if len(snapshot.Content) != 0 || snapshot.SHA256 != "" {
		t.Fatalf("oversized snapshot returned: %+v", snapshot)
	}
}

func TestNewReader_RejectsMissingTrust(t *testing.T) {
	// given
	settings := settingsFor(t, "https://127.0.0.1:9000", nil)
	settings.CAFile = filepath.Join(t.TempDir(), "missing.crt")

	// when
	reader, err := NewReader(settings)

	// then
	if err == nil {
		reader.Close()
		t.Fatal("NewReader accepted HTTPS without configured trust")
	}
}

type recordedRequest struct {
	method string
	path   string
	query  url.Values
}

type fakeMinIO struct {
	mu       sync.Mutex
	recorded []recordedRequest
	content  []byte
}

func (fake *fakeMinIO) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	fake.recorded = append(fake.recorded, recordedRequest{
		method: request.Method, path: request.URL.Path, query: request.URL.Query(),
	})
	fake.mu.Unlock()
	if request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, ".csv") {
		writer.Header().Set("Content-Length", fmt.Sprint(len(fake.content)))
		writer.Header().Set("ETag", `"synthetic"`)
		writer.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		_, _ = writer.Write(fake.content)
		return
	}
	writer.Header().Set("Content-Type", "application/xml")
	if request.URL.Query().Has("location") {
		_, _ = writer.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`</LocationConstraint>`))
		return
	}
	_, _ = writer.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		`<Name>payments-import</Name><KeyCount>0</KeyCount><MaxKeys>1</MaxKeys>` +
		`<IsTruncated>false</IsTruncated></ListBucketResult>`))
}

func (fake *fakeMinIO) requests() []recordedRequest {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]recordedRequest(nil), fake.recorded...)
}

func newTestReader(t *testing.T, settings config.ObjectStore) *Reader {
	t.Helper()
	reader, err := NewReader(settings)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	t.Cleanup(reader.Close)
	return reader
}

// settingsFor targets endpoint and trusts the certificate of trusted when it is a TLS server.
func settingsFor(t *testing.T, endpoint string, trusted *httptest.Server) config.ObjectStore {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	settings := config.ObjectStore{
		Endpoint:              parsed,
		Bucket:                "payments-import",
		FetchTimeout:          2 * time.Second,
		MaximumStatementBytes: 1024 * 1024,
	}
	if trusted != nil && trusted.TLS != nil {
		settings.CAFile = filepath.Join(t.TempDir(), "ca.crt")
		block := &pem.Block{Type: "CERTIFICATE", Bytes: trusted.Certificate().Raw}
		if err := os.WriteFile(
			settings.CAFile,
			pem.EncodeToMemory(block),
			0o600,
		); err != nil {
			t.Fatalf("write CA: %v", err)
		}
	}
	return settings
}

// writeUnrelatedCA writes a valid CA certificate that did not sign the test server certificate.
func writeUnrelatedCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "unrelated test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "unrelated-ca.crt")
	block := &pem.Block{Type: "CERTIFICATE", Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	return path
}
