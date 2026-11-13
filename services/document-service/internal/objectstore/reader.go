// Package objectstore owns inline/key classification and bounded MinIO reads.
package objectstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"
	"unicode/utf16"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/text/encoding/unicode"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

var objectKeyPattern = regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/.*\.pdf$`)

type minioClient interface {
	GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
}

// Reader resolves inline bytes or reads a bounded object from the fixed policies bucket.
type Reader struct {
	client       minioClient
	transport    *http.Transport
	bucket       string
	maximumBytes int
	fetchTimeout time.Duration
}

var _ application.ContentReader = (*Reader)(nil)

// NewReader constructs the verified read-only object-store adapter.
func NewReader(settings config.ObjectStore, maximumBytes int) (*Reader, error) {
	if settings.Endpoint == nil || settings.Bucket == "" || settings.FetchTimeout <= 0 ||
		maximumBytes <= 0 {
		return nil, errors.New(
			"create object reader: configuration and limits are required",
		)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("create object reader: load system trust")
	}
	if settings.Endpoint.Scheme == "https" {
		certificate, readErr := os.ReadFile(settings.CAFile)
		if readErr != nil || !rootCAs.AppendCertsFromPEM(certificate) {
			return nil, errors.New("create object reader: load configured trust")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs}
	transport.DialContext = (&net.Dialer{Timeout: settings.FetchTimeout}).DialContext
	client, err := minio.New(settings.Endpoint.Host, &minio.Options{
		Creds: credentials.NewStaticV4(
			settings.AccessKey.Value(),
			settings.SecretKey.Value(),
			"",
		),
		Secure:       settings.Endpoint.Scheme == "https",
		Transport:    transport,
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   1,
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, errors.New("create object reader: initialize client")
	}
	return &Reader{
		client: client, transport: transport, bucket: settings.Bucket,
		maximumBytes: maximumBytes, fetchTimeout: settings.FetchTimeout,
	}, nil
}

// Close releases retained idle connections without owning remote resources.
func (reader *Reader) Close() {
	if reader != nil && reader.transport != nil {
		reader.transport.CloseIdleConnections()
	}
}

// CheckReadAccess performs a bounded non-mutating list against the policies bucket.
func (reader *Reader) CheckReadAccess(ctx context.Context) error {
	if reader == nil || reader.client == nil {
		return errors.New("check object-store read access: reader is required")
	}
	probeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	items := reader.client.ListObjects(
		probeCtx,
		reader.bucket,
		minio.ListObjectsOptions{MaxKeys: 1},
	)
	select {
	case item, ok := <-items:
		if !ok {
			return nil
		}
		if item.Err != nil {
			return errors.New("check object-store read access: unavailable")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Read preserves inline content and omits missing or failed object retrievals.
func (reader *Reader) Read(
	ctx context.Context,
	stored []byte,
) ([]byte, bool, error) {
	if len(stored) == 0 {
		return nil, false, nil
	}
	key, objectBacked := classifyObjectKey(stored)
	if !objectBacked {
		if len(stored) > reader.maximumBytes {
			return nil, false, application.ErrResponseLimit
		}
		return stored, true, nil
	}
	fetchCtx, cancel := context.WithTimeout(ctx, reader.fetchTimeout)
	defer cancel()
	object, err := reader.client.GetObject(
		fetchCtx,
		reader.bucket,
		key,
		minio.GetObjectOptions{},
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, false, contextErr
		}
		return nil, false, nil
	}
	defer func() { _ = object.Close() }()
	info, err := object.Stat()
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, false, contextErr
		}
		return nil, false, nil
	}
	if info.Size > int64(reader.maximumBytes) {
		return nil, false, application.ErrResponseLimit
	}
	content, err := io.ReadAll(io.LimitReader(object, int64(reader.maximumBytes)+1))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, false, contextErr
		}
		return nil, false, nil
	}
	if len(content) > reader.maximumBytes {
		return nil, false, application.ErrResponseLimit
	}
	return content, true, nil
}

func classifyObjectKey(stored []byte) (string, bool) {
	decodedBytes, _ := unicode.UTF8.NewDecoder().Bytes(stored)
	decoded := string(decodedBytes)
	remaining := decoded
	units := 0
	for _, r := range remaining {
		units += utf16.RuneLen(r)
		if units >= 1024 {
			return "", false
		}
	}
	return decoded, objectKeyPattern.MatchString(decoded)
}

// IsObjectKey reports whether stored bytes use the legacy object-key representation.
func IsObjectKey(stored []byte) bool {
	_, objectBacked := classifyObjectKey(stored)
	return objectBacked
}
