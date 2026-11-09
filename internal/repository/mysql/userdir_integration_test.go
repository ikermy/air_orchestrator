//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/dbrouter"
)

// TestBackfillUserDirectoryAllLocal populates user_directory for all users.
// Run with RUN_BACKFILL_USERDIRECTORY=true.
func TestBackfillUserDirectoryAllLocal(t *testing.T) {
	if os.Getenv("RUN_BACKFILL_USERDIRECTORY") != "true" {
		t.Skip("set RUN_BACKFILL_USERDIRECTORY=true to backfill the global user directory")
	}

	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	stats, err := repo.BackfillUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("BackfillUserDirectory: %v", err)
	}
	t.Logf("user directory backfill: %+v", stats)

	issues, err := repo.VerifyUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("VerifyUserDirectory: %v", err)
	}
	t.Logf("user directory issues: %d", len(issues))
	for _, issue := range issues {
		t.Logf("  user=%d: %s", issue.UserID, issue.Reason)
	}
}

func TestBackfillUserDirectoryLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999995
	emailHash := make([]byte, 32)
	for i := range emailHash {
		emailHash[i] = byte(0xA0 + i)
	}

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_directory WHERE user_id=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_auth WHERE UserId=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id=?", testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'user-directory-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash)
		VALUES (?, 'sha', ?, ?)`, testUserID, "userdir-test@example.test", hex.EncodeToString(emailHash)); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}

	slot := dbrouter.SlotForUser(uint64(testUserID))

	// nil resolver => single-shard layout (shard 0), which matches local config.
	stats, err := repo.BackfillUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("BackfillUserDirectory: %v", err)
	}
	if stats.Inserted < 1 {
		t.Fatalf("expected at least one inserted row, got %+v", stats)
	}

	userID, gotSlot, gotShard, err := repo.LookupUserDirectoryByEmailHash(ctx, emailHash)
	if err != nil {
		t.Fatalf("LookupUserDirectoryByEmailHash: %v", err)
	}
	if userID != uint64(testUserID) {
		t.Fatalf("user_id=%d want %d", userID, testUserID)
	}
	if gotSlot != slot {
		t.Fatalf("slot=%d want %d", gotSlot, slot)
	}
	if gotShard != 0 {
		t.Fatalf("shard=%d want 0", gotShard)
	}

	gotSlot, gotShard, err = repo.LookupUserDirectory(ctx, uint64(testUserID))
	if err != nil {
		t.Fatalf("LookupUserDirectory: %v", err)
	}
	if gotSlot != slot || gotShard != 0 {
		t.Fatalf("lookup by user: slot=%d shard=%d", gotSlot, gotShard)
	}

	// Idempotent: the second run has nothing to backfill.
	stats, err = repo.BackfillUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("second BackfillUserDirectory: %v", err)
	}
	if stats.Total != 0 {
		t.Fatalf("second run found %d rows, want 0", stats.Total)
	}

	unknown := make([]byte, 32)
	if _, _, _, err := repo.LookupUserDirectoryByEmailHash(ctx, unknown); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestVerifyUserDirectoryLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999994
	emailHash := make([]byte, 32)
	for i := range emailHash {
		emailHash[i] = byte(0xB0 + i)
	}

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_directory WHERE user_id=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_auth WHERE UserId=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id=?", testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'user-directory-verify', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash)
		VALUES (?, 'sha', ?, ?)`, testUserID, "userdir-verify@example.test", hex.EncodeToString(emailHash)); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}
	if _, err := repo.BackfillUserDirectory(ctx, nil); err != nil {
		t.Fatalf("BackfillUserDirectory: %v", err)
	}

	issuesFor := func(issues []comdb.UserDirectoryIssue) []comdb.UserDirectoryIssue {
		var out []comdb.UserDirectoryIssue
		for _, issue := range issues {
			if issue.UserID == testUserID {
				out = append(out, issue)
			}
		}
		return out
	}

	issues, err := repo.VerifyUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("VerifyUserDirectory: %v", err)
	}
	if got := issuesFor(issues); len(got) != 0 {
		t.Fatalf("unexpected issues after backfill: %+v", got)
	}

	slot := dbrouter.SlotForUser(testUserID)
	if _, err := repo.Conn().ExecContext(ctx,
		"UPDATE user_directory SET slot=? WHERE user_id=?", (slot+1)%1024, testUserID); err != nil {
		t.Fatalf("corrupt slot: %v", err)
	}
	issues, err = repo.VerifyUserDirectory(ctx, nil)
	if err != nil {
		t.Fatalf("VerifyUserDirectory: %v", err)
	}
	if got := issuesFor(issues); len(got) != 1 || !strings.Contains(got[0].Reason, "slot mismatch") {
		t.Fatalf("expected slot mismatch, got %+v", got)
	}

	if _, err := repo.Conn().ExecContext(ctx,
		"UPDATE user_directory SET slot=?, shard_id=? WHERE user_id=?", slot, (slot%4+1)%4, testUserID); err != nil {
		t.Fatalf("corrupt shard: %v", err)
	}
	issues, err = repo.VerifyUserDirectory(ctx, func(s uint16) uint16 { return s % 4 })
	if err != nil {
		t.Fatalf("VerifyUserDirectory: %v", err)
	}
	if got := issuesFor(issues); len(got) != 1 || !strings.Contains(got[0].Reason, "shard mismatch") {
		t.Fatalf("expected shard mismatch, got %+v", got)
	}
}

func TestRecomputeUserDirectoryShardsLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999992
	emailHash := make([]byte, 32)
	for i := range emailHash {
		emailHash[i] = byte(0xC0 + i)
	}

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_directory WHERE user_id=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM user_auth WHERE UserId=?", testUserID)
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM users WHERE Id=?", testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'user-directory-recompute', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, EmailHash)
		VALUES (?, 'sha', ?, ?)`, testUserID, "userdir-recompute@example.test", hex.EncodeToString(emailHash)); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}
	if _, err := repo.BackfillUserDirectory(ctx, nil); err != nil {
		t.Fatalf("BackfillUserDirectory: %v", err)
	}

	slot := dbrouter.SlotForUser(testUserID)
	resolver := func(s uint16) uint16 {
		if s == slot {
			return 7
		}
		return 0
	}
	updated, err := repo.RecomputeUserDirectoryShards(ctx, resolver)
	if err != nil {
		t.Fatalf("RecomputeUserDirectoryShards: %v", err)
	}
	if updated < 1 {
		t.Fatalf("updated=%d, want >=1", updated)
	}

	_, shard, err := repo.LookupUserDirectory(ctx, testUserID)
	if err != nil {
		t.Fatalf("LookupUserDirectory: %v", err)
	}
	if shard != 7 {
		t.Fatalf("shard=%d, want 7", shard)
	}

	// Recompute back to single-shard layout and verify idempotency.
	updated, err = repo.RecomputeUserDirectoryShards(ctx, func(uint16) uint16 { return 0 })
	if err != nil {
		t.Fatalf("RecomputeUserDirectoryShards restore: %v", err)
	}
	if updated < 1 {
		t.Fatalf("restore updated=%d, want >=1", updated)
	}
	updated, err = repo.RecomputeUserDirectoryShards(ctx, func(uint16) uint16 { return 0 })
	if err != nil {
		t.Fatalf("RecomputeUserDirectoryShards idempotent: %v", err)
	}
	if updated != 0 {
		t.Fatalf("second restore updated=%d, want 0", updated)
	}
}
