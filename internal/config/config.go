// Package config содержит константы и параметры конфигурации,
// доступные всем внутренним слоям без нарушения правила зависимостей.
// Это нейтральный пакет — он не импортирует ни один внутренний пакет проекта.
package config

import (
	"os"
	"time"
)

// MCPSessionSecret — общий секрет для HMAC-подписи X-Session-ID.
func MCPSessionSecret() string { return os.Getenv("MCP_SESSION_SECRET") }

// MCPRequireSignedSession — запрещать legacy X-Session-ID "userId:provider".
func MCPRequireSignedSession() bool { return os.Getenv("MCP_REQUIRE_SIGNED_SESSION") == "true" }

// MCPServiceKey — общий ключ для заголовка X-Service-Key (пусто = проверка выключена).
func MCPServiceKey() string { return os.Getenv("MCP_SERVICE_KEY") }

// MTLSRequired — требовать проверенный клиентский сертификат на /int/*.
func MTLSRequired() bool { return os.Getenv("MTLS_REQUIRED") == "true" }

// TLSCertFile / TLSKeyFile — серверный сертификат и ключ (пусто = без TLS).
func TLSCertFile() string { return os.Getenv("TLS_CERT_FILE") }
func TLSKeyFile() string  { return os.Getenv("TLS_KEY_FILE") }

// TLSCACertFile — CA для проверки клиентских сертификатов (mTLS).
func TLSCACertFile() string { return os.Getenv("TLS_CA_CERT_FILE") }

// ShardStrict — завершать старт при недоступности шардов/неверном конфиге.
func ShardStrict() bool { return os.Getenv("SHARD_STRICT") == "true" }

// UserDeleteSoft — удалять пользователя мягко (status='deleted') вместо
// немедленного hard-delete. По умолчанию false для обратной совместимости.
func UserDeleteSoft() bool { return os.Getenv("USER_DELETE_SOFT") == "true" }

// UserDeleteGrace — сколько времени soft-deleted пользователь хранится до purge.
func UserDeleteGrace() time.Duration {
	if raw := os.Getenv("USER_DELETE_GRACE"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d >= 0 {
			return d
		}
	}
	return 24 * time.Hour
}

// UserPurgeInterval — период фонового purge soft-deleted пользователей.
func UserPurgeInterval() time.Duration {
	if raw := os.Getenv("USER_PURGE_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return time.Hour
}

// DefaultUserMigrationDeadline — крайний срок миграции пользователей на
// bcrypt+AES/EmailHash. После него все немигрировавшие пользователи и их
// данные удаляются из БД, а lazy-миграция прекращается.
const DefaultUserMigrationDeadline = "2026-12-31T23:59:59Z"

// UserMigrationDeadline — момент окончания миграции пользователей.
// Значение задаётся в RFC3339 (например, 2026-12-31T23:59:59Z).
func UserMigrationDeadline() time.Time {
	raw := os.Getenv("USER_MIGRATION_DEADLINE")
	if raw == "" {
		raw = DefaultUserMigrationDeadline
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, DefaultUserMigrationDeadline)
	return t
}

// UserMigrationFinalize — включить финальную стадию миграции пользователей
// (purge немигрировавших + финализация EmailHash NOT NULL). По умолчанию
// включена: до наступления USER_MIGRATION_DEADLINE воркер ничего не делает.
func UserMigrationFinalize() bool {
	raw := os.Getenv("USER_MIGRATION_FINALIZE")
	return raw == "" || raw == "true"
}

// UserMigrationCheckInterval — как часто проверять наступление deadline.
func UserMigrationCheckInterval() time.Duration {
	if d := parsePositiveDuration(os.Getenv("USER_MIGRATION_CHECK_INTERVAL")); d > 0 {
		return d
	}
	return time.Hour
}

func parsePositiveDuration(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0
	}
	return d
}

// Retention / чистка исторических таблиц.
// По умолчанию выключена: удаление истории включается явно RETENTION_ENABLED=true.
func RetentionEnabled() bool { return os.Getenv("RETENTION_ENABLED") == "true" }

func RetentionInterval() time.Duration {
	if d := parsePositiveDuration(os.Getenv("RETENTION_INTERVAL")); d > 0 {
		return d
	}
	return 6 * time.Hour
}

