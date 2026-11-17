package service

import (
	"context"
	"errors"
	"sync"
)

var errConnectionDrainStarted = errors.New("connection registry is draining")

// TrackedConnection is the protocol-neutral lifecycle seam for future upgraded connections.
// Close must initiate a forced close and Wait must honor cancellation while joining its owner.
type TrackedConnection interface {
	Close() error
	Wait(context.Context) error
}

// ConnectionRegistry owns upgraded connections that net/http Server.Shutdown cannot close.
type ConnectionRegistry struct {
	mutex       sync.Mutex
	connections map[TrackedConnection]struct{}
	draining    bool
}

// NewConnectionRegistry constructs an accepting registry.
func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{connections: make(map[TrackedConnection]struct{})}
}

// Register transfers lifecycle ownership until Unregister or Drain completes.
func (registry *ConnectionRegistry) Register(connection TrackedConnection) error {
	if connection == nil {
		return errors.New("register connection: connection is required")
	}
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if registry.draining {
		return errConnectionDrainStarted
	}
	registry.connections[connection] = struct{}{}
	return nil
}

// Unregister releases a completed connection from lifecycle ownership.
func (registry *ConnectionRegistry) Unregister(connection TrackedConnection) {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	delete(registry.connections, connection)
}

// Draining reports whether new connection ownership is rejected.
func (registry *ConnectionRegistry) Draining() bool {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	return registry.draining
}

// BeginDrain rejects new connection registrations before server shutdown starts.
func (registry *ConnectionRegistry) BeginDrain() {
	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	registry.draining = true
}

// Drain rejects new registrations, closes every snapshot connection, then joins each within ctx.
func (registry *ConnectionRegistry) Drain(ctx context.Context) error {
	registry.BeginDrain()
	registry.mutex.Lock()
	connections := make([]TrackedConnection, 0, len(registry.connections))
	for connection := range registry.connections {
		connections = append(connections, connection)
	}
	registry.mutex.Unlock()

	var result error
	for _, connection := range connections {
		result = errors.Join(result, connection.Close())
	}
	for _, connection := range connections {
		result = errors.Join(result, connection.Wait(ctx))
		registry.Unregister(connection)
	}
	return result
}
