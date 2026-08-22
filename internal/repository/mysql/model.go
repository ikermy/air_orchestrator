package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/ikermy/air_common/pkg/mode"
)

func (d *DB) FastCheckActiveUserModel(userID uint32) (bool, error) {
	if userID == 0 {
		return false, fmt.Errorf("неверный userID")
	}

	ctx, cancel := context.WithTimeout(d.Context(), mode.GetSQLTimeToCancel())
	defer cancel()

	query := `
	SELECT EXISTS(
    SELECT 1 
    FROM user_models 
    WHERE UserId = ? AND IsActive = 1
	) AS HasActive`

	var hay bool
	scanErr := d.Conn().QueryRowContext(ctx, query, userID).Scan(&hay)
	if scanErr != nil {
		switch {
		case errors.Is(scanErr, context.DeadlineExceeded):
			return false, fmt.Errorf("тайм-аут (%d с) при получении активной модели: %w", mode.GetSQLTimeToCancel(), scanErr)
		case errors.Is(scanErr, context.Canceled):
			return false, fmt.Errorf("операция отменена: %w", scanErr)
		default:
			return false, fmt.Errorf("ошибка получения активной модели: %w", scanErr)
		}
	}

	return hay, nil
}
