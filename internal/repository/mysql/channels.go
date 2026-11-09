package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ikermy/air-common/v2/pkg/comdom"
	"github.com/ikermy/air-common/v2/pkg/crypto"
	"github.com/ikermy/air-common/v2/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

type channelEntry struct {
	Data    json.RawMessage `json:"data"`
	Enabled bool            `json:"enabled"`
}

func normalizeChannelType(channelType string) (string, error) {
	info, ok := comdom.ChannelBySlug(channelType)
	if !ok || !info.Configurable {
		return "", fmt.Errorf("неизвестный тип канала: %s", channelType)
	}
	return info.Slug, nil
}

func emptyChannelData() json.RawMessage {
	return json.RawMessage(`{}`)
}

func emptyChannelsMap() map[string]channelEntry {
	result := make(map[string]channelEntry)
	for _, info := range comdom.ConfigurableChannels() {
		result[info.Slug] = channelEntry{Data: emptyChannelData(), Enabled: false}
	}
	return result
}

// SaveChannelData переопределяет repository.go:SaveChannelData с поддержкой $mk$.
func (i *Implementation) SaveChannelData(userId uint64, channelType string, data string, enabled bool) error {
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	normalized, err := normalizeChannelType(channelType)
	if err != nil {
		return err
	}
	info, _ := comdom.ChannelBySlug(normalized)

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	var jsonData string
	if json.Valid([]byte(data)) {
		jsonData = data
	} else {
		var key string
		switch normalized {
		case "tgbot":
			key = "token"
		default:
			key = "value"
		}
		jsonData = fmt.Sprintf(`{%q: %q}`, key, data)
	}

	// Шифруем данные канала MasterKey'ом ($mk$) если он доступен
	if i.MasterKeyResolver != nil {
		if mk, ok := i.MasterKeyResolver(userId); ok {
			encrypted, err := crypto.EncryptFieldWithMasterKey(mk, jsonData)
			if err != nil {
				return fmt.Errorf("failed to encrypt channel data with MasterKey: %w", err)
			}
			jsonData = encrypted
		}
	}

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в SaveChannelData: %v", rbErr, userId)
		}
	}()

	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ?)", userId).Scan(&exists); err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}
	if !exists {
		if _, err = tx.ExecContext(ctx, "INSERT INTO channels (UserId) VALUES (?)", userId); err != nil {
			return fmt.Errorf("ошибка создания записи в channels: %w", err)
		}
	}

	query := fmt.Sprintf(
		"UPDATE channels SET `%s` = ?, `%s_enabled` = ? WHERE UserId = ?",
		info.LegacyColumn, info.LegacyColumn)
	if _, err = tx.ExecContext(ctx, query, jsonData, enabledInt, userId); err != nil {
		return fmt.Errorf("ошибка обновления канала %s: %w", channelType, err)
	}

	if err := upsertUserChannelV2(ctx, tx, userId, info, enabledInt, jsonData); err != nil {
		return err
	}

	return tx.Commit()
}

func upsertUserChannelV2(ctx context.Context, tx *sql.Tx, userID uint64, info comdom.ChannelInfo, enabled int, payload string) error {
	channelID := uint64(userID)*10 + uint64(info.Code)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_channels (id, user_id, channel_type, enabled)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE enabled = VALUES(enabled)`,
		channelID, userID, info.Slug, enabled); err != nil {
		return fmt.Errorf("ошибка записи user_channels: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM user_channels WHERE user_id = ? AND channel_type = ?`,
		userID, info.Slug).Scan(&channelID); err != nil {
		return fmt.Errorf("ошибка чтения user_channels: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_channel_configs (channel_id, user_id, config)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE config = VALUES(config)`,
		channelID, userID, payload); err != nil {
		return fmt.Errorf("ошибка записи user_channel_configs: %w", err)
	}
	return nil
}

// GetChannelsData переопределяет repository.go:GetChannelsData с поддержкой $mk$-расшифровки.
func (i *Implementation) GetChannelsData(userId uint64) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	if i.ChannelsV2ReadEnabled {
		if data, err := i.getChannelsDataV2(ctx, userId); err == nil {
			return data, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	return i.getChannelsDataLegacy(ctx, userId)
}

func (i *Implementation) getChannelsDataLegacy(ctx context.Context, userId uint64) (json.RawMessage, error) {
	var exists bool
	if err := i.Conn().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ?)", userId).Scan(&exists); err != nil {
		return nil, fmt.Errorf("ошибка проверки существования каналов: %w", err)
	}
	if !exists {
		return json.Marshal(emptyChannelsMap())
	}

	infos := comdom.ConfigurableChannels()
	type columnValue struct {
		data    sql.NullString
		enabled sql.NullBool
	}
	values := make([]columnValue, len(infos))
	selects := make([]string, 0, len(infos)*2)
	scanTargets := make([]any, 0, len(infos)*2)
	for idx, info := range infos {
		selects = append(selects,
			fmt.Sprintf("`%s`", info.LegacyColumn),
			fmt.Sprintf("`%s_enabled`", info.LegacyColumn))
		scanTargets = append(scanTargets, &values[idx].data, &values[idx].enabled)
	}

	query := fmt.Sprintf("SELECT %s FROM channels WHERE UserId = ?", strings.Join(selects, ", "))
	if err := i.Conn().QueryRowContext(ctx, query, userId).Scan(scanTargets...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ошибка получения данных каналов: %w", err)
	}

	result := make(map[string]channelEntry, len(infos))
	for idx, info := range infos {
		result[info.Slug] = channelEntry{
			Data:    i.decryptChannelField(userId, values[idx].data),
			Enabled: values[idx].enabled.Valid && values[idx].enabled.Bool,
		}
	}

	return json.Marshal(result)
}

