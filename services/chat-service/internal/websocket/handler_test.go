package websocket_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
	chatwebsocket "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/websocket"
)

func TestHandler_RouteAndHandshakeContract(t *testing.T) {
	// given
	server, tracker := newTestServer(t, testSettings())
	client := server.Client()

	// when
	wrongPath, err := client.Get(server.URL + "/ws/chat/main")
	if err != nil {
		t.Fatal(err)
	}
	wrongMethod, err := client.Post(server.URL+"/ws/chat/main/alice", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	nonUpgrade, err := client.Get(server.URL + "/ws/chat/main/alice")
	if err != nil {
		t.Fatal(err)
	}

	// then
	assertHTTPStatus(t, wrongPath, http.StatusNotFound)
	assertHTTPStatus(t, wrongMethod, http.StatusMethodNotAllowed)
	assertHTTPStatus(t, nonUpgrade, http.StatusBadRequest)
	if tracker.registerCount() != 0 {
		t.Fatalf("registered connections = %d, want 0", tracker.registerCount())
	}

	noOrigin, _, err := gorilla.DefaultDialer.Dial(webSocketURL(server, "/main/alice"), nil)
	if err != nil {
		t.Fatal(err)
	}
	closeNormally(t, noOrigin)

	dialer := gorilla.Dialer{
		EnableCompression: true,
		Subprotocols:      []string{"chat"},
	}
	header := make(http.Header)
	header.Set("Origin", server.URL)
	sameOrigin, response, err := dialer.Dial(webSocketURL(server, "/main/bob"), header)
	if err != nil {
		t.Fatal(err)
	}
	if sameOrigin.Subprotocol() != "" {
		t.Fatalf("negotiated subprotocol = %q", sameOrigin.Subprotocol())
	}
	if got := response.Header.Get("Sec-WebSocket-Extensions"); got != "" {
		t.Fatalf("negotiated extensions = %q", got)
	}
	closeNormally(t, sameOrigin)
}

func TestHandler_RealClientsPreserveChatContract(t *testing.T) {
	// given
	server, _ := newTestServer(t, testSettings())
	alice := dial(t, server, "/main/alice")
	carol := dial(t, server, "/other/carol")
	bob := dial(t, server, "/Main/bob")

	// when
	join := readText(t, alice)
	if err := bob.WriteMessage(
		gorilla.TextMessage,
		[]byte(`<span data-chat-parity="true">hello</span>`),
	); err != nil {
		t.Fatal(err)
	}
	message := readText(t, alice)
	if err := bob.WriteMessage(gorilla.TextMessage, nil); err != nil {
		t.Fatal(err)
	}
	empty := readText(t, alice)
	writeClose(t, bob, gorilla.CloseNormalClosure)
	disconnect := readText(t, alice)

	// then
	if join != "<p>[bob] Joined!</p>" {
		t.Fatalf("join = %q", join)
	}
	if message != `<span data-chat-parity="true">hello</span>` {
		t.Fatalf("message = %q", message)
	}
	if empty != "" {
		t.Fatalf("empty message = %q", empty)
	}
	if disconnect != "<p>[bob] Disconnected!</p>" {
		t.Fatalf("disconnect = %q", disconnect)
	}
	assertClose(t, bob, gorilla.CloseNormalClosure)
	writeClose(t, carol, gorilla.CloseNormalClosure)
	assertClose(t, carol, gorilla.CloseNormalClosure)
	closeNormally(t, alice)
}

func TestHandler_RejectsCapacityBeforeUpgrade(t *testing.T) {
	// given
	settings := testSettings()
	settings.MaxConnections = 1
	server, tracker := newTestServer(t, settings)
	first := dial(t, server, "/main/alice")
	waitForConnection(t, tracker)

	// when
	second, response, err := gorilla.DefaultDialer.Dial(
		webSocketURL(server, "/main/bob"),
		nil,
	)

	// then
	if second != nil {
		_ = second.Close()
		t.Fatal("capacity-limited connection upgraded")
	}
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("capacity result = response %v, error %v", response, err)
	}
	closeNormally(t, first)
	waitForUnregister(t, tracker)
	third := dial(t, server, "/main/carol")
	closeNormally(t, third)
}

func TestHandler_ClosesUnsupportedAndOversizedMessages(t *testing.T) {
	t.Run("binary gets 1003", func(t *testing.T) {
		// given
		server, _ := newTestServer(t, testSettings())
		client := dial(t, server, "/main/alice")

		// when
		if err := client.WriteMessage(gorilla.BinaryMessage, []byte("binary")); err != nil {
			t.Fatal(err)
		}

		// then
		assertClose(t, client, gorilla.CloseUnsupportedData)
	})

	t.Run("oversized text gets 1009", func(t *testing.T) {
		// given
		settings := testSettings()
		settings.MaxMessageBytes = 8
		server, _ := newTestServer(t, settings)
		client := dial(t, server, "/main/alice")

		// when
		if err := client.WriteMessage(
			gorilla.TextMessage,
			[]byte("123456789"),
		); err != nil {
			t.Fatal(err)
		}

		// then
		assertClose(t, client, gorilla.CloseMessageTooBig)
	})
}

