package app

import (
	"air_orchestrator/internal/config"
	db "air_orchestrator/internal/db"
	"air_orchestrator/internal/delivery/grpc"
	web "air_orchestrator/internal/delivery/http"
	"air_orchestrator/internal/delivery/mcp"
	"air_orchestrator/internal/domain/state"
	"air_orchestrator/internal/infrastructure/profiler"
	"air_orchestrator/internal/infrastructure/redis"
	cron "air_orchestrator/internal/infrastructure/scheduler"
	exam "air_orchestrator/internal/infrastructure/security"
	"air_orchestrator/internal/infrastructure/smtp"
	adminuc "air_orchestrator/internal/usecase/admin"
	authuc "air_orchestrator/internal/usecase/auth"
	masterkeyuc "air_orchestrator/internal/usecase/masterkey"
	provideruc "air_orchestrator/internal/usecase/provider"
	api "air_orchestrator/internal/usecase/session"
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"os"
	"strings"
	"time"

	"github.com/ikermy/air-common/v2/pkg/com"
	"github.com/ikermy/air-common/v2/pkg/dbrouter"
	"github.com/ikermy/air-common/v2/pkg/endpoint"
	"github.com/ikermy/air-common/v2/pkg/model"
	"github.com/ikermy/air-common/v2/pkg/model/google"
	"github.com/ikermy/air-common/v2/pkg/model/mistral"
	"github.com/ikermy/air-common/v2/pkg/model/openai"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

type App struct {
	ctx      context.Context
	cancel   context.CancelFunc
	web      *web.Web
	api      *api.API
	end      *endpoint.Endpoint
	db       *db.DB
	mcp      *mcp.Handler
	cron     *cron.Scheduler
	profiler *profiler.Profiler
	grpc     *grpc.Server
	prcProxy *grpc.CallsProxy // пока реализован только для звонков но потом всё будет!
	router   *dbrouter.Router // nil в single-DB режиме
}

func (a *App) Run() {
	go func() {
		err := a.web.Handler()
		if err != nil {
			logger.Fatal(err)
		}
	}()

	if a.grpc != nil {
		go func() {
			if err := a.grpc.Start(); err != nil {
				logger.Fatal("gRPC сервер завершился с ошибкой: %v", err)
			}
		}()
	}

	bus := com.NewBus(10)
	go uReader(bus.MsgCh)
	go a.db.HandlerClose()
	go a.web.CleanupLimiter()
	a.cron.Start()

	// Слушаю общий канал уведомлений для отправки сообщений в Instant
	bus.Add(func(ch chan<- com.LogMsg) { a.end.NotificationListener(ch) })

	go func() {
		<-a.ctx.Done()
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			<-ticker.C
			a.db.CloseDoneCh() // Закрываем канал DoneCh принудительно, больше никто не работает с БД
		}()

		logger.Info("App: получен сигнал завершения, начинаю shutdown")

		a.cron.Shutdown()
		a.api.Shutdown()
		a.mcp.Shutdown()
		if a.grpc != nil {
			a.grpc.Shutdown()
		}
		if a.prcProxy != nil {
			if err := a.prcProxy.Shutdown(); err != nil {
				logger.Warn("App: ошибка закрытия call backend: %v", err)
			}
		}
		bus.Add(func(ch chan<- com.LogMsg) { a.end.Shutdown(ch) })

		if err := a.web.Close(); err != nil {
			logger.Error("App: ошибка при закрытии web: %v", err)
		}
		if a.router != nil {
			if err := a.router.Close(); err != nil {
				logger.Warn("App: ошибка закрытия shard router: %v", err)
			}
		}
		logger.Info("App: все модули завершены, закрываю соединение с БД")

		bus.WaitAndClose()
		a.db.CloseDoneCh()
	}()
}

