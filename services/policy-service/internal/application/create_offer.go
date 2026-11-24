// Package application owns Policy use cases, the ports they consume and their failures.
package application

import (
	"errors"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

var (
	// ErrProductNotFound reports that Product has no product with the submitted code.
	ErrProductNotFound = errors.New("product not found")
	// ErrQuoteRejected reports that Pricing rejected the quote input.
	ErrQuoteRejected = errors.New("quote input rejected")
	// ErrProviderUnavailable reports that Product or Pricing cannot currently serve the call.
	ErrProviderUnavailable = errors.New("provider unavailable")
)

// QuoteRequest is the Pricing input for one offer.
type QuoteRequest struct {
	ProductCode    string
	PolicyFrom     time.Time
	PolicyTo       time.Time
	SelectedCovers []string
	Answers        []domain.Answer
}
