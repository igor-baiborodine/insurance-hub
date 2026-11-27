// Package objectstore owns bounded MinIO access to the payments-import bucket.
package objectstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

type minioClient interface {
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
}

// Reader reads from the fixed payments-import bucket.
type Reader struct {
	client    minioClient
	transport *http.Transport
	bucket    string
}

// NewReader constructs the verified read-only object-store adapter.
func NewReader(settings config.ObjectStore) (*Reader, error) {
	if settings.Endpoint == nil || settings.Bucket == "" || settings.FetchTimeout <= 0 {
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
	return &Reader{client: client, transport: transport, bucket: settings.Bucket}, nil
}

// Close releases retained idle connections without owning remote resources.
func (reader *Reader) Close() {
	if reader != nil && reader.transport != nil {
		reader.transport.CloseIdleConnections()
	}
}

// CheckReadAccess performs a bounded non-mutating list against the payments-import bucket.
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
