// Package objectstore owns bounded MinIO access to the payments-import bucket.
package objectstore

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

type minioClient interface {
	GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
}

// Reader reads from the fixed payments-import bucket.
type Reader struct {
	client                minioClient
	transport             *http.Transport
	bucket                string
	maximumStatementBytes int
	fetchTimeout          time.Duration
}

// NewReader constructs the verified read-only object-store adapter.
func NewReader(settings config.ObjectStore) (*Reader, error) {
	if settings.Endpoint == nil || settings.Bucket == "" || settings.FetchTimeout <= 0 ||
		settings.MaximumStatementBytes <= 0 {
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
		maximumStatementBytes: settings.MaximumStatementBytes,
		fetchTimeout:          settings.FetchTimeout,
	}, nil
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

// ReadStatement captures and hashes one bounded source object without retrying the payload read.
func (reader *Reader) ReadStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	if reader == nil || reader.client == nil || key == "" {
		return application.StatementSnapshot{}, application.ErrStatementUnavailable
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
		return application.StatementSnapshot{}, statementReadError(ctx)
	}
	defer func() { _ = object.Close() }()
	content, err := io.ReadAll(io.LimitReader(
		object,
		int64(reader.maximumStatementBytes)+1,
	))
	if err != nil {
		return application.StatementSnapshot{}, statementReadError(ctx)
	}
	if len(content) > reader.maximumStatementBytes {
		return application.StatementSnapshot{}, application.ErrStatementLimit
	}
	digest := sha256.Sum256(content)
	return application.StatementSnapshot{
		SourceName: key,
		SHA256:     fmt.Sprintf("%x", digest),
		Content:    content,
	}, nil
}

func statementReadError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return application.ErrStatementUnavailable
}
