// Package web — delivery-слой HTTP сервера air_orc.
// server.go — стартовая точка: структура Web, конструктор New,
// регистрация всех маршрутов (Handler) и lifecycle-методы.
package web

import (
	"air_orchestrator/internal/config"
	"air_orchestrator/internal/domain/service"
	"air_orchestrator/internal/infrastructure/notification"
	"air_orchestrator/internal/infrastructure/profiler"
	"air_orchestrator/internal/infrastructure/storage"
	"air_orchestrator/internal/metrics"
	db "air_orchestrator/internal/repository/mysql"
	authuc "air_orchestrator/internal/usecase/auth" // Для идентичности UC..
	"air_orchestrator/internal/usecase/session"
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
	"golang.org/x/time/rate"
)

// ─── Псевдонимы доменных интерфейсов ─────────────────────────────────────────
// Exam — интерфейс сервиса безопасности.
type Exam = service.SecurityService

// SMTP — интерфейс почтового сервиса.
type SMTP = service.MailerService

// EventSender — отправка событий frontend-у через сокеты/каналы.
type EventSender interface {
	SendStorageEvent(userID uint32, event string) error
}

// ─── Интерфейс use case MasterKey ─────────────────────────────────────────────

type MasterKeyUC interface {
	CreateMasterKey(userID uint32, respID uint64, encPass string, progress func(string)) (string, error)
	VerifyPassword(userID uint32, respID uint64, encPass string) error
	RewrapOrReset(userID uint32, respID uint64, rawMK string, newPass string) error
	GetMasterKeyData(userID uint32) (encMK string, wrapSalt string, hasMK bool, err error)
}

// ─── Интерфейс use case Admin ─────────────────────────────────────────────────

type AdminUC interface {
	IsDevUser(userID uint32) (bool, error)
	GetConfigs(ctx context.Context, keys []string) (map[string]string, error)
	SetConfigs(ctx context.Context, kvs map[string]string) error
	GetAllConfigs(ctx context.Context) (map[string]string, error)
	ResetSessionKeys(ctx context.Context) error
}

// ─── Интерфейс use case Auth ──────────────────────────────────────────────────

// AuthUC — интерфейс use case аутентификации.
// Delivery зависит от этого интерфейса, а не от конкретного *auth.AuthUseCase.
type AuthUC interface {
	CheckEmail(mail string) (uint32, string, error)
	Register(input authuc.RegisterInput) (*authuc.RegisterResult, error)
	Authenticate(ctx context.Context, input authuc.LoginInput) (*authuc.LoginResult, error)
	MigrateLegacyUser(userID uint32, password, mail string) error
	LoadMasterKeyIfExists(userID uint32, password string) (bool, error)
	RequestPasswordReset(mail string) (token string, found bool, err error)
	ResetPassword(userID uint32, input authuc.ResetPasswordInput) error
	ConfirmEmail(tokenString string) (userID uint32, email string, err error)
}

// ─── Интерфейс use case Provider ──────────────────────────────────────────────

// ProviderUC — интерфейс use case управления провайдерами и API-ключами.
type ProviderUC interface {
	RevokeUserAPIKey(userID uint32, provider commdom.ProviderType) (needRestart bool, err error)
	SetUserAPIKey(userID uint32, provider commdom.ProviderType, apiKey string) (needRestart bool, err error)
}

// ─── Интерфейс use case TestAPI ──────────────────────────────────────────────

// TestAPI — интерфейс use case тестовых AI-сессий.
// Delivery зависит от этого интерфейса, а не от конкретного *session.TestAPI.
type TestAPI interface {
	StartSession(ctx context.Context, userId uint32, respId uint64, provider commdom.ProviderType) (*session.TestSession, *commdom.UniversalModelData, error)
	SendMessage(userId uint32, respId uint64, msg model.Message) error
	GetChannel(userId uint32, respId uint64) (*model.Ch, error)
	GetAnswer(ctx context.Context, userId uint32, respId uint64, timeout time.Duration) (*session.AnswerResponse, error)
	StopSession(userId uint32, respId uint64) error
	CleanupWebSocketSession(userId uint32, respId uint64) error
	StartRealtimeSession(userId uint32, respId uint64, treadId uint64) error
	StopRealtimeSession(userId uint32, respId uint64)
	GetRealtimeChannels(userId uint32, respId uint64) (<-chan []byte, <-chan model.RealtimeEvent, error)
	UnsubscribeRealtimeEvents(userId uint32, respId uint64, sub <-chan model.RealtimeEvent)
	SendRealtimeAudio(userId uint32, respId uint64, pcm16 []byte) error
}

