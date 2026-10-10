package producthttp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type failureCase struct {
	ID       string `json:"id"`
	RawPath  string `json:"rawPath"`
	Expected struct {
		Status      int    `json:"status"`
		ContentType string `json:"contentType"`
		RawBody     string `json:"rawBody"`
	} `json:"expected"`
}

func TestDirectProductRoutingMatchesAcceptedBoundaryCases(t *testing.T) {
	fixtures := readFailureCases(t)
	tests := []struct {
		id       string
		wantCall string
	}{
		{id: "FAIL-PRODUCT-EMPTY-LIST-001", wantCall: "list"},
		{id: "FAIL-PRODUCT-SINGLE-LIST-001", wantCall: "list"},
		{id: "FAIL-PRODUCT-SINGLE-GET-001", wantCall: "get:ONLY"},
		{id: "FAIL-PRODUCT-MISSING-001", wantCall: "get:MISSING"},
		{id: "FAIL-PRODUCT-LOOKUP-LOWERCASE-001", wantCall: "get:only"},
		{id: "FAIL-PRODUCT-LOOKUP-WHITESPACE-001", wantCall: "get: ONLY "},
		{id: "FAIL-PRODUCT-LOOKUP-ENCODED-SLASH-001", wantCall: "get:ONLY/EXTRA"},
		{id: "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001"},
		{id: "FAIL-PRODUCT-LOOKUP-EMPTY-SEGMENT-001", wantCall: "list"},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			fixture, ok := fixtures[test.id]
			if !ok {
				t.Fatalf("missing accepted case %s", test.id)
			}
			calls := make(chan string, 2)
			listResult := []domain.Product{{Code: "ONLY", Name: "Only product"}}
			if test.id == "FAIL-PRODUCT-EMPTY-LIST-001" {
				listResult = []domain.Product{}
			}
			handler := newTestHandler(t,
				func(context.Context) ([]domain.Product, error) {
					calls <- "list"
					return listResult, nil
				},
				func(_ context.Context, code string) (domain.Product, error) {
					calls <- "get:" + code
					if code == "ONLY" {
						return domain.Product{
							Code: "ONLY",
							Name: "Only product",
						}, nil
					}
					return domain.Product{}, application.ErrProductNotFound
				},
			)
			address := startTestServer(t, handler, true)
			response, body := rawGET(
				t,
				address,
				fixture.RawPath,
				test.id == "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001",
			)
			if response.StatusCode != fixture.Expected.Status ||
				response.Header.Get(
					"Content-Type",
				) != fixture.Expected.ContentType ||
				body != fixture.Expected.RawBody {
				t.Errorf(
					"response = (%d, %q, %q), want (%d, %q, %q)",
					response.StatusCode,
					response.Header.Get("Content-Type"),
					body,
					fixture.Expected.Status,
					fixture.Expected.ContentType,
					fixture.Expected.RawBody,
				)
			}
			wantClose := test.id == "FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001"
			if response.Header.Get("Location") != "" || response.Close != wantClose {
				t.Errorf(
					"unexpected redirect or connection policy: %v",
					response.Header,
				)
			}
			if test.wantCall == "" {
				select {
				case call := <-calls:
					t.Errorf(
						"rejected request invoked application callback %q",
						call,
					)
				default:
				}
				return
			}
			select {
			case call := <-calls:
				if call != test.wantCall {
					t.Errorf(
						"application call = %q, want %q",
						call,
						test.wantCall,
					)
				}
			default:
				t.Errorf("missing application call %q", test.wantCall)
			}
			select {
			case call := <-calls:
				t.Errorf("unexpected additional application call %q", call)
			default:
			}
		})
	}
}

func TestNetHTTPRejectsMalformedEscapeBeforeHandlerWithNonJSONBody(t *testing.T) {
	called := make(chan struct{}, 1)
	address := startTestServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
	}), false)
	response, _ := rawGET(t, address, "/products/%ZZ", true)
	if response.StatusCode != http.StatusBadRequest ||
		response.Header.Get("Content-Type") == "application/json" {
		t.Errorf("native malformed URI response = (%d, %q)",
			response.StatusCode, response.Header.Get("Content-Type"))
	}
	select {
	case <-called:
		t.Error("native server invoked handler for malformed URI")
	default:
	}
}

