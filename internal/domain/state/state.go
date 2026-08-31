package state

import (
	"sync"
)

const (
	GrpcKeyMeta         = "x-service-key"
	NewUserStorageLimit = 104857600 // 100 Mb
	DefaultCurrency     = 0         // по умолчанию USDT
	TOTPName            = "MarusiaAI"
	MailSenderName
)

// ─── Runtime-переменные приложения ────────────────────────────────────────────
var (
	Exit     = make(chan struct{}) // Канал завершения работы приложения
	UsersDB  = make(chan struct{}) // Канал уведомления о завершении операций с БД
	exitOnce sync.Once             // Защита от множественного закрытия канала Exit

	// MasterKey — ключ для шифрования app_config и MasterKey в Redis.
	// Заполняется в main.go из APP_MASTER_KEY_FILE.
	// Если не задан — приложение не запустится (fatal).
	MasterKey = []byte("")

	// Redis — параметры подключения (заполняются в main.go из env).
	RedisAddr     string // REDIS_ADDR (default: "" — Redis отключён)
	RedisPassword string // REDIS_PASSWORD
	RedisDB       int    // REDIS_DB (default: 0)

	// Languages supported
	validLang = map[string]struct{}{
		"ru": {},
		"en": {},
		"es": {},
	}
)

func ValidateLanguage(language string) bool {
	_, ok := validLang[language]
	return ok
}

// CloseExit безопасно закрывает канал Exit (защита от panic при повторном close).
func CloseExit() {
	exitOnce.Do(func() {
		close(Exit)
	})
}