// ─── Интерфейс MCPHandler ─────────────────────────────────────────────────────
// MCPHandler — интерфейс MCP-сервера для gin-роута POST /mcp.
type MCPHandler interface {
	ServeHTTP(c *gin.Context)
}

// ─── Типы событий для административных уведомлений ───────────────────────────
// PropEvent — тип события для уведомлений администраторам.
type PropEvent = notification.EventKind

const (
	NewReg      = notification.EventNewReg
	MailConfirm = notification.EventMailConfirm
	UserDelete  = notification.EventUserDelete
)

// ─── Storage service bundle ───────────────────────────────────────────────────

// StorageServices aggregates all storage-related components into a single
// dependency for clean injection into Web handlers.
type StorageServices struct {
	Factory      *storage.StorageFactory
	Sessions     *storage.SessionService
	Reservations *storageusecase.ReservationService
	Migrations   *storageusecase.MigrationService
}

// ─── Главная структура ────────────────────────────────────────────────────────
type Web struct {
	ctx    context.Context
	cancel context.CancelFunc
	Gin    *gin.Engine
	// Доменные зависимости (через интерфейсы)
	mod  *model.Router
	exam Exam
	smtp SMTP
	// Use case (через интерфейсы — не конкретный тип)
	api         TestAPI
	authUC      AuthUC
	providerUC  ProviderUC
	masterKeyUC MasterKeyUC
	adminUC     AdminUC
	// Прямая интеграции репозитариев
	db *db.DB
	// Инфраструктурные зависимости
	profiler *profiler.Profiler
	notifier *notification.AdminNotifier
	// MCP
	mcpHandler MCPHandler
	// Event sender for reauth-userkey, etc.
	evt EventSender
	// Storage (factory, sessions, reservations, migrations)
	storage *StorageServices
	// Rate limiting
	limiterMu sync.Mutex
	limiters  map[uint64]*struct {
		l    *rate.Limiter
		seen time.Time
	}
	// Временное состояние TOTP
	totpPending      sync.Map
	totpSetupPending sync.Map
	// Имя Telegram-бота из app_config
	carpinteroName string
}

// ─── WebSocket upgrader ───────────────────────────────────────────────────────
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func upgradeWebSocket(c *gin.Context) (*websocket.Conn, error) {
	protocols := c.Request.Header.Get("Sec-WebSocket-Protocol")
	if protocols != "" {
		parts := strings.Split(protocols, ",")
		subprotocols := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				subprotocols = append(subprotocols, t)
			}
		}
		cu := websocket.Upgrader{
			CheckOrigin:  func(r *http.Request) bool { return true },
			Subprotocols: subprotocols,
		}
		return cu.Upgrade(c.Writer, c.Request, nil)
	}
	return upgrader.Upgrade(c.Writer, c.Request, nil)
}