func UserBotEventsRetention() time.Duration {
	if d := parsePositiveDuration(os.Getenv("USER_BOT_EVENTS_RETENTION")); d > 0 {
		return d
	}
	return 90 * 24 * time.Hour
}

func AuditLogRetention() time.Duration {
	if d := parsePositiveDuration(os.Getenv("AUDIT_LOG_RETENTION")); d > 0 {
		return d
	}
	return 365 * 24 * time.Hour
}

func WebhookDedupRetention() time.Duration {
	if d := parsePositiveDuration(os.Getenv("WEBHOOK_DEDUP_RETENTION")); d > 0 {
		return d
	}
	return 7 * 24 * time.Hour
}

// ─── Токены и TTL ─────────────────────────────────────────────────────────────

const (
	// AccessTokenTTL — время жизни access-токена.
	AccessTokenTTL = 30 * time.Minute
	// RefreshTokenTTL — время жизни refresh-токена.
	RefreshTokenTTL = 7 * 24 * time.Hour
	// RegKeyTTL — срок жизни ключа регистрации (секунды).
	RegKeyTTL = 30
	// MailTokenTTL — срок жизни токена подтверждения email (часы).
	MailTokenTTL = 24
	// MasterKeyRedisTTL — время жизни MasterKey в Redis (часы).
	// После истечения TTL ключ автоматически удаляется из Redis.
	// При каждом логине пользователя TTL обновляется.
	MasterKeyRedisTTL = 24 * time.Hour
)

// ─── HTTP / RPC таймауты ───────────────────────────────────────────────────────

const (
	// RequestTimeout — таймаут исходящих HTTP/RPC запросов.
	RequestTimeout = 10 * time.Second
)

// ─── Rate Limiting ─────────────────────────────────────────────────────────────

const (
	// RateLimit — максимальное число запросов в секунду на один respId.
	RateLimit = 5
	// RateBurst — максимальный «всплеск» сверх лимита.
	RateBurst = 10
	// MCPToolRateLimit — максимальное число вызовов одного MCP-инструмента
	// в секунду на пользователя.
	MCPToolRateLimit = 5
	// MCPToolRateBurst — допустимый «всплеск» для MCP-инструмента.
	MCPToolRateBurst = 10
	// MCPToolLimiterTTL — время, после которого неиспользуемый лимитер удаляется.
	MCPToolLimiterTTL = 10 * time.Minute
	// MCPToolTimeout — максимальное время выполнения MCP-инструмента.
	MCPToolTimeout = 30 * time.Second
	// MCPMusicToolTimeout — увеличенный таймаут для генерации музыки.
	MCPMusicToolTimeout = 120 * time.Second
	// MCPIdempotencyTTL — время хранения результата по idempotency_key.
	MCPIdempotencyTTL = 10 * time.Minute
)

// ─── gRPC ─────────────────────────────────────────────────────────────────────

const (
	// GrpcPort — порт gRPC сервера по умолчанию.
	// Может быть переопределён через переменную окружения GRPC_PORT.
	GrpcPort = "50051"
)

// ─── Внешние сервисы ──────────────────────────────────────────────────────────

const (
	// RPC для вызова функции позвонить
	WhatsAppRPC = "whatsbot:9090"
	TelegramRPC = "tguserbot:9090"
	///////////////////////////////////
	CrmServiceURL = "http://crm:8080"
	TgBotURL      = "http://tgbot:8080"
	TgUserBotURL  = "http://tguserbot:8080"
	WhatsBotURL   = "http://whatsbot:8080"
	AvitoBotURL   = "http://avitobot:8080"
	WidgetBotURL  = "http://widget:8080"
	InstaBotURL   = "http://insta:8080"
	CRMURL        = "http://crm:8080"
	OPERURL       = "http://oper:8080"
	PAYURL        = "http://pay:8080"
	// VicLogsURL — URL Loki API для чтения логов.
	VicLogsURL = "http://victorialogs:9428"
	// LeadServiceURL — URL сервиса лидов.
	LeadServiceURL = "http://hunter:8080"
	// LeadServiceWS — WebSocket URL сервиса лидов.
	LeadServiceWS = "ws://hunter:8080"
)
