package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ikermy/air-common/v2/pkg/mode"
)

func (i *Implementation) GetUserDetails(userId uint64) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос для получения данных пользователя и подписки
	query := `
  SELECT JSON_OBJECT(
 'Date', u.Date,
 'RoleName', ur.RoleName,
 'Name', u.Name,
 'Balance', u.balance,
 'CurrencyName', cur.Name,
 'Confirmed', ua.Confirmed,
 'Disabled', ua.Disabled,
 'StartDate', s.StartDate,
 'MonthsPaid', s.MonthsPaid,
 'TotalCost', s.TotalCost,
 'Discount', s.Discount,
 'EndDate', s.EndDate,
 'StorageLimit', us.quota_bytes,
 'StorageUsed', us.used_bytes,
 'Telegram_bot', ch.TgBot_enabled,
 'Telegram_user', ch.TgUserBot_enabled,
 'WhatsApp', ch.Whats_enabled,
 'Widget', ch.Widget_enabled,
 'Instagram', ch.Insta_enabled,
 'Avito', ch.Avito_enabled
)
FROM users u
JOIN user_auth ua ON ua.UserId = u.Id
JOIN user_roles ur ON u.RoleId = ur.Id
LEFT JOIN currency cur ON u.currency = cur.Id
LEFT JOIN subscriptions s ON s.UserId = u.Id
LEFT JOIN user_storage_quota us ON u.Id = us.user_id
LEFT JOIN channels ch ON ch.UserId = u.Id
WHERE u.Id = ?
LIMIT 1`

	var result []byte
	err := i.Conn().QueryRowContext(ctx, query, userId).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("пользователь не найден")
		default:
			return nil, fmt.Errorf("ошибка получения данных пользователя: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от GetUserDetails")
	}

	return result, nil
}

func (i *Implementation) GetUserEmail(userId uint64) (string, error) {
	// Проверяем входное значение
	if userId == 0 {
		return "", fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var data sql.NullString
	err := i.Conn().QueryRowContext(ctx, "SELECT Email FROM user_auth WHERE UserId = ?", userId).Scan(&data)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", fmt.Errorf("тайм-аут (%d с) при получении email пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return "", fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return "", nil // Email не найден, но это не ошибка
		default:
			return "", fmt.Errorf("ошибка получения email пользователя: %w", err)
		}
	}

	if !data.Valid {
		return "", nil // Возвращаем пустую строку если данные NULL
	}

	return data.String, nil
}

