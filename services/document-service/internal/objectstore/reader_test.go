package objectstore

import (
	"context"
	"errors"
	"net/url"
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
	httpEndpoint, _ := url.Parse("http://127.0.0.1:9000")
	reader, err := NewReader(config.ObjectStore{
		Endpoint: httpEndpoint, Bucket: "policies", FetchTimeout: time.Second,
	}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	httpsEndpoint, _ := url.Parse("https://minio.example")
	if _, err := NewReader(config.ObjectStore{
		Endpoint: httpsEndpoint, Bucket: "policies", FetchTimeout: time.Second,
	}, 1024); err == nil {
		t.Fatal("HTTPS without configured trust accepted")
	}

	fake := &fakeMinioClient{}
	reader = &Reader{
		client: fake, bucket: "policies", maximumBytes: 1024, fetchTimeout: time.Second,
	}
	if err := reader.CheckReadAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.items = []minio.ObjectInfo{{Err: errors.New("private")}}
	if err := reader.CheckReadAccess(context.Background()); err == nil {
		t.Fatal("object-store readiness failure accepted")
	}
	fake.items = nil
	fake.getErr = errors.New("missing")
	content, present, err := reader.Read(
		context.Background(),
		[]byte("2026/02/SYN-362.pdf"),
	)
	if err != nil || present || content != nil {
		t.Fatalf("missing object = (%q, %t, %v)", content, present, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
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
