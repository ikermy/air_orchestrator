package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ikermy/air_common/pkg/mode"
)

// SetAppConfig сохраняет или обновляет значение конфигурации.
func (d *DB) SetAppConfig(ctx context.Context, key, value string) error {
	storedValue, err := d.prepareAppConfigValueForStorage(key, value)
	if err != nil {
		return fmt.Errorf("SetAppConfig[%s]: %w", key, err)
	}

	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	_, err = d.Conn().ExecContext(ctx,
		"INSERT INTO app_config (`key`, `value`) VALUES (?, ?) "+
			"ON DUPLICATE KEY UPDATE `value` = VALUES(`value`)",
		key, storedValue)
	if err != nil {
		return fmt.Errorf("SetAppConfig[%s]: %w", key, err)
	}
	return nil
}

// GetAppConfig возвращает значение по ключу.
// Возвращает ("", nil) если ключ не найден.
func (d *DB) GetAppConfig(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	var value sql.NullString
	err := d.Conn().QueryRowContext(ctx,
		"SELECT `value` FROM app_config WHERE `key` = ?", key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("GetAppConfig[%s]: %w", key, err)
	}
	decoded, err := d.decodeAppConfigValue(key, value.String)
	if err != nil {
		return "", fmt.Errorf("GetAppConfig[%s]: %w", key, err)
	}
	return decoded, nil
}

// GetAllAppConfig возвращает все настройки как map[key]value.
func (d *DB) GetAllAppConfig(ctx context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx, "SELECT `key`, `value` FROM app_config")
	if err != nil {
		return nil, fmt.Errorf("GetAllAppConfig: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("GetAllAppConfig scan: %w", err)
		}
		decoded, err := d.decodeAppConfigValue(k, v)
		if err != nil {
			return nil, fmt.Errorf("GetAllAppConfig[%s]: %w", k, err)
		}
		result[k] = decoded
	}
	return result, rows.Err()
}