// ─── Конструктор ─────────────────────────────────────────────────────────────
func New(
	parent context.Context,
	x service.SecurityService,
	s service.MailerService,
	m *model.Router,
	d *db.DB,
	a TestAPI,
	authUC AuthUC,
	providerUC ProviderUC,
	masterKeyUC MasterKeyUC,
	adminUC AdminUC,
	storage *StorageServices,
	evt EventSender,
	prof *profiler.Profiler,
	mcpH MCPHandler,
) *Web {
	ctx, cancel := context.WithCancel(parent)
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	configs, err := adminUC.GetConfigs(ctx, []string{"tg.bot"})
	if err != nil {
		logger.Warn("Не удалось получить конфигурацию tg.bot: %v", err)
	}
	return &Web{
		ctx:            ctx,
		cancel:         cancel,
		Gin:            gin.Default(),
		mod:            m,
		db:             d,
		exam:           x,
		smtp:           s,
		api:            a,
		authUC:         authUC,
		providerUC:     providerUC,
		masterKeyUC:    masterKeyUC,
		adminUC:        adminUC,
		profiler:       prof,
		notifier:       notification.New(),
		mcpHandler:     mcpH,
		storage:        storage,
		evt:            evt,
		carpinteroName: configs["tg.bot"],
		limiters: make(map[uint64]*struct {
			l    *rate.Limiter
			seen time.Time
		}),
	}
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────
func (w *Web) Close() error {
	w.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv := &http.Server{Addr: ":8080", Handler: w.Gin}
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Ошибка при завершении HTTP сервера: %v", err)
		return err
	}
	logger.Debug("web модуль успешно завершён")
	return nil
}
func (w *Web) CleanupLimiter() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.limiterMu.Lock()
			now := time.Now()
			for id, c := range w.limiters {
				if now.Sub(c.seen) > time.Hour {
					delete(w.limiters, id)
				}
			}
			w.limiterMu.Unlock()
		case <-w.ctx.Done():
			return
		}
	}
}
func (w *Web) Allow(respId uint64) bool {
	w.limiterMu.Lock()
	defer w.limiterMu.Unlock()
	c, ok := w.limiters[respId]
	if !ok {
		c = &struct {
			l    *rate.Limiter
			seen time.Time
		}{
			l: rate.NewLimiter(rate.Limit(config.RateLimit), config.RateBurst),
		}
		w.limiters[respId] = c
	}
	c.seen = time.Now()
	return c.l.Allow()
}

// ─── Регистрация маршрутов ─────────────────────────────────────────────────────

// Handler регистрирует глобальные middleware, подключает все группы маршрутов
// и запускает HTTP-сервер (блокирующий вызов).
func (w *Web) Handler() error {
	logger.Info("WEB server air_orchestrator started")

	// ── Swagger (только без тега prod и при SWAGGER_ENABLED=true) ─────────────
	setupSwagger(w.Gin)

	// ── Глобальные middleware ─────────────────────────────────────────────────
	w.Gin.Use(metrics.PrometheusMiddleware())
	w.Gin.GET("/metrics", metrics.Handler())
	if w.profiler != nil {
		w.Gin.Use(profiler.HTTPTracingMiddleware(1 * time.Second))
		logger.Info("HTTP трассировка включена (порог: 1s)")
	}
	w.Gin.Use(w.corsMiddleware())

	devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEVELOPMENT")), "true")
	if devMode == true {
		logger.Debug("DEVELOPMENT=true, включены маршруты для локальной разработки")
		w.Gin.GET("/test.html", func(c *gin.Context) {
			c.File("/app/test.html")
		})
	}

	// ── Публичные прокси-маршруты  ───────────────────────────────────────────
	open := w.Gin.Group("/open")
	// для переадресации на Telegram bot
	open.Any("/tgbot/webhook", w.proxyTgBotWebhook)
	open.Any("/tgbot/webhook/*path", w.proxyTgBotWebhook)
	open.Any("/tgbot/setwebhook", w.proxyTgBotSetWebhook)
	// amoCRM авторизация и доступность
	open.GET("/crm/health", w.proxyCRMPublicRequest)
	open.GET("/crm/oauth/amocrm/callback", w.proxyAmoCRMOAuthCallback)
	// OAuth google
	open.GET("/google/oauth/callback", w.GoogleOAuthCallback)
	// Авито
	open.GET("/avito/available", w.proxyAvitoRequest)
	open.GET("/avito/auth/callback", w.proxyAvitoRequest)
	open.POST("/avito/webhook", w.proxyAvitoRequest)

	// ── Публичные маршруты с авторизацией ────────────────────────────────────────
	v1 := w.Gin.Group("/v1")
	w.authRoutes(v1)
	w.channelRoutes(v1)
	w.registerTestAPIRoutes(v1)
	w.registerWidgetRoutes(v1)
	w.registerPaymentRoutes(v1)
	w.dialogRoutes(v1)
	w.modelRoutes(v1)
	w.providerRoutes(v1)
	w.notificationRoutes(v1)
	w.userRoutes(v1)
	w.totpRoutes(v1)
	w.storageRoutes(v1)
	w.embeddingRoutes(v1)
	w.operatorRoutes(v1)
	w.servicesRoutes(v1)
	w.devRoutes(v1)
	w.webSocketRoutes(v1)
	w.crmRoutes(v1)
	w.googleRoutes(v1)
	w.avitoRoutes(v1)

	// Внутренние маршруты только для air_shared сети между контейнерами
	// закрыты снаружи на уровне Envoy вернёт 403
	internal := w.Gin.Group("/int")
	w.mcpRoutes(internal)
	w.intNotificationsRoutes(internal)

	// ── Запуск сервера ─────────────────────────────────────────────────────────
	if isRunningInDocker() {
		if err := w.Gin.Run(":8080"); err != nil {
			return fmt.Errorf("ошибка запуска web сервера air_orchestrator: %w", err)
		}
	} else {
		// Запуск сервера с SSL и локальным сертификатом в режиме разработки без докера
		if err := w.Gin.RunTLS(":443", "localhost.pem", "localhost-key.pem"); err != nil {
			return fmt.Errorf("ошибка запуска WEB сервера air_orchestrator в режиме локальной разаработки: %e", err)
		}
	}

	return nil
}

