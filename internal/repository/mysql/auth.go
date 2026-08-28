package db

import (
	"air_orchestrator/internal/domain/state"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

func (d *DB) CheckEmail(email, emailHMAC string) (uint32, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Ищем по plaintext Email (старые пользователи) ИЛИ по EmailHash (мигрированные/новые).
	var result sql.NullInt32
	err := d.Conn().QueryRowContext(ctx,
		"SELECT UserId FROM user_auth WHERE Email = ? OR EmailHash = ? LIMIT 1",
		email, emailHMAC).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при проверке email: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return 0, nil
		default:
			return 0, fmt.Errorf("ошибка проверки email: %w", err)
		}
	}

	// Проверяем, было ли значение NULL
	if !result.Valid {
		return 0, nil
	}

	return uint32(result.Int32), nil
}

func (d *DB) CreateUser(name, pass, encEmail, emailHMAC, lang string, demo bool) (uint32, error) {
	// Проверяю что нет пустых значений
	if name == "" || pass == "" || encEmail == "" || emailHMAC == "" {
		return 0, fmt.Errorf("получены пустые значения")
	}

	// Проверяю что lang валидный
	ok := state.ValidateLanguage(lang)
	if !ok {
		lang = "en"
	}

	// Если пользователь выбрал демо доступ
	role := 2
	if demo {
		role = 1
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := d.Conn().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			logger.Error("Ошибка отката транзакции в CreateUser: %v", rbErr)
		}
	}()

	// Определяем langId на основе языка
	var langId uint8 = 1 // По умолчанию русский да, второй раз страхуюсь
	err = tx.QueryRowContext(ctx, "SELECT Id FROM languages WHERE Code = ? LIMIT 1", lang).Scan(&langId)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при получении языка: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка получения языка: %w", err)
		}
	}

	// Пока все будут с USDT (currency = 0) state.DefaultCurrency

	// Вставляем пользователя в таблицу users
	result, err := tx.ExecContext(ctx,
		"INSERT INTO users (`Name`, `RoleId`, `Date`, `currency`, `Lang`) VALUES (?, ?, CURRENT_TIMESTAMP(), ?, ?)",
		name, role, state.DefaultCurrency, langId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания пользователя: %w", err)
		}
	}

	// Получаем ID нового пользователя
	newUserId, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("ошибка получения ID пользователя: %w", err)
	}

	// Вставляем данные авторизации в таблицу user_auth
	_, err = tx.ExecContext(ctx,
		"INSERT INTO user_auth (`UserId`, `SHA`, `Email`, `EmailHash`, `Confirmed`) VALUES (?, ?, ?, ?, 0)",
		newUserId, pass, encEmail, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании авторизации: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания авторизации: %w", err)
		}
	}

	// Добавляем запись в subscriptions в зависимости от типа пользователя
	if role == 2 {
		// Обычная подписка
		_, err = tx.ExecContext(ctx,
			"INSERT INTO subscriptions (`UserId`, `StartDate`, `TotalCost`) VALUES (?, CURRENT_DATE(), 0)",
			newUserId)
	} else {
		// Демо-подписка (один месяц)
		_, err = tx.ExecContext(ctx,
			"INSERT INTO subscriptions (`UserId`, `StartDate`, `EndDate`, `TotalCost`) VALUES (?, CURRENT_DATE(), DATE_ADD(CURRENT_DATE(), INTERVAL 1 MONTH), 0)",
			newUserId)
	}
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при создании подписки: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("операция создании подписки отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания подписки: %w", err)
		}
	}

	// Создаю лимит в хранилище пользователя
	_, err = tx.ExecContext(ctx,
		"INSERT INTO user_storage_quota (`user_id`, `quota_bytes`) VALUES (?, ?)",
		newUserId, state.NewUserStorageLimit)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return 0, fmt.Errorf("тайм-аут (%d с) при задании лимита хранилища: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return 0, fmt.Errorf("при задании лимита хранилища операция отменена: %w", err)
		default:
			return 0, fmt.Errorf("ошибка создания лимита хранилища: %w", err)
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return uint32(newUserId), nil
}

func (d *DB) CheckAuth(pass, email string) (json.RawMessage, error) {
	// Проверяем входные значения
	if pass == "" || email == "" {
		return nil, fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос для получения данных пользователя
	query := `
  SELECT JSON_OBJECT(
   'Id', u.Id,
   'Confirmed', ua.Confirmed,
   'Disabled', ua.Disabled
  )
  FROM users u
  JOIN user_auth ua ON ua.UserId = u.Id
  JOIN user_roles ur ON u.RoleId = ur.Id
  LEFT JOIN currency c ON u.currency = c.Id
  WHERE ua.SHA = ? AND ua.Email = ?
  LIMIT 1`

	var result sql.NullString
	err := d.Conn().QueryRowContext(ctx, query, pass, email).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке авторизации: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil
		default:
			return nil, fmt.Errorf("ошибка проверки авторизации: %w", err)
		}
	}

	// Проверяем корректность результата
	if !result.Valid || result.String == "" {
		return nil, nil
	}

	return json.RawMessage(result.String), nil
}

func (d *DB) ConfirmMail(email, emailHMAC string) error {
	if email == "" && emailHMAC == "" {
		return fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET Confirmed = 1 WHERE (Email = ? OR EmailHash = ?) AND Confirmed = 0",
		email, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при подтверждении email: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка подтверждения email: %w", err)
		}
	}

	// Проверяем, была ли обновлена хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества обновленных строк: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("ошибка подтверждения email")
	}

	return nil
}

func (d *DB) UpdatePassword(email, emailHMAC string, newSHA string) error {
	// Проверяю что нет пустых значений
	if (email == "" && emailHMAC == "") || newSHA == "" {
		return fmt.Errorf("получены пустые значения")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := d.Conn().ExecContext(ctx,
		"UPDATE user_auth SET SHA = ? WHERE Email = ? OR EmailHash = ?",
		newSHA, email, emailHMAC)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении пароля: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка обновления пароля: %w", err)
		}
	}

	// Проверяем, была ли обновлена хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества обновленных строк: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("ошибка обновления пароля, пользователь не найден")
	}

	return nil
}
