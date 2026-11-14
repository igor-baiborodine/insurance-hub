// Package jsreport owns the fixed, bounded POLICY rendering boundary.
package jsreport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

const reportPath = "/api/report"

// Settings contains the already-validated private JSReport origin and operation limits.
type Settings struct {
	Origin         *url.URL
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
	MaximumBytes   int
}

// Client renders the fixed POLICY report through a process-owned HTTP client.
type Client struct {
	client       *http.Client
	transport    *http.Transport
	reportURL    string
	requestLimit time.Duration
	maximumBytes int
}

var _ application.PolicyRenderer = (*Client)(nil)

// NewClient constructs a renderer that can call only the fixed report endpoint.
func NewClient(settings Settings) (*Client, error) {
	if settings.Origin == nil || settings.ConnectTimeout <= 0 || settings.RequestTimeout <= 0 ||
		settings.MaximumBytes <= 0 {
		return nil, errors.New(
			"create JSReport client: configuration and limits are required",
		)
	}
	if settings.Origin.Scheme != "http" || settings.Origin.Hostname() == "" ||
		settings.Origin.User != nil || settings.Origin.RawQuery != "" ||
		settings.Origin.Fragment != "" ||
		settings.Origin.Path != "" && settings.Origin.Path != "/" {
		return nil, errors.New(
			"create JSReport client: origin must be a private HTTP origin",
		)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: settings.ConnectTimeout}).DialContext
	origin := *settings.Origin
	origin.Path = reportPath
	origin.RawPath = ""
	return &Client{
		client: &http.Client{
			Transport: transport,
			Timeout:   settings.RequestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		transport:    transport,
		reportURL:    origin.String(),
		requestLimit: settings.RequestTimeout,
		maximumBytes: settings.MaximumBytes,
	}, nil
}

// Close releases retained idle HTTP connections.
func (client *Client) Close() {
	if client != nil && client.transport != nil {
		client.transport.CloseIdleConnections()
	}
}

type reportRequest struct {
	Template reportTemplate `json:"template"`
	Options  reportOptions  `json:"options"`
	Data     reportData     `json:"data"`
}

type reportTemplate struct {
	Name string `json:"name"`
}

type reportOptions struct {
	Timeout string `json:"timeout"`
}

type reportData struct {
	Number       string          `json:"number"`
	From         [3]int          `json:"from"`
	To           [3]int          `json:"to"`
	PolicyHolder string          `json:"policyHolder"`
	ProductCode  string          `json:"productCode"`
	TotalPremium json.RawMessage `json:"totalPremium"`
	AgentLogin   *string         `json:"agentLogin"`
}

// Render submits one exact legacy-compatible request and accepts only a useful bounded PDF.
func (client *Client) Render(
	ctx context.Context,
	registration domain.PolicyRegistration,
) ([]byte, error) {
	requestBody, err := json.Marshal(reportRequest{
		Template: reportTemplate{Name: "POLICY"},
		Options:  reportOptions{Timeout: "6000"},
		Data: reportData{
			Number: registration.Number(), From: registration.From().Parts(),
			To: registration.To().Parts(), PolicyHolder: registration.PolicyHolder(),
			ProductCode:  registration.ProductCode(),
			TotalPremium: json.RawMessage(registration.TotalPremium()),
			AgentLogin:   registration.AgentLogin(),
		},
	})
	if err != nil {
		return nil, errors.New("render policy report: encode accepted request")
	}
	requestCtx, cancel := context.WithTimeout(ctx, client.requestLimit)
	defer cancel()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodPost,
		client.reportURL,
		bytes.NewReader(requestBody),
	)
	if err != nil {
		return nil, errors.New("render policy report: create fixed request")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, classifyRenderFailure(requestCtx, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, renderUnavailable(errors.New("unexpected status"))
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/pdf" {
		return nil, renderUnavailable(errors.New("unexpected content type"))
	}
	pdf, err := io.ReadAll(io.LimitReader(response.Body, int64(client.maximumBytes)+1))
	if err != nil {
		return nil, classifyRenderFailure(requestCtx, err)
	}
	if len(pdf) == 0 || len(pdf) > client.maximumBytes ||
		!bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, renderUnavailable(errors.New("invalid PDF response"))
	}
	return pdf, nil
}

func classifyRenderFailure(operationCtx context.Context, cause error) error {
	if err := operationCtx.Err(); err != nil {
		return fmt.Errorf("render policy report: %w", err)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return fmt.Errorf("render policy report: %w", cause)
	}
	return renderUnavailable(cause)
}

func renderUnavailable(cause error) error {
	return renderError{cause: cause}
}

type renderError struct{ cause error }

func (renderError) Error() string { return "render policy report: dependency unavailable" }

func (failure renderError) Unwrap() []error {
	return []error{application.ErrUnavailable, failure.cause}
}