func (i *Implementation) UserInfo(userID uint64) (json.RawMessage, error) {
	// Проверяем входное значение
	if userID == 0 {
		return nil, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос напрямую
	query := `
  SELECT JSON_OBJECT(
   'Date', u.Date,
   'Name', u.Name,
   'Role', u.RoleId,
   'Lang', u.Lang,
   'TimeZone', u.TimeZone,
   'AvailibleLang', (
    SELECT JSON_ARRAYAGG(
     JSON_OBJECT(
      'id', l.Id,
      'name', l.Code
     )
    )
    FROM languages l
   ),
   'Balance', u.balance,
   'Currency', u.currency,
   'AvailibleCurrency', (
    SELECT JSON_ARRAYAGG(
     JSON_OBJECT(
      'id', c.Id,
      'name', c.Name
     )
    )
    FROM currency c
   ),
   'TimeChange', u.Timechange,
   'GPTName', ug.Name,
   'Email', ua.Email,
   'Subscription', JSON_OBJECT(
    'StartDate', s.StartDate,
    'EndDate', s.EndDate,
    'MonthsPaid', s.MonthsPaid,
    'TotalCost', s.TotalCost,
    'Discount', s.Discount,
 	'StorageLimit', us.quota_bytes,
 	'StorageUsed', us.used_bytes
   ),
   'Notifications', JSON_OBJECT(
    'Email', IFNULL(n.Email = 1, false),
    'TgBotIsSet', n.Telegram IS NOT NULL AND n.Telegram <> 0,
    'Instant', IFNULL(n.Instant = 1, false),
    'TelegramEnabled', IFNULL(n.Telegram_enabled = 1, false),
    'Start', IFNULL(n.Start = 1, false),
    'End', IFNULL(n.End = 1, false),
    'Target', IFNULL(n.Target = 1, false)
   ),
   'ChannelsAvailable', JSON_OBJECT(
    'TgBotEnabled', IFNULL(ch.TgBot_enabled, 0),
    'WidgetEnabled', IFNULL(ch.Widget_enabled, 0),
    'TgUserBotEnabled', IFNULL(ch.TgUserBot_enabled, 0),
    'WhatsEnabled', IFNULL(ch.Whats_enabled, 0),
    'InstaEnabled', IFNULL(ch.Insta_enabled, 0)
   ),
   'TotpEnabled', ua.TOTPSecret IS NOT NULL,
   'MasterKey', ua.MasterKey IS NOT NULL
  ) AS userInfo
  FROM users u
  LEFT JOIN user_models um ON u.Id = um.UserId AND um.IsActive = 1
  LEFT JOIN user_gpt ug ON um.ModelId = ug.Id
  LEFT JOIN user_auth ua ON u.Id = ua.UserId
  LEFT JOIN subscriptions s ON u.Id = s.UserId
  LEFT JOIN user_storage_quota us ON u.Id = us.user_id
  LEFT JOIN notifications n ON u.Id = n.UserId
  LEFT JOIN channels ch ON u.Id = ch.UserId
  WHERE u.Id = ?`

	// Выполняем запрос
	var result []byte
	err := i.Conn().QueryRowContext(ctx, query, userID).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении информации о пользователе: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("информация о пользователе не найдена")
		default:
			return nil, fmt.Errorf("ошибка получения информации о пользователе: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от UserInfo")
	}

	return result, nil
}