func New(parent context.Context, prof *profiler.Profiler, redisCfg state.Redis) *App {
	// Локальный дочерний контекст для уровня app
	ctx, cancel := context.WithCancel(parent)

	d, err := db.New(ctx)
	if err != nil {
		logger.Fatal("Ошибка инициализации базы данных: %v", err)
	}

	if err := d.EncryptAppConfigSensitiveValues(); err != nil {
		logger.Fatal("Ошибка шифрования sensitive app_config: %v", err)
	}

	shardRouter := initShardRouter(ctx)
	if shardRouter != nil {
		d.SetShardResolver(shardRouter)
		if refConn, refErr := shardRouter.DBForReference(); refErr != nil {
			logger.Debug("DB router: reference connection is not configured: %v", refErr)
		} else {
			d.SetReferenceConn(refConn)
		}
	}

	e := endpoint.New(ctx, d)
	m := model.NewModelRouter(ctx, d,
		model.WithDialogSaver(e),
		openai.NewAsRouterOption(),
		mistral.NewAsRouterOption(),
		google.NewAsRouterOption())

	x := exam.New()
	if err := x.LoadOrInitKey(ctx, d); err != nil {
		logger.Fatal("Ошибка инициализации session key:", err)
	}

	// ── Redis (опционально) ─────────────────────────────────────────────────
	var redisCli *redis.Client
	if redisCfg.RedisAddr != "" && len(state.MasterKey) > 0 {
		var redisErr error
		redisCli, redisErr = redis.New(ctx, redisCfg.RedisAddr, redisCfg.RedisPassword, redisCfg.RedisDB)
		if redisErr != nil {
			logger.Warn("Redis: не удалось подключиться: %v — работа без Redis", redisErr)
			redisCli = nil
		} else {
			x.SetRedisClient(redisCli)
			logger.Info("Redis: подключён, адрес=%s", redisCfg.RedisAddr)

			// Загружаем все MasterKey из Redis в masterKeyCache
			if loadErr := x.LoadAllMasterKeysFromRedis(ctx); loadErr != nil {
				logger.Warn("Redis: ошибка загрузки MasterKey: %v", loadErr)
			} else {
				logger.Info("MasterKey: загружены из Redis в masterKeyCache")
			}
		}
	} else {
		logger.Info("MasterKey: Redis не настроен — только RAM-кэш")
	}

	// Инжектируем резолвер MasterKey в DB.
	// Используется для расшифровки полей с префиксом "$mk$" (диалоги, model data,
	// user_api_keys). API-ключи без MasterKey могут храниться через "$app$".
	// MasterKey проверяется только как ворота авторизации в ProviderUseCase.SetUserAPIKey:
	// если пользователь настроил MasterKey, ключ должен быть в cache (= пользователь залогинен).
	d.SetMasterKeyResolver(x.GetMasterKey)

	s := smtp.New(ctx, d, e)
	a := api.New(ctx, m, d, e)

	webStorage, err := newStorageServices(d, x, redisCli)
	if err != nil {
		logger.Fatal("Ошибка инициализации storage: %v", err)
	}

	if webStorage.Factory != nil {
		webStorage.Factory.OnStorageLocked(func(userID uint64) {
			_ = e.SendNotification(com.CarpCh{Event: "reauth-userkey", UserID: userID})
		})
	}

	sendEvt := &storageEventSender{end: e}

	mcpH := mcp.New(ctx, d, m, storageusecase.NewService(webStorage.Factory))
	authUC := authuc.New(d, x, s)
	providerUC := provideruc.New(d, m, x)
	masterKeyUC := masterkeyuc.New(d, x)
	adminUC := adminuc.New(d)

	w := web.New(ctx, x, s, m, d, a.GetTestAPI(),
		authUC,
		providerUC,
		masterKeyUC,
		adminUC,
		webStorage,
		sendEvt,
		prof,
		mcpH,
	)
	// Инжектируем web.CallLeadTarget в mcp чтобы избежать дублирования HTTP-логики.
	// web не импортирует mcp — связь только через LeadTargetFn (func тип).
	mcpH.SetLeadTargetFn(w.CallLeadTarget)
	mcpH.SetSessionAuth(config.MCPSessionSecret(), config.MCPRequireSignedSession())
	mcpH.SetServiceAuth(config.MCPServiceKey())

	if shardRouter != nil {
		w.AddReadinessCheck(shardRouter.Ping)
	}

	// Создаём планировщик фоновых задач
	cr := cron.New(ctx, d, e)
	if webStorage.Reservations != nil {
		cr.SetReservationSweep(webStorage.Reservations.Tick)
	}
	if webStorage.Migrations != nil {
		cr.SetMigrationWorker(func(ctx context.Context) { _ = webStorage.Migrations.ProcessPending(ctx) })
	}
	if config.UserDeleteSoft() {
		grace := config.UserDeleteGrace()
		cr.SetPurgeWorker(func(ctx context.Context) {
			purged, err := d.PurgeDeletedUsers(ctx, grace, 100)
			if err != nil {
				logger.Warn("Cron: purge soft-deleted users: %v", err)
				return
			}
			if purged > 0 {
				logger.Info("Cron: purged %d soft-deleted user(s)", purged)
			}
		}, config.UserPurgeInterval())
	}
	if config.RetentionEnabled() {
		cr.SetRetentionWorker(func(ctx context.Context) {
			if _, err := d.RunRetention(ctx,
				config.UserBotEventsRetention(),
				config.AuditLogRetention(),
				config.WebhookDedupRetention()); err != nil {
				logger.Warn("Cron: retention: %v", err)
			}
		}, config.RetentionInterval())
	}
	if config.UserMigrationFinalize() {
		cr.SetFinalMigrationWorker(func(ctx context.Context) {
			purged, finalized, err := d.RunUserMigrationFinalization(ctx, 100)
			if err != nil {
				logger.Warn("Cron: final user migration: %v", err)
				return
			}
			if purged > 0 || finalized {
				logger.Info("Cron: final user migration: purged=%d finalized=%t", purged, finalized)
			}
		}, config.UserMigrationCheckInterval())
	}

	// Инициализируем gRPC ConfigService для межсервисного доступа к конфигурациям.
	// Сервисный ключ задаётся администратором через POST /dev/generatesvckey.
	// До генерации ключа gRPC сервер не запускается.
	grpcPort := strings.TrimSpace(os.Getenv("GRPC_PORT"))
	if grpcPort == "" {
		grpcPort = config.GrpcPort
	}
	svcKey, err := d.GetAppConfig(ctx, "svc.service_key")
	if err != nil {
		logger.Fatal("Ошибка чтения gRPC service key: %v", err)
	}

	var g *grpc.Server
	var callsProxy *grpc.CallsProxy
	if svcKey == "" {
		logger.Warn("App: svc.service_key не задан — gRPC ConfigService не запущен. Используйте POST /dev/generatesvckey")
	} else {
		g, err = grpc.New(d, x, svcKey, grpcPort)
		if err != nil {
			logger.Fatal("Ошибка запуска gRPC сервера: %v", err)
		}
		callsProxy, err = grpc.DialCallsBackends(config.WhatsAppRPC, config.TelegramRPC, d)
		if err != nil {
			logger.Warn("App: call backends are unavailable: %v", err)
		} else {
			callsProxy.SetModelProvider(m)
			g.RegisterCallsService(callsProxy)
			logger.Info("App: CallsService proxy configured (WhatsApp=%s, Telegram=%s)", config.WhatsAppRPC, config.TelegramRPC)
		}
	}

	return &App{
		ctx:      ctx,
		cancel:   cancel,
		web:      w,
		api:      a,
		end:      e,
		db:       d,
		mcp:      mcpH,
		cron:     cr,
		profiler: prof,
		grpc:     g,
		prcProxy: callsProxy,
		router:   shardRouter,
	}
}

// ExitCh возвращает канал, который закрывается при завершении работы приложения.
func (a *App) ExitCh() <-chan struct{} {
	return a.db.GetExitCh()
}

// storageEventSender адаптирует endpoint.Endpoint под web.EventSender.
type storageEventSender struct{ end *endpoint.Endpoint }

func (s *storageEventSender) SendStorageEvent(userID uint64, event string) error {
	return s.end.SendNotification(com.CarpCh{Event: event, UserID: userID})
}

func uReader(readCh <-chan com.LogMsg) {
	for info := range readCh {
		switch info.Log {
		case 0: // Info
			logger.Info("%s: %v", info.Mod, info.Msg, info.UID)
		case 1: // Error
			logger.Error("%s: %v", info.Mod, info.Msg, info.UID)
		case 2: // Warn
			logger.Warn("%s: %v", info.Mod, info.Msg, info.UID)
		case 3: // Debug
			logger.Debug("%s: %v", info.Mod, info.Msg, info.UID)
		}
	}
}
