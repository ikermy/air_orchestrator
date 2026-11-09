//go:build integration

package mysql

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestUtilsReadsV2Local(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const userID uint64 = 999999989

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM subscriptions WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM notifications WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_auth WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, userID)
	}
	defer cleanup()
	cleanup()

	var (
		langID     int
		langCode   string
		currencyID int
	)
	if err := repo.Conn().QueryRowContext(ctx, `SELECT Id, Code FROM languages ORDER BY Id LIMIT 1`).
		Scan(&langID, &langCode); err != nil {
		t.Skipf("no languages fixture: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `SELECT Id FROM currency ORDER BY Id LIMIT 1`).
		Scan(&currencyID); err != nil {
		t.Skipf("no currency fixture: %v", err)
	}

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-utils', 0, 12.34, ?, ?, 'UTC', NOW())`,
		userID, currencyID, langID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant)
		VALUES (?, 1, 123456789, 1, 1)`, userID); err != nil {
		t.Fatalf("create notifications: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, Confirmed, Disabled)
		VALUES (?, 'sha', ?, 1, 0)`, userID, "integration-utils@example.test"); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO subscriptions (UserId, StartDate, MonthsPaid, TotalCost, EndDate)
		VALUES (?, '2026-01-01', 12, 100.00, '2026-12-31')`, userID); err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	var legacyLang string
	if err := repo.Conn().QueryRowContext(ctx, `SELECT UserLang(?)`, userID).Scan(&legacyLang); err != nil {
		t.Fatalf("legacy UserLang: %v", err)
	}
	if got := repo.UserLanguage(userID); got != legacyLang || got != langCode {
		t.Fatalf("UserLanguage = %q, legacy = %q, want %q", got, legacyLang, langCode)
	}

	var legacyNotifications string
	if err := repo.Conn().QueryRowContext(ctx, `SELECT GetNotificationChannel(?)`, userID).
		Scan(&legacyNotifications); err != nil {
		t.Fatalf("legacy GetNotificationChannel: %v", err)
	}
	gotNotifications, err := repo.GetNotificationChannel(userID)
	if err != nil {
		t.Fatalf("GetNotificationChannel: %v", err)
	}
	if !jsonEqual(t, legacyNotifications, string(gotNotifications)) {
		t.Fatalf("notifications differ: legacy=%s v2=%s", legacyNotifications, gotNotifications)
	}

	var legacyLimits string
	if err := repo.Conn().QueryRowContext(ctx, `SELECT GetUserSubscriptionLimites(?)`, userID).
		Scan(&legacyLimits); err != nil {
		t.Fatalf("legacy GetUserSubscriptionLimites: %v", err)
	}
	gotLimits, err := repo.GetUserSubscriptionLimites(userID)
	if err != nil {
		t.Fatalf("GetUserSubscriptionLimites: %v", err)
	}
	if !jsonEqual(t, legacyLimits, string(gotLimits)) {
		t.Fatalf("limits differ: legacy=%s v2=%s", legacyLimits, gotLimits)
	}
}

func TestDeleteAllUserDataLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		userID uint64 = 999999978
		responderID uint64 = 999999978
		dialogID    uint64 = 999999978
		channelID   uint64 = 999999978
	)

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialog_contexts WHERE dialog_id=?`, dialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialog_messages WHERE dialog_id=?`, dialogID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialog_contexts WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channel_configs WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channels WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_bots WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_directory WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM dialogs WHERE `+"`User`"+`=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE user_id=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM channels WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM billing WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM subscriptions WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM notifications WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_auth WHERE UserId=?`, userID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, userID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-delete-all', 1, 0, 1, 1, 'UTC', NOW())`, userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO notifications (UserId, Email, Telegram_enabled, Instant) VALUES (?, 1, 1, 1)`, userID); err != nil {
		t.Fatalf("create notifications: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_auth (UserId, SHA, Email, Confirmed, Disabled)
		VALUES (?, 'sha', 'integration-delete-all@example.test', 1, 0)`, userID); err != nil {
		t.Fatalf("create user_auth: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `INSERT INTO channels (UserId) VALUES (?)`, userID); err != nil {
		t.Fatalf("create channels: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO billing (UserId, Payment, Months, Discont) VALUES (?, 10.00, 1, 0)`, userID); err != nil {
		t.Fatalf("create billing: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO subscriptions (UserId, StartDate, MonthsPaid, TotalCost, EndDate)
		VALUES (?, '2026-01-01', 1, 10.00, '2026-02-01')`, userID); err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO responders (Id, user_id, Type, Name, RealId, is_orphan)
		VALUES (?, ?, 0, 'integration-delete-all', 0, 0)`, responderID, userID); err != nil {
		t.Fatalf("create responder: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO dialogs (Id, Type, User, Responder, Data) VALUES (?, 0, ?, ?, '[]')`,
		dialogID, userID, responderID); err != nil {
		t.Fatalf("create dialog: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO dialog_messages (id, dialog_id, user_id, seq, role, content)
		VALUES (?, ?, ?, 0, 'user', 'hello')`, dialogID+1, dialogID, userID); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_channels (id, user_id, channel_type, enabled)
		VALUES (?, ?, 'widget', 1)`, channelID, userID); err != nil {
		t.Fatalf("create user_channel: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_channel_configs (channel_id, user_id, config)
		VALUES (?, ?, '{"value":"x"}')`, channelID, userID); err != nil {
		t.Fatalf("create channel config: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_bots (id, user_id, bot_type, external_bot_id, alias, auth_ciphertext)
		VALUES (?, ?, 'telegram', 1, 'delete-all-test', '')`, channelID+1, userID); err != nil {
		t.Fatalf("create user_bot: %v", err)
	}
	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO user_directory (user_id, email_hash, slot, shard_id)
		VALUES (?, UNHEX(REPEAT('EE', 32)), 0, 0)`, userID); err != nil {
		t.Fatalf("create user_directory: %v", err)
	}

	if err := repo.DeleteAllUserData(userID); err != nil {
		t.Fatalf("DeleteAllUserData: %v", err)
	}

	checks := []struct {
		name  string
		query string
	}{
		{"users", `SELECT COUNT(*) FROM users WHERE Id=?`},
		{"notifications", `SELECT COUNT(*) FROM notifications WHERE UserId=?`},
		{"user_auth", `SELECT COUNT(*) FROM user_auth WHERE UserId=?`},
		{"channels", `SELECT COUNT(*) FROM channels WHERE UserId=?`},
		{"billing", `SELECT COUNT(*) FROM billing WHERE UserId=?`},
		{"subscriptions", `SELECT COUNT(*) FROM subscriptions WHERE UserId=?`},
		{"dialogs", "SELECT COUNT(*) FROM dialogs WHERE `User`=?"},
		{"responders", `SELECT COUNT(*) FROM responders WHERE user_id=?`},
		{"dialog_messages", `SELECT COUNT(*) FROM dialog_messages WHERE user_id=?`},
		{"user_channels", `SELECT COUNT(*) FROM user_channels WHERE user_id=?`},
		{"user_channel_configs", `SELECT COUNT(*) FROM user_channel_configs WHERE user_id=?`},
		{"user_bots", `SELECT COUNT(*) FROM user_bots WHERE user_id=?`},
		{"user_directory", `SELECT COUNT(*) FROM user_directory WHERE user_id=?`},
	}
	for _, check := range checks {
		var count int
		if err := repo.Conn().QueryRowContext(ctx, check.query, userID).Scan(&count); err != nil {
			t.Fatalf("check %s: %v", check.name, err)
		}
		if count != 0 {
			t.Fatalf("%s still has %d rows", check.name, count)
		}
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		t.Fatalf("unmarshal legacy JSON %q: %v", a, err)
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		t.Fatalf("unmarshal v2 JSON %q: %v", b, err)
	}
	return reflect.DeepEqual(av, bv)
}
