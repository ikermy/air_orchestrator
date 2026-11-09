package mysql

import (
	"context"
	"fmt"
	"sync/atomic"
)

var webhookInsertCount atomic.Uint64

// RegisterWebhookEvent records a webhook event id for idempotency.
// It returns true when the event is new and false when it is a duplicate.
// Old rows are cleaned up lazily (7 days) every 1000 registrations.
func (i *Implementation) RegisterWebhookEvent(ctx context.Context, source, eventID string) (bool, error) {
	res, err := i.Conn().ExecContext(ctx, `
		INSERT IGNORE INTO webhook_dedup (source, event_id) VALUES (?, ?)`,
		source, eventID)
	if err != nil {
		return false, fmt.Errorf("RegisterWebhookEvent: %w", err)
	}
	affected, _ := res.RowsAffected()

	if n := webhookInsertCount.Add(1); n%1000 == 0 {
		_, _ = i.Conn().ExecContext(ctx, `
			DELETE FROM webhook_dedup
			WHERE created_at < NOW(3) - INTERVAL 7 DAY
			LIMIT 1000`)
	}
	return affected > 0, nil
}
