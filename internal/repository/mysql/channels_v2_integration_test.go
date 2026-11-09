//go:build integration

package mysql

import (
	"context"
	"testing"
)

func TestChannelsV2ReadLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const userID uint64 = 1
	repo.ChannelsV2ReadEnabled = true

	data, err := repo.GetChannelsData(userID)
	if err != nil {
		t.Fatalf("GetChannelsData: %v", err)
	}
	t.Logf("channels v2 for user %d: %s", userID, data)

	active, err := repo.CheckActiveChannels(userID)
	if err != nil {
		t.Fatalf("CheckActiveChannels: %v", err)
	}
	t.Logf("active=%v", active)

	names, err := repo.GetActiveChannels(userID)
	if err != nil {
		t.Fatalf("GetActiveChannels: %v", err)
	}
	t.Logf("active names=%v", names)
}

func TestSaveChannelDataV2Local(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999998
	const payload = `{"origin":"integration-test"}`

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE ucc FROM user_channel_configs ucc JOIN user_channels uc ON uc.id=ucc.channel_id WHERE uc.user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channels WHERE user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM channels WHERE UserId=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-channel-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create test user: %v", err)
	}

	if err := repo.SaveChannelData(testUserID, "widget", payload, true); err != nil {
		t.Fatalf("SaveChannelData: %v", err)
	}

	var (
		channelType string
		enabled     bool
		configJSON  string
	)
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT uc.channel_type, uc.enabled, ucc.config
		FROM user_channels uc
		JOIN user_channel_configs ucc ON ucc.channel_id = uc.id
		WHERE uc.user_id = ?`, testUserID).
		Scan(&channelType, &enabled, &configJSON); err != nil {
		t.Fatalf("read v2 channel: %v", err)
	}
	if channelType != "widget" || !enabled || configJSON != payload {
		t.Fatalf("unexpected v2 channel: type=%s enabled=%v config=%s", channelType, enabled, configJSON)
	}

	var legacy string
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT Widget FROM channels WHERE UserId=?`, testUserID).Scan(&legacy); err != nil {
		t.Fatalf("read legacy channel: %v", err)
	}
	if legacy != payload {
		t.Fatalf("legacy payload = %q, want %q", legacy, payload)
	}

	if err := repo.DeleteChannelData(testUserID, "widget"); err != nil {
		t.Fatalf("DeleteChannelData: %v", err)
	}
	var configs int
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT uc.enabled, COUNT(ucc.channel_id)
		FROM user_channels uc
		LEFT JOIN user_channel_configs ucc ON ucc.channel_id = uc.id
		WHERE uc.user_id = ? AND uc.channel_type = 'widget'
		GROUP BY uc.id`, testUserID).Scan(&enabled, &configs); err != nil {
		t.Fatalf("read deleted v2 channel: %v", err)
	}
	if enabled || configs != 0 {
		t.Fatalf("v2 channel not deleted: enabled=%v configs=%d", enabled, configs)
	}
}

func TestChannelEnabledV2Local(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const testUserID uint64 = 999999997
	const payload = `{"origin":"integration-test"}`

	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, `DELETE ucc FROM user_channel_configs ucc JOIN user_channels uc ON uc.id=ucc.channel_id WHERE uc.user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM user_channels WHERE user_id=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM channels WHERE UserId=?`, testUserID)
		_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, testUserID)
	}
	defer cleanup()
	cleanup()

	if _, err := repo.Conn().ExecContext(ctx, `
		INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
		VALUES (?, 'integration-channel-enabled-test', 1, 0, 1, 1, 'UTC', NOW())`, testUserID); err != nil {
		t.Fatalf("create test user: %v", err)
	}

	if err := repo.SaveChannelData(testUserID, "widget", payload, false); err != nil {
		t.Fatalf("SaveChannelData: %v", err)
	}
	if err := repo.SetChannelEnabled(testUserID, "Widget", true); err != nil {
		t.Fatalf("SetChannelEnabled: %v", err)
	}
	var enabled int
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT enabled FROM user_channels WHERE user_id=? AND channel_type='widget'`, testUserID).
		Scan(&enabled); err != nil {
		t.Fatalf("read v2 enabled: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("v2 channel not enabled: %d", enabled)
	}

	if err := repo.DisableAllUserChannel(testUserID); err != nil {
		t.Fatalf("DisableAllUserChannel: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT enabled FROM user_channels WHERE user_id=? AND channel_type='widget'`, testUserID).
		Scan(&enabled); err != nil {
		t.Fatalf("read v2 enabled after disable: %v", err)
	}
	if enabled != 0 {
		t.Fatalf("v2 channel not disabled: %d", enabled)
	}
}
