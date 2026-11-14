package jsreport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/jsreport"
)

func TestClient_Render_MapsAcceptedRequestWithoutPrecisionLoss(t *testing.T) {
	// given
	var requestBody map[string]any
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodPost || request.URL.Path != "/api/report" ||
				request.URL.RawQuery != "" || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf(
					"unexpected request: %s %s",
					request.Method,
					request.URL.String(),
				)
			}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&requestBody); err != nil {
				t.Error(err)
			}
			writer.Header().Set("Content-Type", "application/pdf; charset=binary")
			_, _ = io.WriteString(writer, "%PDF-SYN-363")
		}),
	)
	defer server.Close()
	client := newTestClient(t, server.URL, 1024)
	defer client.Close()

	// when
	pdf, err := client.Render(context.Background(), syntheticRegistration(t))

	// then
	if err != nil || string(pdf) != "%PDF-SYN-363" {
		t.Fatalf("pdf = %q, error = %v", pdf, err)
	}
	want := map[string]any{
		"template": map[string]any{"name": "POLICY"},
		"options":  map[string]any{"timeout": "6000"},
		"data": map[string]any{
			"number": "SYN-363",
			"from":   []any{json.Number("2026"), json.Number("2"), json.Number("3")},
			"to": []any{
				json.Number("2027"),
				json.Number("2"),
				json.Number("2"),
			},
			"policyHolder": "Synthetic Holder",
			"productCode":  "HSI",
			"totalPremium": json.Number("9007199254740993.1234567"),
			"agentLogin":   nil,
		},
	}
	if !reflect.DeepEqual(requestBody, want) {
		t.Fatalf("request = %#v, want %#v", requestBody, want)
	}
}

func TestClient_Render_RejectsUnsafeResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		maximum     int
	}{
		{"non-success", http.StatusBadGateway, "application/pdf", "%PDF-error", 32},
		{"wrong media type", http.StatusOK, "text/plain", "%PDF-wrong", 32},
		{"empty", http.StatusOK, "application/pdf", "", 32},
		{"non-PDF", http.StatusOK, "application/pdf", "not-pdf", 32},
		{"oversized", http.StatusOK, "application/pdf", "%PDF-oversized", 5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			server := httptest.NewServer(
				http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", test.contentType)
					writer.WriteHeader(test.status)
					_, _ = io.WriteString(writer, test.body)
				}),
			)
			defer server.Close()
			client := newTestClient(t, server.URL, test.maximum)
			defer client.Close()

			// when
			_, err := client.Render(context.Background(), syntheticRegistration(t))

			// then
			if !errors.Is(err, application.ErrUnavailable) ||
				test.body != "" && strings.Contains(err.Error(), test.body) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestClient_Render_HonorsRequestTimeout(t *testing.T) {
	// given
	release := make(chan struct{})
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			<-release
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(writer, "%PDF-late")
		}),
	)
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	client, err := jsreport.NewClient(jsreport.Settings{
		Origin: origin, ConnectTimeout: time.Second, RequestTimeout: 10 * time.Millisecond,
		MaximumBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// when
	_, err = client.Render(context.Background(), syntheticRegistration(t))
	close(release)

	// then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestClient_Render_DoesNotFollowRedirects(t *testing.T) {
	// given
	destinationCalls := 0
	destination := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			destinationCalls++
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(writer, "%PDF-leaked")
		}),
	)
	defer destination.Close()
	redirect := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(
				writer,
				request,
				destination.URL,
				http.StatusTemporaryRedirect,
			)
		}),
	)
	defer redirect.Close()
	client := newTestClient(t, redirect.URL, 1024)
	defer client.Close()

	// when
	_, err := client.Render(context.Background(), syntheticRegistration(t))

	// then
	if !errors.Is(err, application.ErrUnavailable) || destinationCalls != 0 {
		t.Fatalf("redirect result = (%v, destination calls %d)", err, destinationCalls)
	}
}

func TestClient_Render_DoesNotUseDefaultTransportProxy(t *testing.T) {
	// given
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = io.WriteString(writer, "%PDF-direct")
		}),
	)
	defer server.Close()
	proxyCalls := 0
	originalTransport := http.DefaultTransport
	http.DefaultTransport = &http.Transport{Proxy: func(*http.Request) (*url.URL, error) {
		proxyCalls++
		return nil, errors.New("unexpected proxy call")
	}}
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	client := newTestClient(t, server.URL, 1024)
	http.DefaultTransport = originalTransport
	defer client.Close()

	// when
	pdf, err := client.Render(context.Background(), syntheticRegistration(t))

	// then
	if err != nil || string(pdf) != "%PDF-direct" || proxyCalls != 0 {
		t.Fatalf("direct render = (%q, %v), proxy calls = %d", pdf, err, proxyCalls)
	}
}

func TestNewClient_RejectsNonFixedOriginAndInvalidLimits(t *testing.T) {
	tests := []string{
		"https://jsreport.example",
		"http://user@jsreport.example",
		"http://jsreport.example/path",
		"http://jsreport.example?query=value",
	}
	for _, value := range tests {
		origin, _ := url.Parse(value)
		if _, err := jsreport.NewClient(jsreport.Settings{
			Origin: origin, ConnectTimeout: time.Second, RequestTimeout: time.Second,
			MaximumBytes: 1,
		}); err == nil {
			t.Fatalf("unsafe origin %q accepted", value)
		}
	}
	if _, err := jsreport.NewClient(jsreport.Settings{}); err == nil {
		t.Fatal("empty settings accepted")
	}
}

func newTestClient(t *testing.T, endpoint string, maximum int) *jsreport.Client {
	t.Helper()
	origin, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client, err := jsreport.NewClient(jsreport.Settings{
		Origin: origin, ConnectTimeout: time.Second, RequestTimeout: time.Second,
		MaximumBytes: maximum,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func syntheticRegistration(t *testing.T) domain.PolicyRegistration {
	t.Helper()
	registration, err := domain.NewPolicyRegistration(domain.PolicyRegistrationInput{
		Number: "SYN-363", From: [3]int{2026, 2, 3}, To: [3]int{2027, 2, 2},
		PolicyHolder: "Synthetic Holder", ProductCode: "HSI",
		TotalPremium: "9007199254740993.1234567",
	})
	if err != nil {
		t.Fatal(err)
	}
	return registration
}
