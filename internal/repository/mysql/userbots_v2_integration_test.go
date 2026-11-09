//go:build integration

package mysql

import (
	"context"
	"os"
	"testing"
)

func TestBackfillUserBotsLocal(t *testing.T) {
	if os.Getenv("RUN_BACKFILL_USERBOTS") != "true" {
		t.Skip("set RUN_BACKFILL_USERBOTS=true to backfill user bots")
	}
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	stats, err := repo.BackfillUserBots(ctx)
	if err != nil {
		t.Fatalf("BackfillUserBots: %v", err)
	}
	t.Logf("user bots backfill: %+v", stats)

	var sourceBots, targetBots int
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM service_tgbots) + (SELECT COUNT(*) FROM service_wabots)`).
		Scan(&sourceBots); err != nil {
		t.Fatalf("count source bots: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_bots`).Scan(&targetBots); err != nil {
		t.Fatalf("count user_bots: %v", err)
	}
	if sourceBots != targetBots {
		t.Fatalf("user_bots = %d, want %d", targetBots, sourceBots)
	}

	var mismatchedBots, orphanEvents, orphanState int
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_bots b
		LEFT JOIN users u ON u.Id = b.user_id
		WHERE u.Id IS NULL`).Scan(&mismatchedBots); err != nil {
		t.Fatalf("verify bot owners: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_bot_events e
		LEFT JOIN user_bots b ON b.id = e.user_bot_id
		WHERE b.id IS NULL OR b.user_id <> e.user_id`).Scan(&orphanEvents); err != nil {
		t.Fatalf("verify events: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_bot_state s
		LEFT JOIN user_bots b ON b.id = s.user_bot_id
		WHERE b.id IS NULL OR b.user_id <> s.user_id`).Scan(&orphanState); err != nil {
		t.Fatalf("verify state: %v", err)
	}
	if mismatchedBots != 0 || orphanEvents != 0 || orphanState != 0 {
		t.Fatalf("bots=%d events=%d state=%d", mismatchedBots, orphanEvents, orphanState)
	}

	var eventsBefore, stateBefore int
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_bot_events`).Scan(&eventsBefore); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_bot_state`).Scan(&stateBefore); err != nil {
		t.Fatalf("count state: %v", err)
	}

	again, err := repo.BackfillUserBots(ctx)
	if err != nil {
		t.Fatalf("BackfillUserBots (rerun): %v", err)
	}
	var eventsAfter, stateAfter int
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_bot_events`).Scan(&eventsAfter); err != nil {
		t.Fatalf("count events after rerun: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM user_bot_state`).Scan(&stateAfter); err != nil {
		t.Fatalf("count state after rerun: %v", err)
	}
	if again.Events != 0 || eventsBefore != eventsAfter || stateBefore != stateAfter {
		t.Fatalf("backfill is not idempotent: stats=%+v events=%d->%d state=%d->%d",
			again, eventsBefore, eventsAfter, stateBefore, stateAfter)
	}
}
