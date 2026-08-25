package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/ikermy/air-common/pkg/mode"
)

// ServiceList возвращает список типов сервисов, подключённых к пользователю.
func (d *DB) ServiceList(userId uint32) ([]string, error) {
	if userId == 0 {
		return nil, fmt.Errorf("получен некорректный userId")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT ServiceType FROM service WHERE UserId = ? ORDER BY ServiceType", userId)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении списка сервисов: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения списка сервисов: %w", err)
		}
	}
	defer func() { _ = rows.Close() }()

	var services []string
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, fmt.Errorf("ошибка сканирования сервиса: %w", err)
		}
		services = append(services, svc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации сервисов: %w", err)
	}
	return services, nil
}

// AddService добавляет тип сервиса пользователю (игнорирует дубликаты).
func (d *DB) AddService(userId uint32, serviceType string) error {
	if userId == 0 || serviceType == "" {
		return fmt.Errorf("получены некорректные значения")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"INSERT IGNORE INTO service (UserId, ServiceType) VALUES (?, ?)", userId, serviceType)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при добавлении сервиса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка добавления сервиса: %w", err)
		}
	}
	return nil
}

// DeleteService удаляет тип сервиса у пользователя.
func (d *DB) DeleteService(userId uint32, serviceType string) error {
	if userId == 0 || serviceType == "" {
		return fmt.Errorf("получены некорректные значения")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	_, err := d.Conn().ExecContext(ctx,
		"DELETE FROM service WHERE UserId = ? AND ServiceType = ?", userId, serviceType)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при удалении сервиса: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка удаления сервиса: %w", err)
		}
	}
	return nil
}
