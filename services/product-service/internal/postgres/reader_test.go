package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
)

func TestReaderErrorClassificationPreservesSafeCauses(t *testing.T) {
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