func (w *Web) storageRoutes(v1 *gin.RouterGroup) {
	s3 := v1.Group("/storage")
	s3.Use(w.authAllowMiddleware())
	s3.GET("/config", w.GetStorageConfig)
	s3.POST("/session", w.CreateStorageSession)
	s3.GET("/quota", w.GetStorageQuota)
	s3.PUT("/config/external", w.PutExternalStorageConfig)
	s3.POST("/config/test", w.TestExternalStorageConfig)
	s3.POST("/config/switch", w.SwitchToInternalStorage)
	s3.POST("/presigned-upload", w.CreatePresignedUpload)
	s3.POST("/commit", w.CommitStorageUpload)
	s3.GET("/objects", w.ListStorageObjects)
	s3.DELETE("/object", w.DeleteStorageObject)
	s3.POST("/delete-all", w.DeleteAllStorageObjects)
	s3.GET("/health", w.StorageHealth)
	s3.GET("/presigned-download", w.PresignStorageDownload)
	s3.POST("/migration", w.StartStorageMigration)
	s3.GET("/migration/:id", w.GetStorageMigration)
	s3.POST("/migration/:id/cancel", w.CancelStorageMigration)
}

func isRunningInDocker() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// ─── Методы регистрации групп маршрутов ───────────────────────────────────────

// authRoutes — авторизация, регистрация, токены, сброс пароля.
func (w *Web) authRoutes(v1 *gin.RouterGroup) {
	auth := v1.Group("/auth")
	auth.POST("/check-email", w.CheckMail)
	auth.POST("/register", w.RegNewUser)
	auth.POST("/session-key", w.GetAuthKey)
	auth.POST("/login", w.Login)
	auth.POST("/logout", w.Logout)
	auth.POST("/totp", w.AuthTOTP)
	auth.GET("/email/confirm", w.ConfirmEmail)
	auth.GET("/token/refresh", w.RefreshToken)

	resetPass := auth.Group("/reset-password")
	resetPass.POST("/request", w.HandleResetPass)
	resetPass.POST("/validate", w.CheckResetPass)
	resetPass.POST("/confirm", w.ResetPass)

	// Защищённые auth-эндпоинты (требуют Bearer token)
	authProtected := v1.Group("/auth")
	authProtected.Use(w.authAllowMiddleware())
	authProtected.POST("/rewrap-master-key", w.RewrapMasterKey)
	authProtected.POST("/verify-password", w.VerifyUserPassword)
	authProtected.GET("/check-subscription", w.paySubscription)
}

// channelRoutes — управление каналами коммуникации.
func (w *Web) channelRoutes(v1 *gin.RouterGroup) {
	channel := v1.Group("/channel")
	channel.Use(w.authAllowMiddleware())
	channel.GET("/name", w.GetChannelName)
	channel.GET("", w.ReadChannel)
	channel.POST("", w.SaveChannel)
	channel.DELETE("", w.DeleteChannel)
	channel.GET("/available/:ch_type", w.availableHandler)
}

// registerTestAPIRoutes — тестовые запросы к AI-модели.
func (w *Web) registerTestAPIRoutes(v1 *gin.RouterGroup) {
	test := v1.Group("/api/test")
	test.Use(w.authAllowMiddleware())
	test.GET("/start", w.testStartSessionHandler)
	test.POST("/ask", w.testAskHandler)
	test.DELETE("/stop", w.testStopSessionHandler)
}

