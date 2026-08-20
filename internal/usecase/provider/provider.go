package provider

import (
	"air_orchestrator/internal/domain/repository"
	"air_orchestrator/internal/domain/service"
	"fmt"

	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_common/pkg/model/commdom"
)

// Store — интерфейс репозитория для ProviderUseCase (ISP).
type Store interface {
	repository.MasterKeyRepository
	GetActiveProvider(userID uint32) (commdom.ProviderType, error)
	SetUserAPIKey(userID uint32, provider commdom.ProviderType, key string) error
}

// ProviderUseCase содержит бизнес-логику управления провайдерами и API-ключами.
type ProviderUseCase struct {
	store Store
	mod   *model.Router
	exam  service.SecurityService
}

// New создаёт новый экземпляр ProviderUseCase.
func New(store Store, mod *model.Router, exam service.SecurityService) *ProviderUseCase {
	return &ProviderUseCase{
		store: store,
		mod:   mod,
		exam:  exam,
	}
}

// RevokeUserAPIKey отзывает API-ключ провайдера.
// Возвращает needRestart=true, если отозванный провайдер был активным.
func (uc *ProviderUseCase) RevokeUserAPIKey(userID uint32, provider commdom.ProviderType) (bool, error) {
	activeProv, _ := uc.store.GetActiveProvider(userID)

	err := uc.mod.RevokeUserAPIKey(userID, provider)
	if err != nil {
		return false, fmt.Errorf("ошибка при отзыве API-ключа: %w", err)
	}

	needRestart := activeProv != 0 && activeProv == provider
	return needRestart, nil
}

// SetUserAPIKey устанавливает API-ключ провайдера.
// Выполняет проверки состояния MasterKey перед сохранением.
func (uc *ProviderUseCase) SetUserAPIKey(userID uint32, provider commdom.ProviderType, apiKey string) (bool, error) {
	// Проверяем наличие MasterKey в кэше.
	_, masterKeyInCache := uc.exam.GetMasterKey(userID)
	if !masterKeyInCache {
		_, _, hasMK, mkErr := uc.store.GetMasterKeyData(userID)
		if mkErr != nil {
			return false, fmt.Errorf("ошибка проверки MasterKey: %w", mkErr)
		}
		if hasMK {
			return false, fmt.Errorf("MASTER_KEY_REQUIRED")
		}
	}

	activeProv, _ := uc.store.GetActiveProvider(userID)

	err := uc.store.SetUserAPIKey(userID, provider, apiKey)
	if err != nil {
		return false, fmt.Errorf("ошибка при сохранении API-ключа: %w", err)
	}

	needRestart := activeProv != 0 && activeProv == provider
	return needRestart, nil
}
