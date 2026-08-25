package app

import (
	db "air_orchestrator/internal/repository/mysql"
	"context"
	"fmt"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// RunAppConfigRekey запускает one-shot перекодирование sensitive значений app_config
// со старого APP_MASTER_KEY на NEW_APP_MASTER_KEY и завершает работу без старта HTTP сервера.
func RunAppConfigRekey(parent context.Context) error {
	if err := db.ValidateAppConfigRekeyConfig(); err != nil {
		return fmt.Errorf("валидация APP_CONFIG_REKEY окружения: %w", err)
	}

	d, err := db.New(parent)
	if err != nil {
		return fmt.Errorf("инициализация БД: %w", err)
	}
	defer func() {
		if closeErr := d.Close(); closeErr != nil {
			logger.Warn("rekey: ошибка закрытия БД: %v", closeErr)
		}
	}()

	result, err := d.RekeyAppConfigSensitiveValues()
	if err != nil {
		return err
	}
	logger.Info("App: перекодирование sensitive app_config завершено успешно, dry_run=%v, ключей перекодировано: %d, ключи: %v",
		result.DryRun, result.Count, result.Keys)
	return nil
}