func TestCompatibilityListenerBoundsRequestLineAndHeaderTime(t *testing.T) {
	called := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called <- struct{}{}
	})
	address := startTestServer(t, handler, true)
	response, body := rawGET(
		t,
		address,
		"/products/"+strings.Repeat("A", maxRequestLineBytes),
		true,
	)
	if response.StatusCode != http.StatusRequestURITooLong || body != "" {
		t.Errorf(
			"overlong request = (%d, %q), want (414, empty)",
			response.StatusCode,
			body,
		)
	}
	select {
	case <-called:
		t.Error("overlong request invoked application handler")
	default:
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	if err := Serve(&http.Server{Handler: handler}, listener); err == nil {
		t.Error("compatibility listener accepted an unbounded read-header timeout")
	}

	listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	if err := Serve(&http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
	}, listener); err == nil {
		t.Error("compatibility listener accepted an unbounded idle timeout")
	}
}

func TestCompatibilityListenerPreservesKeepAliveAndIdleTimeout(t *testing.T) {
	// given
	var calls atomic.Int64
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(writer, "ok")
	})
	server, address, done := startConfiguredTestServer(t, handler, 40*time.Millisecond)
	defer stopTestServer(t, server, done)
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)

	// when
	first := requestOnConnection(t, connection, reader, "/products")
	second := requestOnConnection(t, connection, reader, "/products/ONLY")
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	_, idleErr := reader.ReadByte()

	// then
	if first.StatusCode != http.StatusOK || first.Close ||
		second.StatusCode != http.StatusOK || second.Close || calls.Load() != 2 {
		t.Fatalf(
			"responses=(%d close=%t, %d close=%t) calls=%d",
			first.StatusCode,
			first.Close,
			second.StatusCode,
			second.Close,
			calls.Load(),
		)
	}
	if !errors.Is(idleErr, io.EOF) {
		t.Fatalf("idle connection read error = %v, want EOF", idleErr)
	}
}

func TestCompatibilityListenerChecksMalformedTargetAfterKeepAliveRequest(t *testing.T) {
	// given
	var calls atomic.Int64
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(writer, "ok")
	})
	server, address, done := startConfiguredTestServer(t, handler, time.Second)
	defer stopTestServer(t, server, done)
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(connection)
	first := requestOnConnection(t, connection, reader, "/products")

	// when
	if _, err := fmt.Fprint(
		connection,
		"GET /products/%ZZ HTTP/1.1\r\nHost: localhost\r\n\r\n",
	); err != nil {
		t.Fatal(err)
	}
	malformed, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(malformed.Body)
	_ = malformed.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	// then
	fixture := readFailureCases(t)["FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001"]
	if first.StatusCode != http.StatusOK || first.Close ||
		malformed.StatusCode != fixture.Expected.Status ||
		malformed.Header.Get("Content-Type") != fixture.Expected.ContentType ||
		string(body) != fixture.Expected.RawBody || !malformed.Close || calls.Load() != 1 {
		t.Fatalf(
			"first=(%d close=%t) malformed=(%d close=%t type=%q body=%q) calls=%d",
			first.StatusCode,
			first.Close,
			malformed.StatusCode,
			malformed.Close,
			malformed.Header.Get("Content-Type"),
			body,
			calls.Load(),
		)
	}
}

