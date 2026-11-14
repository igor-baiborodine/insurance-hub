package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
)

type fakeMinioPutClient struct {
	bucket      string
	key         string
	content     []byte
	contentType string
	size        int64
	err         error
}

func (client *fakeMinioPutClient) PutObject(
	_ context.Context,
	bucket string,
	key string,
	content io.Reader,
	size int64,
	options minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	client.bucket = bucket
	client.key = key
	client.size = size
	client.contentType = options.ContentType
	client.content, _ = io.ReadAll(content)
	return minio.UploadInfo{}, client.err
}

func TestWriter_UploadPDF_UsesAcceptedObjectContract(t *testing.T) {
	// given
	client := &fakeMinioPutClient{}
	writer, err := newWriter(client, nil, "policies", time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}
	pdf := []byte("%PDF-SYN-363")

	// when
	err = writer.UploadPDF(context.Background(), "2026/02/SYN-363.pdf", pdf)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if client.bucket != "policies" || client.key != "2026/02/SYN-363.pdf" ||
		client.contentType != "application/pdf" || client.size != int64(len(pdf)) ||
		!bytes.Equal(client.content, pdf) {
		t.Fatalf("upload = %#v", client)
	}
}

func TestWriter_UploadPDF_RejectsInvalidInputBeforeMinIO(t *testing.T) {
	client := &fakeMinioPutClient{}
	writer, err := newWriter(client, nil, "policies", time.Second, 8)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		key  string
		pdf  []byte
	}{
		{"invalid key", "SYN-363.pdf", []byte("%PDF-ok")},
		{"empty PDF", "2026/02/SYN-363.pdf", nil},
		{"invalid PDF", "2026/02/SYN-363.pdf", []byte("not-pdf")},
		{"oversized PDF", "2026/02/SYN-363.pdf", []byte("%PDF-more")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := writer.UploadPDF(context.Background(), test.key, test.pdf)

			// then
			if err == nil || client.key != "" {
				t.Fatalf("error = %v, key = %q", err, client.key)
			}
		})
	}
}

func TestWriter_UploadPDF_ClassifiesDependencyFailureSafely(t *testing.T) {
	// given
	cause := errors.New("private-secret")
	client := &fakeMinioPutClient{err: cause}
	writer, err := newWriter(client, nil, "policies", time.Second, 1024)
	if err != nil {
		t.Fatal(err)
	}

	// when
	err = writer.UploadPDF(
		context.Background(),
		"2026/02/SYN-363.pdf",
		[]byte("%PDF-SYN-363"),
	)

	// then
	if !errors.Is(err, application.ErrUnavailable) || !errors.Is(err, cause) ||
		strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewWriter_RejectsInvalidSettings(t *testing.T) {
	endpoint, _ := url.Parse("http://127.0.0.1:9000")
	if _, err := NewWriter(
		config.ObjectStore{Endpoint: endpoint, Bucket: "other"},
		time.Second,
		1,
	); err == nil {
		t.Fatal("foreign bucket accepted")
	}
	if _, err := NewWriter(config.ObjectStore{}, 0, 0); err == nil {
		t.Fatal("empty settings accepted")
	}
}
