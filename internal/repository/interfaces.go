// Package repository содержит интерфейсы репозиториев — абстракции
// слоя хранения данных, не зависящие от конкретной СУБД.
// Реализации находятся в internal/repository/mysql/.
package repository

import (
	"context"
	"encoding/json"

	"github.com/ikermy/air-common/v2/pkg/com"
	"github.com/ikermy/air-common/v2/pkg/comdb"
	"github.com/ikermy/air-common/v2/pkg/comdom"
	"golang.org/x/oauth2"
)

// ---------------------------------------------------------------------------
// Под-интерфейсы (Interface Segregation Principle)
// Охватывают методы, реализованные непосредственно в internal/repository/mysql.
// Методы из comdb.Exterior (ReadDialog, DeleteDialog, SetUserAPIKey и т.д.)
// покрываются через составной Repository ниже.
// ---------------------------------------------------------------------------

// AppConfig — хранилище настроек приложения (таблица app_config).
type AppConfig interface {
	SetAppConfig(ctx context.Context, key, value string) error
	GetAppConfig(ctx context.Context, key string) (string, error)
	GetAllAppConfig(ctx context.Context) (map[string]string, error)
}

type UserRepository interface {
	// reader — операции чтения данных пользователя.
	CheckEmail(email, emailHMAC string) (uint64, error)
	GetAuthData(email, emailHMAC string) (storedHash string, userId uint64, confirmed, disabled, isLegacy bool, err error)
	GetUserDetails(userId uint64) (json.RawMessage, error)
	GetUserEmail(userId uint64) (string, error)
	UserInfo(userID uint64) (json.RawMessage, error)
	CheckDemo(userId uint64) (bool, error)
	GetDevUserData(userId uint64) (json.RawMessage, error)
	UsersWithoutSubscription() ([]uint64, error)
	UserTimeZone(userID uint64) (string, error)
	UserLanguage(userID uint64) string
	CheckUserSubscription(provider com.SubscriptionProvider, userID uint64) error
	// GetUserSubscriptionLimites только для CheckUserSubscription
	GetUserSubscriptionLimites(userID uint64) (json.RawMessage, error)

	// writer — операции записи данных пользователя.
	CreateUser(name, pass, encEmail, emailHMAC, lang string, demo bool) (uint64, error)
	ConfirmMail(email, emailHMAC string) error
	UpdatePassword(email, emailHMAC string, newSHA string) error
	UpdateDevData(userId uint64, name, encEmail, emailHMAC, newHash string) error
	MigrateUserSecurity(userId uint64, newHash, encEmail, emailHMAC string) error
	DeleteAllUserData(userID uint64) error
	SaveUserTimeZone(userID uint64, timeZone string) error
	SetUsersSubscriptionNotified(users []uint64) error
	SaveUserLanguage(userID uint64, language string) error
}

// ModelRepository — операции с AI-моделями пользователей.
type ModelRepository interface {
	GetTypesGPT(provider comdom.ProviderType, modelType comdom.ModelType) (json.RawMessage, error)
	UpdateDevGPTModel(provider string, modId uint8) error
	DeleteFileFromUserGPT(userId uint64, fileID string) error
	AddFileFromUserGPT(userId uint64, fileID, fileName string) error
	GetOrSetTreadAndResponder(userID uint64, responderRealId uint64, responderName string, chatType comdom.ChannelType) (uint64, error)
	GetModelByProviderAnyStatus(userID uint64, provider comdom.ProviderType) (*comdom.UserModelRecord, error)
	FastCheckActiveUserModel(userID uint64) (bool, error)
}

// ChannelRepository — операции с каналами связи.
type ChannelRepository interface {
	SaveChannelData(userId uint64, channelType string, data string, enabled bool) error
	GetChannelsData(userId uint64) (json.RawMessage, error)
	DeleteChannelData(userId uint64, channelType string) error
	CheckActiveChannels(userId uint64) (bool, error)
	GetActiveChannels(userId uint64) ([]string, error)
	DisableAllUserChannel(userID uint64) error
}

// NotificationRepository — операции с уведомлениями.
type NotificationRepository interface {
	UpdateNotification(userId uint64, tip string, status bool, telegaId uint64) error
	GetNotificationsData(userId uint64) (json.RawMessage, error)
	SaveNotificationEvent(userId uint64, start, end, target bool) error
	DeleteNotificationsChannel(userId uint64, chanelName string) error
}

