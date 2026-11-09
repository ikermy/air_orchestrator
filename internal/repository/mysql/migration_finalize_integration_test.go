//go:build integration

package mysql

import (
	"context"
	"testing"
)

// TestFinalUserMigrationLocal проверяет выборку и удаление немигрировавших
// пользователей, не затрагивая остальные данные dev-БД.
func TestFinalUserMigrationLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		legacyID   uint64 = 999999981
		migratedID uint64 = 999999982
	)
	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM audit_log WHERE user_id IN (?, ?)", legacyID, migratedID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id IN (?, ?)", legacyID, migratedID)
	}
	defer cleanup()
	cleanup()

	for _, id := range []uint64{legacyID, migratedID} {
		if _, err := repo.Conn().ExecContext(ctx, `
			INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
			VALUES (?, 'final-migration-test', 1, 0, 1, 1, 'UTC', NOW())`, id); err != nil {
			t.Fatalf("create user %d: %v", id, err)
		}
	}

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash) VALUES (?, 'sha', ?, NULL)`,
		legacyID, "final-legacy@example.test"); err != nil {
		t.Fatalf("create legacy user_auth: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash) VALUES (?, 'sha', ?, ?)`,
		migratedID, "final-migrated@example.test", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("create migrated user_auth: %v", err)
	}

	ids, err := repo.listNonMigratedUserIDs(ctx, 100000)
	if err != nil {
		t.Fatalf("listNonMigratedUserIDs: %v", err)
	}
	found := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		found[id] = true
	}
	if !found[legacyID] {
		t.Fatalf("legacy user %d not selected", legacyID)
	}
	if found[migratedID] {
		t.Fatalf("migrated user %d must not be selected", migratedID)
	}

	if _, err := repo.purgeNonMigratedUsersByIDs(ctx, []uint64{legacyID}); err != nil {
		t.Fatalf("purgeNonMigratedUsersByIDs: %v", err)
	}

	countUser := func(id uint64) int {
		var n int
		if err := repo.Conn().QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE Id=?", id).Scan(&n); err != nil {
			t.Fatalf("count user %d: %v", id, err)
		}
		return n
	}
	if countUser(legacyID) != 0 {
		t.Fatalf("legacy user %d still exists", legacyID)
	}
	if countUser(migratedID) != 1 {
		t.Fatalf("migrated user %d was deleted", migratedID)
	}

	var auditCount int
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM audit_log WHERE user_id=? AND action='user.final_migration_purge'",
		legacyID).Scan(&auditCount); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("final_migration_purge audit=%d, want 1", auditCount)
	}

	// До наступления deadline воркер не должен ничего удалять.
	t.Setenv("USER_MIGRATION_DEADLINE", "2099-01-01T00:00:00Z")
	purged, finalized, err := repo.RunUserMigrationFinalization(ctx, 10)
	if err != nil {
		t.Fatalf("RunUserMigrationFinalization before deadline: %v", err)
	}
	if purged != 0 || finalized {
		t.Fatalf("before deadline: purged=%d finalized=%t, want 0/false", purged, finalized)
	}
	if countUser(migratedID) != 1 {
		t.Fatalf("migrated user %d disappeared", migratedID)
	}
}
