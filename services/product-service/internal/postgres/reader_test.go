package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
)

func TestReaderErrorClassification_PreservesSafeCauses(t *testing.T) {
	// given
	sensitiveCause := errors.New("connection failed with secret-password")

	// when
	storageErr := classify("list products", sensitiveCause)
	deadlineErr := classify("list products", context.DeadlineExceeded)

	// then
	if !errors.Is(storageErr, application.ErrUnavailable) ||
		!errors.Is(storageErr, sensitiveCause) {
		t.Errorf("storage classification = %v", storageErr)
	}
	if strings.Contains(storageErr.Error(), "secret-password") {
		t.Errorf("storage diagnostic exposed cause: %v", storageErr)
	}
	if !errors.Is(deadlineErr, context.DeadlineExceeded) ||
		errors.Is(deadlineErr, application.ErrUnavailable) {
		t.Errorf("deadline classification = %v", deadlineErr)
	}
}

func TestPoolAndReaderConstructors_RejectInvalidDependencies(t *testing.T) {
	// given
	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "non-positive pool maximum",
			run: func() error {
				_, err := openPool(
					context.Background(),
					"postgresql://localhost/product",
					0,
					time.Second,
				)
				return err
			},
		},
		{
			name: "non-positive connect timeout",
			run: func() error {
				_, err := openPool(
					context.Background(),
					"postgresql://localhost/product",
					1,
					0,
				)
				return err
			},
		},
		{
			name: "missing reader pool",
			run: func() error {
				_, err := NewReader(nil, time.Second, time.Second)
				return err
			},
		},
		{
			name: "missing readiness pool",
			run: func() error {
				return CheckReadAccess(context.Background(), nil)
			},
		},
		{
			name: "non-positive acquire timeout",
			run: func() error {
				_, err := NewReader(nil, 0, time.Second)
				return err
			},
		},
		{
			name: "non-positive query timeout",
			run: func() error {
				_, err := NewReader(nil, time.Second, 0)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := test.run()

			// then
			if err == nil {
				t.Fatal("constructor error = nil")
			}
		})
	}
}
