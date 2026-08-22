// Package repository содержит интерфейсы репозиториев — абстракции
// слоя хранения данных, не зависящие от конкретной СУБД.
// Реализации находятся в internal/repository/mysql/.
package repository

import (
	"context"
	"encoding/json"

	"github.com/ikermy/air_common/pkg/com"
	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_common/pkg/model/commdom"
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
	CheckEmail(email, emailHMAC string) (uint32, error)
	GetAuthData(email, emailHMAC string) (storedHash string, userId uint32, confirmed, disabled, isLegacy bool, err error)
	GetUserDetails(userId uint32) (json.RawMessage, error)
	GetUserEmail(userId uint32) (string, error)
	UserInfo(userID uint32) (json.RawMessage, error)
	CheckDemo(userId uint32) (bool, error)
	GetDevUserData(userId uint32) (json.RawMessage, error)
	UsersWithoutSubscription() ([]uint32, error)
	UserTimeZone(userID uint32) (string, error)
	UserLanguage(userID uint32) string
	CheckUserSubscription(provider com.SubscriptionProvider, userID uint32) error
	// GetUserSubscriptionLimites только для CheckUserSubscription
	GetUserSubscriptionLimites(userID uint32) (json.RawMessage, error)

	// writer — операции записи данных пользователя.
	CreateUser(name, pass, encEmail, emailHMAC, lang string, demo bool) (uint32, error)
	ConfirmMail(email, emailHMAC string) error
	UpdatePassword(email, emailHMAC string, newSHA string) error
	UpdateDevData(userId uint32, name, encEmail, emailHMAC, newHash string) error
	MigrateUserSecurity(userId uint32, newHash, encEmail, emailHMAC string) error
	DeleteAllUserData(userID uint32) error
	SaveUserTimeZone(userID uint32, timeZone string) error
	SetUsersSubscriptionNotified(users []uint32) error
	GetOrSetUserStorageLimit(userID uint32, delta int64) (uint64, uint64, error)
	SaveUserLanguage(userID uint32, language string) error
}

// ModelRepository — операции с AI-моделями пользователей.
type ModelRepository interface {
	GetTypesGPT() (json.RawMessage, error)
	UpdateDevGPTModel(provider string, modId uint8) error
	DeleteFileFromUserGPT(userId uint32, fileID string) error
	AddFileFromUserGPT(userId uint32, fileID, fileName string) error
	GetOrSetTreadAndResponder(userID uint32, responderRealId uint64, responderName string, chatType comdb.ChatType) (uint64, error)
	GetModelByProviderAnyStatus(userID uint32, provider commdom.ProviderType) (*commdom.UserModelRecord, error)
	FastCheckActiveUserModel(userID uint32) (bool, error)
}

// ChannelRepository — операции с каналами связи.
type ChannelRepository interface {
	SaveChannelData(userId uint32, channelType string, data string, enabled bool) error
	GetChannelsData(userId uint32) (json.RawMessage, error)
	DeleteChannelData(userId uint32, channelType string) error
	CheckActiveChannels(userId uint32) (bool, error)
	GetActiveChannels(userId uint32) ([]string, error)
	DisableAllUserChannel(userID uint32) error
}

// NotificationRepository — операции с уведомлениями.
type NotificationRepository interface {
	UpdateNotification(userId uint32, tip string, status bool, telegaId uint64) error
	GetNotificationsData(userId uint32) (json.RawMessage, error)
	SaveNotificationEvent(userId uint32, start, end, target bool) error
	DeleteNotificationsChannel(userId uint32, chanelName string) error
}

// DialogRepository — операции с диалогами.
type DialogRepository interface {
	GetUserDialogs(userId uint32) (json.RawMessage, error)
	ReadDialog(dialogId uint64, limit ...uint8) (json.RawMessage, error)
	DeleteDialog(userID uint32, dialogId uint64) error
}

// ServiceRepository — операции с подключёнными сервисами пользователя.
type ServiceRepository interface {
	ServiceList(userId uint32) ([]string, error)
	AddService(userId uint32, serviceType string) error
	DeleteService(userId uint32, serviceType string) error
}

// TOTPRepository — операции с двухфакторной аутентификацией (TOTP).
type TOTPRepository interface {
	SaveTOTPSecret(ctx context.Context, userId uint32, encSecret string) error
	GetTOTPData(ctx context.Context, userId uint32) (encSecret string, enabled bool, err error)
	ClearTOTPSecret(ctx context.Context, userId uint32) error
}

// MasterKeyRepository — операции с мастер-ключом пользователя.
type MasterKeyRepository interface {
	GetPasswordHash(userId uint32) (string, error)
	SaveMasterKey(userId uint32, encMK, wrapSalt string) error
	GetMasterKeyData(userId uint32) (encMK, wrapSalt string, hasMK bool, err error)
	ClearMasterKey(userId uint32) error
	DeleteEncryptedUserData(userId uint32) error
	// Шифрование API-ключей MasterKey'ом пользователя ($mk$)
	EncryptUserAPIKeysWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// Шифрование каналов связи MasterKey'ом пользователя ($mk$)
	EncryptChannelsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptDialogsWSS шифрует все plaintext Data во всех диалогах пользователя.
	EncryptDialogsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptGoogleTokenWSS шифрует Google OAuth токен пользователя.
	EncryptGoogleTokenWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptVectorEmbeddingsWSS шифрует doc_name и content в таблице
	EncryptVectorEmbeddingsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptCRMConfigsWSS шифрует все поля в таблице crm_configs пользователя.
	EncryptCRMConfigsWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptCRMOAuthStatesWSS шифрует поля в таблице crm_oauth_states
	EncryptCRMOAuthStatesWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptUserStorageConfigWSS шифрует поля в таблице user_storage_config
	EncryptUserStorageConfigWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
	// EncryptUserStorageServicesBotDataWSS шифрует данные authData в таблицах хранения параметров ботов для service
	EncryptUserStorageServicesBotDataWSS(userId uint32, masterKey [32]byte, progressCallback func(string)) error
}

// OperatorRepository — операции с операторами живого чата.
type OperatorRepository interface {
	OperatorsList(ctx context.Context, userID uint32) (json.RawMessage, error)
	SaveOperators(ctx context.Context, userID uint32, operatorType string, data json.RawMessage) error
}

// GoogleRepository — операции с Google OAuth токенами (планировщик).
type GoogleRepository interface {
	GetUsersWithGoogleToken() ([]uint32, error)
	RefreshGoogleTokenIfNeeded(userID uint32, oauthConfig *oauth2.Config) error
	GetUserSubscriptionLimites(userID uint32) (json.RawMessage, error) // требуется com.CheckUserSubscription
	SaveGoogleToken(userID uint32, googleEmail string, token *oauth2.Token) error
	GetGoogleToken(userID uint32) (*oauth2.Token, string, error)
	DeleteGoogleToken(userID uint32) error
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
