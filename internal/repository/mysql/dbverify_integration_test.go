//go:build integration

package mysql

import (
	"context"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/dbverify"
)

// TestDBVerifySelfLocal executes every DefaultTables query against the local
// schema and compares a user with itself (must produce no diffs).
func TestDBVerifySelfLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999993
	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_auth WHERE UserId=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id=?", testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'dbverify-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash)
		VALUES (?, 'sha', 'dbverify@example.test', NULL)`, testUserID); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}

	diffs, err := dbverify.CompareUser(ctx, repo.Conn(), repo.Conn(), testUserID, nil)
	if err != nil {
		t.Fatalf("CompareUser: %v", err)
	}
	if len(diffs) != 0 {
		t.Fatalf("self-comparison produced diffs: %+v", diffs)
	}
}
