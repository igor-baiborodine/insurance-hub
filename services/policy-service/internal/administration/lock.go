package administration

import (
	"context"
	"fmt"
	"time"
)

// policyLockID serializes Policy migration and grant setup.
const policyLockID int64 = 190004

const (
	administrationLockBudget       = 30 * time.Second
	administrationLockPollInterval = time.Second
)

func acquirePolicyLock(ctx context.Context, db queryRower) error {
	lockCtx, cancel := context.WithTimeout(ctx, administrationLockBudget)
	defer cancel()
	ticker := time.NewTicker(administrationLockPollInterval)
	defer ticker.Stop()
	for {
		var acquired bool
		if err := db.QueryRowContext(lockCtx, acquireAdministrationLockQuery, policyLockID).
			Scan(&acquired); err != nil {
			return fmt.Errorf("acquire Policy administration lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-lockCtx.Done():
			return fmt.Errorf("acquire Policy administration lock: %w", lockCtx.Err())
		case <-ticker.C:
		}
	}
}
