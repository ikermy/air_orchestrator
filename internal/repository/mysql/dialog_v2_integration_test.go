//go:build integration

package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/comdom"
	"github.com/ikermy/air-common/v2/pkg/crypto"
)

func TestBackfillDialogsLocal(t *testing.T) {
	if os.Getenv("RUN_BACKFILL") != "true" {
		t.Skip("set RUN_BACKFILL=true to backfill legacy dialogs")
	}
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	rows, err := repo.Conn().QueryContext(ctx,
		"SELECT Id FROM dialogs WHERE Data IS NOT NULL AND Data NOT LIKE ? ORDER BY Id",
		"$mk$%")
	if err != nil {
		t.Fatalf("list dialogs: %v", err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan dialog id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	var okCount, failCount int
	for _, id := range ids {
		if _, err := repo.ReadDialog(id); err != nil {
			failCount++
			continue
		}
		okCount++
	}
	t.Logf("backfill: dialogs=%d ok=%d failed=%d", len(ids), okCount, failCount)

	var mismatched int
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dialogs d
		WHERE d.messages_migrated = 1
		  AND JSON_LENGTH(d.Data) <> (SELECT COUNT(*) FROM dialog_messages m WHERE m.dialog_id = d.Id)`).
		Scan(&mismatched); err != nil {
		t.Fatalf("verify backfill: %v", err)
	}
	if mismatched != 0 {
		t.Fatalf("%d migrated dialogs have message count mismatch", mismatched)
	}
}

func TestDialogV2ReadCompareLocal(t *testing.T) {
	if os.Getenv("RUN_DIALOG_V2_COMPARE") != "true" {
		t.Skip("set RUN_DIALOG_V2_COMPARE=true to compare legacy and v2 reads")
	}
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	rows, err := repo.Conn().QueryContext(ctx,
		"SELECT Id FROM dialogs WHERE messages_migrated = 1 ORDER BY Id")
	if err != nil {
		t.Fatalf("list migrated dialogs: %v", err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatalf("scan dialog id: %v", err)
		}
		ids = append(ids, id)
	}
	rows.Close()

	var mismatches int
	for _, id := range ids {
		repo.DialogV2ReadEnabled = false
		legacy, err := repo.ReadDialog(id)
		if err != nil {
			t.Logf("dialog %d: legacy read failed: %v", id, err)
			mismatches++
			continue
		}
		repo.DialogV2ReadEnabled = true
		v2, err := repo.ReadDialog(id)
		if err != nil {
			t.Logf("dialog %d: v2 read failed: %v", id, err)
			mismatches++
			continue
		}
		if !reflect.DeepEqual(normalizeReadPayload(t, id, legacy), normalizeReadPayload(t, id, v2)) {
			mismatches++
		}
	}
	if mismatches != 0 {
		t.Fatalf("%d/%d dialogs differ between legacy and v2", mismatches, len(ids))
	}
	t.Logf("compared %d dialogs: all equal", len(ids))
}

func normalizeReadPayload(t *testing.T, id uint64, raw []byte) any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("dialog %d: unmarshal payload: %v", id, err)
	}
	if s, ok := obj["Data"].(string); ok {
		var arr any
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			obj["Data"] = arr
		}
	}
	if arr, ok := obj["Data"].([]any); ok {
		for i, item := range arr {
			if s, ok := item.(string); ok {
				var parsed any
				if err := json.Unmarshal([]byte(s), &parsed); err == nil {
					arr[i] = parsed
				}
			}
		}
	}
	return obj
}

func TestDialogEncryptedBackfillLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		testUserID      uint64 = 999999996
		testResponderID uint64 = 999999996
		testDialogID    uint64 = 999999996
	)

	key := [32]byte{}
	for i := range key {
		key[i] = byte(i + 1)
	}
	plain := `[{"creator":2,"message":"encrypted hello","timestamp":"2026-02-02T00:00:00Z"}]`
	encrypted, err := crypto.EncryptFieldWithMasterKey(key, plain)
	if err != nil {
		t.Fatalf("encrypt fixture: %v", err)
	}

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialogs WHERE Id=?`, testDialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE Id=?`, testResponderID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-encrypted-dialog', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO responders (Id, user_id, Type, Name, RealId, is_orphan) VALUES (?, ?, 0, 'integration-encrypted', 0, 0)`,
		testResponderID, testUserID); err != nil {
		t.Fatalf("create test responder: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO dialogs (Id, Type, User, Responder, Data) VALUES (?, 0, ?, ?, ?)`,
		testDialogID, testUserID, testResponderID, encrypted); err != nil {
		t.Fatalf("create test dialog: %v", err)
	}

	repo.SetMasterKeyResolver(func(userID uint64) ([32]byte, bool) {
		if userID == testUserID {
			return key, true
		}
		return [32]byte{}, false
	})

	if _, err := repo.ReadDialog(testDialogID); err != nil {
		t.Fatalf("ReadDialog: %v", err)
	}

	var (
		migrated bool
		content  string
	)
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT messages_migrated FROM dialogs WHERE Id=?`, testDialogID).Scan(&migrated); err != nil {
		t.Fatalf("read messages_migrated: %v", err)
	}
	if !migrated {
		t.Fatal("encrypted dialog was not marked as migrated")
	}
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT content FROM dialog_messages WHERE dialog_id=? AND seq=0`, testDialogID).Scan(&content); err != nil {
		t.Fatalf("read decrypted message: %v", err)
	}
	if content != "encrypted hello" {
		t.Fatalf("content = %q, want %q", content, "encrypted hello")
	}
}

func TestDialogV2Local(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		testUserID      uint64 = 999999995
		testResponderID uint64 = 999999995
		testDialogID    uint64 = 999999995
	)

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialogs WHERE Id=?`, testDialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE Id=?`, testResponderID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-dialog-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create test user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO responders (Id, user_id, Type, Name, RealId, is_orphan) VALUES (?, ?, 0, 'integration-responder', 0, 0)`,
		testResponderID, testUserID); err != nil {
		t.Fatalf("create test responder: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO dialogs (Id, Type, User, Responder, Data) VALUES (?, 0, ?, ?, '[]')`,
		testDialogID, testUserID, testResponderID); err != nil {
		t.Fatalf("create test dialog: %v", err)
	}

	msg := json.RawMessage(`{"creator":2,"message":"hello v2","timestamp":"2026-01-01T00:00:00Z"}`)
	if err := repo.SaveDialog(testDialogID, msg); err != nil {
		t.Fatalf("SaveDialog: %v", err)
	}
	var (
		count   int
		seq     uint64
		role    string
		content string
	)
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(seq), MIN(role), MIN(content)
		FROM dialog_messages WHERE dialog_id = ?`, testDialogID).
		Scan(&count, &seq, &role, &content); err != nil {
		t.Fatalf("read dialog_messages: %v", err)
	}
	if count != 1 || seq != 0 || role != "user" || content != "hello v2" {
		t.Fatalf("unexpected v2 message: count=%d seq=%d role=%s content=%q", count, seq, role, content)
	}

	ctxData := json.RawMessage(`{"thread":"abc"}`)
	if err := repo.SaveContext(testDialogID, comdom.ProviderMistral, ctxData); err != nil {
		t.Fatalf("SaveContext: %v", err)
	}
	var stored string
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT context_json FROM dialog_contexts WHERE dialog_id=? AND provider=?`,
		testDialogID, comdom.ProviderMistral.String()).Scan(&stored); err != nil {
		t.Fatalf("read dialog_contexts: %v", err)
	}
	if stored != string(ctxData) {
		t.Fatalf("context_json = %q, want %q", stored, ctxData)
	}

	if _, err := repo.ReadDialog(testDialogID); err != nil {
		t.Fatalf("ReadDialog: %v", err)
	}
	var migrated bool
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT messages_migrated FROM dialogs WHERE Id=?`, testDialogID).Scan(&migrated); err != nil {
		t.Fatalf("read messages_migrated: %v", err)
	}
	if !migrated {
		t.Fatal("dialog was not marked as migrated")
	}
}

func TestSaveReadContextLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		testUserID      uint64 = 999999979
		testResponderID uint64 = 999999979
		testDialogID    uint64 = 999999979
	)

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialog_contexts WHERE dialog_id=?`, testDialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialogs WHERE Id=?`, testDialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE Id=?`, testResponderID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-context', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO responders (Id, user_id, Type, Name, RealId, is_orphan)
		VALUES (?, ?, 0, 'integration-context', 0, 0)`, testResponderID, testUserID); err != nil {
		t.Fatalf("create responder: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO dialogs (Id, Type, User, Responder) VALUES (?, 0, ?, ?)`,
		testDialogID, testUserID, testResponderID); err != nil {
		t.Fatalf("create dialog: %v", err)
	}

	mistral := json.RawMessage(`{"thread":"abc","model":"m1"}`)
	openai := json.RawMessage(`{"thread":"def"}`)
	if err := repo.SaveContext(testDialogID, comdom.ProviderMistral, mistral); err != nil {
		t.Fatalf("SaveContext mistral: %v", err)
	}
	if err := repo.SaveContext(testDialogID, comdom.ProviderOpenAI, openai); err != nil {
		t.Fatalf("SaveContext openai: %v", err)
	}

	var legacyCtx string
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT Context FROM dialogs WHERE Id=?`, testDialogID).Scan(&legacyCtx); err != nil {
		t.Fatalf("read legacy Context: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(legacyCtx), &keys); err != nil {
		t.Fatalf("unmarshal legacy Context: %v", err)
	}
	if len(keys) != 2 || keys["mistral"] == nil || keys["openai"] == nil {
		t.Fatalf("legacy Context keys = %v", keys)
	}

	for _, enabled := range []bool{false, true} {
		repo.DialogV2ReadEnabled = enabled
		got, err := repo.ReadContext(testDialogID, comdom.ProviderMistral)
		if err != nil {
			t.Fatalf("ReadContext (v2=%v): %v", enabled, err)
		}
		if !reflect.DeepEqual(normalizeJSON(t, got), normalizeJSON(t, mistral)) {
			t.Fatalf("context (v2=%v) = %s, want %s", enabled, got, mistral)
		}
	}
}

func normalizeJSON(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal JSON %q: %v", raw, err)
	}
	return v
}

func TestDeleteDialogAndMetaLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		normalUser  uint64 = 999999992
		otherUser   uint64 = 999999991
		demoUser    uint64 = 999999990
		responderID uint64 = 999999990
		dialogID    uint64 = 999999990
		otherDialog uint64 = 999999991
	)

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialogs WHERE Id IN (?,?)`, dialogID, otherDialog)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE Id=?`, responderID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id IN (?,?,?)`, normalUser, otherUser, demoUser)
	}
	defer cleanup()
	cleanup()

	for _, u := range []struct {
		id   uint64
		role int
	}{
		{normalUser, 0},
		{otherUser, 0},
		{demoUser, 1},
	} {
		if _, err := repo.Conn().ExecContext(ctx, `
			INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
			VALUES (?, 'integration-delete-dialog', ?, 0, 1, 1, 'UTC', NOW())`, u.id, u.role); err != nil {
			t.Fatalf("create user %d: %v", u.id, err)
		}
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO responders (Id, user_id, Type, Name, RealId, is_orphan)
		VALUES (?, ?, 0, 'integration-delete', 0, 0)`, responderID, normalUser); err != nil {
		t.Fatalf("create responder: %v", err)
	}
	for _, d := range []struct {
		id   uint64
		user uint64
	}{
		{dialogID, normalUser},
		{otherDialog, otherUser},
	} {
		if _, err := repo.Conn().ExecContext(ctx, `
			INSERT INTO dialogs (Id, Type, User, Responder, Data) VALUES (?, 0, ?, ?, '[]')`,
			d.id, d.user, responderID); err != nil {
			t.Fatalf("create dialog %d: %v", d.id, err)
		}
	}

	if err := repo.DeleteDialog(otherUser, dialogID); err == nil {
		t.Fatal("DeleteDialog allowed deleting another user's dialog")
	}
	if err := repo.DeleteDialog(demoUser, dialogID); !errors.Is(err, comdb.ErrDemoUserDeletion) {
		t.Fatalf("demo delete error = %v", err)
	}
	if err := repo.UpdateDialogsMeta(dialogID, "target"); err != nil {
		t.Fatalf("UpdateDialogsMeta target: %v", err)
	}
	if err := repo.UpdateDialogsMeta(dialogID, "trigger"); err != nil {
		t.Fatalf("UpdateDialogsMeta trigger: %v", err)
	}
	if err := repo.UpdateDialogsMeta(dialogID, "bogus"); err == nil {
		t.Fatal("UpdateDialogsMeta accepted invalid meta")
	}
	var target, trigger int
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT Target, `Trigger` FROM dialogs WHERE Id=?", dialogID).Scan(&target, &trigger); err != nil {
		t.Fatalf("read dialog meta: %v", err)
	}
	if target != 1 || trigger != 1 {
		t.Fatalf("target=%d trigger=%d", target, trigger)
	}
	if err := repo.DeleteDialog(normalUser, dialogID); err != nil {
		t.Fatalf("DeleteDialog: %v", err)
	}
	var left int
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM dialogs WHERE Id=?`, dialogID).Scan(&left); err != nil {
		t.Fatalf("check dialog after delete: %v", err)
	}
	if left != 0 {
		t.Fatal("dialog was not deleted")
	}
}
