package masterkeyuc

import (
	"air_orchestrator/internal/domain/repository"
	"air_orchestrator/internal/domain/service"
	"fmt"
)

type Store interface {
	repository.MasterKeyRepository
}

type MasterKeyUseCase struct {
	store Store
	exam  service.SecurityService
}

func New(store Store, exam service.SecurityService) *MasterKeyUseCase {
	return &MasterKeyUseCase{
		store: store,
		exam:  exam,
	}
}

// CreateMasterKey создает MasterKey для пользователя.
func (uc *MasterKeyUseCase) CreateMasterKey(userID uint32, respID uint64, encPass string, progress func(string)) (string, error) {
	// 1. Проверяем, не существует ли уже MasterKey
	_, _, hasMK, err := uc.store.GetMasterKeyData(userID)
	if err != nil {
		return "", fmt.Errorf("ошибка проверки существования MasterKey: %w", err)
	}
	if hasMK {
		return "", fmt.Errorf("MASTER_KEY_ALREADY_EXISTS")
	}

	// 2. Получаем хеш пароля для верификации
	hash, err := uc.store.GetPasswordHash(userID)
	if err != nil {
		return "", fmt.Errorf("ошибка получения хеша пароля: %w", err)
	}

	// 3. Верифицируем пароль
	pass, err := uc.exam.DecryptPassword(respID, encPass)
	if err != nil {
		return "", fmt.Errorf("ошибка расшифровки пароля: %w", err)
	}
	if !uc.exam.VerifyPassword(hash, pass) {
		return "", fmt.Errorf("INVALID_PASSWORD")
	}

	// 4. Генерируем новый MasterKey
	rawB64, wrapB64, wrapSalt, err := uc.exam.GenerateAndWrapMasterKey(userID, pass)
	if err != nil {
		return "", fmt.Errorf("ошибка генерации MasterKey: %w", err)
	}

	// Нужен []byte/array мастер-ключа для шифрования данных
	// GenerateAndWrapMasterKey не возвращает [32]byte mk напрямую в этой версии интерфейса
	// но мы можем получить его из кэша после LoadMasterKey или использовать вспомогательную функцию
	// На самом деле, лучше бы GenerateAndWrapMasterKey возвращал mk.
	// Судя по интерфейсу, мы можем загрузить его:
	if err = uc.exam.LoadMasterKey(userID, pass, wrapB64, wrapSalt); err != nil {
		return "", fmt.Errorf("ошибка загрузки MasterKey в кэш: %w", err)
	}

	mk, ok := uc.exam.GetMasterKey(userID)
	if !ok {
		return "", fmt.Errorf("ошибка получения MasterKey из кэша")
	}

	// 5. Сохраняем MasterKey в БД
	if err = uc.store.SaveMasterKey(userID, wrapB64, wrapSalt); err != nil {
		return "", fmt.Errorf("ошибка сохранения MasterKey: %w", err)
	}

	// 6. Шифруем все существующие данные пользователя этим MasterKey
	if err = uc.store.EncryptUserAPIKeysWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования API-ключей: %w", err)
	}
	if err = uc.store.EncryptChannelsWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования каналов: %w", err)
	}
	if err = uc.store.EncryptDialogsWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования диалогов: %w", err)
	}
	if err = uc.store.EncryptGoogleTokenWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования Google токена: %w", err)
	}
	if err = uc.store.EncryptVectorEmbeddingsWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования векторных документов: %w", err)
	}
	if err = uc.store.EncryptCRMConfigsWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования CRM конфигов: %w", err)
	}
	if err = uc.store.EncryptCRMOAuthStatesWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования CRM OAuth состояний: %w", err)
	}
	if err = uc.store.EncryptUserStorageConfigWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования конфигов хранилища: %w", err)
	}
	if err = uc.store.EncryptUserStorageServicesBotDataWSS(userID, mk, progress); err != nil {
		return "", fmt.Errorf("ошибка шифрования конфигов дотов в service: %w", err)
	}

	return rawB64, nil
}

// VerifyPassword проверяет пароль пользователя.
func (uc *MasterKeyUseCase) VerifyPassword(userID uint32, respID uint64, encPass string) error {
	hash, err := uc.store.GetPasswordHash(userID)
	if err != nil {
		return fmt.Errorf("ошибка получения хеша пароля: %w", err)
	}

	pass, err := uc.exam.DecryptPassword(respID, encPass)
	if err != nil {
		return fmt.Errorf("ошибка расшифровки пароля: %w", err)
	}

	if !uc.exam.VerifyPassword(hash, pass) {
		return fmt.Errorf("INVALID_PASSWORD")
	}

	return nil
}

// RewrapOrReset перешифровывает MasterKey или сбрасывает его.
func (uc *MasterKeyUseCase) RewrapOrReset(userID uint32, respID uint64, rawMKB64 string, newPass string) error {
	// Если rawMKB64 пустой — значит MasterKey утрачен, сбрасываем всё
	if rawMKB64 == "" {
		if err := uc.store.ClearMasterKey(userID); err != nil {
			return fmt.Errorf("ошибка сброса MasterKey: %w", err)
		}
		if err := uc.store.DeleteEncryptedUserData(userID); err != nil {
			return fmt.Errorf("ошибка удаления зашифрованных данных: %w", err)
		}
		return nil
	}

	// Иначе — перешифровываем MasterKey
	// Нужно расшифровать новый пароль
	pass, err := uc.exam.DecryptPassword(respID, newPass)
	if err != nil {
		return fmt.Errorf("ошибка расшифровки нового пароля: %w", err)
	}

	wrapB64, wrapSalt, err := uc.exam.WrapMasterKey(rawMKB64, pass)
	if err != nil {
		return fmt.Errorf("ошибка перешифрования MasterKey: %w", err)
	}

	if err := uc.store.SaveMasterKey(userID, wrapB64, wrapSalt); err != nil {
		return fmt.Errorf("ошибка сохранения нового MasterKey: %w", err)
	}

	return nil
}

func (uc *MasterKeyUseCase) GetMasterKeyData(userID uint32) (string, string, bool, error) {
	return uc.store.GetMasterKeyData(userID)
}
