package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
)

func (d *DB) OperatorsList(ctx context.Context, userID uint32) (json.RawMessage, error) {
	// Проверяем входное значение
	if userID == 0 {
		return nil, fmt.Errorf("получен некорректный userID")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	var result sql.NullString
	err := d.Conn().QueryRowContext(ctx, "SELECT Telegram FROM operators WHERE UserId = ?", userID).Scan(&result)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении списка операторов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		case errors.Is(err, sql.ErrNoRows):
			// Возвращаем пустой JSON массив если нет записей
			return json.RawMessage("[]"), nil
		default:
			return nil, fmt.Errorf("ошибка получения списка операторов: %w", err)
		}
	}

	// Проверяем, было ли значение NULL или пустое
	if !result.Valid || result.String == "" {
		return json.RawMessage("[]"), nil
	}

	return json.RawMessage(result.String), nil
}

// SaveOperators сохраняет список операторов в таблицу operators через SP SaveOperators.
func (d *DB) SaveOperators(ctx context.Context, userID uint32, operatorType string, data json.RawMessage) error {
	// Проверяем входные значения
	if userID == 0 || operatorType == "" {
		return fmt.Errorf("получены некорректные значения: userID или operatorType пусты")
	}
	if len(data) == 0 || !json.Valid(data) {
		return fmt.Errorf("получены некорректные данные JSON")
	}

	// Дочерний контекст с тайм-аутом на операцию
	ctx, cancel := context.WithTimeout(ctx, mode.GetSQLTimeToCancel())
	defer cancel()

	// Определяем SQL запрос в зависимости от типа оператора (защита от SQL injection)
	var query string
	switch operatorType {
	case "Telegram":
		query = "INSERT INTO operators (UserId, Telegram) VALUES (?, ?) ON DUPLICATE KEY UPDATE Telegram = ?, Timechange = NOW()"
	case "Widget":
		query = "INSERT INTO operators (UserId, Widget) VALUES (?, ?) ON DUPLICATE KEY UPDATE Widget = ?, Timechange = NOW()"
	default:
		return fmt.Errorf("неподдерживаемый тип оператора: %s", operatorType)
	}

	// Выполняем запрос
	_, err := d.Conn().ExecContext(ctx, query, userID, string(data), string(data))
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при сохранении операторов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка сохранения операторов: %w", err)
		}
	}

	return nil
}
