package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

func (d *DB) GetUserDialogs(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// SQL запрос напрямую
	query := `
  SELECT JSON_ARRAYAGG(
   JSON_OBJECT(
    'DialogId', d.Id,
    'Date', d.Date,
    'Type', ct.Name,
    'Responder', r.Name,
    'Target', d.Target,
    'Trigger', d.` + "`Trigger`" + `
   )
  ) AS dialogData
  FROM dialogs d
  JOIN users u ON d.User = u.Id
  JOIN responders r ON d.Responder = r.Id
  JOIN chat_type ct ON d.Type = ct.Id
  WHERE d.User = ?`

	// Выполняем запрос
	var data sql.NullString
	err := d.Conn().QueryRowContext(ctx, query, userId).Scan(&data)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении диалогов пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, nil // Диалоги не найдены, но это не ошибка
		default:
			return nil, fmt.Errorf("ошибка получения диалогов пользователя: %w", err)
		}
	}

	// Если пользователя или диалогов нет, то null
	if !data.Valid {
		return nil, nil // Возвращаем nil для пустых данных
	}

	return json.RawMessage(data.String), nil
}

func (d *DB) GetDevUserData(userId uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Сначала проверяем роль пользователя
	var userRole int
	err := d.Conn().QueryRowContext(ctx, "SELECT RoleId FROM users WHERE Id = ?", userId).Scan(&userRole)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при проверке роли пользователя: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("пользователь не найден")
		default:
			return nil, fmt.Errorf("ошибка проверки роли пользователя: %w", err)
		}
	}

	// Если роль не 0, возвращаем пустой JSON объект
	if userRole != 0 {
		return json.RawMessage("{}"), nil
	}

	// SQL запрос для получения данных разработчика
	query := `
  SELECT JSON_OBJECT(
  'Name', u.Name,
  'Email', ua.Email,
  'TimeZone', u.TimeZone,
  'Balance', u.balance,
  'GptModels', (
    SELECT JSON_ARRAYAGG(
      JSON_OBJECT(
        'Id', ug.Id,
        'Name', ug.Name,
        'Model', gm.Name,
        'Provider', mp.Name,
        'IsActive', IF(um.IsActive = 1, true, false)
      )
    )
    FROM user_models um
    LEFT JOIN user_gpt ug ON um.ModelId = ug.Id
    LEFT JOIN gpt_models gm ON ug.Model = gm.Id
    LEFT JOIN model_providers mp ON um.Provider = mp.Id
    WHERE um.UserId = u.Id
  ),
  'AvailableProviders', (
    SELECT JSON_ARRAYAGG(
      JSON_OBJECT(
        'provider', p.Name,
        'models', (
          SELECT JSON_ARRAYAGG(
            JSON_OBJECT(
              'id', m2.Id,
              'name', m2.Name
            )
          )
          FROM gpt_models m2
          WHERE m2.Provider = p.Id
        ),
        'default_model', (
          SELECT JSON_OBJECT(
            'id', m3.Id,
            'name', m3.Name
          )
          FROM gpt_models m3
          WHERE m3.Provider = p.Id AND m3.IsDefault = 1
          LIMIT 1
        )
      )
    )
    FROM model_providers p
  )
) AS json_result
FROM users u
LEFT JOIN user_auth ua ON ua.UserId = u.Id
WHERE u.Id = ?`

	// Выполняем запрос
	var result []byte
	err = d.Conn().QueryRowContext(ctx, query, userId).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении данных разработчика: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("данные пользователя не найдены")
		default:
			return nil, fmt.Errorf("ошибка получения данных разработчика: %w", err)
		}
	}

	// Проверяем корректность результата
	if len(result) == 0 {
		return nil, fmt.Errorf("пустой результат от GetDevUserData")
	}

	return result, nil
}

func (d *DB) UpdateDevData(userId uint32, name, encEmail, emailHMAC, sha string) error {
	// Проверяем входное значение userId
	if userId == 0 {
		return fmt.Errorf("получен некорректный userId")
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
			logger.Error("Ошибка отката транзакции в UpdateDevData: %v", rbErr, userId)
		}
	}()

	// Обновляем Users.Name, если Name не пустое
	if name != "" {
		_, err = tx.ExecContext(ctx, "UPDATE users SET Name = ? WHERE Id = ?", name, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении имени пользователя: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении имени: %w", err)
			default:
				return fmt.Errorf("ошибка обновления имени пользователя: %w", err)
			}
		}
	}

	// Обновляем user_auth.Email и EmailHash, если encEmail не пустое
	if encEmail != "" {
		_, err = tx.ExecContext(ctx,
			"UPDATE user_auth SET Email = ?, EmailHash = ? WHERE UserId = ?",
			encEmail, emailHMAC, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении email: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении email: %w", err)
			default:
				return fmt.Errorf("ошибка обновления email: %w", err)
			}
		}
	}

	// Обновляем user_auth.SHA, если SHA не пустое
	if sha != "" {
		_, err = tx.ExecContext(ctx, "UPDATE user_auth SET SHA = ? WHERE UserId = ?", sha, userId)
		if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				return fmt.Errorf("тайм-аут (%d с) при обновлении пароля: %w", mode.GetSQLTimeToCancel(), err)
			case errors.Is(err, context.Canceled):
				return fmt.Errorf("операция отменена при обновлении пароля: %w", err)
			default:
				return fmt.Errorf("ошибка обновления пароля: %w", err)
			}
		}
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	return nil
}

func (d *DB) UpdateDevGPTModel(provider string, modId uint8) error {
	// Проверяем входные данные
	if provider == "" {
		return fmt.Errorf("получен пустой provider")
	}
	if modId == 0 {
		return fmt.Errorf("получен некорректный modId")
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
			logger.Error("Ошибка отката транзакции в UpdateDevGPTModel: %v", rbErr)
		}
	}()

	// Получаем ID провайдера по имени
	var providerId int
	err = tx.QueryRowContext(ctx,
		"SELECT Id FROM model_providers WHERE Name = ?",
		provider).Scan(&providerId)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("провайдер '%s' не найден", provider)
		}
		return fmt.Errorf("ошибка получения ID провайдера: %w", err)
	}

	// Проверяем, что модель существует и принадлежит этому провайдеру
	var modelExists bool
	err = tx.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM gpt_models WHERE Id = ? AND Provider = ?)",
		modId, providerId).Scan(&modelExists)
	if err != nil {
		return fmt.Errorf("ошибка проверки существования модели: %w", err)
	}
	if !modelExists {
		return fmt.Errorf("модель с ID %d не найдена для провайдера '%s'", modId, provider)
	}

	// Сбрасываем IsDefault для всех моделей этого провайдера
	_, err = tx.ExecContext(ctx,
		"UPDATE gpt_models SET IsDefault = 0 WHERE Provider = ?",
		providerId)
	if err != nil {
		return fmt.Errorf("ошибка сброса флага IsDefault: %w", err)
	}

	// Устанавливаем IsDefault = 1 для выбранной модели
	result, err := tx.ExecContext(ctx,
		"UPDATE gpt_models SET IsDefault = 1 WHERE Id = ? AND Provider = ?",
		modId, providerId)
	if err != nil {
		return fmt.Errorf("ошибка установки модели по умолчанию: %w", err)
	}

	// Проверяем, была ли затронута хотя бы одна строка
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("ошибка получения количества затронутых строк: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("модель не была обновлена")
	}

	// Фиксируем транзакцию
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ошибка фиксации транзакции: %w", err)
	}

	logger.Info("Модель по умолчанию обновлена: provider='%s', modelId=%d", provider, modId)
	return nil
}
