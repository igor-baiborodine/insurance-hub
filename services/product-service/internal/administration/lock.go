package administration

import (
	"context"
	"fmt"
	"time"
)

// productLockID serializes Product migration, grant setup and catalog seeding.
const productLockID int64 = 190001

const (
	administrationLockBudget       = 30 * time.Second
	administrationLockPollInterval = time.Second
)

func acquireProductLock(ctx context.Context, db queryRower) error {
	lockCtx, cancel := context.WithTimeout(ctx, administrationLockBudget)
	defer cancel()
	ticker := time.NewTicker(administrationLockPollInterval)
	defer ticker.Stop()
	for {
		var acquired bool
		if err := db.QueryRowContext(
			lockCtx,
			`SELECT pg_try_advisory_xact_lock($1)`,
			productLockID,
		).Scan(&acquired); err != nil {
			return fmt.Errorf("acquire product administration lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-lockCtx.Done():
			return fmt.Errorf("acquire product administration lock: %w", lockCtx.Err())
		case <-ticker.C:
		}
	}
}
