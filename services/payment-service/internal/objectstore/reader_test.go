package objectstore

import (
	"bytes"
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
	"io"
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

func TestReader_DiscoverStatements_ReturnsEligibleDailySourcesInDateOrder(t *testing.T) {
	// given
	fake := &fakeMinIO{keys: []string{
		"bankStatements_2018_8_3.csv",
		"_processed_bankStatements_2018_8_1.csv",
		"bankStatements_2018_8_2.csv",
		"_processed_bankStatements_2018_8_2.csv",
		"bankStatements_2018_8_4.csv",
		"unrelated.csv",
	}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	reader := newTestReader(t, settingsFor(t, server.URL, nil))

	// when
	sources, err := reader.DiscoverStatements(
		t.Context(),
		time.Date(2018, time.August, 3, 23, 59, 0, 0, time.FixedZone("west", -4*60*60)),
	)
	// then
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"bankStatements_2018_8_2.csv",
		"bankStatements_2018_8_3.csv",
		"bankStatements_2018_8_4.csv",
	}
	if fmt.Sprint(sources) != fmt.Sprint(want) {
		t.Fatalf("sources = %v, want %v", sources, want)
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

func TestReader_ArchiveStatement_UsesConditionalCreationAndDeletion(t *testing.T) {
	accepted := []byte("accepted statement bytes")
	changed := []byte("changed statement bytes")
	const source = "bankStatements_2018_8_2.csv"

	t.Run("matching source is archived", func(t *testing.T) {
		fake := newArchiveMinIO(source, accepted)
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		reader := newTestReader(t, settingsFor(t, server.URL, nil))

		err := reader.ArchiveStatement(t.Context(), statementSnapshot(source, accepted))
		if err != nil {
			t.Fatal(err)
		}
		if fake.exists(source) ||
			!bytes.Equal(fake.object(processedName(source)), accepted) {
			t.Fatal("matching source was not moved to the processed key")
		}
		if fake.putIfNoneMatch != "*" ||
			fake.deleteIfMatch != `"`+objectETag(accepted)+`"` {
			t.Fatalf("preconditions = (put %q, delete %q)",
				fake.putIfNoneMatch, fake.deleteIfMatch)
		}
	})

	t.Run("replacement immediately before delete survives", func(t *testing.T) {
		fake := newArchiveMinIO(source, accepted)
		fake.beforeDelete = func() { fake.setObject(source, changed) }
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		reader := newTestReader(t, settingsFor(t, server.URL, nil))

		err := reader.ArchiveStatement(t.Context(), statementSnapshot(source, accepted))

		if !errors.Is(err, application.ErrStatementConflict) {
			t.Fatalf("error = %v, want statement conflict", err)
		}
		if !bytes.Equal(fake.object(source), changed) ||
			!bytes.Equal(fake.object(processedName(source)), accepted) {
			t.Fatal("source replacement or accepted processed bytes were lost")
		}
	})

	t.Run("conflict immediately before processed creation survives", func(t *testing.T) {
		fake := newArchiveMinIO(source, accepted)
		fake.beforePut = func(key string) {
			if key == processedName(source) {
				fake.setObject(key, changed)
			}
		}
		server := httptest.NewServer(fake)
		t.Cleanup(server.Close)
		reader := newTestReader(t, settingsFor(t, server.URL, nil))

		err := reader.ArchiveStatement(t.Context(), statementSnapshot(source, accepted))

		if !errors.Is(err, application.ErrStatementConflict) {
			t.Fatalf("error = %v, want statement conflict", err)
		}
		if !bytes.Equal(fake.object(source), accepted) ||
			!bytes.Equal(fake.object(processedName(source)), changed) {
			t.Fatal("source or conflicting processed bytes were overwritten")
		}
		if fake.putIfNoneMatch != "*" {
			t.Fatalf("processed creation If-None-Match = %q", fake.putIfNoneMatch)
		}
	})
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
	keys     []string
}

type archiveMinIO struct {
	mu             sync.Mutex
	objects        map[string][]byte
	beforePut      func(string)
	beforeDelete   func()
	putIfNoneMatch string
	deleteIfMatch  string
}

func newArchiveMinIO(source string, content []byte) *archiveMinIO {
	return &archiveMinIO{objects: map[string][]byte{source: append([]byte(nil), content...)}}
}

func (fake *archiveMinIO) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Has("location") {
		writer.Header().Set("Content-Type", "application/xml")
		_, _ = writer.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`us-east-1</LocationConstraint>`))
		return
	}
	key := strings.TrimPrefix(request.URL.Path, "/payments-import/")
	switch request.Method {
	case http.MethodHead, http.MethodGet:
		content, exists := fake.objectValue(key)
		if !exists {
			writeObjectError(writer, http.StatusNotFound, "NoSuchKey")
			return
		}
		if match := request.Header.Get("If-Match"); match != "" &&
			match != `"`+objectETag(content)+`"` {
			writeObjectError(
				writer,
				http.StatusPreconditionFailed,
				"PreconditionFailed",
			)
			return
		}
		writer.Header().Set("Content-Length", fmt.Sprint(len(content)))
		writer.Header().Set("ETag", `"`+objectETag(content)+`"`)
		writer.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		if request.Method == http.MethodGet {
			_, _ = writer.Write(content)
		}
	case http.MethodPut:
		fake.mu.Lock()
		fake.putIfNoneMatch = request.Header.Get("If-None-Match")
		beforePut := fake.beforePut
		fake.beforePut = nil
		fake.mu.Unlock()
		if beforePut != nil {
			beforePut(key)
		}
		if request.Header.Get("If-None-Match") == "*" && fake.exists(key) {
			writeObjectError(
				writer,
				http.StatusPreconditionFailed,
				"PreconditionFailed",
			)
			return
		}
		content, err := io.ReadAll(request.Body)
		if err != nil {
			writeObjectError(writer, http.StatusInternalServerError, "InternalError")
			return
		}
		fake.setObject(key, content)
		writer.Header().Set("ETag", `"`+objectETag(content)+`"`)
		writer.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		fake.mu.Lock()
		fake.deleteIfMatch = request.Header.Get("If-Match")
		beforeDelete := fake.beforeDelete
		fake.beforeDelete = nil
		fake.mu.Unlock()
		if beforeDelete != nil {
			beforeDelete()
		}
		content, exists := fake.objectValue(key)
		if exists && request.Header.Get("If-Match") != `"`+objectETag(content)+`"` {
			writeObjectError(
				writer,
				http.StatusPreconditionFailed,
				"PreconditionFailed",
			)
			return
		}
		fake.mu.Lock()
		delete(fake.objects, key)
		fake.mu.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	default:
		writeObjectError(writer, http.StatusMethodNotAllowed, "MethodNotAllowed")
	}
}

