//go:build integration

package mysql

import (
	"context"
	"testing"
	"time"
)

func TestRetentionLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const eventID = "retention-test-event"
	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM webhook_dedup WHERE source='retention-test'")
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM audit_log WHERE action='retention.test'")
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO webhook_dedup (source, event_id, created_at)
		VALUES ('retention-test', ?, DATE_SUB(NOW(3), INTERVAL 30 DAY))`, eventID); err != nil {
		t.Fatalf("insert webhook_dedup: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO audit_log (user_id, actor_id, action, details, created_at)
		VALUES (0, 0, 'retention.test', JSON_OBJECT(), DATE_SUB(NOW(3), INTERVAL 400 DAY))`); err != nil {
		t.Fatalf("insert audit_log: %v", err)
	}

	stats, err := repo.RunRetention(ctx, 90*24*time.Hour, 365*24*time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("RunRetention: %v", err)
	}
	if stats.WebhookDedup != 1 {
		t.Fatalf("webhook_dedup deleted=%d, want 1", stats.WebhookDedup)
	}
	if stats.AuditLog < 1 {
		t.Fatalf("audit_log deleted=%d, want >=1", stats.AuditLog)
	}

	var n int
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM webhook_dedup WHERE source='retention-test'").Scan(&n); err != nil {
		t.Fatalf("count webhook_dedup: %v", err)
	}
	if n != 0 {
		t.Fatalf("old webhook_dedup row was not deleted")
	}
}
