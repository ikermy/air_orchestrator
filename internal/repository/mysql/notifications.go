package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

func (i *Implementation) UpdateNotification(userId uint32, tip string, status bool, telegaId uint64) error {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в UpdateNotification: %v", rbErr, userId)
		}
	}()

	// Проверяем существование записи для данного UserId
	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM notifications WHERE UserId = ?)", userId).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}

	enabledInt := 0
	if status {
		enabledInt = 1
	}

	if exists {
		// Обновляем существующую запись
		switch tip {
		case "email":
			_, err = tx.ExecContext(ctx, `UPDATE notifications SET Email = ? WHERE UserId = ?`, enabledInt, userId)
		case "telega":
			if telegaId != 0 {
				_, err = tx.ExecContext(ctx, `UPDATE notifications SET Telegram_enabled = ?, Telegram = ? WHERE UserId = ?`,
					enabledInt, telegaId, userId)
			} else {
				_, err = tx.ExecContext(ctx, `UPDATE notifications SET Telegram_enabled = ? WHERE UserId = ?`,
					enabledInt, userId)
			}
		case "instant":
			_, err = tx.ExecContext(ctx, `UPDATE notifications SET Instant = ? WHERE UserId = ?`, enabledInt, userId)
		default:
			return fmt.Errorf("неизвестный тип уведомления: %s", tip)
		}
	} else {
		// Создаём новую запись
		switch tip {
		case "email":
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, ?, 0, 0, 0)`,
				userId, enabledInt)
		case "telega":
			telegaValue := sql.NullInt64{Int64: int64(telegaId), Valid: telegaId != 0}
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, 0, ?, ?, 0)`,
				userId, telegaValue, enabledInt)
		case "instant":
			_, err = tx.ExecContext(ctx, `INSERT INTO notifications (UserId, Email, Telegram, Telegram_enabled, Instant) VALUES (?, 0, 0, 0, ?)`,
				userId, enabledInt)
		default:
			return fmt.Errorf("неизвестный тип уведомления: %s", tip)
		}
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка обновления уведомления: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (i *Implementation) GetNotificationsData(userId uint32) (json.RawMessage, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Получаем email пользователя
	var userEmail sql.NullString
	err := i.Conn().QueryRowContext(ctx, "SELECT Email FROM user_auth WHERE UserId = ? LIMIT 1", userId).Scan(&userEmail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении email пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения email пользователя: %w", err)
		}
	}

	// Проверяем существование записи в notifications
	var found bool
	err = i.Conn().QueryRowContext(ctx, "SELECT COUNT(*) > 0 FROM notifications WHERE UserId = ?", userId).Scan(&found)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке существования уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка проверки существования уведомлений: %w", err)
		}
	}

	var result []byte

	if found {
		// Формируем JSON с данными уведомлений
		query := `
   SELECT JSON_OBJECT(
    'email', JSON_OBJECT(
     'data', ?,
     'enabled', Email = 1
    ),
    'telega', JSON_OBJECT(
     'data', IF(Telegram = 0, NULL, CAST(Telegram AS CHAR)),
     'enabled', Telegram_enabled = 1
    ),
    'instant', JSON_OBJECT(
     'enabled', Instant = 1
    ),
    'events', JSON_OBJECT(
     'start', Start = 1,
     'end', End = 1,
     'target', Target = 1
    )
   )
   FROM notifications
   WHERE UserId = ?`

		err = i.Conn().QueryRowContext(ctx, query, userEmail.String, userId).Scan(&result)
	} else {
		// Возвращаем структуру по умолчанию
		query := `
   SELECT JSON_OBJECT(
    'email', JSON_OBJECT(
     'data', ?,
     'enabled', false
    ),
    'telega', JSON_OBJECT(
     'data', NULL,
     'enabled', false
    ),
    'instant', JSON_OBJECT(
     'enabled', false
    ),
    'events', JSON_OBJECT(
     'start', false,
     'end', false,
     'target', false
    )
   )`

		err = i.Conn().QueryRowContext(ctx, query, userEmail.String).Scan(&result)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		default:
			return nil, fmt.Errorf("ошибка получения данных уведомлений: %w", err)
		}
	}

	if len(result) == 0 {
		return nil, nil
	}

	return result, nil
}

func (i *Implementation) SaveNotificationEvent(userId uint32, start, end, target bool) error {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в SaveNotificationEvent: %v", rbErr, userId)
		}
	}()

	// Проверяем существование записи для данного UserId
	var exists bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM notifications WHERE UserId = ?)", userId).Scan(&exists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования записи: %w", err)
	}

	if exists {
		// Обновляем существующую запись
		_, err = tx.ExecContext(ctx, `
   UPDATE notifications
   SET Start = ?, End = ?, Target = ?
   WHERE UserId = ?`,
			start, end, target, userId)
	} else {
		// Создаём новую запись
		_, err = tx.ExecContext(ctx, `
   INSERT INTO notifications (UserId, Start, End, Target)
   VALUES (?, ?, ?, ?)`,
			userId, start, end, target)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении события уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения события уведомления: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (i *Implementation) DeleteNotificationsChannel(userId uint32, chanelName string) error {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := i.Conn().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в DeleteNotificationsChannel: %v", rbErr, userId)
		}
	}()

	// Обновляем соответствующий канал в зависимости от типа
	switch chanelName {
	case "email":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Email = 0 WHERE UserId = ?", userId)
	case "telegram":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Telegram = NULL, Telegram_enabled = 0 WHERE UserId = ?", userId)
	case "instant":
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET Instant = 0 WHERE UserId = ?", userId)
	default:
		return fmt.Errorf("неизвестный тип канала: %s", chanelName)
	}

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении канала уведомлений: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления канала уведомлений: %w", err)
		}
	}

	// Если все каналы отключены, сбрасываем флаги событий
	_, err = tx.ExecContext(ctx, `
  UPDATE notifications
  SET Start = 0, End = 0, Target = 0
  WHERE UserId = ?
    AND (Email = 0 OR Email IS NULL)
    AND (Telegram = 0 OR Telegram IS NULL)
    AND Instant = 0`, userId)

	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сбросе флагов событий: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена при сбросе флагов: %w", err)
		default:
			return fmt.Errorf("ошибка сброса флагов событий: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}
