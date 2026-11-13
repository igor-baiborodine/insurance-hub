package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

type metadataReader func(context.Context, string) ([]domain.StoredDocument, error)

func (reader metadataReader) FindByPolicyNumber(
	ctx context.Context,
	policyNumber string,
) ([]domain.StoredDocument, error) {
	return reader(ctx, policyNumber)
}

type contentReader func(context.Context, []byte) ([]byte, bool, error)

func (reader contentReader) Read(ctx context.Context, stored []byte) ([]byte, bool, error) {
	return reader(ctx, stored)
}

func TestFindDocuments_Execute_PreservesDuplicatesAndOmitsUnavailableContent(t *testing.T) {
	// given
	metadata := metadataReader(
		func(_ context.Context, policyNumber string) ([]domain.StoredDocument, error) {
			if policyNumber != "SYN-362" {
				t.Fatalf("policyNumber = %q", policyNumber)
			}
			return []domain.StoredDocument{
				{PolicyNumber: policyNumber, Bytes: []byte("first")},
				{PolicyNumber: policyNumber, Bytes: nil},
				{PolicyNumber: policyNumber, Bytes: []byte("first")},
			}, nil
		},
	)
	content := contentReader(func(_ context.Context, stored []byte) ([]byte, bool, error) {
		if len(stored) == 0 {
			return nil, false, nil
		}
		return append([]byte(nil), stored...), true, nil
	})
	lookup, err := application.NewFindDocuments(metadata, content, 32, 2)
	if err != nil {
		t.Fatal(err)
	}

	// when
	result, err := lookup.Execute(context.Background(), "SYN-362")
	// then
	if err != nil {
		t.Fatal(err)
	}
	if result.Omitted != 1 || len(result.Documents) != 2 {
		t.Fatalf("result = %+v", result)
	}
	for _, document := range result.Documents {
		if document.PolicyNumber != "SYN-362" || string(document.Content) != "first" {
			t.Errorf("document = %+v", document)
		}
	}
}

func TestFindDocuments_Execute_FailsWholeLookupAtAggregateLimit(t *testing.T) {
	// given
	metadata := metadataReader(func(context.Context, string) ([]domain.StoredDocument, error) {
		return []domain.StoredDocument{{Bytes: []byte("123")}, {Bytes: []byte("456")}}, nil
	})
	content := contentReader(func(_ context.Context, stored []byte) ([]byte, bool, error) {
		return stored, true, nil
	})
	lookup, err := application.NewFindDocuments(metadata, content, 5, 1)
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, err = lookup.Execute(context.Background(), "SYN-362")

	// then
	if !errors.Is(err, application.ErrResponseLimit) {
		t.Fatalf("error = %v", err)
	}
}

func TestFindDocuments_Execute_RejectsConcurrentLookupWithoutWaiting(t *testing.T) {
	// given
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	metadata := metadataReader(
		func(ctx context.Context, _ string) ([]domain.StoredDocument, error) {
			once.Do(func() { close(started) })
			select {
			case <-release:
				return nil, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	)
	content := contentReader(func(context.Context, []byte) ([]byte, bool, error) {
		return nil, false, nil
	})
	lookup, err := application.NewFindDocuments(metadata, content, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, executeErr := lookup.Execute(context.Background(), "first")
		firstDone <- executeErr
	}()
	<-started

	// when
	_, secondErr := lookup.Execute(context.Background(), "second")
	close(release)
	firstErr := <-firstDone

	// then
	if !errors.Is(secondErr, application.ErrResponseLimit) {
		t.Errorf("second error = %v", secondErr)
	}
	if firstErr != nil {
		t.Errorf("first error = %v", firstErr)
	}
}
