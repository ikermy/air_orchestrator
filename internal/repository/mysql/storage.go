package mysql

import (
	"air_orchestrator/internal/infrastructure/storage"
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ikermy/air-common/pkg/crypto"
)

func (i *Implementation) StorageConfig(ctx context.Context, userID uint32) (storage.BackendConfig, error) {
	if userID == 0 {
		return storage.BackendConfig{}, fmt.Errorf("invalid user ID")
	}

	var cfg storage.BackendConfig
	var typ, endpoint, bucket, region, access, secret sql.NullString
	var sts sql.NullBool

	err := i.Conn().QueryRowContext(ctx, `SELECT storage_type, endpoint, bucket, region, access_key_ciphertext, secret_key_ciphertext, external_sts_supported FROM user_storage_config WHERE user_id=?`, userID).Scan(&typ, &endpoint, &bucket, &region, &access, &secret, &sts)
	if errors.Is(err, sql.ErrNoRows) {
		return storage.BackendConfig{UserID: userID, Type: storage.BackendInternal}, nil
	}

	if err != nil {
		return cfg, err
	}

	cfg = storage.BackendConfig{UserID: userID, Type: storage.BackendType(typ.String), Endpoint: endpoint.String, Bucket: bucket.String, Region: region.String, AccessKeyCiphertext: access.String, SecretKeyCiphertext: secret.String, ExternalSTSSupported: sts.Bool}
	if cfg.Type == storage.BackendExternal {
		if i.MasterKeyResolver == nil {
			return storage.BackendConfig{}, fmt.Errorf("master key resolver is not configured")
		}
		mk, ok := i.MasterKeyResolver(userID)
		if !ok {
			return storage.BackendConfig{}, fmt.Errorf("storage credentials are locked")
		}
		var decryptErr error
		cfg.AccessKeyCiphertext, decryptErr = decryptStorageCredential(mk, cfg.AccessKeyCiphertext)
		if decryptErr != nil {
			return storage.BackendConfig{}, fmt.Errorf("decrypt access key: %w", decryptErr)
		}
		cfg.SecretKeyCiphertext, decryptErr = decryptStorageCredential(mk, cfg.SecretKeyCiphertext)
		if decryptErr != nil {
			return storage.BackendConfig{}, fmt.Errorf("decrypt secret key: %w", decryptErr)
		}
	}

	return cfg, nil
}

func (i *Implementation) SaveStorageConfig(ctx context.Context, cfg storage.BackendConfig) error {
	if cfg.UserID == 0 || cfg.Type == "" {
		return fmt.Errorf("invalid storage config")
	}
	// Внутренний MinIO не является пользовательским S3-подключением:
	// region для него не хранится в конфигурации пользователя.
	if cfg.Type == storage.BackendInternal {
		cfg.Region = ""
	}

	if cfg.Type == storage.BackendExternal {
		// Шифруем credentials MasterKey'ом, если он доступен. При отсутствии
		// resolver или ключа сохраняем значения открытыми, как остальные методы
		// сохранения критичных данных в проекте.
		if i.MasterKeyResolver != nil {
			if mk, ok := i.MasterKeyResolver(cfg.UserID); ok {
				var err error
				cfg.AccessKeyCiphertext, err = encryptStorageCredential(mk, cfg.AccessKeyCiphertext)
				if err != nil {
					return fmt.Errorf("encrypt access key: %w", err)
				}
				cfg.SecretKeyCiphertext, err = encryptStorageCredential(mk, cfg.SecretKeyCiphertext)
				if err != nil {
					return fmt.Errorf("encrypt secret key: %w", err)
				}
			}
		}
	}

	_, err := i.Conn().ExecContext(ctx, `INSERT INTO user_storage_config (user_id, storage_type, endpoint, bucket, region, access_key_ciphertext, secret_key_ciphertext, external_sts_supported) VALUES (?, ?, NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), NULLIF(?,''), ?) ON DUPLICATE KEY UPDATE storage_type=VALUES(storage_type), endpoint=VALUES(endpoint), bucket=VALUES(bucket), region=VALUES(region), access_key_ciphertext=VALUES(access_key_ciphertext), secret_key_ciphertext=VALUES(secret_key_ciphertext), external_sts_supported=VALUES(external_sts_supported)`, cfg.UserID, cfg.Type, cfg.Endpoint, cfg.Bucket, cfg.Region, cfg.AccessKeyCiphertext, cfg.SecretKeyCiphertext, cfg.ExternalSTSSupported)

	return err
}

