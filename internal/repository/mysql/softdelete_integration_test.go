//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSoftDeleteAndPurgeLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999991
	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM audit_log WHERE user_id=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id=?", testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'soft-delete-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}

	if err := repo.SoftDeleteUserData(testUserID, testUserID); err != nil {
		t.Fatalf("SoftDeleteUserData: %v", err)
	}

	var (
		status    string
		deletedAt sql.NullTime
	)
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT status, deleted_at FROM users WHERE Id=?", testUserID).Scan(&status, &deletedAt); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "deleted" || !deletedAt.Valid {
		t.Fatalf("status=%q deletedAt=%v", status, deletedAt)
	}

	auditCount := func(action string) int {
		var n int
		if err := repo.Conn().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM audit_log WHERE user_id=? AND action=?", testUserID, action).Scan(&n); err != nil {
			t.Fatalf("audit count: %v", err)
		}
		return n
	}
	if got := auditCount("user.soft_delete"); got != 1 {
		t.Fatalf("soft_delete audit=%d, want 1", got)
	}

	// Idempotent: second call must not duplicate the audit entry.
	if err := repo.SoftDeleteUserData(testUserID, testUserID); err != nil {
		t.Fatalf("second SoftDeleteUserData: %v", err)
	}
	if got := auditCount("user.soft_delete"); got != 1 {
		t.Fatalf("soft_delete audit after second call=%d, want 1", got)
	}

	// Grace period not elapsed -> user must stay.
	if _, err := repo.PurgeDeletedUsers(ctx, time.Hour, 10); err != nil {
		t.Fatalf("PurgeDeletedUsers: %v", err)
	}
	var exists int
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE Id=?", testUserID).Scan(&exists); err != nil {
		t.Fatalf("count user: %v", err)
	}
	if exists != 1 {
		t.Fatalf("user was purged before the grace period")
	}

	// Zero grace -> user is hard-deleted and audited.
	if _, err := repo.PurgeDeletedUsers(ctx, 0, 10); err != nil {
		t.Fatalf("PurgeDeletedUsers(0): %v", err)
	}
	var remaining int
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE Id=?", testUserID).Scan(&remaining); err != nil {
		t.Fatalf("count user after purge: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("user still exists after purge")
	}
	if got := auditCount("user.hard_delete"); got != 1 {
		t.Fatalf("hard_delete audit=%d, want 1", got)
	}

	// Soft-delete of an unknown user is a no-op.
	if err := repo.SoftDeleteUserData(testUserID, testUserID); err != nil {
		t.Fatalf("SoftDeleteUserData(unknown): %v", err)
	}
}

func TestWriteAuditLogLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999990
	_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM audit_log WHERE user_id=?", testUserID)
	defer func() { _, _ = repo.Conn().ExecContext(ctx, "DELETE FROM audit_log WHERE user_id=?", testUserID) }()

	if err := repo.WriteAuditLog(ctx, testUserID, "test.action", json.RawMessage(`{"k":"v"}`)); err != nil {
		t.Fatalf("WriteAuditLog: %v", err)
	}

	var details sql.NullString
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT details FROM audit_log WHERE user_id=? AND action='test.action'", testUserID).Scan(&details); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !details.Valid || !strings.Contains(details.String, `"k"`) {
		t.Fatalf("unexpected details: %q", details.String)
	}
}