func (i *Implementation) getChannelsDataV2(ctx context.Context, userId uint64) (json.RawMessage, error) {
	rows, err := i.Conn().QueryContext(ctx, `
		SELECT uc.channel_type, uc.enabled, ucc.config
		FROM user_channels uc
		LEFT JOIN user_channel_configs ucc ON ucc.channel_id = uc.id
		WHERE uc.user_id = ?`, userId)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения каналов v2: %w", err)
	}
	defer rows.Close()

	found := make(map[string]channelEntry)
	for rows.Next() {
		var (
			channelType string
			enabled     bool
			config      sql.NullString
		)
		if err := rows.Scan(&channelType, &enabled, &config); err != nil {
			return nil, fmt.Errorf("ошибка чтения каналов v2: %w", err)
		}
		found[channelType] = channelEntry{
			Data:    i.decryptChannelField(userId, config),
			Enabled: enabled,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка чтения каналов v2: %w", err)
	}
	if len(found) == 0 {
		return nil, sql.ErrNoRows
	}

	result := emptyChannelsMap()
	for slug, entry := range found {
		result[slug] = entry
	}
	return json.Marshal(result)
}

func (i *Implementation) DeleteChannelData(userId uint64, channelType string) error {
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	normalized, err := normalizeChannelType(channelType)
	if err != nil {
		return err
	}
	info, _ := comdom.ChannelBySlug(normalized)

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteChannelData: %v", rbErr, userId)
		}
	}()

	query := fmt.Sprintf(
		"UPDATE channels SET `%s` = NULL, `%s_enabled` = 0 WHERE UserId = ?",
		info.LegacyColumn, info.LegacyColumn)
	if _, err := tx.ExecContext(ctx, query, userId); err != nil {
		return fmt.Errorf("ошибка обновления канала %s: %w", channelType, err)
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE ucc FROM user_channel_configs ucc
		JOIN user_channels uc ON uc.id = ucc.channel_id
		WHERE uc.user_id = ? AND uc.channel_type = ?`, userId, normalized); err != nil {
		return fmt.Errorf("ошибка удаления конфига канала v2: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_channels SET enabled = 0 WHERE user_id = ? AND channel_type = ?`,
		userId, normalized); err != nil {
		return fmt.Errorf("ошибка отключения канала v2: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}
	return nil
}

// CheckActiveChannels возвращает true если у пользователя хотя бы один канал активен.
func (i *Implementation) CheckActiveChannels(userId uint64) (bool, error) {
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	if i.ChannelsV2ReadEnabled {
		var active bool
		err := i.Conn().QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM user_channels WHERE user_id = ? AND enabled = 1)`, userId).
			Scan(&active)
		if err == nil {
			return active, nil
		}
	}

	conditions := make([]string, 0, len(comdom.ConfigurableChannels()))
	for _, info := range comdom.ConfigurableChannels() {
		conditions = append(conditions, fmt.Sprintf("`%s_enabled` = 1", info.LegacyColumn))
	}

	var active bool
	query := fmt.Sprintf(
		"SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ? AND (%s))",
		strings.Join(conditions, " OR "))
	err := i.Conn().QueryRowContext(ctx, query, userId).Scan(&active)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при проверке активных каналов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", err)
		default:
			return false, fmt.Errorf("ошибка проверки активных каналов: %w", err)
		}
	}
	return active, nil
}

// GetActiveChannels возвращает список имён активных каналов пользователя.
func (i *Implementation) GetActiveChannels(userId uint64) ([]string, error) {
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	if i.ChannelsV2ReadEnabled {
		if channels, err := i.getActiveChannelsV2(ctx, userId); err == nil {
			return channels, nil
		}
	}

	infos := comdom.ConfigurableChannels()
	selects := make([]string, len(infos))
	values := make([]sql.NullBool, len(infos))
	scanTargets := make([]any, len(infos))
	for idx, info := range infos {
		selects[idx] = fmt.Sprintf("`%s_enabled`", info.LegacyColumn)
		scanTargets[idx] = &values[idx]
	}

	query := fmt.Sprintf("SELECT %s FROM channels WHERE UserId = ? LIMIT 1", strings.Join(selects, ", "))
	err := i.Conn().QueryRowContext(ctx, query, userId).Scan(scanTargets...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("ошибка получения активных каналов: %w", err)
	}

	channels := make([]string, 0, len(infos))
	for idx, info := range infos {
		if values[idx].Valid && values[idx].Bool {
			channels = append(channels, info.Slug)
		}
	}
	return channels, nil
}

func (i *Implementation) getActiveChannelsV2(ctx context.Context, userId uint64) ([]string, error) {
	rows, err := i.Conn().QueryContext(ctx,
		`SELECT channel_type FROM user_channels WHERE user_id = ? AND enabled = 1`, userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	channels := make([]string, 0, len(found))
	for _, info := range comdom.ConfigurableChannels() {
		if found[info.Slug] {
			channels = append(channels, info.Slug)
		}
	}
	return channels, nil
}
