// Package objectstore owns bounded MinIO access to the payments-import bucket.
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/signer"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
)

type minioClient interface {
	GetObject(context.Context, string, string, minio.GetObjectOptions) (*minio.Object, error)
	GetBucketLocation(context.Context, string) (string, error)
	ListObjects(context.Context, string, minio.ListObjectsOptions) <-chan minio.ObjectInfo
	PutObject(
		context.Context,
		string,
		string,
		io.Reader,
		int64,
		minio.PutObjectOptions,
	) (minio.UploadInfo, error)
	StatObject(
		context.Context,
		string,
		string,
		minio.StatObjectOptions,
	) (minio.ObjectInfo, error)
}

type conditionalDelete func(context.Context, string, string) error

// Reader reads and reconciles statements in the fixed payments-import bucket.
type Reader struct {
	client                minioClient
	transport             *http.Transport
	bucket                string
	maximumStatementBytes int
	fetchTimeout          time.Duration
	deleteAccepted        conditionalDelete
}

// NewReader constructs the verified bounded object-store adapter.
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
	reader := &Reader{
		client: client, transport: transport, bucket: settings.Bucket,
		maximumStatementBytes: settings.MaximumStatementBytes,
		fetchTimeout:          settings.FetchTimeout,
	}
	reader.deleteAccepted = newConditionalDelete(
		settings.Endpoint,
		settings.Bucket,
		settings.AccessKey.Value(),
		settings.SecretKey.Value(),
		client,
		&http.Client{Transport: transport},
	)
	return reader, nil
}

// Close releases retained idle connections without owning remote resources.
func (reader *Reader) Close() {
	if reader != nil && reader.transport != nil {
		reader.transport.CloseIdleConnections()
	}
}

// Check verifies the required read boundary for service readiness.
func (reader *Reader) Check(ctx context.Context) error { return reader.CheckReadAccess(ctx) }

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

// DiscoverStatements lists eligible source identities through the supplied UTC business date.
func (reader *Reader) DiscoverStatements(
	ctx context.Context,
	businessDate time.Time,
) ([]string, error) {
	if reader == nil || reader.client == nil {
		return nil, application.ErrStatementUnavailable
	}
	deadline := time.Date(
		businessDate.UTC().Year(),
		businessDate.UTC().Month(),
		businessDate.UTC().Day(),
		0,
		0,
		0,
		0,
		time.UTC,
	)
	listCtx, cancel := context.WithTimeout(ctx, reader.fetchTimeout)
	defer cancel()
	dates := make(map[string]time.Time)
	for item := range reader.client.ListObjects(
		listCtx,
		reader.bucket,
		minio.ListObjectsOptions{Recursive: true},
	) {
		if item.Err != nil {
			if err := listCtx.Err(); err != nil {
				return nil, err
			}
			return nil, application.ErrStatementUnavailable
		}
		if strings.HasPrefix(item.Key, "_processed_") {
			continue
		}
		sourceName := item.Key
		date, valid := application.StatementSourceDate(sourceName)
		if !valid || date.After(deadline) {
			continue
		}
		dates[sourceName] = date
	}
	if err := listCtx.Err(); err != nil {
		return nil, err
	}
	sources := make([]string, 0, len(dates))
	for sourceName := range dates {
		sources = append(sources, sourceName)
	}
	sort.Slice(sources, func(left, right int) bool {
		leftDate := dates[sources[left]]
		rightDate := dates[sources[right]]
		if leftDate.Equal(rightDate) {
			return sources[left] < sources[right]
		}
		return leftDate.Before(rightDate)
	})
	return sources, nil
}

// ReadStatement captures and hashes one bounded source object without retrying the payload read.
func (reader *Reader) ReadStatement(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, error) {
	if reader == nil || reader.client == nil || !application.ValidStatementSourceName(key) {
		return application.StatementSnapshot{}, application.ErrStatementUnavailable
	}
	return reader.readObject(ctx, key, key)
}

// ReadProcessedStatement reads accepted bytes only for durable-receipt recovery.
func (reader *Reader) ReadProcessedStatement(
	ctx context.Context,
	sourceName string,
) (application.StatementSnapshot, error) {
	if reader == nil || reader.client == nil ||
		!application.ValidStatementSourceName(sourceName) {
		return application.StatementSnapshot{}, application.ErrStatementUnavailable
	}
	return reader.readObject(ctx, processedName(sourceName), sourceName)
}

func (reader *Reader) readObject(
	ctx context.Context,
	key string,
	sourceName string,
) (application.StatementSnapshot, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, reader.fetchTimeout)
	defer cancel()
	snapshot, _, err := reader.readObjectWithETag(fetchCtx, key)
	if err != nil {
		return application.StatementSnapshot{}, err
	}
	snapshot.SourceName = sourceName
	return snapshot, nil
}

// ArchiveStatement creates or verifies accepted bytes, then conditionally removes their source.
func (reader *Reader) ArchiveStatement(
	ctx context.Context,
	snapshot application.StatementSnapshot,
) error {
	if reader == nil || reader.client == nil || reader.deleteAccepted == nil ||
		!validSnapshot(snapshot) {
		return application.ErrStatementInvalid
	}
	archiveCtx, cancel := context.WithTimeout(ctx, reader.fetchTimeout)
	defer cancel()
	if err := reader.ensureProcessed(archiveCtx, snapshot); err != nil {
		return err
	}
	return reader.removeSource(archiveCtx, snapshot)
}

