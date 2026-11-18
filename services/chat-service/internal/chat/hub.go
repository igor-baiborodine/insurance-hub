// Package chat owns process-local Chat room and session behavior.
package chat

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	// ErrDraining reports that the hub no longer accepts new sessions.
	ErrDraining = errors.New("chat hub is draining")
	// ErrSessionClosed reports that a session is no longer owned by the hub.
	ErrSessionClosed = errors.New("chat session is closed")
)

// Message is an immutable text payload shared across outbound session queues.
type Message string

// BroadcastEvent identifies the bounded kind of room event being delivered.
type BroadcastEvent uint8

const (
	BroadcastEventJoin BroadcastEvent = iota + 1
	BroadcastEventMessage
	BroadcastEventDisconnect
)

// DeliveryOutcome identifies the bounded result of one attempted room delivery.
type DeliveryOutcome uint8

const (
	DeliveryOutcomeDelivered DeliveryOutcome = iota + 1
	DeliveryOutcomeQueueFull
)

// Observer receives bounded delivery outcomes without exposing Chat content or identities.
type Observer interface {
	ObserveBroadcast(BroadcastEvent, DeliveryOutcome)
}

// RemovalReason identifies why a session stopped belonging to the hub.
type RemovalReason uint32

const (
	RemovalUnknown RemovalReason = iota
	RemovalLeft
	RemovalQueueFull
	RemovalDraining
)

// Session is one process-local participant and its bounded outbound queue.
type Session struct {
	hub      *Hub
	room     *room
	username string
	outbound chan Message
	done     chan struct{}
	reason   atomic.Uint32
}

// Messages returns the session's outbound text messages.
func (session *Session) Messages() <-chan Message { return session.outbound }

// Done closes when the session no longer belongs to the hub.
func (session *Session) Done() <-chan struct{} { return session.done }

// RemovalReason reports why Done closed, or RemovalUnknown while the session is active.
func (session *Session) RemovalReason() RemovalReason {
	return RemovalReason(session.reason.Load())
}

type room struct {
	topic    string
	sessions []*Session
}

type delivery struct {
	room    *room
	origin  *Session
	message Message
	event   BroadcastEvent
}

// Hub serializes process-local room membership and message delivery.
type Hub struct {
	mu                sync.Mutex
	outboundQueueSize int
	rooms             []*room
	draining          bool
	observer          Observer
}

// NewHub creates a process-local hub with the configured per-session outbound queue size.
func NewHub(outboundQueueSize int, observer Observer) (*Hub, error) {
	if outboundQueueSize <= 0 {
		return nil, errors.New("create chat hub: outbound queue size must be positive")
	}
	return &Hub{outboundQueueSize: outboundQueueSize, observer: observer}, nil
}

// Join adds a session and broadcasts the accepted join message to its peers.
func (hub *Hub) Join(topic, username string) (*Session, error) {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	if hub.draining {
		return nil, ErrDraining
	}
	joinedRoom := hub.findOrCreateRoom(topic)
	session := &Session{
		hub:      hub,
		room:     joinedRoom,
		username: username,
		outbound: make(chan Message, hub.outboundQueueSize),
		done:     make(chan struct{}),
	}
	joinedRoom.sessions = append(joinedRoom.sessions, session)
	hub.deliverLocked(delivery{
		room:    joinedRoom,
		origin:  session,
		message: Message("<p>[" + username + "] Joined!</p>"),
		event:   BroadcastEventJoin,
	})
	return session, nil
}

// Broadcast sends an immutable copy of payload to every other session in the same room.
func (hub *Hub) Broadcast(origin *Session, payload []byte) error {
	message := Message(string(payload))

	hub.mu.Lock()
	defer hub.mu.Unlock()

	if !hub.containsLocked(origin) {
		return ErrSessionClosed
	}
	hub.deliverLocked(delivery{
		room: origin.room, origin: origin, message: message, event: BroadcastEventMessage,
	})
	return nil
}

// Leave removes an active session once and broadcasts its disconnect message unless draining.
func (hub *Hub) Leave(session *Session) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	if !hub.removeLocked(session, RemovalLeft) {
		return false
	}
	if !hub.draining {
		hub.deliverLocked(disconnectDelivery(session))
	}
	return true
}

// BeginDrain rejects new sessions and removes current sessions without disconnect broadcasts.
func (hub *Hub) BeginDrain() {
	hub.mu.Lock()
	defer hub.mu.Unlock()

	if hub.draining {
		return
	}
	hub.draining = true
	for _, joinedRoom := range hub.rooms {
		for _, session := range joinedRoom.sessions {
			session.reason.Store(uint32(RemovalDraining))
			close(session.done)
		}
		joinedRoom.sessions = nil
	}
	hub.rooms = nil
}

func (hub *Hub) findOrCreateRoom(topic string) *room {
	for _, joinedRoom := range hub.rooms {
		if strings.EqualFold(joinedRoom.topic, topic) {
			return joinedRoom
		}
	}
	joinedRoom := &room{topic: topic}
	hub.rooms = append(hub.rooms, joinedRoom)
	return joinedRoom
}

func (hub *Hub) containsLocked(session *Session) bool {
	if session == nil || session.hub != hub || session.room == nil {
		return false
	}
	for _, candidate := range session.room.sessions {
		if candidate == session {
			return true
		}
	}
	return false
}

func (hub *Hub) removeLocked(session *Session, reason RemovalReason) bool {
	if !hub.containsLocked(session) {
		return false
	}
	joinedRoom := session.room
	for index, candidate := range joinedRoom.sessions {
		if candidate != session {
			continue
		}
		joinedRoom.sessions = append(
			joinedRoom.sessions[:index],
			joinedRoom.sessions[index+1:]...)
		break
	}
	session.reason.Store(uint32(reason))
	close(session.done)
	if len(joinedRoom.sessions) == 0 {
		hub.removeRoomLocked(joinedRoom)
	}
	return true
}

func (hub *Hub) removeRoomLocked(emptyRoom *room) {
	for index, joinedRoom := range hub.rooms {
		if joinedRoom != emptyRoom {
			continue
		}
		hub.rooms = append(hub.rooms[:index], hub.rooms[index+1:]...)
		return
	}
}

func (hub *Hub) deliverLocked(first delivery) {
	pending := []delivery{first}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		recipients := append([]*Session(nil), current.room.sessions...)
		for _, recipient := range recipients {
			if recipient == current.origin || !hub.containsLocked(recipient) {
				continue
			}
			select {
			case recipient.outbound <- current.message:
				hub.observe(current.event, DeliveryOutcomeDelivered)
			default:
				hub.observe(current.event, DeliveryOutcomeQueueFull)
				if hub.removeLocked(recipient, RemovalQueueFull) && !hub.draining {
					pending = append(pending, disconnectDelivery(recipient))
				}
			}
		}
	}
}

func (hub *Hub) observe(event BroadcastEvent, outcome DeliveryOutcome) {
	if hub.observer != nil {
		hub.observer.ObserveBroadcast(event, outcome)
	}
}

func disconnectDelivery(session *Session) delivery {
	return delivery{
		room:    session.room,
		message: Message("<p>[" + session.username + "] Disconnected!</p>"),
		event:   BroadcastEventDisconnect,
	}
}
