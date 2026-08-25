package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikermy/air-common/pkg/mode"
)

// GetUsersWithGoogleToken возвращает список userId всех пользователей,
// у которых есть активная запись в google_oauth_tokens.
func (d *DB) GetUsersWithGoogleToken() ([]uint32, error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT user_id FROM google_oauth_tokens ORDER BY user_id")
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("тайм-аут (%d с) при получении пользователей с Google токенами: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return nil, fmt.Errorf("операция отменена: %w", err)
		default:
			return nil, fmt.Errorf("ошибка получения пользователей с Google токенами: %w", err)
		}
	}
	defer func() { _ = rows.Close() }()

	var users []uint32
	for rows.Next() {
		var uid uint32
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("ошибка сканирования userId: %w", err)
		}
		users = append(users, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации по строкам: %w", err)
	}
	return users, nil
}

// GetMigratedUsersEmails возвращает зашифрованные email всех мигрированных пользователей.
func (d *DB) GetMigratedUsersEmails() ([]struct {
	UserId   uint32
	EncEmail string
}, error) {
	ctx, cancel := context.WithTimeout(d.Context(), 30*time.Second)
	defer cancel()

	rows, err := d.Conn().QueryContext(ctx,
		"SELECT UserId, Email FROM user_auth WHERE EmailHash IS NOT NULL")
	if err != nil {
		return nil, fmt.Errorf("ошибка получения мигрированных пользователей: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []struct {
		UserId   uint32
		EncEmail string
	}
	for rows.Next() {
		var item struct {
			UserId   uint32
			EncEmail string
		}
		if err := rows.Scan(&item.UserId, &item.EncEmail); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// UsersWithoutSubscription находит пользователей у которых истекла
func (d *DB) UsersWithoutSubscription() ([]uint32, error) {
	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	query := `
		SELECT u.Id
FROM users u
JOIN user_auth a ON a.UserId = u.Id
JOIN subscriptions s
  ON u.Id = s.UserId AND s.Notified = FALSE AND s.EndDate < CURRENT_DATE()
WHERE u.RoleId = 2 AND a.Disabled = 0;
	`
	rows, err := d.Conn().QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("ошибка при поиске пользователей без подписки: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var userIds []uint32
	for rows.Next() {
		var userId uint32
		if err := rows.Scan(&userId); err != nil {
			return nil, fmt.Errorf("ошибка сканирования userId: %w", err)
		}
		userIds = append(userIds, userId)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации по строкам: %w", err)
	}

	return userIds, nil
}

func (d *DB) SetUsersSubscriptionNotified(users []uint32) error {
	if len(users) == 0 {
		return nil // Нет пользователей для обновления
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	// Создаем строку с плейсхолдерами для IN (?, ?, ...)
	placeholders := strings.Repeat("?,", len(users))
	placeholders = placeholders[:len(placeholders)-1] // Удаляем последнюю запятую

	query := fmt.Sprintf("UPDATE subscriptions SET Notified = TRUE WHERE UserId IN (%s)", placeholders)

	// Преобразуем []uint32 в []any для передачи в ExecContext
	args := make([]any, len(users))
	for i, v := range users {
		args[i] = v
	}

	_, err := d.Conn().ExecContext(ctx, query, args...)
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return fmt.Errorf("тайм-аут (%d с) при обновлении статуса уведомления: %w", mode.GetSQLTimeToCancel(), err)
		case errors.Is(err, context.Canceled):
			return fmt.Errorf("операция отменена: %w", err)
		default:
			return fmt.Errorf("ошибка при обновлении статуса уведомления: %w", err)
		}
	}

	return nil
}
