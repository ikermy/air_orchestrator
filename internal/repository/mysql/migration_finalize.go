package mysql

// migration_finalize.go — окончание миграции пользователей на bcrypt+AES/EmailHash.
//
// До USER_MIGRATION_DEADLINE пользователи мигрируют lazy-путём при входе.
// После наступления срока scheduler:
//  1. пакетно удаляет всех немигрировавших пользователей (user_auth.EmailHash IS NULL)
//     вместе со всеми их данными;
//  2. финализирует схему (user_auth.EmailHash NOT NULL);
//  3. фиксирует состояние в app_config, после чего lazy-миграция больше не нужна.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"air_orchestrator/internal/config"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

const (
	// migrationStateKey хранит состояние миграции в app_config.
	migrationStateKey = "users.email_hash_migration"
	// migrationStateFinalizedPrefix — значение после успешной финализации.
	migrationStateFinalizedPrefix = "finalized:"
	// finalMigrationBatchSize — сколько пользователей удалять за один проход.
	finalMigrationBatchSize = 100
)

// UserMigrationDeadline возвращает крайний срок миграции пользователей.
func UserMigrationDeadline() time.Time {
	return config.UserMigrationDeadline()
}

// UserMigrationDeadlineReached сообщает, наступил ли крайний срок.
func (i *Implementation) UserMigrationDeadlineReached() bool {
	return !time.Now().Before(UserMigrationDeadline())
}

// CountNonMigratedUsers возвращает число пользователей без EmailHash.
func (i *Implementation) CountNonMigratedUsers(ctx context.Context) (int, error) {
	var count int
	if err := i.Conn().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_auth WHERE EmailHash IS NULL").Scan(&count); err != nil {
		return 0, fmt.Errorf("count non-migrated users: %w", err)
	}
	return count, nil
}

// listNonMigratedUserIDs возвращает до limit ID пользователей без EmailHash.
func (i *Implementation) listNonMigratedUserIDs(ctx context.Context, limit int) ([]uint64, error) {
	if limit <= 0 {
		limit = finalMigrationBatchSize
	}

	rows, err := i.Conn().QueryContext(ctx, `
		SELECT u.Id
		FROM users u
		JOIN user_auth a ON a.UserId = u.Id
		WHERE a.EmailHash IS NULL
		ORDER BY u.Id
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list non-migrated users: load: %w", err)
	}

	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("list non-migrated users: scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("list non-migrated users: rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("list non-migrated users: close: %w", err)
	}
	return ids, nil
}

// purgeNonMigratedUsersByIDs удаляет переданных пользователей и все их данные.
// Служебный метод: production-путь получает ID из listNonMigratedUserIDs.
func (i *Implementation) purgeNonMigratedUsersByIDs(ctx context.Context, ids []uint64) (int, error) {
	deadline := UserMigrationDeadline().UTC().Format(time.RFC3339)
	purged := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return purged, err
		}
		if err := i.DeleteAllUserData(id); err != nil {
			return purged, fmt.Errorf("purge non-migrated user %d: %w", id, err)
		}
		// DeleteAllUserData пишет user.hard_delete; добавляем отдельную запись,
		// чтобы можно было отличить финальную миграцию от обычного удаления.
		if _, err := i.Conn().ExecContext(ctx, `
			INSERT INTO audit_log (user_id, actor_id, action, details)
			VALUES (?, 0, 'user.final_migration_purge',
			        JSON_OBJECT('source', 'final_migration', 'deadline', ?))`,
			id, deadline); err != nil {
			return purged, fmt.Errorf("purge non-migrated user %d: audit: %w", id, err)
		}
		purged++
	}
	return purged, nil
}

// PurgeNonMigratedUsers удаляет до limit немигрировавших пользователей и все их
// данные. Возвращает число удалённых пользователей. Идемпотентна.
func (i *Implementation) PurgeNonMigratedUsers(ctx context.Context, limit int) (int, error) {
	ids, err := i.listNonMigratedUserIDs(ctx, limit)
	if err != nil {
		return 0, err
	}
	return i.purgeNonMigratedUsersByIDs(ctx, ids)
}

// FinalizeEmailHashMigration переводит user_auth.EmailHash в NOT NULL, если
// немигрировавших пользователей не осталось. Возвращает true, если финализация
// выполнена этим вызовом; повторные вызовы — no-op.
func (i *Implementation) FinalizeEmailHashMigration(ctx context.Context) (bool, error) {
	var state string
	err := i.Conn().QueryRowContext(ctx,
		"SELECT `value` FROM app_config WHERE `key` = ?", migrationStateKey).Scan(&state)
	switch {
	case err == nil && strings.HasPrefix(state, migrationStateFinalizedPrefix):
		return false, nil
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("finalize migration: read state: %w", err)
	}

	count, err := i.CountNonMigratedUsers(ctx)
	if err != nil {
		return false, err
	}
	if count > 0 {
		return false, fmt.Errorf("finalize migration: осталось немигрировавших пользователей: %d", count)
	}

	if _, err := i.Conn().ExecContext(ctx,
		"ALTER TABLE user_auth MODIFY `EmailHash` VARCHAR(64) NOT NULL COLLATE 'utf8mb4_general_ci'"); err != nil {
		return false, fmt.Errorf("finalize migration: alter user_auth: %w", err)
	}

	value := migrationStateFinalizedPrefix + time.Now().UTC().Format(time.RFC3339)
	if _, err := i.Conn().ExecContext(ctx,
		"INSERT INTO app_config (`key`, `value`) VALUES (?, ?) "+
			"ON DUPLICATE KEY UPDATE `value` = VALUES(`value`)",
		migrationStateKey, value); err != nil {
		return false, fmt.Errorf("finalize migration: save state: %w", err)
	}
	return true, nil
}

// RunUserMigrationFinalization — один проход финальной стадии. До наступления
// deadline ничего не делает. После deadline пакетно удаляет немигрировавших
// пользователей и финализирует EmailHash. Возвращает число удалённых и признак
// выполненной финализации.
func (i *Implementation) RunUserMigrationFinalization(ctx context.Context, batchSize int) (int, bool, error) {
	if !i.UserMigrationDeadlineReached() {
		return 0, false, nil
	}
	if batchSize <= 0 {
		batchSize = finalMigrationBatchSize
	}

	purged := 0
	for {
		n, err := i.PurgeNonMigratedUsers(ctx, batchSize)
		purged += n
		if err != nil {
			return purged, false, err
		}
		if n < batchSize {
			break
		}
	}

	// Мигрировавшие пользователи могли войти до появления upsert в
	// MigrateUserSecurity — добираем их в глобальный каталог.
	if _, err := i.BackfillUserDirectory(ctx, nil); err != nil {
		return purged, false, fmt.Errorf("finalize migration: backfill user_directory: %w", err)
	}

	finalized, err := i.FinalizeEmailHashMigration(ctx)
	if err != nil {
		return purged, false, err
	}
	return purged, finalized, nil
}

// LogUserMigrationStatus пишет в лог текущее состояние миграции.
func (i *Implementation) LogUserMigrationStatus(ctx context.Context) {
	count, err := i.CountNonMigratedUsers(ctx)
	if err != nil {
		logger.Warn("DB: не удалось посчитать немигрировавших пользователей: %v", err)
		return
	}
	deadline := UserMigrationDeadline()
	if count == 0 {
		logger.Info("DB: миграция пользователей завершена, deadline=%s", deadline.Format(time.RFC3339))
		return
	}
	logger.Info("DB: немигрировавших пользователей: %d, deadline=%s", count, deadline.Format(time.RFC3339))
}