// registerWidgetRoutes — публичные и приватные эндпоинты виджета.
func (w *Web) registerWidgetRoutes(v1 *gin.RouterGroup) {
	widget := v1.Group("/widget")
	// Проксирование как есть без получения UID
	widget.GET("/available", w.proxyWidgetRequest)
	widget.POST("/exam", w.proxyWidgetRequest)
	widget.GET("/validate", w.proxyWidgetRequest)
	widget.POST("/refresh", w.proxyWidgetRequest)
	widget.GET("/username", w.proxyWidgetRequest)
	widget.GET("/dialog", w.proxyWidgetRequest)
	widget.POST("/data", w.proxyWidgetRequest)
	widget.GET("/events", w.proxyWidgetEvents)
	widget.POST("/events-ticket", w.proxyWidgetRequest)

	// Получение UID из токена на стороне air_orc
	orcAuth := widget.Group("")
	orcAuth.Use(w.authAllowMiddleware())
	orcAuth.GET("/enable", w.proxyWidgetUIDRequest)
	orcAuth.GET("/disable", w.proxyWidgetUIDRequest)
	orcAuth.GET("/restart", w.proxyWidgetUIDRequest)
	orcAuth.POST("/code", w.proxyWidgetUIDRequest)
}

// registerPaymentRoutes — тарифы, создание и статус платежей.
func (w *Web) registerPaymentRoutes(v1 *gin.RouterGroup) {
	pay := v1.Group("/pay")
	// Donation прямая переадресация на pay без userID и SSE
	pay.GET("/donation/donation-currencies", w.donationCurrencies)
	pay.POST("/donation/donation-address", w.donationAddress)
	pay.Use(w.authAllowMiddleware())
	pay.GET("/tariff", w.payTariff)
	pay.GET("/currencies", w.payCurrencies)
	pay.POST("/create-payment", w.payCreateBybitPayment)
	pay.GET("/payment-status", w.payPaymentStatus)
	pay.GET("/payment-status-stream", w.payHandlePaymentStatusSSE)
}

// dialogRoutes — история диалогов и детали пользователя.
func (w *Web) dialogRoutes(v1 *gin.RouterGroup) {
	dialog := v1.Group("/dialog")
	dialog.Use(w.authAllowMiddleware())
	dialog.GET("/all", w.GetUserDialogs)
	dialog.POST("/view/:id", w.ViewDialog)
	dialog.DELETE("/:id", w.DeleteDialog)
	dialog.DELETE("/list", w.DeleteDialogs)
}

// modelRoutes — CRUD AI-моделей пользователя.
func (w *Web) modelRoutes(v1 *gin.RouterGroup) {
	mod := v1.Group("/model")
	mod.Use(w.authAllowMiddleware())
	mod.GET("/demo", w.CheckDemoUser)
	mod.GET("", w.ReadUserModel)
	mod.POST("/upload-file", w.FileUpload)
	mod.POST("/delete-file", w.FileDelete)
	mod.POST("/add-file", w.FileAdd)
	mod.POST("/create", w.CreateModel)
	mod.POST("/update", w.UpdateModel)
	mod.GET("/list", w.List)
	mod.GET("/set-active", w.SetModelActive)
	mod.POST("/voice/clone", w.CloneMistralVoice)
	mod.GET("/voices", w.ListMistralVoices)
	mod.GET("/voices/:voiceID", w.GetMistralVoice)
	mod.PATCH("/voices/:voiceID", w.UpdateMistralVoice)
	mod.DELETE("/voices/:voiceID", w.DeleteMistralVoice)
	mod.GET("/voices/:voiceID/sample", w.GetMistralVoiceSample)
	// удаление модели происходит в  /ws/delete-model
}

// providerRoutes — управление API-ключами провайдеров.
func (w *Web) providerRoutes(v1 *gin.RouterGroup) {
	provider := v1.Group("/provider")
	provider.Use(w.authAllowMiddleware())
	provider.GET("/available", w.ProvidersWithApiKeys)
	provider.DELETE("/revoke", w.RevokeUserAPIKey)
	provider.POST("/set-key", w.SetUserAPIKey)
}

