package state

const (
	GrpcKeyMeta         = "x-service-key"
	NewUserStorageLimit = 104857600 // 100 Mb
	DefaultCurrency     = 0         // по умолчанию USDT
	TOTPName            = "MarusiaAI"
	MailSenderAlias     = "marusia@kermy.org"
)

// ─── Runtime-переменные приложения ────────────────────────────────────────────
var (
	// MasterKey — ключ для шифрования app_config и MasterKey в Redis.
	// Заполняется в main.go из APP_MASTER_KEY_FILE.
	// Если не задан — приложение не запустится (fatal).
	MasterKey = []byte("")

	// Languages supported
	validLang = map[string]struct{}{
		"ru": {},
		"en": {},
		"es": {},
	}
)

// Redis — параметры подключения (заполняются в main.go из env).
type Redis struct {
	RedisAddr     string // REDIS_ADDR (default: "" — Redis отключён)
	RedisPassword string // REDIS_PASSWORD
	RedisDB       int    // REDIS_DB (default: 0)
}

func ValidateLanguage(language string) bool {
	_, ok := validLang[language]
	return ok
}
