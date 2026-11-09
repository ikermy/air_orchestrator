package mysql

// retention.go — периодическая чистка исторических таблиц.

import (
	"context"
	"fmt"
	"time"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// RetentionStats — сколько строк удалено за проход.
type RetentionStats struct {
	UserBotEvents int64
	AuditLog      int64
	WebhookDedup  int64
}

// RunRetention удаляет устаревшие события ботов, записи аудита и webhook-дедупа.
// Удаление идёт пакетами, чтобы не держать долгие блокировки.
func (i *Implementation) RunRetention(ctx context.Context, events, audit, webhook time.Duration) (RetentionStats, error) {
	var stats RetentionStats

	deleteOlderThan := func(table, column string, retention time.Duration, batch int) (int64, error) {
		if retention <= 0 {
			return 0, nil
		}
		if batch <= 0 {
			batch = 5000
		}
		seconds := int64(retention / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		var total int64
		for {
			res, err := i.Conn().ExecContext(ctx, fmt.Sprintf(
				"DELETE FROM `%s` WHERE `%s` < DATE_SUB(NOW(3), INTERVAL ? SECOND) LIMIT ?",
				table, column), seconds, batch)
			if err != nil {
				return total, fmt.Errorf("retention %s: %w", table, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return total, fmt.Errorf("retention %s rows: %w", table, err)
			}
			total += n
			if n < int64(batch) {
				return total, nil
			}
			if err := ctx.Err(); err != nil {
				return total, err
			}
		}
	}

	var err error
	if stats.UserBotEvents, err = deleteOlderThan("user_bot_events", "created_at", events, 5000); err != nil {
		return stats, err
	}
	if stats.AuditLog, err = deleteOlderThan("audit_log", "created_at", audit, 5000); err != nil {
		return stats, err
	}
	if stats.WebhookDedup, err = deleteOlderThan("webhook_dedup", "created_at", webhook, 5000); err != nil {
		return stats, err
	}
	if stats != (RetentionStats{}) {
		logger.Info("DB: retention: events=%d audit=%d webhook_dedup=%d",
			stats.UserBotEvents, stats.AuditLog, stats.WebhookDedup)
	}
	return stats, nil
}
