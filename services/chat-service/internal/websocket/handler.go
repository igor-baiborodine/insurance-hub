// Package websocket owns the Chat HTTP upgrade and WebSocket protocol boundary.
package websocket

import (
	"context"
	"errors"
	"net/http"
	"time"

	gorilla "github.com/gorilla/websocket"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

// Lifecycle adapts concrete WebSocket connections to the process connection registry.
type Lifecycle struct {
	Register   func(*Connection) error
	Unregister func(*Connection)
}

// HandshakeOutcome identifies the bounded result of one accepted-route upgrade attempt.
type HandshakeOutcome uint8

const (
	HandshakeAccepted HandshakeOutcome = iota + 1
	HandshakeCapacityRejected
	HandshakeUpgradeFailed
)

// CloseReason identifies the bounded reason an upgraded connection stopped.
type CloseReason uint8

const (
	CloseReasonNormal CloseReason = iota + 1
	CloseReasonUnsupportedData
	CloseReasonMessageTooLarge
	CloseReasonSlowPeer
	CloseReasonDraining
	CloseReasonTransportFailure
)

// Observer receives bounded WebSocket lifecycle events without request or payload values.
type Observer interface {
	ObserveHandshake(context.Context, time.Time, HandshakeOutcome)
	ConnectionOpened()
	ConnectionClosed(CloseReason)
}

// Handler exposes only the accepted Chat WebSocket route.
type Handler struct {
	hub       *chat.Hub
	settings  config.WebSocketSettings
	lifecycle Lifecycle
	observer  Observer
	upgrader  gorilla.Upgrader
	slots     chan struct{}
	mux       *http.ServeMux
}

// NewHandler constructs the bounded Chat WebSocket adapter.
func NewHandler(
	hub *chat.Hub,
	settings config.WebSocketSettings,
	lifecycle Lifecycle,
	observer Observer,
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
		observer:  observer,
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
	started := time.Now()
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
		handler.observeHandshake(request.Context(), started, HandshakeCapacityRejected)
		http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	transport, err := handler.upgrader.Upgrade(writer, request, nil)
	if err != nil {
		handler.observeHandshake(request.Context(), started, HandshakeUpgradeFailed)
		return
	}
	handler.observeHandshake(request.Context(), started, HandshakeAccepted)
	if handler.observer != nil {
		handler.observer.ConnectionOpened()
	}
	closeReason := CloseReasonTransportFailure
	defer func() {
		if handler.observer != nil {
			handler.observer.ConnectionClosed(closeReason)
		}
	}()
	connection := newConnection(transport, handler.hub, handler.settings)
	if err := handler.lifecycle.Register(connection); err != nil {
		connection.reject(gorilla.CloseGoingAway)
		closeReason = CloseReasonDraining
		return
	}
	registered = connection

	session, err := handler.hub.Join(request.PathValue("topic"), request.PathValue("username"))
	if err != nil {
		connection.reject(gorilla.CloseGoingAway)
		closeReason = CloseReasonDraining
		return
	}
	closeReason = connection.serve(session)
}

func (handler *Handler) observeHandshake(
	ctx context.Context,
	started time.Time,
	outcome HandshakeOutcome,
) {
	if handler.observer != nil {
		handler.observer.ObserveHandshake(ctx, started, outcome)
	}
}