func encryptStorageCredential(masterKey [32]byte, value string) (string, error) {
	if value == "" || crypto.IsEncryptedWithMasterKey(value) {
		return value, nil
	}
	return crypto.EncryptFieldWithMasterKey(masterKey, value)
}

func decryptStorageCredential(masterKey [32]byte, value string) (string, error) {
	if value == "" || !crypto.IsEncryptedWithMasterKey(value) {
		return value, nil
	}
	return crypto.DecryptFieldWithMasterKey(masterKey, value)
}

func (i *Implementation) EnsureStorageQuota(ctx context.Context, userID uint32) error {
	if userID == 0 {
		return fmt.Errorf("invalid user ID")
	}

	_, err := i.Conn().ExecContext(ctx, `INSERT INTO user_storage_quota (user_id) VALUES (?) ON DUPLICATE KEY UPDATE user_id = user_id`, userID)

	return err
}

func (i *Implementation) ReserveStorage(ctx context.Context, userID uint32, size int64) error {
	if userID == 0 || size <= 0 {
		return fmt.Errorf("invalid storage reservation")
	}

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var quota, used, reserved uint64
	if err = tx.QueryRowContext(ctx, `SELECT quota_bytes, used_bytes, reserved_bytes FROM user_storage_quota WHERE user_id=? FOR UPDATE`, userID).Scan(&quota, &used, &reserved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("storage quota not initialized")
		}
		return err
	}

	if quota > 0 && (used > quota || reserved > quota-used || uint64(size) > quota-used-reserved) {
		return fmt.Errorf("storage quota exceeded")
	}

	if _, err = tx.ExecContext(ctx, `UPDATE user_storage_quota SET reserved_bytes=reserved_bytes+? WHERE user_id=?`, size, userID); err != nil {
		return err
	}

	return tx.Commit()
}

func (i *Implementation) CommitStorage(ctx context.Context, userID uint32, size int64) error {
	if userID == 0 || size <= 0 {
		return fmt.Errorf("invalid storage commit")
	}

	result, err := i.Conn().ExecContext(ctx, `UPDATE user_storage_quota SET reserved_bytes=reserved_bytes-?, used_bytes=used_bytes+? WHERE user_id=? AND reserved_bytes>=?`, size, size, userID, size)
	if err != nil {
		return err
	}

	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("storage reservation not found")
	}

	return nil
}

func (i *Implementation) ReleaseStorage(ctx context.Context, userID uint32, size int64) error {
	if userID == 0 || size <= 0 {
		return fmt.Errorf("invalid storage release")
	}

	result, err := i.Conn().ExecContext(ctx, `UPDATE user_storage_quota SET reserved_bytes=reserved_bytes-? WHERE user_id=? AND reserved_bytes>=?`, size, userID, size)
	if err != nil {
		return err
	}

	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("storage reservation not found")
	}

	return nil
}

func (i *Implementation) ResetStorageQuota(ctx context.Context, userID uint32) error {
	if userID == 0 {
		return fmt.Errorf("invalid storage quota reset")
	}
	_, err := i.Conn().ExecContext(ctx, `UPDATE user_storage_quota SET used_bytes=0, reserved_bytes=0 WHERE user_id=?`, userID)
	return err
}

func (i *Implementation) StorageQuota(ctx context.Context, userID uint32) (quota, used, reserved uint64, err error) {
	if userID == 0 {
		return 0, 0, 0, fmt.Errorf("invalid user ID")
	}

	err = i.Conn().QueryRowContext(ctx, `SELECT quota_bytes, used_bytes, reserved_bytes FROM user_storage_quota WHERE user_id=?`, userID).Scan(&quota, &used, &reserved)

	return
}

