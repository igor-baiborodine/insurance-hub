package objectstore

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
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
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

func TestClassifyObjectKey_PreservesJavaCompatibilityEdges(t *testing.T) {
	// given
	tests := []struct {
		name string
		data []byte
		key  bool
	}{
		{"ordinary key", []byte("2026/02/SYN-362.pdf"), true},
		{
			"1023 UTF-16 units",
			[]byte("2026/02/" + strings.Repeat("x", 1011) + ".pdf"),
			true,
		},
		{
			"1024 UTF-16 units",
			[]byte("2026/02/" + strings.Repeat("x", 1012) + ".pdf"),
			false,
		},
		{"supplementary character", []byte("2026/02/😀.pdf"), true},
		{"newline", []byte("2026/02/a\nb.pdf"), false},
		{"carriage return", []byte("2026/02/a\rb.pdf"), false},
		{"next line", []byte("2026/02/a\u0085b.pdf"), false},
		{"line separator", []byte("2026/02/a\u2028b.pdf"), false},
		{"paragraph separator", []byte("2026/02/a\u2029b.pdf"), false},
		{"non-ASCII digits", []byte("٢٠٢٦/٠٢/a.pdf"), false},
		{"malformed UTF-8", []byte{0xff, 0xfe}, false},
		{"replacement-decoded key", []byte("2026/02/\xff.pdf"), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			key, got := classifyObjectKey(test.data)

			// then
			if got != test.key {
				t.Errorf("key = %t, want %t", got, test.key)
			}
			if test.name == "replacement-decoded key" && key != "2026/02/�.pdf" {
				t.Errorf("decoded key = %q", key)
			}
		})
	}
}

type fakeMinioClient struct {
	items  []minio.ObjectInfo
	getErr error
}

func (client *fakeMinioClient) GetObject(
	context.Context,
	string,
	string,
	minio.GetObjectOptions,
) (*minio.Object, error) {
	return nil, client.getErr
}

func (client *fakeMinioClient) ListObjects(
	context.Context,
	string,
	minio.ListObjectsOptions,
) <-chan minio.ObjectInfo {
	items := make(chan minio.ObjectInfo, len(client.items))
	for _, item := range client.items {
		items <- item
	}
	close(items)
	return items
}

func TestReader_RejectsInvalidSettingsAndLimitsInlineContent(t *testing.T) {
	// when / then
	if _, err := NewReader(config.ObjectStore{}, 1); err == nil {
		t.Fatal("invalid object-store settings accepted")
	}
	if err := (*Reader)(nil).CheckReadAccess(context.Background()); err == nil {
		t.Fatal("missing reader accepted")
	}
	reader := &Reader{maximumBytes: 1, fetchTimeout: time.Second}
	if _, _, err := reader.Read(
		context.Background(),
		[]byte("inline"),
	); !errors.Is(
		err,
		application.ErrResponseLimit,
	) {
		t.Fatalf("limit error = %v", err)
	}
	reader.Close()
}

func TestReader_ConstructsAndChecksBoundedObjectFailures(t *testing.T) {
	// given
	httpEndpoint, _ := url.Parse("http://127.0.0.1:9000")

	// when
	reader, err := NewReader(config.ObjectStore{
		Endpoint: httpEndpoint, Bucket: "policies", FetchTimeout: time.Second,
	}, 1024)
	// then
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()

	// given
	httpsEndpoint, _ := url.Parse("https://minio.example")

	// when / then
	if _, err := NewReader(config.ObjectStore{
		Endpoint: httpsEndpoint, Bucket: "policies", FetchTimeout: time.Second,
	}, 1024); err == nil {
		t.Fatal("HTTPS without configured trust accepted")
	}

	fake := &fakeMinioClient{}
	reader = &Reader{
		client: fake, bucket: "policies", maximumBytes: 1024, fetchTimeout: time.Second,
	}

	// when / then
	if err := reader.CheckReadAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.items = []minio.ObjectInfo{{Err: errors.New("private")}}

	// when / then
	if err := reader.CheckReadAccess(context.Background()); err == nil {
		t.Fatal("object-store readiness failure accepted")
	}
	fake.items = nil
	fake.getErr = errors.New("missing")

	// when
	content, present, err := reader.Read(
		context.Background(),
		[]byte("2026/02/SYN-362.pdf"),
	)

	// then
	if err != nil || present || content != nil {
		t.Fatalf("missing object = (%q, %t, %v)", content, present, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// when / then
	if _, _, err := reader.Read(
		cancelled,
		[]byte("2026/02/SYN-362.pdf"),
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancelled read error = %v", err)
	}
}

func TestReader_Read_PreservesInlineAndOmitsEmptyStoredBytes(t *testing.T) {
	// given
	reader := &Reader{maximumBytes: 8}

	// when
	inline, present, err := reader.Read(context.Background(), []byte("%PDF"))
	_, emptyPresent, emptyErr := reader.Read(context.Background(), nil)

	// then
	if err != nil || !present || string(inline) != "%PDF" {
		t.Fatalf("inline = %q, present = %t, error = %v", inline, present, err)
	}
	if emptyErr != nil || emptyPresent {
		t.Fatalf("empty present = %t, error = %v", emptyPresent, emptyErr)
	}
}

func TestReader_Read_PreservesZeroByteObjectAndRejectsOversize(t *testing.T) {
	// given
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Query().Has("location") {
				writer.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(
					writer,
					`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`,
				)
				return
			}
			var content []byte
			switch request.URL.Path {
			case "/policies/2026/02/zero.pdf":
				content = []byte{}
			case "/policies/2026/02/oversize.pdf":
				content = []byte("12345")
			default:
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Length", fmt.Sprint(len(content)))
			writer.Header().Set("ETag", `"synthetic"`)
			writer.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
			if request.Method == http.MethodGet {
				_, _ = writer.Write(content)
			}
		}),
	)
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	reader, err := NewReader(config.ObjectStore{
		Endpoint: endpoint, Bucket: "policies", FetchTimeout: time.Second,
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// when
	content, present, err := reader.Read(
		context.Background(),
		[]byte("2026/02/zero.pdf"),
	)

	// then
	if err != nil || !present || content == nil || len(content) != 0 {
		t.Fatalf("zero-byte object = (%v, %t, %v)", content, present, err)
	}

	// when / then
	if _, _, err := reader.Read(
		context.Background(),
		[]byte("2026/02/oversize.pdf"),
	); !errors.Is(err, application.ErrResponseLimit) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestReader_Read_OmitsObjectWhenConfiguredTrustIsWrong(t *testing.T) {
	// given
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("wrong trust reached object server")
	}))
	defer target.Close()
	caFile := filepath.Join(t.TempDir(), "wrong-ca.crt")
	certificate := newWrongAuthority(t)
	if err := os.WriteFile(caFile, certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := url.Parse(target.URL)
	reader, err := NewReader(config.ObjectStore{
		Endpoint: endpoint, Bucket: "policies", CAFile: caFile, FetchTimeout: time.Second,
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	// when
	content, present, err := reader.Read(
		context.Background(),
		[]byte("2026/02/untrusted.pdf"),
	)

	// then
	if err != nil || present || content != nil {
		t.Fatalf("wrong-trust object = (%v, %t, %v)", content, present, err)
	}
}

func newWrongAuthority(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(362),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	encoded, err := x509.CreateCertificate(
		rand.Reader,
		template,
		template,
		&key.PublicKey,
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})
}
