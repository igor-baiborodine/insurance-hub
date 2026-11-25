package application

import "context"

// EventIntent is one durable Kafka publication request. Trace fields are the only propagated
// headers; an intent created before trace persistence was introduced leaves them empty.
type EventIntent struct {
	ID          [16]byte
	Topic       string
	Key         string
	Payload     []byte
	TraceParent string
	TraceState  string
}

// EventIntentStore locks and publishes the next eligible intent transactionally. The callback
// runs while the selected row is locked; a successful callback and published marker commit
// together, while any failure leaves the intent pending.
type EventIntentStore interface {
	PublishNext(
		context.Context,
		func(context.Context, EventIntent) error,
	) (bool, error)
}
