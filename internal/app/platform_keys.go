package app

import (
	"os"
	"strings"

	"github.com/ikermy/air-common/v2/pkg/comdom"
	"github.com/ikermy/air-common/v2/pkg/model"
)

// platformKeyDB подставляет платформенный ключ провайдера, если у пользователя
// нет собственного ключа. Это осознанное решение: данные уходят провайдеру
// как есть (TLS), что описано в карточке приложения/сервиса.
type platformKeyDB struct {
	model.DB
	keys map[comdom.ProviderType]string
}

func (p *platformKeyDB) GetUserAPIKey(userID uint64, provider comdom.ProviderType) (string, error) {
	key, err := p.DB.GetUserAPIKey(userID, provider)
	if strings.TrimSpace(key) != "" {
		return key, nil
	}
	if fallback := strings.TrimSpace(p.keys[provider]); fallback != "" {
		return fallback, nil
	}
	return key, err
}

// platformKeysFromEnv читает ключи из AIR_PLATFORM_KEY_<PROVIDER>.
func platformKeysFromEnv() map[comdom.ProviderType]string {
	out := make(map[comdom.ProviderType]string)
	add := func(provider comdom.ProviderType, env string) {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			out[provider] = v
		}
	}
	add(comdom.ProviderOpenAI, "AIR_PLATFORM_KEY_OPENAI")
	add(comdom.ProviderMistral, "AIR_PLATFORM_KEY_MISTRAL")
	add(comdom.ProviderGoogle, "AIR_PLATFORM_KEY_GOOGLE")
	add(comdom.ProviderElevenLabs, "AIR_PLATFORM_KEY_ELEVENLABS")
	return out
}

// withPlatformKeys оборачивает DB, если задан хотя бы один платформенный ключ.
func withPlatformKeys(db model.DB) model.DB {
	keys := platformKeysFromEnv()
	if len(keys) == 0 {
		return db
	}
	return &platformKeyDB{DB: db, keys: keys}
}