// DialogRepository — операции с диалогами.
type DialogRepository interface {
	GetUserDialogs(userId uint64) (json.RawMessage, error)
	ReadDialog(dialogId uint64, limit ...uint8) (json.RawMessage, error)
	DeleteDialog(userID uint64, dialogId uint64) error
}

// ServiceRepository — операции с подключёнными сервисами пользователя.
type ServiceRepository interface {
	ServiceList(userId uint64) ([]string, error)
	AddService(userId uint64, serviceType string) error
	DeleteService(userId uint64, serviceType string) error
}

// TOTPRepository — операции с двухфакторной аутентификацией (TOTP).
type TOTPRepository interface {
	SaveTOTPSecret(ctx context.Context, userId uint64, encSecret string) error
	GetTOTPData(ctx context.Context, userId uint64) (encSecret string, enabled bool, err error)
	ClearTOTPSecret(ctx context.Context, userId uint64) error
}

// MasterKeyRepository — операции с мастер-ключом пользователя.
type MasterKeyRepository interface {
	GetPasswordHash(userId uint64) (string, error)
	SaveMasterKey(userId uint64, encMK, wrapSalt string) error
	GetMasterKeyData(userId uint64) (encMK, wrapSalt string, hasMK bool, err error)
	ClearMasterKey(userId uint64) error
	DeleteEncryptedUserData(userId uint64) error
	// Шифрование API-ключей MasterKey'ом пользователя ($mk$)
	EncryptUserAPIKeysWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// Шифрование каналов связи MasterKey'ом пользователя ($mk$)
	EncryptChannelsWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptDialogsWSS шифрует все plaintext Data во всех диалогах пользователя.
	EncryptDialogsWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptGoogleTokenWSS шифрует Google OAuth токен пользователя.
	EncryptGoogleTokenWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptVectorEmbeddingsWSS шифрует doc_name и content в таблице
	EncryptVectorEmbeddingsWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptCRMConfigsWSS шифрует все поля в таблице crm_configs пользователя.
	EncryptCRMConfigsWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptCRMOAuthStatesWSS шифрует поля в таблице crm_oauth_states
	EncryptCRMOAuthStatesWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptUserStorageConfigWSS шифрует поля в таблице user_storage_config
	EncryptUserStorageConfigWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
	// EncryptUserStorageServicesBotDataWSS шифрует данные authData в таблицах хранения параметров ботов для service
	EncryptUserStorageServicesBotDataWSS(userId uint64, masterKey [32]byte, progressCallback func(string)) error
}

// OperatorRepository — операции с операторами живого чата.
type OperatorRepository interface {
	OperatorsList(ctx context.Context, userID uint64) (json.RawMessage, error)
	SaveOperators(ctx context.Context, userID uint64, operatorType string, data json.RawMessage) error
}

// GoogleRepository — операции с Google OAuth токенами (планировщик).
type GoogleRepository interface {
	GetUsersWithGoogleToken() ([]uint64, error)
	RefreshGoogleTokenIfNeeded(userID uint64, oauthConfig *oauth2.Config) error
	GetUserSubscriptionLimites(userID uint64) (json.RawMessage, error) // требуется com.CheckUserSubscription
	SaveGoogleToken(userID uint64, googleEmail string, token *oauth2.Token) error
	GetGoogleToken(userID uint64) (*oauth2.Token, string, error)
	DeleteGoogleToken(userID uint64) error
}

// ---------------------------------------------------------------------------
// Составной интерфейс
// ---------------------------------------------------------------------------

// Repository — единый интерфейс доступа к данным, объединяющий:
//   - comdb.Exterior  — внешний контракт (диалоги, модели, Google, API-ключи…)
//   - все предметные под-интерфейсы — методы, реализуемые internal/repository/mysql
//
// Реализуется *mysql.DB.
// Методы, присутствующие одновременно в comdb.Exterior и в под-интерфейсах
// (ReadUserModel, SaveUserModel, DisableAllUserChannel и др.), имеют одинаковые
// сигнатуры — Go корректно дедуплицирует их в составном интерфейсе.
type Repository interface {
	comdb.Exterior
	AppConfig
	UserRepository
	ModelRepository
	ChannelRepository
	NotificationRepository
	DialogRepository
	ServiceRepository
	TOTPRepository
	MasterKeyRepository
	OperatorRepository
	GoogleRepository
}
