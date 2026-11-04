package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	elasticsearchapi "github.com/elastic/go-elasticsearch/v8"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

var ErrWriteFailed = errors.New("elasticsearch policy replacement failed")

// Writer implements whole-document replacement for the policy projection port.
type Writer struct {
	client *elasticsearchapi.Client
	index  string
}

var _ application.PolicyViewWriter = (*Writer)(nil)

func NewWriter(client *Client) (*Writer, error) {
	if client == nil || client.Client == nil {
		return nil, ErrClientRequired
	}
	return &Writer{client: client.Client, index: client.index}, nil
}

func (writer *Writer) ReplacePolicy(ctx context.Context, policy domain.Policy) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(newPolicyDocument(policy))
	if err != nil {
		return ErrWriteFailed
	}
	response, err := writer.client.Index(
		writer.index,
		bytes.NewReader(payload),
		writer.client.Index.WithContext(ctx),
		writer.client.Index.WithDocumentID(policy.Number),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return ErrWriteFailed
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil ||
		response.IsError() {
		return ErrWriteFailed
	}
	return ctx.Err()
}

type policyDocument struct {
	Number       string  `json:"number"`
	DateFrom     *string `json:"dateFrom"`
	DateTo       *string `json:"dateTo"`
	PolicyHolder string  `json:"policyHolder"`
}

func newPolicyDocument(policy domain.Policy) policyDocument {
	return policyDocument{
		Number:       policy.Number,
		DateFrom:     dateString(policy.DateFrom),
		DateTo:       dateString(policy.DateTo),
		PolicyHolder: policy.PolicyHolder,
	}
}

func dateString(date *domain.Date) *string {
	if date == nil {
		return nil
	}
	value := date.String()
	return &value
}