func (i *Implementation) ListReservedUsers(ctx context.Context) ([]uint32, error) {
	rows, err := i.Conn().QueryContext(ctx, `SELECT user_id FROM user_storage_quota WHERE reserved_bytes > 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var users []uint32
	for rows.Next() {
		var uid uint32
		if scanErr := rows.Scan(&uid); scanErr != nil {
			return nil, scanErr
		}
		users = append(users, uid)
	}

	return users, rows.Err()
}

// ReleaseAllReservedStorage clears only the reservation portion of quota after
// Redis recovery. Committed usage is never changed.
func (i *Implementation) ReleaseAllReservedStorage(ctx context.Context, userID uint32) error {
	if userID == 0 {
		return fmt.Errorf("invalid user ID")
	}
	_, err := i.Conn().ExecContext(ctx, `UPDATE user_storage_quota SET reserved_bytes=0 WHERE user_id=?`, userID)
	return err
}

func (i *Implementation) CreateMigration(ctx context.Context, userID uint32, sourceType, targetType storage.BackendType) (storageusecase.MigrationRecord, error) {
	if userID == 0 {
		return storageusecase.MigrationRecord{}, fmt.Errorf("invalid user ID")
	}
	if sourceType == targetType || (sourceType != storage.BackendInternal && sourceType != storage.BackendExternal) || (targetType != storage.BackendInternal && targetType != storage.BackendExternal) {
		return storageusecase.MigrationRecord{}, fmt.Errorf("invalid migration backend types")
	}

	var active uint64
	if err := i.Conn().QueryRowContext(ctx, `SELECT COUNT(*) FROM storage_migrations WHERE user_id=? AND state IN ('pending','running')`, userID).Scan(&active); err != nil {
		return storageusecase.MigrationRecord{}, err
	}

	if active > 0 {
		return storageusecase.MigrationRecord{}, fmt.Errorf("migration already active for user")
	}
	result, err := i.Conn().ExecContext(ctx, `INSERT INTO storage_migrations (user_id, source_type, target_type, state, manifest) VALUES (?, ?, ?, 'pending', JSON_OBJECT())`, userID, sourceType, targetType)

	if err != nil {
		return storageusecase.MigrationRecord{}, err
	}
	id, err := result.LastInsertId()

	if err != nil {
		return storageusecase.MigrationRecord{}, err
	}

	return storageusecase.MigrationRecord{ID: uint64(id), UserID: userID, State: storageusecase.MigrationPending, UpdatedAt: time.Now()}, nil
}

func (i *Implementation) UpdateMigration(ctx context.Context, record storageusecase.MigrationRecord) error {
	if record.ID == 0 {
		return fmt.Errorf("invalid migration ID")
	}

	keys, _ := json.Marshal(record.VerifiedKeys)
	_, err := i.Conn().ExecContext(ctx, `
    UPDATE storage_migrations
    SET state=?, last_error=?,
        manifest=JSON_SET(
            COALESCE(manifest, JSON_OBJECT()),
            '$.copied', ?,
            '$.verified', ?,
            '$.deleted', ?,
            '$.total', ?,
            '$.verified_keys', ?
        )
    WHERE id=?`,
		record.State,
		nullString(record.LastError),
		record.Copied,
		record.Verified,
		record.Deleted,
		record.Total,
		string(keys),
		record.ID,
	)

	return err
}

func (i *Implementation) GetMigration(ctx context.Context, id uint64) (storageusecase.MigrationRecord, error) {
	var r storageusecase.MigrationRecord
	var keysJSON sql.NullString
	var state, last sql.NullString

	err := i.Conn().QueryRowContext(ctx, `SELECT id, user_id, state, JSON_UNQUOTE(JSON_EXTRACT(manifest,'$.copied')), JSON_UNQUOTE(JSON_EXTRACT(manifest,'$.verified')), JSON_UNQUOTE(JSON_EXTRACT(manifest,'$.deleted')), JSON_UNQUOTE(JSON_EXTRACT(manifest,'$.total')), JSON_UNQUOTE(JSON_EXTRACT(manifest,'$.verified_keys')), last_error, updated_at FROM storage_migrations WHERE id=?`, id).Scan(&r.ID, &r.UserID, &state, &r.Copied, &r.Verified, &r.Deleted, &r.Total, &keysJSON, &last, &r.UpdatedAt)
	if err != nil {
		return r, err
	}

	r.State = storageusecase.MigrationState(state.String)

	if keysJSON.Valid {
		_ = json.Unmarshal([]byte(keysJSON.String), &r.VerifiedKeys)
	}

	r.LastError = last.String

	return r, nil
}

func (i *Implementation) CancelMigration(ctx context.Context, id uint64) error {
	result, err := i.Conn().ExecContext(ctx, `UPDATE storage_migrations SET state='cancelled' WHERE id=? AND state IN ('pending','failed')`, id)
	if err != nil {
		return err
	}

	n, _ := result.RowsAffected()
	if n != 1 {
		return fmt.Errorf("migration is running, completed, or not found")
	}

	return nil
}

func (i *Implementation) ListPendingMigrations(ctx context.Context, limit int) ([]storageusecase.MigrationRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}

	rows, err := i.Conn().QueryContext(ctx, `SELECT id, user_id, state, updated_at FROM storage_migrations WHERE state IN ('pending','failed') ORDER BY updated_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []storageusecase.MigrationRecord
	for rows.Next() {
		var r storageusecase.MigrationRecord
		var state string
		if err := rows.Scan(&r.ID, &r.UserID, &state, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.State = storageusecase.MigrationState(state)
		result = append(result, r)
	}

	return result, rows.Err()
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
