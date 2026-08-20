// Package auth содержит use case аутентификации и регистрации пользователей.
// Бизнес-логика вынесена из delivery-слоя в соответствии с Clean Architecture.
package auth

import (
	"air_orchestrator/internal/domain/repository"
	"air_orchestrator/internal/domain/service"
	"context"
	"fmt"
)

// Store — минимальный интерфейс репозитория для AuthUseCase (ISP).
type Store interface {
	repository.UserRepository
	repository.MasterKeyRepository
	repository.TOTPRepository
}

// AuthUseCase содержит бизнес-логику аутентификации и регистрации.
type AuthUseCase struct {
	store  Store
	exam   service.SecurityService
	mailer service.MailerService
}

// New создаёт новый экземпляр AuthUseCase.
func New(store Store, exam service.SecurityService, mailer service.MailerService) *AuthUseCase {
	return &AuthUseCase{store: store, exam: exam, mailer: mailer}
}

// ─── Регистрация ──────────────────────────────────────────────────────────────

// RegisterInput — входные данные для регистрации нового пользователя.
type RegisterInput struct {
	Name     string
	Mail     string
	Password string // plaintext-пароль (уже расшифрованный на уровне delivery)
	Demo     bool
	Language string
}

// RegisterResult — результат успешной регистрации.
type RegisterResult struct {
	UserID       uint32
	ConfirmToken string // токен для письма подтверждения email
}

// CheckEmail проверяет занятость email и возвращает userID, если он существует.
// Если пользователь не найден, возвращает временный регистрационный ключ.
func (uc *AuthUseCase) CheckEmail(mail string) (uint32, string, error) {
	emailHMAC := uc.exam.EmailHMAC(mail)
	userID, err := uc.store.CheckEmail(mail, emailHMAC)
	if err != nil {
		return 0, "", fmt.Errorf("ошибка проверки email: %w", err)
	}

	return userID, "", nil
}

// Register регистрирует нового пользователя.
// Хеширует пароль, шифрует email, создаёт запись в БД и генерирует токен подтверждения.
// Отправку письма выполняет вызывающая сторона (асинхронно).
func (uc *AuthUseCase) Register(input RegisterInput) (*RegisterResult, error) {
	hashedPass, err := uc.exam.HashPassword(input.Password)
	if err != nil {
		return nil, fmt.Errorf("ошибка хеширования пароля: %w", err)
	}

	encEmail, err := uc.exam.EncryptEmail(input.Mail)
	if err != nil {
		return nil, fmt.Errorf("ошибка шифрования email: %w", err)
	}

	emailHMAC := uc.exam.EmailHMAC(input.Mail)
	userID, err := uc.store.CreateUser(input.Name, hashedPass, encEmail, emailHMAC, input.Language, input.Demo)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания пользователя: %w", err)
	}

	token, err := uc.exam.GetMailConfirmationToken(userID, input.Mail)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации токена подтверждения: %w", err)
	}

	return &RegisterResult{UserID: userID, ConfirmToken: token}, nil
}

// ─── Аутентификация ───────────────────────────────────────────────────────────

// LoginInput — входные данные для входа в систему.
type LoginInput struct {
	Mail     string
	Password string // plaintext-пароль (уже расшифрованный на уровне delivery)
	RespID   uint64
}

// LoginResult — результат успешной аутентификации.
type LoginResult struct {
	UserID      uint32
	Confirmed   int
	Disabled    int
	IsLegacy    bool
	TOTPEnabled bool
	Password    string // нужен для последующей загрузки MasterKey
}

// Authenticate проверяет учётные данные и возвращает результат аутентификации.
// Не генерирует токены — это задача delivery-слоя.
func (uc *AuthUseCase) Authenticate(ctx context.Context, input LoginInput) (*LoginResult, error) {
	emailHMAC := uc.exam.EmailHMAC(input.Mail)
	storedHash, userID, confirmed, disabled, isLegacy, err := uc.store.GetAuthData(input.Mail, emailHMAC)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения данных авторизации: %w", err)
	}

	if userID == 0 {
		return nil, fmt.Errorf("пользователь не найден")
	}

	if !uc.exam.VerifyPassword(storedHash, input.Password) {
		return nil, fmt.Errorf("неверный пароль")
	}

	_, totpEnabled, totpErr := uc.store.GetTOTPData(ctx, userID)
	if totpErr != nil {
		return nil, fmt.Errorf("ошибка проверки TOTP: %w", totpErr)
	}

	return &LoginResult{
		UserID:      userID,
		Confirmed:   confirmed,
		Disabled:    disabled,
		IsLegacy:    isLegacy,
		TOTPEnabled: totpEnabled,
		Password:    input.Password,
	}, nil
}