// notificationRoutes — настройки и отправка уведомлений.
func (w *Web) notificationRoutes(v1 *gin.RouterGroup) {
	// Настройки уведомлений пользователя
	nota := v1.Group("/nota")
	nota.Use(w.authAllowMiddleware())
	nota.GET("/mail", w.GetMail)
	nota.GET("", w.ReadNotifications)
	nota.POST("", w.SaveNotifications)
	nota.POST("/code", w.SendVerificationCode)
	nota.POST("/events", w.SaveNotificationsEvents)
	nota.DELETE("", w.DeleteNotificationsChannel)
}

// intNotificationsRoutes отправка уведомлений без авторизации — вызывается внутренними сервисами
// только внутренняя сеть air_share
func (w *Web) intNotificationsRoutes(int *gin.RouterGroup) {
	notif := int.Group("/notification")
	notif.POST("/mail", w.SendMailNotification)
	notif.POST("/telega", w.SendTelegaNotification)
	notif.POST("/instant", w.SendInstantNotification)
}

// mcpRoutes — MCP-сервер (Model Context Protocol, Streamable HTTP).
// только внутренняя сеть air_share
func (w *Web) mcpRoutes(v1 *gin.RouterGroup) {
	v1.POST("/mcp", w.mcpHandler.ServeHTTP)
}

// userRoutes — данные профиля и настройки пользователя.
func (w *Web) userRoutes(v1 *gin.RouterGroup) {
	userData := v1.Group("/user")
	userData.Use(w.authAllowMiddleware())
	userData.GET("/data", w.UserData)
	userData.POST("/timezone", w.UserTimeZone)
	userData.GET("/details", w.GetUserDetails)
}

// totpRoutes — двухфакторная аутентификация.
func (w *Web) totpRoutes(v1 *gin.RouterGroup) {
	totp := v1.Group("/totp")
	totp.Use(w.authAllowMiddleware())
	totp.POST("/setup", w.TOTPSetup)
	totp.POST("/confirm", w.TOTPConfirm)
	totp.DELETE("", w.TOTPDisable)
}

// embeddingRoutes — текстовые эмбеддинги (RAG).
func (w *Web) embeddingRoutes(v1 *gin.RouterGroup) {
	emb := v1.Group("/embedding")
	emb.Use(w.authAllowMiddleware())
	emb.POST("/upload", w.UploadEmbedding)
	emb.GET("/list", w.ListUserDocuments)
	emb.DELETE("/:id", w.DeleteDocument)
}

// operatorRoutes — управление операторами.
func (w *Web) operatorRoutes(v1 *gin.RouterGroup) {
	operators := v1.Group("/operators")
	operators.Use(w.authAllowMiddleware())
	operators.GET("", w.OperatorsList)
	operators.POST("", w.SaveOperators)
}

// servicesRoutes — пользовательские сервисы и прокси к ним.
func (w *Web) servicesRoutes(v1 *gin.RouterGroup) {
	services := v1.Group("/services")
	services.Use(w.authAllowMiddleware())

	// CRUD: управление списком сервисов пользователя
	services.GET("/list", w.GetServicesList)
	services.POST("/add", w.AddService)
	services.DELETE("/delete", w.DeleteService)

	// Direct proxy to LeadService. The handler adds uid from the validated JWT
	// and preserves the HTTP method, query parameters and request body.
	lead := v1.Group("/services/lead")
	lead.Use(w.authAllowMiddleware())
	lead.Any("/*path", w.proxyServicesRequest)
}

// devRoutes — инструменты разработчика и администратора.
func (w *Web) devRoutes(v1 *gin.RouterGroup) {
	dev := v1.Group("/dev")
	dev.Use(w.authAllowMiddleware(), w.devOnlyMiddleware())
	dev.POST("/get-data", w.GetDev)
	dev.POST("/set-distrib-mail", w.SetDistribMail)
	dev.POST("/set-session-key", w.CreateNewSessionKey)
	dev.POST("/check-settings", w.CheckSettings)
	dev.POST("/set-gauth", w.SetGAUTH)
	dev.POST("/set-carpintero", w.SetCarpintero)
	dev.POST("/set-operbot", w.SetOperBot)
	dev.POST("/get-service-key", w.GetServiceKey)
	dev.POST("/generate-service-key", w.GenerateServiceKey)
	dev.POST("/generate-widget-key", w.GenerateWidgetKey)
}