func TestConnection_CloseAndWaitUseTrackedLifecycle(t *testing.T) {
	// given
	server, tracker := newTestServer(t, testSettings())
	client := dial(t, server, "/main/alice")
	connection := waitForConnection(t, tracker)

	// when
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}

	// then
	assertClose(t, client, gorilla.CloseGoingAway)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	waitForUnregister(t, tracker)
}

func TestConnection_WaitCancellationForcesTransportClose(t *testing.T) {
	// given
	server, tracker := newTestServer(t, testSettings())
	client := dial(t, server, "/main/alice")
	connection := waitForConnection(t, tracker)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	err := connection.Wait(ctx)

	// then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error = %v, want %v", err, context.Canceled)
	}
	if readErr := client.SetReadDeadline(time.Now().Add(time.Second)); readErr != nil {
		t.Fatal(readErr)
	}
	if _, _, readErr := client.ReadMessage(); readErr == nil {
		t.Fatal("client remained readable after forced transport close")
	}
	waitForUnregister(t, tracker)
}

func TestNewHandler_RejectsInvalidDependencies(t *testing.T) {
	hub, err := chat.NewHub(8)
	if err != nil {
		t.Fatal(err)
	}
	settings := testSettings()
	lifecycle := lifecycleForTests(newTracker())

	tests := []struct {
		name      string
		hub       *chat.Hub
		settings  config.WebSocketSettings
		lifecycle chatwebsocket.Lifecycle
	}{
		{"missing hub", nil, settings, lifecycle},
		{"zero capacity", hub, config.WebSocketSettings{}, lifecycle},
		{"missing lifecycle", hub, settings, chatwebsocket.Lifecycle{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := chatwebsocket.NewHandler(
				test.hub,
				test.settings,
				test.lifecycle,
			); err == nil {
				t.Fatal("invalid handler dependencies accepted")
			}
		})
	}
}

type tracker struct {
	mu           sync.Mutex
	connections  []*chatwebsocket.Connection
	registered   chan *chatwebsocket.Connection
	unregistered chan *chatwebsocket.Connection
}

func newTracker() *tracker {
	return &tracker{
		registered:   make(chan *chatwebsocket.Connection, 8),
		unregistered: make(chan *chatwebsocket.Connection, 8),
	}
}

func lifecycleForTests(tracker *tracker) chatwebsocket.Lifecycle {
	return chatwebsocket.Lifecycle{
		Register: func(connection *chatwebsocket.Connection) error {
			tracker.mu.Lock()
			tracker.connections = append(tracker.connections, connection)
			tracker.mu.Unlock()
			tracker.registered <- connection
			return nil
		},
		Unregister: func(connection *chatwebsocket.Connection) {
			tracker.unregistered <- connection
		},
	}
}

func (tracker *tracker) registerCount() int {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.connections)
}

func newTestServer(
	t *testing.T,
	settings config.WebSocketSettings,
) (*httptest.Server, *tracker) {
	t.Helper()
	hub, err := chat.NewHub(settings.OutboundQueueSize)
	if err != nil {
		t.Fatal(err)
	}
	tracker := newTracker()
	handler, err := chatwebsocket.NewHandler(hub, settings, lifecycleForTests(tracker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server, tracker
}

func testSettings() config.WebSocketSettings {
	return config.WebSocketSettings{
		MaxConnections:    8,
		MaxMessageBytes:   32 * 1024,
		OutboundQueueSize: 8,
		WriteTimeout:      time.Second,
		PingInterval:      time.Hour,
		PongTimeout:       time.Minute,
		CloseTimeout:      time.Second,
	}
}

func webSocketURL(server *httptest.Server, suffix string) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/chat" + suffix
}

func dial(t *testing.T, server *httptest.Server, suffix string) *gorilla.Conn {
	t.Helper()
	client, _, err := gorilla.DefaultDialer.Dial(webSocketURL(server, suffix), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func readText(t *testing.T, client *gorilla.Conn) string {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	messageType, payload, err := client.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != gorilla.TextMessage {
		t.Fatalf("message type = %d, want text", messageType)
	}
	return string(payload)
}

func writeClose(t *testing.T, client *gorilla.Conn, code int) {
	t.Helper()
	if err := client.WriteControl(
		gorilla.CloseMessage,
		gorilla.FormatCloseMessage(code, ""),
		time.Now().Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
}

func assertClose(t *testing.T, client *gorilla.Conn, wantCode int) {
	t.Helper()
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, _, err := client.ReadMessage()
	var closeErr *gorilla.CloseError
	if !errors.As(err, &closeErr) || closeErr.Code != wantCode {
		t.Fatalf("close error = %v, want code %d", err, wantCode)
	}
}

func closeNormally(t *testing.T, client *gorilla.Conn) {
	t.Helper()
	writeClose(t, client, gorilla.CloseNormalClosure)
	assertClose(t, client, gorilla.CloseNormalClosure)
}

func assertHTTPStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != want {
		t.Fatalf("HTTP status = %d, want %d", response.StatusCode, want)
	}
}

func waitForConnection(t *testing.T, tracker *tracker) *chatwebsocket.Connection {
	t.Helper()
	select {
	case connection := <-tracker.registered:
		return connection
	case <-time.After(time.Second):
		t.Fatal("connection was not registered")
		return nil
	}
}

func waitForUnregister(t *testing.T, tracker *tracker) {
	t.Helper()
	select {
	case <-tracker.unregistered:
	case <-time.After(time.Second):
		t.Fatal("connection was not unregistered")
	}
}
