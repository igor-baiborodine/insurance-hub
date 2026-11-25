package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
)

// PublishNext locks the oldest eligible pending intent, invokes publish while the lock is held,
// and marks the row only after broker acknowledgement. The query excludes every intent whose
// policy still has an earlier unpublished row, so concurrent publishers cannot overtake it.
func (s *Store) PublishNext(
	ctx context.Context,
	publish func(context.Context, application.EventIntent) error,
) (published bool, resultErr error) {
	if publish == nil {
		return false, errors.New("publish next Policy event: callback is required")
	}
	resultErr = s.InTransaction(ctx, func(ctx context.Context, tx Transaction) error {
		row, err := tx.queries.LockNextOutboxIntent(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lock next Policy event intent: %w", err)
		}
		if !row.IntentID.Valid {
			return errors.New("read next Policy event intent: intent ID is missing")
		}
		intent := application.EventIntent{
			ID:          row.IntentID.Bytes,
			Topic:       row.Topic,
			Key:         row.MessageKey,
			Payload:     row.Payload,
			TraceParent: row.TraceParent.String,
			TraceState:  row.TraceState.String,
		}
		if err := publish(ctx, intent); err != nil {
			return err
		}
		updated, err := tx.queries.MarkOutboxIntentPublished(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("mark Policy event intent published: %w", err)
		}
		if updated != 1 {
			return errors.New(
				"mark Policy event intent published: row changed concurrently",
			)
		}
		published = true
		return nil
	})
	return published, resultErr
}
