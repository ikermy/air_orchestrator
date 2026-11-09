package mysql

import (
	"air_orchestrator/internal/domain/state"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/v2/pkg/dbrouter"
	"github.com/ikermy/air-common/v2/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

func (i *Implementation) CheckEmail(email, emailHMAC string) (uint64, error) {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Ищем по plaintext Email (старые пользователи) ИЛИ по EmailHash (мигрированные/новые).
	var result sql.NullInt64
	err := i.Conn().QueryRowContext(ctx,
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

	return uint64(result.Int64), nil
}

func (i *Implementation) CreateUser(name, pass, encEmail, emailHMAC, lang string, demo bool) (uint64, error) {
	// Если пользователь выбрал демо доступ
	role := 2
	if demo {
		role = 1
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Начинаем транзакцию для атомарности операций
	tx, err := i.Conn().BeginTx(ctx, nil)
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

	// Вставляем пользователя в таблицу users.
	// При IDGEN_ENABLED=true ID генерируется приложением (Snowflake) и
	// помечается id_source='snowflake'; иначе используется AUTO_INCREMENT
	// (legacy) до полного rollout.
	var newUserId uint64
	if i.IDGenEnabled {
		if i.IDGen == nil {
			return 0, fmt.Errorf("IDGEN_ENABLED=true, но генератор ID не инициализирован")
		}
		newUserId = i.IDGen.Next()
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO users (`Id`, `id_source`, `Name`, `RoleId`, `Date`, `currency`, `Lang`) VALUES (?, 'snowflake', ?, ?, CURRENT_TIMESTAMP(), ?, ?)",
			newUserId, name, role, state.DefaultCurrency, langId); err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return 0, fmt.Errorf("тайм-аут (%d с) при создании пользователя: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return 0, fmt.Errorf("операция отменена: %w", err)
			default:
				return 0, fmt.Errorf("ошибка создания пользователя: %w", err)
			}
		}
	} else {
		result, err := tx.ExecContext(ctx,
			"INSERT INTO users (`id_source`, `Name`, `RoleId`, `Date`, `currency`, `Lang`) VALUES ('legacy', ?, ?, CURRENT_TIMESTAMP(), ?, ?)",
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
		lastID, err := result.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("ошибка получения ID пользователя: %w", err)
		}
		newUserId = uint64(lastID)
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

	// Регистрируем пользователя в глобальном каталоге шардирования.
	// slot = xxhash64(BE64(user_id)) & 1023; shard_id=0 в single-DB режиме.
	if decodedHash, decErr := hex.DecodeString(emailHMAC); decErr == nil && len(decodedHash) == 32 {
		slot := dbrouter.SlotForUser(newUserId)
		if _, dirErr := tx.ExecContext(ctx, `
			INSERT INTO user_directory (user_id, email_hash, slot, shard_id)
			VALUES (?, ?, ?, 0)
			ON DUPLICATE KEY UPDATE slot = VALUES(slot)`,
			newUserId, decodedHash, slot); dirErr != nil {
			return 0, fmt.Errorf("ошибка записи user_directory: %w", dirErr)
		}
	} else {
		logger.Warn("CreateUser: некорректный email hash, user_directory не заполнен: %v", decErr, newUserId)
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return newUserId, nil
}

//func (i *Implementation) CheckAuth(pass, email string) (json.RawMessage, error) {
//	// Проверяем входные значения
//	if pass == "" || email == "" {
//		return nil, fmt.Errorf("получены пустые значения")
//	}
//
//	// Дочерний контекст с тайм-аутом на операцию
//	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
//	defer cancel()
//
//	// SQL запрос для получения данных пользователя
//	query := `
//  SELECT JSON_OBJECT(
//   'Id', u.Id,
//   'Confirmed', ua.Confirmed,
//   'Disabled', ua.Disabled
//  )
//  FROM users u
//  JOIN user_auth ua ON ua.UserId = u.Id
//  JOIN user_roles ur ON u.RoleId = ur.Id
//  LEFT JOIN currency c ON u.currency = c.Id
//  WHERE ua.SHA = ? AND ua.Email = ?
//  LIMIT 1`
//
//	var result sql.NullString
//	err := i.Conn().QueryRowContext(ctx, query, pass, email).Scan(&result)
//	if err != nil {
//		switch {
//		case errors.Is(err, context.DeadlineExceeded):
//			return nil, fmt.Errorf("тайм-аут (%d с) при проверке авторизации: %w", mode.GetSQLTimeToCancel(), err)
//		case errors.Is(err, context.Canceled):
//			return nil, fmt.Errorf("операция отменена: %w", err)
//		case errors.Is(err, sql.ErrNoRows):
//			return nil, nil
//		default:
//			return nil, fmt.Errorf("ошибка проверки авторизации: %w", err)
//		}
//	}
//
//	// Проверяем корректность результата
//	if !result.Valid || result.String == "" {
//		return nil, nil
//	}
//
//	return json.RawMessage(result.String), nil
//}

func (i *Implementation) ConfirmMail(email, emailHMAC string) error {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := i.Conn().ExecContext(ctx,
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

func (i *Implementation) UpdatePassword(email, emailHMAC string, newSHA string) error {
	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(i.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Поиск по Email (старые пользователи) или по EmailHash (новые/мигрированные)
	result, err := i.Conn().ExecContext(ctx,
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
