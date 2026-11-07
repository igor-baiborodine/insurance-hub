package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

const maximumWriterResponseBytes = 4096

var ErrWriteFailed = errors.New("elasticsearch policy replacement failed")

// Writer performs refreshed whole-document replacement on the isolated projection index.
type Writer struct {
	client *Client
}

var _ application.PolicyProjectionWriter = (*Writer)(nil)

func NewWriter(client *Client) (*Writer, error) {
	if client == nil || client.Client == nil {
		return nil, ErrClientRequired
	}
	if client.index != ProjectionIndex {
		return nil, errors.New("configure elasticsearch writer: isolated index is required")
	}
	return &Writer{client: client}, nil
}

func (writer *Writer) ReplacePolicy(
	ctx context.Context,
	policy domain.PolicyProjection,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(newPolicyDocument(policy))
	if err != nil {
		return ErrWriteFailed
	}
	operationCtx, cancel := withOperationTimeout(ctx, writer.client.timeout)
	defer cancel()
	response, err := writer.client.Index(
		ProjectionIndex,
		bytes.NewReader(payload),
		writer.client.Index.WithContext(operationCtx),
		writer.client.Index.WithDocumentID(policy.Number),
		writer.client.Index.WithRefresh("true"),
	)
	if err != nil {
		if contextErr := operationCtx.Err(); contextErr != nil {
			return contextErr
		}
		return ErrWriteFailed
	}
	if response == nil || response.Body == nil {
		return ErrWriteFailed
	}
	defer func() { _ = response.Body.Close() }()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maximumWriterResponseBytes+1))
	if contextErr := operationCtx.Err(); contextErr != nil {
		return contextErr
	}
	if readErr != nil || len(data) > maximumWriterResponseBytes || response.IsError() {
		return ErrWriteFailed
	}
	var result struct {
		ID     string `json:"_id"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.ID != policy.Number ||
		(result.Result != "created" && result.Result != "updated") {
		return ErrWriteFailed
	}
	return nil
}

type policyDocument struct {
	Number       string          `json:"number"`
	From         *string         `json:"from"`
	To           *string         `json:"to"`
	PolicyHolder *string         `json:"policyHolder"`
	ProductCode  *string         `json:"productCode"`
	TotalPremium *domain.Premium `json:"totalPremium"`
	AgentLogin   *string         `json:"agentLogin"`
}

func newPolicyDocument(policy domain.PolicyProjection) policyDocument {
	return policyDocument{
		Number: policy.Number, From: dateString(policy.From), To: dateString(policy.To),
		PolicyHolder: policy.PolicyHolder, ProductCode: policy.ProductCode,
		TotalPremium: policy.TotalPremium, AgentLogin: policy.AgentLogin,
	}
}

func dateString(date *domain.Date) *string {
	if date == nil {
		return nil
	}
	value := date.String()
	return &value
}