// webSocketRoutes — все WebSocket эндпоинты.
func (w *Web) webSocketRoutes(v1 *gin.RouterGroup) {
	ws := v1.Group("/ws")
	ws.Use(w.authAllowMiddleware())

	// Test API
	ws.GET("/test-model", w.testWebSocketHandler)
	ws.GET("/test-realtime", w.testRealtimeHandler)

	// MasterKey
	ws.GET("/create-master-key", w.CreateMasterKeyWSS)

	// Системные
	ws.GET("/instant", w.Instant)
	ws.GET("/log", w.LogWSSHandler)
	ws.GET("/delete-model", w.DeleteModelWSSHandler)
	ws.GET("/delete-all", w.DeleteAllUserDataWSSHandler)

	// Сервисы (Telegram / WhatsApp)
	ws.GET("/lead/tg-auth", w.ServiceAddTgAk)
	ws.GET("/lead/wa-auth", w.ServiceAddWaAk)
	ws.GET("/lead/events", w.ServiceEventWSS)
	ws.GET("/lead/start", w.ServiceStartWSS)
	// Перезапуск активных каналов при изменении модели
	ws.GET("/restart", w.RestartChannels)

	// Telegram пользователи
	tguser := ws.Group("/tguser")
	tguser.GET("", w.TgAuthWebSocket)
	tguser.GET("/contacts", w.TgGetContactsWS)

	// WhatsApp пользователи
	whats := ws.Group("/whats")
	whats.GET("", w.WaAuthWebSocket)
	whats.GET("/contacts", w.WaGetContactsWS)
}

// crmRoutes — CRM-прокси (AmoCRM).
func (w *Web) crmRoutes(v1 *gin.RouterGroup) {
	// Публичные эндпоинты (без авторизации)
	//v1.GET("/crm/health", w.proxyCRMPublicRequest)
	//v1.GET("/crm/oauth/amocrm/callback", w.proxyAmoCRMOAuthCallback)
	// Защищённые эндпоинты (авторизация через CRMAuthMiddleware)
	crmAPI := v1.Group("/crm/api")
	crmAPI.Use(w.CRMAuthMiddleware())
	crmAPI.Any("/*path", w.proxyCRMRequest)
}

// googleRoutes — Google OAuth.
func (w *Web) googleRoutes(v1 *gin.RouterGroup) {
	// Публичный OAuth callback
	//v1.GET("/google/oauth/callback", w.GoogleOAuthCallback)
	// Защищённые эндпоинты
	google := v1.Group("/google")
	google.Use(w.authAllowMiddleware())
	google.GET("/oauth/url", w.GoogleOAuthURL)
	google.GET("/token/status", w.GetGoogleTokenStatus)
	google.DELETE("/token/revoke", w.RevokeGoogleToken)
}

// avitoRoutes — Avito-интеграция.
func (w *Web) avitoRoutes(v1 *gin.RouterGroup) {
	// Публичные эндпоинты (без авторизации) сейчас в группе /open
	//v1.GET("/avito/available", w.proxyAvitoRequest)
	//v1.GET("/avito/auth/callback", w.proxyAvitoRequest)
	//v1.POST("/avito/webhook", w.proxyAvitoRequest)
	// Защищённые эндпоинты
	avito := v1.Group("/avito")
	avito.Use(w.authAllowMiddleware())
	avito.GET("/status", w.proxyAvitoRequest)
	avito.POST("/auth/url", w.proxyAvitoRequest)
	avito.POST("/disconnect", w.proxyAvitoRequest)
	avito.GET("/chats", w.proxyAvitoRequest)
	avito.GET("/subscriptions", w.proxyAvitoRequest)
	avito.POST("/subscribe", w.proxyAvitoRequest)
	avito.POST("/unsubscribe", w.proxyAvitoRequest)
}

// SendAdminNotification делегирует отправку уведомления инфраструктурному AdminNotifier.
func (w *Web) SendAdminNotification(event PropEvent, message string) {
	w.notifier.Notify(event, message)
}

// corsMiddleware разрешает кросс-доменные запросы (CORS).
// Разрешает любой Origin — ограничение нужно добавить для production.
func (w *Web) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if origin := c.Request.Header.Get("Origin"); origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Methods", "POST, GET, DELETE, PUT, PATCH, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