// MigrateLegacyUser выполняет тихую миграцию legacy-хэша (SHA3-256 → bcrypt).
// Вызывается асинхронно из delivery-слоя.
func (uc *AuthUseCase) MigrateLegacyUser(userID uint32, password, mail string) error {
	newHash, err := uc.exam.HashPassword(password)
	if err != nil {
		return fmt.Errorf("ошибка хеширования: %w", err)
	}
	encEmail, err := uc.exam.EncryptEmail(mail)
	if err != nil {
		return fmt.Errorf("ошибка шифрования email: %w", err)
	}
	return uc.store.MigrateUserSecurity(userID, newHash, encEmail, uc.exam.EmailHMAC(mail))
}

// LoadMasterKeyIfExists загружает MasterKey пользователя в RAM-кэш.
// Возвращает true, если MasterKey существует.
func (uc *AuthUseCase) LoadMasterKeyIfExists(userID uint32, password string) (bool, error) {
	encMK, wrapSalt, hasMK, err := uc.store.GetMasterKeyData(userID)
	if err != nil {
		return false, fmt.Errorf("ошибка получения MasterKey: %w", err)
	}
	if !hasMK {
		return false, nil
	}
	if loadErr := uc.exam.LoadMasterKey(userID, password, encMK, wrapSalt); loadErr != nil {
		return true, fmt.Errorf("ошибка загрузки MasterKey в кэш: %w", loadErr)
	}
	return true, nil
}

// ─── Сброс пароля ─────────────────────────────────────────────────────────────

// RequestPasswordReset генерирует токен для сброса пароля.
// Если пользователь не найден — возвращает ("", false, nil).
// Не отправляет письмо — это делает delivery-слой.
func (uc *AuthUseCase) RequestPasswordReset(mail string) (token string, found bool, err error) {
	emailHMAC := uc.exam.EmailHMAC(mail)
	userID, err := uc.store.CheckEmail(mail, emailHMAC)
	if err != nil || userID == 0 {
		return "", false, nil // пользователь не найден — не раскрываем информацию
	}

	token, err = uc.exam.GetMailConfirmationToken(userID, mail)
	if err != nil {
		return "", true, fmt.Errorf("ошибка генерации токена: %w", err)
	}
	return token, true, nil
}

// ResetPasswordInput — входные данные для сброса пароля.
type ResetPasswordInput struct {
	Mail         string
	Password     string // plaintext новый пароль
	RawMasterKey string // опционально: raw base64 MasterKey для re-wrap
}

// ResetPassword устанавливает новый пароль пользователю.
// При наличии RawMasterKey — перешифровывает MasterKey с новым паролем.
// При отсутствии и наличии существующего MasterKey — очищает зашифрованные данные.
func (uc *AuthUseCase) ResetPassword(userID uint32, input ResetPasswordInput) error {
	hashedPass, err := uc.exam.HashPassword(input.Password)
	if err != nil {
		return fmt.Errorf("ошибка хеширования пароля: %w", err)
	}

	emailHMAC := uc.exam.EmailHMAC(input.Mail)
	if err := uc.store.UpdatePassword(input.Mail, emailHMAC, hashedPass); err != nil {
		return fmt.Errorf("ошибка обновления пароля: %w", err)
	}

	if input.RawMasterKey != "" {
		encMK, wrapSalt, wrapErr := uc.exam.WrapMasterKey(input.RawMasterKey, input.Password)
		if wrapErr != nil {
			return fmt.Errorf("ошибка перешифровки MasterKey: %w", wrapErr)
		}
		if saveErr := uc.store.SaveMasterKey(userID, encMK, wrapSalt); saveErr != nil {
			return fmt.Errorf("ошибка сохранения MasterKey: %w", saveErr)
		}
		// Загружаем в кэш немедленно
		_ = uc.exam.LoadMasterKey(userID, input.Password, encMK, wrapSalt)
		return nil
	}

	// RawMasterKey не передан — проверяем наличие MasterKey
	_, _, hasMK, mkErr := uc.store.GetMasterKeyData(userID)
	if mkErr != nil {
		return fmt.Errorf("ошибка проверки MasterKey: %w", mkErr)
	}
	if hasMK {
		// MasterKey есть, но ключ утерян — удаляем зашифрованные данные
		if delErr := uc.store.DeleteEncryptedUserData(userID); delErr != nil {
			return fmt.Errorf("ошибка удаления зашифрованных данных: %w", delErr)
		}
		if clearErr := uc.store.ClearMasterKey(userID); clearErr != nil {
			return fmt.Errorf("ошибка очистки MasterKey: %w", clearErr)
		}
	}
	return nil
}

// ─── Подтверждение email ──────────────────────────────────────────────────────

// ConfirmEmail подтверждает email по токену из письма.
// Возвращает userID и email для дальнейших действий (S3-директория, уведомление).
func (uc *AuthUseCase) ConfirmEmail(tokenString string) (userID uint32, email string, err error) {
	userID, email, err = uc.exam.ParseMailConfirmationToken(tokenString)
	if err != nil {
		return 0, "", fmt.Errorf("недействительный токен: %w", err)
	}
	emailHMAC := uc.exam.EmailHMAC(email)
	if err = uc.store.ConfirmMail(email, emailHMAC); err != nil {
		return 0, "", fmt.Errorf("ошибка подтверждения email: %w", err)
	}
	return userID, email, nil
}
