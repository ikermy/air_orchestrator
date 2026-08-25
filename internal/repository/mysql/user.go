package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
)

func (d *DB) GetUserDetails(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
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
	err := d.Conn().QueryRowContext(ctx, query, userId).Scan(&result)
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

func (d *DB) GetUserEmail(userId uint32) (string, error) {
	// Проверяем входное значение
	if userId == 0 {
		return "", fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var data sql.NullString
	err := d.Conn().QueryRowContext(ctx, "SELECT Email FROM user_auth WHERE UserId = ?", userId).Scan(&data)
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

func (d *DB) UserInfo(userID uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userID == 0 {
		return nil, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
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
	err := d.Conn().QueryRowContext(ctx, query, userID).Scan(&result)
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

func (d *DB) DeleteAllUserData(userID uint32) error {
	// Проверяем входное значение
	if userID == 0 {
		return fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Вызываем хранимую процедуру для удаления всех данных пользователя
	_, err := d.Conn().ExecContext(ctx, "CALL DeleteAllUserData(?)", userID)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении всех данных пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления всех данных пользователя: %w", err)
		}
	}

	return nil
}

func (d *DB) SaveUserTimeZone(userID uint32, timeZone string) error {
	// Проверяем входные значения
	if userID == 0 || timeZone == "" {
		return fmt.Errorf("получены некорректные значения: userId или timeZone пусты")
	}
	if len(timeZone) == 0 || len(timeZone) > 64 {
		return fmt.Errorf("получены некорректные данные timeZone")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx, "UPDATE users SET TimeZone = ? WHERE Id = ?", timeZone, userID)
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

func (d *DB) SaveUserLanguage(userID uint32, language string) error {
	// Проверяем входные значения
	if userID == 0 || language == "" {
		return fmt.Errorf("получены некорректные значения: userId или language пусты")
	}
	if len(language) != 2 {
		return fmt.Errorf("получены некорректные данные language")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
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
func (d *DB) CheckDemo(userId uint32) (bool, error) {
	if userId == 0 {
		return false, fmt.Errorf("получен пустой userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	var roleId uint8
	err := d.Conn().QueryRowContext(ctx, "SELECT RoleId FROM users WHERE Id = ?", userId).Scan(&roleId)
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
