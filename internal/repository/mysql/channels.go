package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/crypto"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// SaveChannelData переопределяет db.go:SaveChannelData с поддержкой $mk$.
func (d *DB) SaveChannelData(userId uint32, channelType string, data string, enabled bool) error {
	if userId == 0 || channelType == "" {
		return fmt.Errorf("получены некорректные значения: userId или channelType пусты")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	var jsonData string
	if json.Valid([]byte(data)) {
		jsonData = data
	} else {
		var key string
		switch channelType {
		case "tgbot", "tgubot":
			key = "token"
		default:
			key = "value"
		}
		jsonData = fmt.Sprintf(`{%q: %q}`, key, data)
	}

	// Шифруем данные канала MasterKey'ом ($mk$) если он доступен
	if d.MasterKeyResolver != nil {
		if mk, ok := d.MasterKeyResolver(userId); ok {
			encrypted, err := crypto.EncryptFieldWithMasterKey(mk, jsonData)
			if err != nil {
				return fmt.Errorf("failed to encrypt channel data with MasterKey: %w", err)
			}
			jsonData = encrypted
		}
	}

	tx, err := d.Conn().BeginTx(ctx, nil)
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
	// TODO в air-common добавить хэлперы для работы с каналами FromString... для ввода чёткой структуры имён каналов
	switch channelType {
	case "tgbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgBot = ?, TgBot_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "widget":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Widget = ?, Widget_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "tgubot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgUserBot = ?, TgUserBot_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "whatsbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Whats = ?, Whats_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "insta":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Insta = ?, Insta_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	case "avito":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Avito = ?, Avito_enabled = ? WHERE UserId = ?`, jsonData, enabledInt, userId)
	default:
		return fmt.Errorf("неизвестный тип канала: %s", channelType)
	}
	if err != nil {
		return fmt.Errorf("ошибка обновления канала %s: %w", channelType, err)
	}
	return tx.Commit()
}

// GetChannelsData переопределяет db.go:GetChannelsData с поддержкой $mk$-расшифровки.
func (d *DB) GetChannelsData(userId uint32) (json.RawMessage, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var exists bool
	if err := d.Conn().QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM channels WHERE UserId = ?)", userId).Scan(&exists); err != nil {
		return nil, fmt.Errorf("ошибка проверки существования каналов: %w", err)
	}

	type entry struct {
		Data    json.RawMessage `json:"data"`
		Enabled bool            `json:"enabled"`
	}
	empty := json.RawMessage(`{}`)

	if !exists {
		res, _ := json.Marshal(map[string]entry{
			"tgbot":     {Data: empty, Enabled: false},
			"widget":    {Data: empty, Enabled: false},
			"tguserbot": {Data: empty, Enabled: false},
			"whatsbot":  {Data: empty, Enabled: false},
			"avito":     {Data: empty, Enabled: false},
		})
		return res, nil
	}

	var (
		tgBot    sql.NullString
		tgBotEn  int
		widget   sql.NullString
		widgetEn int
		tgUBot   sql.NullString
		tgUBotEn int
		whats    sql.NullString
		whatsEn  int
		avitoStr sql.NullString
		avitoEn  sql.NullInt32
	)

	err := d.Conn().QueryRowContext(ctx, `
		SELECT TgBot, TgBot_enabled, Widget, Widget_enabled,
		       TgUserBot, TgUserBot_enabled, Whats, Whats_enabled,
		       Avito, Avito_enabled
		FROM channels WHERE UserId = ?`, userId).
		Scan(&tgBot, &tgBotEn, &widget, &widgetEn,
			&tgUBot, &tgUBotEn, &whats, &whatsEn,
			&avitoStr, &avitoEn)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("ошибка получения данных каналов: %w", err)
	}

	return json.Marshal(map[string]entry{
		"tgbot":     {Data: d.decryptChannelField(userId, tgBot), Enabled: tgBotEn == 1},
		"widget":    {Data: d.decryptChannelField(userId, widget), Enabled: widgetEn == 1},
		"tguserbot": {Data: d.decryptChannelField(userId, tgUBot), Enabled: tgUBotEn == 1},
		"whatsbot":  {Data: d.decryptChannelField(userId, whats), Enabled: whatsEn == 1},
		"avito":     {Data: d.decryptChannelField(userId, avitoStr), Enabled: avitoEn.Valid && avitoEn.Int32 == 1},
	})
}

func (d *DB) DeleteChannelData(userId uint32, channelType string) error {
	// Проверяем входные значения
	if userId == 0 || channelType == "" {
		return fmt.Errorf("получены некорректные значения: userId или channelType пусты")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteChannelData: %v", rbErr, userId)
		}
	}()

	// Обновляем данные в зависимости от типа канала
	switch channelType {
	case "tgbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgBot = null, TgBot_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала TgBot: %w", err)
		}
	case "widget":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Widget = null, Widget_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Widget: %w", err)
		}
	case "tgubot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET TgUserBot = null, TgUserBot_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала TgUserBot: %w", err)
		}
	case "whatsbot":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Whats = null, Whats_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Whats: %w", err)
		}
	case "insta":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Insta = null, Insta_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Insta: %w", err)
		}
	case "avito":
		_, err = tx.ExecContext(ctx, `UPDATE channels SET Avito = null, Avito_enabled = 0 WHERE UserId = ?`, userId)
		if err != nil {
			return fmt.Errorf("ошибка обновления канала Avito: %w", err)
		}
	default:
		return fmt.Errorf("неизвестный тип канала: %s", channelType)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

// CheckActiveChannels возвращает true если у пользователя хотя бы один канал активен.
func (d *DB) CheckActiveChannels(userId uint32) (bool, error) {
	if userId == 0 {
		return false, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var active bool
	err := d.Conn().QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM channels
			WHERE UserId = ?
			  AND (TgBot_enabled = 1
			    OR Widget_enabled = 1
			    OR TgUserBot_enabled = 1
			    OR Whats_enabled = 1
			    OR Insta_enabled = 1
			    OR Avito_enabled = 1)
		)`, userId).Scan(&active)
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
func (d *DB) GetActiveChannels(userId uint32) ([]string, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var tg, wid, tgu, wa, inst, av sql.NullBool
	err := d.Conn().QueryRowContext(ctx,
		`SELECT TgBot_enabled, Widget_enabled, TgUserBot_enabled, Whats_enabled, Insta_enabled, Avito_enabled
		 FROM channels WHERE UserId = ? LIMIT 1`, userId).
		Scan(&tg, &wid, &tgu, &wa, &inst, &av)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("ошибка получения активных каналов: %w", err)
	}

	var channels []string
	if tg.Valid && tg.Bool {
		channels = append(channels, "tgbot")
	}
	if wid.Valid && wid.Bool {
		channels = append(channels, "widget")
	}
	if tgu.Valid && tgu.Bool {
		channels = append(channels, "tguserbot")
	}
	if wa.Valid && wa.Bool {
		channels = append(channels, "whatsbot")
	}
	if inst.Valid && inst.Bool {
		channels = append(channels, "insta")
	}
	if av.Valid && av.Bool {
		channels = append(channels, "avito")
	}
	return channels, nil
}
