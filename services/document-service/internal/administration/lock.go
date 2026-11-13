package administration

import (
	"context"
	"fmt"
	"time"
)

// documentLockID serializes Document migration and grant setup.
const documentLockID int64 = 190002

const (
	administrationLockBudget       = 30 * time.Second
	administrationLockPollInterval = time.Second
)

func acquireDocumentLock(ctx context.Context, db queryRower) error {
	lockCtx, cancel := context.WithTimeout(ctx, administrationLockBudget)
	defer cancel()
	ticker := time.NewTicker(administrationLockPollInterval)
	defer ticker.Stop()
	for {
		var acquired bool
		if err := db.QueryRowContext(
			lockCtx,
			acquireAdministrationLockQuery,
			documentLockID,
		).Scan(&acquired); err != nil {
			return fmt.Errorf("acquire document administration lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-lockCtx.Done():
			return fmt.Errorf("acquire document administration lock: %w", lockCtx.Err())
		case <-ticker.C:
		}
	}
}
