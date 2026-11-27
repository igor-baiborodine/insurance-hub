package administration

import (
	"context"
	"fmt"
	"time"
)

// paymentLockID serializes Payment schema administration.
const paymentLockID int64 = 190005

const (
	administrationLockBudget       = 30 * time.Second
	administrationLockPollInterval = time.Second
)

func acquirePaymentLock(ctx context.Context, db queryRower) error {
	lockCtx, cancel := context.WithTimeout(ctx, administrationLockBudget)
	defer cancel()
	ticker := time.NewTicker(administrationLockPollInterval)
	defer ticker.Stop()
	for {
		var acquired bool
		if err := db.QueryRowContext(lockCtx, acquireAdministrationLockQuery, paymentLockID).
			Scan(&acquired); err != nil {
			return fmt.Errorf("acquire Payment administration lock: %w", err)
		}
		if acquired {
			return nil
		}
		select {
		case <-lockCtx.Done():
			return fmt.Errorf("acquire Payment administration lock: %w", lockCtx.Err())
		case <-ticker.C:
		}
	}
}
