// Package websocket owns the Chat HTTP upgrade and WebSocket protocol boundary.
package websocket

import (
	"errors"
	"net/http"

	gorilla "github.com/gorilla/websocket"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

// Lifecycle adapts concrete WebSocket connections to the process connection registry.
type Lifecycle struct {
	Register   func(*Connection) error
	Unregister func(*Connection)
}

// Handler exposes only the accepted Chat WebSocket route.
type Handler struct {
	hub       *chat.Hub
	settings  config.WebSocketSettings
	lifecycle Lifecycle
	upgrader  gorilla.Upgrader
	slots     chan struct{}
	mux       *http.ServeMux
}

// NewHandler constructs the bounded Chat WebSocket adapter.
func NewHandler(
	hub *chat.Hub,
	settings config.WebSocketSettings,
	lifecycle Lifecycle,
) (*Handler, error) {
	if hub == nil {
		return nil, errors.New("create WebSocket handler: chat hub is required")
	}
	if settings.MaxConnections <= 0 || settings.MaxMessageBytes <= 0 ||
		settings.WriteTimeout <= 0 || settings.PingInterval <= 0 ||
		settings.PongTimeout <= 0 || settings.CloseTimeout <= 0 {
		return nil, errors.New("create WebSocket handler: invalid settings")
	}
	if lifecycle.Register == nil || lifecycle.Unregister == nil {
		return nil, errors.New("create WebSocket handler: connection lifecycle is required")
	}

	handler := &Handler{
		hub:       hub,
		settings:  settings,
		lifecycle: lifecycle,
		upgrader: gorilla.Upgrader{
			HandshakeTimeout:  settings.WriteTimeout,
			EnableCompression: false,
		},
		slots: make(chan struct{}, settings.MaxConnections),
		mux:   http.NewServeMux(),
	}
	handler.mux.HandleFunc("GET /ws/chat/{topic}/{username}", handler.upgrade)
	return handler, nil
}

// ServeHTTP delegates exact method/path selection to the standard library mux.
func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) upgrade(writer http.ResponseWriter, request *http.Request) {
	var registered *Connection
	select {
	case handler.slots <- struct{}{}:
		defer func() {
			<-handler.slots
			if registered != nil {
				handler.lifecycle.Unregister(registered)
			}
		}()
	default:
		http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	transport, err := handler.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	connection := newConnection(transport, handler.hub, handler.settings)
	if err := handler.lifecycle.Register(connection); err != nil {
		connection.reject(gorilla.CloseGoingAway)
		return
	}
	registered = connection

	session, err := handler.hub.Join(request.PathValue("topic"), request.PathValue("username"))
	if err != nil {
		connection.reject(gorilla.CloseGoingAway)
		return
	}
	connection.serve(session)
}