func (i *Implementation) DeleteAllUserData(userID uint64) error {
	if userID == 0 {
		return fmt.Errorf("получен некорректный userID")
	}

	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel()*3)
	defer cancel()

	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("deleteAllUserData begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	simple := []struct {
		table  string
		column string
	}{
		{"crypto_payments", "user_id"},
		{"billing", "UserId"},
		{"subscriptions", "UserId"},
		{"notifications", "UserId"},
		{"user_directory", "user_id"},
		{"user_bot_state", "user_id"},
		{"user_bot_events", "user_id"},
		{"user_bots", "user_id"},
		{"user_channel_configs", "user_id"},
		{"user_channels", "user_id"},
		{"dialog_messages", "user_id"},
		{"dialog_contexts", "user_id"},
		{"responders", "user_id"},
		{"channels", "UserId"},
		{"dialogs", "User"},
		{"user_auth", "UserId"},
		{"user_api_keys", "UserId"},
		{"google_oauth_tokens", "user_id"},
		{"vector_embeddings", "user_id"},
		{"crm_oauth_states", "user_id"},
		{"crm_configs", "user_id"},
		{"user_storage_config", "user_id"},
		{"user_storage_quota", "user_id"},
		{"storage_migrations", "user_id"},
		{"service_contacts", "UserId"},
		{"service_gpt", "UserId"},
		{"service_proxy", "UserId"},
		{"service_tgbots", "UserId"},
		{"service_wabots", "UserId"},
		{"service", "UserId"},
		{"operators", "UserId"},
	}
	for _, item := range simple {
		query := fmt.Sprintf("DELETE FROM `%s` WHERE `%s` = ?", item.table, item.column)
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return fmt.Errorf("deleteAllUserData %s: %w", item.table, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM user_gpt
		WHERE Id IN (SELECT ModelId FROM user_models WHERE UserId = ?)`, userID); err != nil {
		return fmt.Errorf("deleteAllUserData user_gpt: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_models WHERE UserId = ?", userID); err != nil {
		return fmt.Errorf("deleteAllUserData user_models: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE Id = ?", userID); err != nil {
		return fmt.Errorf("deleteAllUserData users: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, actor_id, action, details)
		VALUES (?, ?, 'user.hard_delete', JSON_OBJECT('source', 'DeleteAllUserData'))`,
		userID, userID); err != nil {
		return fmt.Errorf("deleteAllUserData audit: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("deleteAllUserData commit: %w", err)
	}
	return nil
}

// SoftDeleteUserData marks the user as deleted and records an audit entry.
// Repeated calls are idempotent.
func (i *Implementation) SoftDeleteUserData(userID uint64, actorID uint64) error {
	if userID == 0 {
		return fmt.Errorf("получен некорректный userID")
	}

	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	res, err := i.Conn().ExecContext(ctx, `
		UPDATE users SET status = 'deleted', deleted_at = NOW(3)
		WHERE Id = ? AND status <> 'deleted'`, userID)
	if err != nil {
		return fmt.Errorf("softDeleteUserData: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil
	}

	if _, err := i.Conn().ExecContext(ctx, `
		INSERT INTO audit_log (user_id, actor_id, action, details)
		VALUES (?, ?, 'user.soft_delete', JSON_OBJECT('source', 'SoftDeleteUserData'))`,
		userID, actorID); err != nil {
		return fmt.Errorf("softDeleteUserData audit: %w", err)
	}
	return nil
}

// PurgeDeletedUsers hard-deletes users soft-deleted before now-grace.
// At most limit users are purged per call (limit <= 0 means 100).
func (i *Implementation) PurgeDeletedUsers(ctx context.Context, grace time.Duration, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	// Compare against DB time to avoid clock skew between the app and MariaDB.
	graceSeconds := int64(grace / time.Second)
	if graceSeconds < 0 {
		graceSeconds = 0
	}

	rows, err := i.Conn().QueryContext(ctx, `
		SELECT Id FROM users
		WHERE status = 'deleted' AND deleted_at IS NOT NULL
		  AND deleted_at <= DATE_SUB(NOW(3), INTERVAL ? SECOND)
		ORDER BY Id
		LIMIT ?`, graceSeconds, limit)
	if err != nil {
		return 0, fmt.Errorf("purgeDeletedUsers load: %w", err)
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("purgeDeletedUsers scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("purgeDeletedUsers close: %w", err)
	}

	purged := 0
	for _, id := range ids {
		if err := i.DeleteAllUserData(id); err != nil {
			return purged, fmt.Errorf("purgeDeletedUsers user %d: %w", id, err)
		}
		purged++
	}
	return purged, nil
}

func (i *Implementation) SaveUserTimeZone(userID uint64, timeZone string) error {
	// Проверяем входные значения
	if userID == 0 || timeZone == "" {
		return fmt.Errorf("получены некорректные значения: userId или timeZone пусты")
	}
	if len(timeZone) == 0 || len(timeZone) > 64 {
		return fmt.Errorf("получены некорректные данные timeZone")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := i.Conn().ExecContext(ctx, "UPDATE users SET TimeZone = ? WHERE Id = ?", timeZone, userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении часового пояса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения часового пояса: %w", err)
		}
	}

	return nil
}

func (i *Implementation) SaveUserLanguage(userID uint64, language string) error {
	// Проверяем входные значения
	if userID == 0 || language == "" {
		return fmt.Errorf("получены некорректные значения: userId или language пусты")
	}
	if len(language) != 2 {
		return fmt.Errorf("получены некорректные данные language")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := i.Conn().ExecContext(ctx,
		"UPDATE users SET lang = (SELECT id FROM languages WHERE Code=?) WHERE users.Id=?", language, userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении языка пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения языка пользователя: %w", err)
		}
	}

	return nil
}

// CheckDemo проверяет, является ли пользователь демо-пользователем (RoleId=1).
func (i *Implementation) CheckDemo(userId uint64) (bool, error) {
	if userId == 0 {
		return false, fmt.Errorf("получен пустой userId")
	}

	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var roleId uint8
	err := i.Conn().QueryRowContext(ctx, "SELECT RoleId FROM users WHERE Id = ?", userId).Scan(&roleId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при проверке демо-роли: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return false, fmt.Errorf("пользователь с Id=%d не найден", userId)
		default:
			return false, fmt.Errorf("ошибка проверки демо-роли: %w", err)
		}
	}
	return roleId == 1, nil
}
