//go:build integration

package mysql

import (
	"context"
	"os"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/comdom"
)

func TestGetOrSetTreadAndResponderLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		userA  uint64 = 999999980
		userB  uint64 = 999999981
		realID uint64 = 999999999001
		nameA         = "integration-responder-a"
	)

	cleanup := func() {
		for _, uid := range []uint64{userA, userB} {
			_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM dialogs WHERE `User`=?", uid)
			_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM responders WHERE user_id=?`, uid)
			_, _ = repo.Conn().ExecContext(ctx, `DELETE FROM users WHERE Id=?`, uid)
		}
	}
	defer cleanup()
	cleanup()

	for _, uid := range []uint64{userA, userB} {
		if _, err := repo.Conn().ExecContext(ctx, `
			INSERT INTO users (Id, Name, RoleId, balance, currency, Lang, TimeZone, Timechange)
			VALUES (?, ?, 1, 0, 1, 1, 'UTC', NOW())`, uid, nameA); err != nil {
			t.Fatalf("create test user %d: %v", uid, err)
		}
	}

	dialogA, err := repo.GetOrSetTreadAndResponder(userA, realID, nameA, comdom.TelegramBot)
	if err != nil {
		t.Fatalf("GetOrSetTreadAndResponder(A): %v", err)
	}
	again, err := repo.GetOrSetTreadAndResponder(userA, realID, nameA, comdom.TelegramBot)
	if err != nil {
		t.Fatalf("GetOrSetTreadAndResponder(A, again): %v", err)
	}
	if dialogA != again {
		t.Fatalf("dialog id changed: %d -> %d", dialogA, again)
	}

	dialogB, err := repo.GetOrSetTreadAndResponder(userB, realID, "integration-responder-b", comdom.TelegramBot)
	if err != nil {
		t.Fatalf("GetOrSetTreadAndResponder(B): %v", err)
	}
	if dialogA == dialogB {
		t.Fatalf("users share dialog id %d", dialogA)
	}

	var respondersA, respondersB, dialogsA, owners int
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM responders WHERE user_id=? AND Type=? AND RealId=?`,
		userA, int(comdom.TelegramBot), int64(realID)).Scan(&respondersA); err != nil {
		t.Fatalf("count responders A: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM responders WHERE user_id=? AND Type=? AND RealId=?`,
		userB, int(comdom.TelegramBot), int64(realID)).Scan(&respondersB); err != nil {
		t.Fatalf("count responders B: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dialogs WHERE `User`=?", userA).Scan(&dialogsA); err != nil {
		t.Fatalf("count dialogs A: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM responders r JOIN dialogs d ON d.Responder=r.Id WHERE r.user_id=?`,
		userA).Scan(&owners); err != nil {
		t.Fatalf("verify owner: %v", err)
	}
	if respondersA != 1 || respondersB != 1 || dialogsA != 1 || owners != 1 {
		t.Fatalf("respondersA=%d respondersB=%d dialogsA=%d owners=%d", respondersA, respondersB, dialogsA, owners)
	}
}

func TestBackfillRespondersLocal(t *testing.T) {
	if os.Getenv("RUN_BACKFILL_RESPONDERS") != "true" {
		t.Skip("set RUN_BACKFILL_RESPONDERS=true to backfill responders")
	}
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	stats, err := repo.BackfillResponders(ctx)
	if err != nil {
		t.Fatalf("BackfillResponders: %v", err)
	}
	t.Logf("responders backfill: %+v", stats)

	var mismatchedOwners, unflaggedOrphans, duplicates int
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM responders r
		JOIN dialogs d ON d.Responder = r.Id
		WHERE r.user_id IS NULL OR r.user_id <> d.User`).Scan(&mismatchedOwners); err != nil {
		t.Fatalf("verify owners: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM responders WHERE user_id IS NULL AND is_orphan = 0`).Scan(&unflaggedOrphans); err != nil {
		t.Fatalf("verify orphans: %v", err)
	}
	if err := repo.Conn().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT user_id, Type, RealId
			FROM responders
			WHERE user_id IS NOT NULL AND RealId IS NOT NULL
			GROUP BY user_id, Type, RealId
			HAVING COUNT(*) > 1
		) d`).Scan(&duplicates); err != nil {
		t.Fatalf("verify duplicates: %v", err)
	}
	if mismatchedOwners != 0 || unflaggedOrphans != 0 || duplicates != 0 {
		t.Fatalf("owners=%d orphans=%d duplicates=%d", mismatchedOwners, unflaggedOrphans, duplicates)
	}

	var before int
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM responders`).Scan(&before); err != nil {
		t.Fatalf("count responders: %v", err)
	}
	again, err := repo.BackfillResponders(ctx)
	if err != nil {
		t.Fatalf("BackfillResponders (rerun): %v", err)
	}
	var after int
	if err := repo.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM responders`).Scan(&after); err != nil {
		t.Fatalf("count responders after rerun: %v", err)
	}
	if again.Copies != 0 || again.Repointed != 0 || again.Merged != 0 || before != after {
		t.Fatalf("backfill is not idempotent: stats=%+v rows=%d->%d", again, before, after)
	}
}
