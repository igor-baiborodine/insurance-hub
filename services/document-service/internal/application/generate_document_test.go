package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

type rendererFunc func(context.Context, domain.PolicyRegistration) ([]byte, error)

func (function rendererFunc) Render(
	ctx context.Context,
	registration domain.PolicyRegistration,
) ([]byte, error) {
	return function(ctx, registration)
}

type uploaderFunc func(context.Context, string, []byte) error

func (function uploaderFunc) UploadPDF(ctx context.Context, key string, pdf []byte) error {
	return function(ctx, key, pdf)
}

type metadataWriterFunc func(context.Context, string, string) error

func (function metadataWriterFunc) InsertObjectKey(
	ctx context.Context,
	policyNumber string,
	key string,
) error {
	return function(ctx, policyNumber, key)
}

func TestGenerateDocument_Execute_OrdersAcceptedEffects(t *testing.T) {
	// given
	registration := syntheticRegistration(t)
	var effects []string
	generation, err := application.NewGenerateDocument(
		rendererFunc(func(context.Context, domain.PolicyRegistration) ([]byte, error) {
			effects = append(effects, "render")
			return []byte("%PDF-synthetic"), nil
		}),
		uploaderFunc(func(_ context.Context, key string, pdf []byte) error {
			effects = append(effects, "upload:"+key+":"+string(pdf))
			return nil
		}),
		metadataWriterFunc(func(_ context.Context, policyNumber, key string) error {
			effects = append(effects, "insert:"+policyNumber+":"+key)
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	result, err := generation.Execute(context.Background(), registration)
	// then
	if err != nil {
		t.Fatal(err)
	}
	wantKey := "2026/02/SYN-363.pdf"
	if result.ObjectKey != wantKey {
		t.Fatalf("object key = %q, want %q", result.ObjectKey, wantKey)
	}
	wantEffects := []string{
		"render",
		"upload:" + wantKey + ":%PDF-synthetic",
		"insert:SYN-363:" + wantKey,
	}
	if !reflect.DeepEqual(effects, wantEffects) {
		t.Fatalf("effects = %v, want %v", effects, wantEffects)
	}
}

func TestGenerateDocument_Execute_PreservesPartialEffectContract(t *testing.T) {
	registration := syntheticRegistration(t)
	failure := errors.New("synthetic failure")
	tests := []struct {
		name        string
		renderError error
		uploadError error
		insertError error
		wantEffects []string
	}{
		{"render failure", failure, nil, nil, []string{"render"}},
		{"upload failure", nil, failure, nil, []string{"render", "upload"}},
		{"database failure", nil, nil, failure, []string{"render", "upload", "insert"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			var effects []string
			generation, err := application.NewGenerateDocument(
				rendererFunc(
					func(context.Context, domain.PolicyRegistration) ([]byte, error) {
						effects = append(effects, "render")
						return []byte("%PDF-synthetic"), test.renderError
					},
				),
				uploaderFunc(func(context.Context, string, []byte) error {
					effects = append(effects, "upload")
					return test.uploadError
				}),
				metadataWriterFunc(func(context.Context, string, string) error {
					effects = append(effects, "insert")
					return test.insertError
				}),
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			_, err = generation.Execute(context.Background(), registration)

			// then
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if !reflect.DeepEqual(effects, test.wantEffects) {
				t.Fatalf("effects = %v, want %v", effects, test.wantEffects)
			}
		})
	}
}

func TestGenerateDocument_Execute_HonorsCancellationBeforeEffects(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	generation, err := application.NewGenerateDocument(
		rendererFunc(func(context.Context, domain.PolicyRegistration) ([]byte, error) {
			called = true
			return nil, nil
		}),
		uploaderFunc(func(context.Context, string, []byte) error { return nil }),
		metadataWriterFunc(func(context.Context, string, string) error { return nil }),
	)
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, err = generation.Execute(ctx, syntheticRegistration(t))

	// then
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error = %v, renderer called = %t", err, called)
	}
}

func syntheticRegistration(t *testing.T) domain.PolicyRegistration {
	t.Helper()
	registration, err := domain.NewPolicyRegistration(domain.PolicyRegistrationInput{
		Number:       "SYN-363",
		From:         [3]int{2026, 2, 3},
		To:           [3]int{2027, 2, 2},
		PolicyHolder: "Synthetic Holder",
		ProductCode:  "HSI",
		TotalPremium: "9007199254740993.1234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	return registration
}
