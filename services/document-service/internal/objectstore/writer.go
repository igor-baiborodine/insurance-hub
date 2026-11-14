package objectstore

import (
	"bytes"
	"context"
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

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

const policyPDFMediaType = "application/pdf"

type minioPutClient interface {
	PutObject(
		context.Context,
		string,
		string,
		io.Reader,
		int64,
		minio.PutObjectOptions,
	) (minio.UploadInfo, error)
}

// Writer uploads generated PDFs to the fixed policies bucket.
type Writer struct {
	client        minioPutClient
	transport     *http.Transport
	bucket        string
	uploadTimeout time.Duration
	maximumBytes  int
}

var _ application.DocumentUploader = (*Writer)(nil)

// NewWriter constructs a bounded object-store writer with separate supplied credentials.
func NewWriter(
	settings config.ObjectStore,
	uploadTimeout time.Duration,
	maximumBytes int,
) (*Writer, error) {
	if settings.Endpoint == nil || settings.Bucket != "policies" || uploadTimeout <= 0 ||
		maximumBytes <= 0 {
		return nil, errors.New(
			"create object writer: configuration and limits are required",
		)
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("create object writer: load system trust")
	}
	if settings.Endpoint.Scheme == "https" {
		certificate, readErr := os.ReadFile(settings.CAFile)
		if readErr != nil || !rootCAs.AppendCertsFromPEM(certificate) {
			return nil, errors.New("create object writer: load configured trust")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: rootCAs}
	transport.DialContext = (&net.Dialer{Timeout: uploadTimeout}).DialContext
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
		return nil, errors.New("create object writer: initialize client")
	}
	return newWriter(client, transport, settings.Bucket, uploadTimeout, maximumBytes)
}

func newWriter(
	client minioPutClient,
	transport *http.Transport,
	bucket string,
	uploadTimeout time.Duration,
	maximumBytes int,
) (*Writer, error) {
	if client == nil || bucket != "policies" || uploadTimeout <= 0 || maximumBytes <= 0 {
		return nil, errors.New(
			"create object writer: configuration and limits are required",
		)
	}
	return &Writer{
		client: client, transport: transport, bucket: bucket,
		uploadTimeout: uploadTimeout, maximumBytes: maximumBytes,
	}, nil
}

// Close releases retained idle connections without changing remote objects.
func (writer *Writer) Close() {
	if writer != nil && writer.transport != nil {
		writer.transport.CloseIdleConnections()
	}
}

// UploadPDF writes a known-size generated PDF under its accepted date-derived key.
func (writer *Writer) UploadPDF(ctx context.Context, key string, pdf []byte) error {
	if !objectKeyPattern.MatchString(key) || len(pdf) == 0 || len(pdf) > writer.maximumBytes ||
		!bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return errors.New("upload policy document: invalid key or PDF")
	}
	uploadCtx, cancel := context.WithTimeout(ctx, writer.uploadTimeout)
	defer cancel()
	_, err := writer.client.PutObject(
		uploadCtx,
		writer.bucket,
		key,
		bytes.NewReader(pdf),
		int64(len(pdf)),
		minio.PutObjectOptions{ContentType: policyPDFMediaType},
	)
	if err != nil {
		if contextErr := uploadCtx.Err(); contextErr != nil {
			return fmt.Errorf("upload policy document: %w", contextErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("upload policy document: %w", err)
		}
		return objectWriteError{cause: err}
	}
	return nil
}

type objectWriteError struct{ cause error }

func (objectWriteError) Error() string {
	return "upload policy document: dependency unavailable"
}

func (failure objectWriteError) Unwrap() []error {
	return []error{application.ErrUnavailable, failure.cause}
}
