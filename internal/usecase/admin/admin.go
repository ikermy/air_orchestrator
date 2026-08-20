package admin

import (
	"air_orchestrator/internal/domain/repository"
	"context"
	"fmt"
)

// Store — интерфейс репозитория для AdminUseCase.
type Store interface {
	repository.AppConfig
	repository.UserRepository
}

// AdminUseCase содержит логику административных операций и настройки системы.
type AdminUseCase struct {
	store Store
}

// New создает новый экземпляр AdminUseCase.
func New(store Store) *AdminUseCase {
	return &AdminUseCase{store: store}
}

// IsDevUser проверяет, является ли пользователь разработчиком.
func (uc *AdminUseCase) IsDevUser(userID uint32) (bool, error) {
	data, err := uc.store.GetDevUserData(userID)
	if err != nil {
		return false, fmt.Errorf("ошибка проверки прав разработчика: %w", err)
	}
	return len(data) > 0, nil
}

// GetConfigs возвращает значения нескольких ключей конфигурации.
func (uc *AdminUseCase) GetConfigs(ctx context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, key := range keys {
		val, err := uc.store.GetAppConfig(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("ошибка получения конфига %s: %w", key, err)
		}
		result[key] = val
	}
	return result, nil
}

// SetConfigs сохраняет несколько ключей конфигурации.
func (uc *AdminUseCase) SetConfigs(ctx context.Context, kvs map[string]string) error {
	for k, v := range kvs {
		if err := uc.store.SetAppConfig(ctx, k, v); err != nil {
			return fmt.Errorf("ошибка сохранения конфига %s: %w", k, err)
		}
	}
	return nil
}

// GetAllConfigs возвращает все ключи конфигурации.
func (uc *AdminUseCase) GetAllConfigs(ctx context.Context) (map[string]string, error) {
	return uc.store.GetAllAppConfig(ctx)
}

// ResetSessionKeys сбрасывает ключи сессии.
func (uc *AdminUseCase) ResetSessionKeys(ctx context.Context) error {
	if err := uc.store.SetAppConfig(ctx, "auth.session", ""); err != nil {
		return err
	}
	return uc.store.SetAppConfig(ctx, "auth.created", "")
}
