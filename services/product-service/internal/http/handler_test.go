package producthttp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
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
			listBody := fixtures["FAIL-PRODUCT-SINGLE-LIST-001"].Expected.RawBody
			if test.id == "FAIL-PRODUCT-EMPTY-LIST-001" {
				listBody = fixtures[test.id].Expected.RawBody
			}
			handler := NewHandler(Callbacks{
				List: func(writer http.ResponseWriter, _ *http.Request) {
					calls <- "list"
					writeTestJSON(writer, listBody)
				},
				Get: func(writer http.ResponseWriter, request *http.Request, code string) {
					calls <- "get:" + code
					if code == "ONLY" {
						writeTestJSON(
							writer,
							fixtures["FAIL-PRODUCT-SINGLE-GET-001"].Expected.RawBody,
						)
						return
					}
					WriteNotFound(writer, request)
				},
			})
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
			if response.Header.Get("Location") != "" || !response.Close {
				t.Errorf(
					"unexpected redirect or persistent connection: %v",
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

func writeTestJSON(writer http.ResponseWriter, body string) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, body)
}

func startTestServer(t *testing.T, handler http.Handler, compatible bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	done := make(chan error, 1)
	go func() {
		if compatible {
			done <- Serve(server, listener)
			return
		}
		done <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("test HTTP server did not stop")
		}
	})
	return listener.Addr().String()
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
