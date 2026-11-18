package websocket

import (
	"context"
	"errors"
	"sync"
	"time"

	gorilla "github.com/gorilla/websocket"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

type closeRequest struct{ code int }

// Connection owns one Gorilla reader, one writer and bounded lifecycle completion.
type Connection struct {
	transport     *gorilla.Conn
	hub           *chat.Hub
	settings      config.WebSocketSettings
	closeRequests chan closeRequest
	done          chan struct{}
	closeOnce     sync.Once
}

func newConnection(
	transport *gorilla.Conn,
	hub *chat.Hub,
	settings config.WebSocketSettings,
) *Connection {
	return &Connection{
		transport:     transport,
		hub:           hub,
		settings:      settings,
		closeRequests: make(chan closeRequest, 1),
		done:          make(chan struct{}),
	}
}

// Close requests a service-drain close without blocking the registry caller.
func (connection *Connection) Close() error {
	connection.requestClose(gorilla.CloseGoingAway)
	return nil
}

// Wait joins the connection owner or forcibly closes the transport when ctx expires.
func (connection *Connection) Wait(ctx context.Context) error {
	select {
	case <-connection.done:
		return nil
	case <-ctx.Done():
		connection.closeTransport()
		return ctx.Err()
	}
}

func (connection *Connection) reject(code int) {
	deadline := time.Now().Add(connection.settings.CloseTimeout)
	_ = connection.transport.WriteControl(
		gorilla.CloseMessage,
		gorilla.FormatCloseMessage(code, ""),
		deadline,
	)
	connection.closeTransport()
	close(connection.done)
}

func (connection *Connection) serve(session *chat.Session) CloseReason {
	writerResult := make(chan error, 1)
	go func() {
		writerResult <- connection.writePump(session)
		connection.closeTransport()
	}()

	closeCode := connection.readPump(session)
	connection.requestClose(closeCode)
	connection.hub.Leave(session)
	<-writerResult
	connection.closeTransport()
	close(connection.done)
	return closeReasonForConnection(closeCode, session.RemovalReason())
}

func (connection *Connection) readPump(session *chat.Session) int {
	connection.transport.SetReadLimit(connection.settings.MaxMessageBytes)
	if err := connection.transport.SetReadDeadline(
		time.Now().Add(connection.settings.PongTimeout),
	); err != nil {
		return gorilla.CloseInternalServerErr
	}
	connection.transport.SetPongHandler(func(string) error {
		return connection.transport.SetReadDeadline(
			time.Now().Add(connection.settings.PongTimeout),
		)
	})
	connection.transport.SetCloseHandler(func(code int, text string) error {
		return &gorilla.CloseError{Code: code, Text: text}
	})

	for {
		messageType, payload, err := connection.transport.ReadMessage()
		if err != nil {
			return closeCodeForReadError(err)
		}
		if messageType != gorilla.TextMessage {
			return gorilla.CloseUnsupportedData
		}
		if err := connection.hub.Broadcast(session, payload); err != nil {
			return closeCodeForSession(session)
		}
	}
}

func (connection *Connection) writePump(session *chat.Session) error {
	ticker := time.NewTicker(connection.settings.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case request := <-connection.closeRequests:
			return connection.writeClose(request.code)
		default:
		}

		select {
		case request := <-connection.closeRequests:
			return connection.writeClose(request.code)
		case <-session.Done():
			select {
			case request := <-connection.closeRequests:
				return connection.writeClose(request.code)
			default:
				return connection.writeClose(closeCodeForSession(session))
			}
		case message := <-session.Messages():
			if err := connection.writeText(message); err != nil {
				return err
			}
		case <-ticker.C:
			if err := connection.writePing(); err != nil {
				return err
			}
		}
	}
}

func (connection *Connection) writeText(message chat.Message) error {
	if err := connection.transport.SetWriteDeadline(
		time.Now().Add(connection.settings.WriteTimeout),
	); err != nil {
		return err
	}
	return connection.transport.WriteMessage(gorilla.TextMessage, []byte(message))
}

func (connection *Connection) writePing() error {
	return connection.transport.WriteControl(
		gorilla.PingMessage,
		nil,
		time.Now().Add(connection.settings.WriteTimeout),
	)
}

func (connection *Connection) writeClose(code int) error {
	return connection.transport.WriteControl(
		gorilla.CloseMessage,
		gorilla.FormatCloseMessage(code, ""),
		time.Now().Add(connection.settings.CloseTimeout),
	)
}

func (connection *Connection) requestClose(code int) {
	select {
	case connection.closeRequests <- closeRequest{code: code}:
	default:
	}
}

func (connection *Connection) closeTransport() {
	connection.closeOnce.Do(func() { _ = connection.transport.Close() })
}

func closeCodeForReadError(err error) int {
	if errors.Is(err, gorilla.ErrReadLimit) {
		return gorilla.CloseMessageTooBig
	}
	var closeErr *gorilla.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code
	}
	return gorilla.CloseInternalServerErr
}

func closeCodeForSession(session *chat.Session) int {
	switch session.RemovalReason() {
	case chat.RemovalQueueFull:
		return gorilla.CloseTryAgainLater
	case chat.RemovalDraining:
		return gorilla.CloseGoingAway
	default:
		return gorilla.CloseNormalClosure
	}
}

func closeReasonForConnection(code int, removal chat.RemovalReason) CloseReason {
	switch removal {
	case chat.RemovalQueueFull:
		return CloseReasonSlowPeer
	case chat.RemovalDraining:
		return CloseReasonDraining
	default:
	}
	switch code {
	case gorilla.CloseNormalClosure, gorilla.CloseGoingAway, gorilla.CloseNoStatusReceived:
		return CloseReasonNormal
	case gorilla.CloseUnsupportedData:
		return CloseReasonUnsupportedData
	case gorilla.CloseMessageTooBig:
		return CloseReasonMessageTooLarge
	case gorilla.CloseTryAgainLater:
		return CloseReasonSlowPeer
	default:
		return CloseReasonTransportFailure
	}
}