func TestHandlerSerializesLegacyProductShapeLosslessly(t *testing.T) {
	// given
	fraction, err := domain.NewDecimal("12.3400")
	if err != nil {
		t.Fatal(err)
	}
	precision, err := domain.NewDecimal("12345678901234567890.123456789")
	if err != nil {
		t.Fatal(err)
	}
	choice, err := domain.NewQuestion("CHOICE", 3, "Choice", domain.ChoiceQuestion{
		Choices: []domain.Choice{{Code: "B", Label: "Second"}, {Code: "A", Label: "First"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	date, err := domain.NewQuestion("DATE", 1, "Date", domain.DateQuestion{})
	if err != nil {
		t.Fatal(err)
	}
	numeric, err := domain.NewQuestion("NUMBER", 2, "Number", domain.NumericQuestion{})
	if err != nil {
		t.Fatal(err)
	}
	product := domain.Product{
		Code:        "EDGE",
		Name:        "Rich",
		Image:       "/edge.png",
		Description: "Synthetic",
		Covers: []domain.Cover{
			{Code: "FRACTION", Optional: true, SumInsured: &fraction},
			{Code: "PRECISION", Description: "exact", SumInsured: &precision},
			{Code: "ABSENT"},
		},
		Questions: []domain.Question{
			choice,
			date,
			numeric,
		},
		MaxNumberOfInsured: 7,
		Icon:               "edge",
	}
	handler := newTestHandler(
		t,
		func(context.Context) ([]domain.Product, error) { return []domain.Product{product}, nil },
		func(context.Context, string) (domain.Product, error) { return product, nil },
	)

	// when
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products/EDGE", nil))

	// then
	want := `{"code":"EDGE","name":"Rich","image":"/edge.png","description":"Synthetic",` +
		`"covers":[{"code":"FRACTION","optional":true,"sumInsured":12.3400},` +
		`{"code":"PRECISION","description":"exact","optional":false,` +
		`"sumInsured":12345678901234567890.123456789},{"code":"ABSENT","optional":false}],` +
		`"questions":[{"type":"choice","code":"CHOICE","index":3,"text":"Choice",` +
		`"choices":[{"code":"B","label":"Second"},{"code":"A","label":"First"}]},` +
		`{"type":"date","code":"DATE","index":1,"text":"Date"},` +
		`{"type":"numeric","code":"NUMBER","index":2,"text":"Number"}],` +
		`"maxNumberOfInsured":7,"icon":"edge"}`
	if response.Code != http.StatusOK ||
		response.Header().
			Get("Content-Type") !=
			"application/json" || response.Body.String() != want {
		t.Errorf(
			"response = (%d, %q, %s), want (200, application/json, %s)",
			response.Code,
			response.Header().Get("Content-Type"),
			response.Body.String(),
			want,
		)
	}
}

func TestHandlerOmitsEmptyLegacyFieldsButKeepsScalarDefaults(t *testing.T) {
	handler := newTestHandler(t,
		func(context.Context) ([]domain.Product, error) { return []domain.Product{}, nil },
		func(context.Context, string) (domain.Product, error) {
			return domain.Product{Code: "ONLY"}, nil
		},
	)

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/products", nil))
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/products/ONLY", nil))

	if list.Body.String() != "[]" || get.Body.String() !=
		`{"code":"ONLY","maxNumberOfInsured":0}` {
		t.Errorf("list=%s get=%s", list.Body.String(), get.Body.String())
	}
}

func TestHandlerMapsErrorsSafelyWithoutRetryOrPartialSuccess(t *testing.T) {
	privateMarker := "postgres://reader:secret@127.0.0.1/product raw definition"
	tests := []struct {
		name     string
		path     string
		list     ListProducts
		get      GetProduct
		wantCode int
		wantBody string
	}{
		{
			name: "missing",
			path: "/products/MISSING",
			list: func(context.Context) ([]domain.Product, error) { return nil, nil },
			get: func(context.Context, string) (domain.Product, error) {
				return domain.Product{}, fmt.Errorf(
					"lookup: %w",
					application.ErrProductNotFound,
				)
			},
			wantCode: http.StatusNotFound,
			wantBody: `{"message":"Page Not Found","_links":{"self":{"href":"/products/MISSING","templated":false}}}`,
		},
		{
			name: "unavailable",
			path: "/products",
			list: func(context.Context) ([]domain.Product, error) {
				return nil, errors.Join(
					application.ErrUnavailable,
					errors.New(privateMarker),
				)
			},
			get:      func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
			wantCode: http.StatusInternalServerError,
			wantBody: `{"message":"Internal Server Error"}`,
		},
		{
			name: "late invalid list item",
			path: "/products",
			list: func(context.Context) ([]domain.Product, error) {
				return []domain.Product{{Code: "GOOD"}, {}}, nil
			},
			get:      func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
			wantCode: http.StatusInternalServerError,
			wantBody: `{"message":"Internal Server Error"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			calls := 0
			handler, err := NewHandler(
				slog.New(
					slog.NewTextHandler(&logs, nil),
				),
				Settings{RequestTimeout: time.Second},
				func(ctx context.Context) ([]domain.Product, error) { calls++; return test.list(ctx) },
				func(ctx context.Context, code string) (domain.Product, error) {
					calls++
					return test.get(ctx, code)
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if response.Code != test.wantCode ||
				response.Body.String() != test.wantBody ||
				calls != 1 {
				t.Errorf(
					"response=(%d,%s) calls=%d",
					response.Code,
					response.Body.String(),
					calls,
				)
			}
			if strings.Contains(response.Body.String(), privateMarker) ||
				strings.Contains(logs.String(), privateMarker) {
				t.Fatalf(
					"private detail exposed: response=%s logs=%s",
					response.Body.String(),
					logs.String(),
				)
			}
		})
	}
}

func TestHandlerUsesCallerContextAndConfiguredDeadline(t *testing.T) {
	t.Run("caller cancellation writes no response", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		writer := &trackingResponseWriter{header: make(http.Header)}
		handler := newTestHandler(
			t,
			func(ctx context.Context) ([]domain.Product, error) { return nil, ctx.Err() },
			func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
		)
		handler.ServeHTTP(
			writer,
			httptest.NewRequest(http.MethodGet, "/products", nil).WithContext(ctx),
		)
		if writer.wroteHeader || writer.body.Len() != 0 {
			t.Errorf(
				"canceled request wrote status/body: %d %q",
				writer.status,
				writer.body.String(),
			)
		}
	})

	t.Run(
		"configured deadline returns safe error while caller remains connected",
		func(t *testing.T) {
			handler, err := NewHandler(
				slog.New(
					slog.NewTextHandler(io.Discard, nil),
				),
				Settings{RequestTimeout: time.Millisecond},
				func(ctx context.Context) ([]domain.Product, error) { <-ctx.Done(); return nil, ctx.Err() },
				func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
			)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(
				response,
				httptest.NewRequest(http.MethodGet, "/products", nil),
			)
			if response.Code != http.StatusInternalServerError ||
				response.Body.String() != `{"message":"Internal Server Error"}` {
				t.Errorf(
					"deadline response = (%d, %s)",
					response.Code,
					response.Body.String(),
				)
			}
		},
	)

	t.Run("configured deadline during mapping prevents success", func(t *testing.T) {
		// given
		product := productWithChoices(t, 100_000)
		applicationReturnedWithBudget := false
		handler, err := NewHandler(
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			Settings{RequestTimeout: time.Millisecond},
			func(ctx context.Context) ([]domain.Product, error) {
				applicationReturnedWithBudget = ctx.Err() == nil
				return []domain.Product{product}, nil
			},
			func(context.Context, string) (domain.Product, error) {
				return domain.Product{}, nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()

		// when
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/products", nil),
		)

		// then
		if !applicationReturnedWithBudget {
			t.Fatal("application did not return before the request deadline")
		}
		if response.Code != http.StatusInternalServerError ||
			response.Body.String() != `{"message":"Internal Server Error"}` {
			t.Errorf(
				"mapping deadline response = (%d, %s)",
				response.Code,
				response.Body.String(),
			)
		}
	})

	t.Run("configured deadline during serialization prevents success", func(t *testing.T) {
		// given
		product := domain.Product{
			Code:        "LARGE",
			Description: strings.Repeat("x", 8<<20),
		}
		applicationReturnedWithBudget := false
		handler, err := NewHandler(
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			Settings{RequestTimeout: time.Millisecond},
			func(ctx context.Context) ([]domain.Product, error) {
				applicationReturnedWithBudget = ctx.Err() == nil
				return []domain.Product{product}, nil
			},
			func(context.Context, string) (domain.Product, error) {
				return domain.Product{}, nil
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()

		// when
		handler.ServeHTTP(
			response,
			httptest.NewRequest(http.MethodGet, "/products", nil),
		)

		// then
		if !applicationReturnedWithBudget {
			t.Fatal("application did not return before the request deadline")
		}
		if response.Code != http.StatusInternalServerError ||
			response.Body.String() != `{"message":"Internal Server Error"}` {
			t.Errorf(
				"serialization deadline response = (%d, body bytes=%d)",
				response.Code,
				response.Body.Len(),
			)
		}
	})
}

func productWithChoices(t *testing.T, count int) domain.Product {
	t.Helper()
	choices := make([]domain.Choice, count)
	for index := range choices {
		choices[index] = domain.Choice{Code: "C", Label: "Choice"}
	}
	question, err := domain.NewQuestion(
		"CHOICE",
		1,
		"Choice",
		domain.ChoiceQuestion{Choices: choices},
	)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Product{Code: "LARGE", Questions: []domain.Question{question}}
}

func TestNewHandlerRejectsInvalidDependencies(t *testing.T) {
	list := ListProducts(func(context.Context) ([]domain.Product, error) { return nil, nil })
	get := GetProduct(func(context.Context, string) (domain.Product, error) {
		return domain.Product{}, nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name string
		log  *slog.Logger
		set  Settings
		list ListProducts
		get  GetProduct
	}{
		{name: "logger", set: Settings{RequestTimeout: time.Second}, list: list, get: get},
		{name: "timeout", log: logger, list: list, get: get},
		{name: "list", log: logger, set: Settings{RequestTimeout: time.Second}, get: get},
		{name: "get", log: logger, set: Settings{RequestTimeout: time.Second}, list: list},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewHandler(
				test.log,
				test.set,
				test.list,
				test.get,
			); err == nil {
				t.Error("NewHandler accepted invalid dependencies")
			}
		})
	}
}

type trackingResponseWriter struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func (writer *trackingResponseWriter) Header() http.Header { return writer.header }

func (writer *trackingResponseWriter) WriteHeader(status int) {
	writer.wroteHeader = true
	writer.status = status
}

func (writer *trackingResponseWriter) Write(body []byte) (int, error) {
	writer.wroteHeader = true
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.body.Write(body)
}

func newTestHandler(t *testing.T, list ListProducts, get GetProduct) http.Handler {
	t.Helper()
	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		Settings{RequestTimeout: time.Second},
		list,
		get,
	)
	if err != nil {
		t.Fatalf("new HTTP handler: %v", err)
	}
	return handler
}

func readFailureCases(t *testing.T) map[string]failureCase {
	t.Helper()
	data, err := os.ReadFile(
		"../../../../legacy/product-service/src/test/resources/product-read-baseline/failures/cases.json",
	)
	if err != nil {
		t.Fatalf("read accepted Product failures: %v", err)
	}
	var fixture struct {
		Cases []failureCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode accepted Product failures: %v", err)
	}
	cases := make(map[string]failureCase, len(fixture.Cases))
	for _, entry := range fixture.Cases {
		cases[entry.ID] = entry
	}
	return cases
}

func startTestServer(t *testing.T, handler http.Handler, compatible bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
		IdleTimeout:       time.Second,
	}
	done := make(chan error, 1)
	go func() {
		if compatible {
			done <- Serve(server, listener)
			return
		}
		done <- server.Serve(listener)
	}()
	t.Cleanup(func() { stopTestServer(t, server, done) })
	return listener.Addr().String()
}

func startConfiguredTestServer(
	t *testing.T,
	handler http.Handler,
	idleTimeout time.Duration,
) (*http.Server, string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
		IdleTimeout:       idleTimeout,
	}
	done := make(chan error, 1)
	go func() { done <- Serve(server, listener) }()
	return server, listener.Addr().String(), done
}

func stopTestServer(t *testing.T, server *http.Server, done <-chan error) {
	t.Helper()
	_ = server.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("test HTTP server did not stop")
	}
}

func requestOnConnection(
	t *testing.T,
	connection net.Conn,
	reader *bufio.Reader,
	path string,
) *http.Response {
	t.Helper()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(
		connection,
		"GET %s HTTP/1.1\r\nHost: localhost\r\n\r\n",
		path,
	); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return response
}

func rawGET(t *testing.T, address, path string, closeConnection bool) (*http.Response, string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial test HTTP server: %v", err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	connectionHeader := "keep-alive"
	if closeConnection {
		connectionHeader = "close"
	}
	if _, err := fmt.Fprintf(connection,
		"GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: %s\r\n\r\n",
		path, connectionHeader,
	); err != nil {
		t.Fatalf("write raw request: %v", err)
	}
	response, err := http.ReadResponse(
		bufio.NewReader(connection),
		&http.Request{Method: http.MethodGet},
	)
	if err != nil {
		t.Fatalf("read raw response: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return response, string(body)
}