func (fake *archiveMinIO) object(key string) []byte {
	content, _ := fake.objectValue(key)
	return content
}

func (fake *archiveMinIO) objectValue(key string) ([]byte, bool) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	content, exists := fake.objects[key]
	return append([]byte(nil), content...), exists
}

func (fake *archiveMinIO) exists(key string) bool {
	_, exists := fake.objectValue(key)
	return exists
}

func (fake *archiveMinIO) setObject(key string, content []byte) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.objects[key] = append([]byte(nil), content...)
}

func statementSnapshot(source string, content []byte) application.StatementSnapshot {
	digest := sha256.Sum256(content)
	return application.StatementSnapshot{
		SourceName: source,
		SHA256:     fmt.Sprintf("%x", digest),
		Content:    append([]byte(nil), content...),
	}
}

func objectETag(content []byte) string {
	digest := sha256.Sum256(content)
	return fmt.Sprintf("%x", digest)
}

func writeObjectError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(`<Error><Code>` + code + `</Code></Error>`))
}

func (fake *fakeMinIO) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	fake.recorded = append(fake.recorded, recordedRequest{
		method: request.Method, path: request.URL.Path, query: request.URL.Query(),
	})
	fake.mu.Unlock()
	if (request.Method == http.MethodHead || request.Method == http.MethodGet) &&
		strings.HasSuffix(request.URL.Path, ".csv") {
		writer.Header().Set("Content-Length", fmt.Sprint(len(fake.content)))
		writer.Header().Set("ETag", `"synthetic"`)
		writer.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
		if request.Method == http.MethodGet {
			_, _ = writer.Write(fake.content)
		}
		return
	}
	writer.Header().Set("Content-Type", "application/xml")
	if request.URL.Query().Has("location") {
		_, _ = writer.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`</LocationConstraint>`))
		return
	}
	if request.URL.Query().Get("list-type") == "2" {
		var contents strings.Builder
		for _, key := range fake.keys {
			contents.WriteString("<Contents><Key>")
			contents.WriteString(key)
			contents.WriteString("</Key><Size>1</Size></Contents>")
		}
		_, _ = writer.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` +
			`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
			`<Name>payments-import</Name><KeyCount>` + fmt.Sprint(len(fake.keys)) +
			`</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>` +
			contents.String() + `</ListBucketResult>`))
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
