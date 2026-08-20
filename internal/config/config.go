// Package config содержит константы и параметры конфигурации,
// доступные всем внутренним слоям без нарушения правила зависимостей.
// Это нейтральный пакет — он не импортирует ни один внутренний пакет проекта.
package config

import (
	"time"
)

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
	// LokiURL — URL Loki API для чтения логов.
	LokiURL = "http://air_loki:3100"
	// LeadServiceURL — URL сервиса лидов.
	LeadServiceURL = "http://hunter:8080"
	// LeadServiceWS — WebSocket URL сервиса лидов.
	LeadServiceWS = "ws://hunter:8080"
)
