package main

import (
	"air_orchestrator/internal/app"
	"air_orchestrator/internal/domain/state"
	"air_orchestrator/internal/infrastructure/profiler"
	db "air_orchestrator/internal/repository/mysql"
	"context"
	"flag"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"strings"

	"github.com/ikermy/air-common/pkg/com"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// @title air_orchestrator API
// @version 1.0
// @description API Server for air_orchestrator
// @host localhost:8080
// @BasePath /
func main() {
	enableProfiling := flag.Bool("profile", false, "Включить профилирование приложения")
	pprofPort := flag.String("pprof-port", "6060", "Порт для pprof HTTP сервера")
	enableCPU := flag.Bool("cpu-profile", false, "Включить CPU профилирование при старте")
	autoSaveInterval := flag.Duration("profile-interval", 10*time.Minute, "Интервал автосохранения профилей (0 = отключено)")
	flag.Parse()

	logger.Debug(com.GetVersionInfo())

	// Инициализируем инфраструктурные переменные из env vars (порты, домен, TTL, логи).
	// Все значения имеют разумные дефолты; некорректные критичные — fatal.
	mode.InitFromEnv(logger.Fatalf)

	// Логгер: режим os.Stdout для Docker
	logSetup := logger.StdOut()
	// Можно установить через mode.SetLogLevel иначе установится из env в InitFromEnv
	// Если не устанавливать ничего = info
	// Уровень логирования читается из env.LOG_LEVEL
	logSetup.WithLogLevel(logSetup.FromString(mode.GetLogLevel()))
	logSetup.Apply()

	// Корневой контекст процесса, отменяется по сигналам ОС
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// ── APP_MASTER_KEY_FILE (обязателен) ────────────────────────────────────────
	// Читается до всех проверок, включая rekey, так как требуется везде.
	masterKeyPath := strings.TrimSpace(os.Getenv("APP_MASTER_KEY_FILE"))
	if masterKeyPath == "" {
		logger.Fatal("APP_MASTER_KEY_FILE не установлен — укажите путь к файлу с мастер-ключом")
	}
	data, err := os.ReadFile(masterKeyPath)
	if err != nil {
		logger.Fatal("Не удалось прочитать APP_MASTER_KEY_FILE: %v", err)
	}
	state.MasterKey = []byte(strings.TrimSpace(string(data)))
	if len(state.MasterKey) == 0 {
		logger.Fatal("APP_MASTER_KEY_FILE пуст")
	}
	logger.Info("MasterKey: загружен из %s", masterKeyPath)

	if db.IsAppConfigRekeyMode() {
		logger.Info("Запуск в режиме APP_CONFIG_REKEY=true")
		if err := app.RunAppConfigRekey(ctx); err != nil {
			logger.Fatal("Ошибка перекодирования app_config: %v", err)
		}
		logger.Info("APP_CONFIG_REKEY завершён, основной сервер не запускается")
		return
	}

	// ── Redis ───────────────────────────────────────────────────────────────────
	state.RedisAddr = os.Getenv("REDIS_ADDR")
	state.RedisPassword = os.Getenv("REDIS_PASSWORD")
	dbStr := os.Getenv("REDIS_DB")
	if dbStr != "" {
		if n, err := strconv.Atoi(dbStr); err == nil {
			state.RedisDB = n
		}
	}
	if state.RedisAddr != "" {
		logger.Info("Redis: адрес=%s, db=%d", state.RedisAddr, state.RedisDB)
	} else {
		logger.Warn("Redis: не настроен (REDIS_ADDR пуст)")
	}

	// Инициализация профилировщика
	var prof *profiler.Profiler
	if *enableProfiling || os.Getenv("PROFILING_ENABLED") == "true" {
		profConfig := profiler.Config{
			Enabled:          true,
			PprofPort:        *pprofPort,
			ProfilesDir:      "./profiles",
			AutoSaveInterval: *autoSaveInterval,
			EnableCPUProfile: *enableCPU,
		}

		var err error
		prof, err = profiler.New(ctx, profConfig)
		if err != nil {
			logger.Warn("Не удалось инициализировать профилировщик: %v", err)
		} else {
			// Создаем анализатор и логируем начальное состояние
			analyzer := profiler.NewAnalyzer(prof)
			analyzer.LogMemoryStats()

			// Запускаем периодический анализ производительности
			go func() {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						analyzer.AnalyzePerformance()
					}
				}
			}()
		}
	}

	a := app.New(ctx, prof)
	a.Run()

	// Ожидание завершения работы
	<-state.Exit

	// Корректное завершение профилировщика
	if prof != nil {
		if err := prof.Shutdown(); err != nil {
			logger.Warn("Ошибка при завершении профилировщика: %v", err)
		}
	}

	logger.Infoln("Приложение air_orchestrator завершено")
}