func (reader *Reader) ensureProcessed(
	ctx context.Context,
	snapshot application.StatementSnapshot,
) error {
	key := processedName(snapshot.SourceName)
	processed, err := reader.readObject(ctx, key, snapshot.SourceName)
	if err == nil {
		return matchingDigest(processed, snapshot.SHA256)
	}
	if !errors.Is(err, application.ErrStatementNotFound) {
		return err
	}
	options := minio.PutObjectOptions{ContentType: "text/csv", SendContentMd5: true}
	options.SetMatchETagExcept("*")
	_, putErr := reader.client.PutObject(
		ctx,
		reader.bucket,
		key,
		bytes.NewReader(snapshot.Content),
		int64(len(snapshot.Content)),
		options,
	)
	processed, readErr := reader.readObject(ctx, key, snapshot.SourceName)
	if readErr == nil {
		return matchingDigest(processed, snapshot.SHA256)
	}
	if putErr != nil {
		return application.ErrStatementUnavailable
	}
	return readErr
}

func (reader *Reader) removeSource(
	ctx context.Context,
	snapshot application.StatementSnapshot,
) error {
	current, etag, err := reader.readObjectWithETag(ctx, snapshot.SourceName)
	if errors.Is(err, application.ErrStatementNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := matchingDigest(current, snapshot.SHA256); err != nil {
		return err
	}
	deleteErr := reader.deleteAccepted(ctx, snapshot.SourceName, etag)
	after, _, readErr := reader.readObjectWithETag(ctx, snapshot.SourceName)
	if errors.Is(readErr, application.ErrStatementNotFound) {
		return nil
	}
	if readErr != nil {
		return application.ErrStatementUnavailable
	}
	if after.SHA256 != snapshot.SHA256 {
		return application.ErrStatementConflict
	}
	if deleteErr != nil {
		return deleteErr
	}
	return application.ErrStatementUnavailable
}

func (reader *Reader) readObjectWithETag(
	ctx context.Context,
	key string,
) (application.StatementSnapshot, string, error) {
	info, err := reader.client.StatObject(ctx, reader.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return application.StatementSnapshot{}, "", objectReadError(ctx, err)
	}
	if info.ETag == "" {
		return application.StatementSnapshot{}, "", application.ErrStatementUnavailable
	}
	options := minio.GetObjectOptions{}
	if err := options.SetMatchETag(info.ETag); err != nil {
		return application.StatementSnapshot{}, "", application.ErrStatementUnavailable
	}
	object, err := reader.client.GetObject(ctx, reader.bucket, key, options)
	if err != nil {
		return application.StatementSnapshot{}, "", objectReadError(ctx, err)
	}
	defer func() { _ = object.Close() }()
	content, err := io.ReadAll(io.LimitReader(
		object,
		int64(reader.maximumStatementBytes)+1,
	))
	if err != nil {
		return application.StatementSnapshot{}, "", objectReadError(ctx, err)
	}
	if len(content) > reader.maximumStatementBytes {
		return application.StatementSnapshot{}, "", application.ErrStatementLimit
	}
	digest := sha256.Sum256(content)
	return application.StatementSnapshot{
		SourceName: key,
		SHA256:     fmt.Sprintf("%x", digest),
		Content:    content,
	}, info.ETag, nil
}

func validSnapshot(snapshot application.StatementSnapshot) bool {
	if !application.ValidStatementSourceName(snapshot.SourceName) ||
		len(snapshot.SHA256) != 64 || len(snapshot.Content) == 0 {
		return false
	}
	digest := sha256.Sum256(snapshot.Content)
	return fmt.Sprintf("%x", digest) == snapshot.SHA256
}

func matchingDigest(snapshot application.StatementSnapshot, digest string) error {
	if snapshot.SHA256 != digest {
		return application.ErrStatementConflict
	}
	return nil
}

func processedName(sourceName string) string { return "_processed_" + sourceName }

func objectReadError(ctx context.Context, err error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.Code == "NoSuchObject" {
		return application.ErrStatementNotFound
	}
	return application.ErrStatementUnavailable
}

func newConditionalDelete(
	endpoint *url.URL,
	bucket string,
	accessKey string,
	secretKey string,
	client minioClient,
	httpClient *http.Client,
) conditionalDelete {
	return func(ctx context.Context, key string, etag string) error {
		location, err := client.GetBucketLocation(ctx, bucket)
		if err != nil {
			return application.ErrStatementUnavailable
		}
		if location == "" {
			location = "us-east-1"
		}
		target := *endpoint
		target.Path = strings.TrimSuffix(target.Path, "/") + "/" + bucket + "/" + key
		target.RawPath = ""
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodDelete,
			target.String(),
			nil,
		)
		if err != nil {
			return application.ErrStatementUnavailable
		}
		request.Header.Set("If-Match", `"`+etag+`"`)
		request.Header.Set(
			"X-Amz-Content-Sha256",
			"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		)
		signed := signer.SignV4(*request, accessKey, secretKey, "", location)
		response, err := httpClient.Do(signed)
		if err != nil {
			return application.ErrStatementUnavailable
		}
		defer func() { _ = response.Body.Close() }()
		switch response.StatusCode {
		case http.StatusNoContent, http.StatusNotFound:
			return nil
		case http.StatusPreconditionFailed:
			return application.ErrStatementConflict
		default:
			return application.ErrStatementUnavailable
		}
	}
}
