package profiler

//startTime := time.Now()
//defer func() {
//	duration := time.Since(startTime)
//	if duration > time.Second {
//		logger.Warn("⚠️ IncrementDemoUsage выполнен за %v (userId=%d, respId=%d)", duration, userId, respId)
//	} else {
//		logger.Debug("⏱️ IncrementDemoUsage выполнен за %v (userId=%d, respId=%d)", duration, userId, respId)
//	}
//}()

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	rpprof "runtime/pprof"
	"sync"
	"time"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// Profiler управляет профилированием приложения
type Profiler struct {
	ctx          context.Context
	cancel       context.CancelFunc
	enabled      bool
	pprofServer  *http.Server
	profilesDir  string
	cpuFile      *os.File
	mu           sync.Mutex
	autoSaveFunc context.CancelFunc
}

// Config конфигурация профилировщика
type Config struct {
	Enabled          bool          // Включено ли профилирование
	PprofPort        string        // Порт для HTTP pprof сервера (например "6060")
	ProfilesDir      string        // Директория для сохранения профилей
	AutoSaveInterval time.Duration // Интервал автосохранения профилей (0 = отключено)
	EnableCPUProfile bool          // Включить CPU профилирование при старте
}

// New создает новый экземпляр профилировщика
func New(parent context.Context, cfg Config) (*Profiler, error) {
	if !cfg.Enabled {
		logger.Info("Profiler: профилирование отключено")
		return &Profiler{enabled: false}, nil
	}

	ctx, cancel := context.WithCancel(parent)

	// Устанавливаем директорию по умолчанию
	if cfg.ProfilesDir == "" {
		cfg.ProfilesDir = "./profiles"
	}

	// Создаем директорию для профилей
	if err := os.MkdirAll(cfg.ProfilesDir, 0755); err != nil {
		cancel()
		return nil, fmt.Errorf("не удалось создать директорию для профилей: %w", err)
	}

	p := &Profiler{
		ctx:         ctx,
		cancel:      cancel,
		enabled:     true,
		profilesDir: cfg.ProfilesDir,
	}

	// Запускаем HTTP pprof сервер
	if cfg.PprofPort != "" {
		if err := p.startPprofServer(cfg.PprofPort); err != nil {
			cancel()
			return nil, fmt.Errorf("не удалось запустить pprof сервер: %w", err)
		}
	}

	// Запускаем CPU профилирование
	if cfg.EnableCPUProfile {
		if err := p.StartCPUProfile(); err != nil {
			logger.Warn("Profiler: не удалось запустить CPU профилирование: %v", err)
		}
	}

	// Запускаем автосохранение профилей
	if cfg.AutoSaveInterval > 0 {
		p.startAutoSave(cfg.AutoSaveInterval)
	}

	logger.Info("Profiler: профилирование включено, директория: %s", cfg.ProfilesDir)
	if cfg.PprofPort != "" {
		logger.Info("Profiler: pprof сервер запущен на порту %s (http://localhost:%s/debug/pprof/)", cfg.PprofPort, cfg.PprofPort)
	}

	return p, nil
}

// startPprofServer запускает HTTP сервер для pprof
func (p *Profiler) startPprofServer(port string) error {
	mux := http.NewServeMux()

	// Регистрируем стандартные pprof хэндлеры
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// Дополнительные эндпоинты для specific профилей
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))

	p.pprofServer = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		if err := p.pprofServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Profiler: ошибка pprof сервера: %v", err)
		}
	}()

	return nil
}

// StartCPUProfile начинает CPU профилирование
func (p *Profiler) StartCPUProfile() error {
	if !p.enabled {
		return fmt.Errorf("профилирование отключено")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cpuFile != nil {
		return fmt.Errorf("CPU профилирование уже запущено")
	}

	filename := filepath.Join(p.profilesDir, fmt.Sprintf("cpu-%s.prof", time.Now().Format("2006-01-02-15-04-05")))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("не удалось создать файл профиля: %w", err)
	}

	if err := rpprof.StartCPUProfile(f); err != nil {
		f.Close()
		return fmt.Errorf("не удалось запустить CPU профилирование: %w", err)
	}

	p.cpuFile = f
	logger.Info("Profiler: CPU профилирование запущено -> %s", filename)
	return nil
}

// StopCPUProfile останавливает CPU профилирование
func (p *Profiler) StopCPUProfile() error {
	if !p.enabled {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cpuFile == nil {
		return nil // Уже остановлено
	}

	rpprof.StopCPUProfile()
	filename := p.cpuFile.Name()

	if err := p.cpuFile.Close(); err != nil {
		logger.Warn("Profiler: ошибка закрытия файла CPU профиля: %v", err)
	}

	p.cpuFile = nil
	logger.Info("Profiler: CPU профилирование остановлено -> %s", filename)
	return nil
}

// SaveMemoryProfile сохраняет профиль памяти
func (p *Profiler) SaveMemoryProfile() error {
	if !p.enabled {
		return fmt.Errorf("профилирование отключено")
	}

	filename := filepath.Join(p.profilesDir, fmt.Sprintf("mem-%s.prof", time.Now().Format("2006-01-02-15-04-05")))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("не удалось создать файл профиля: %w", err)
	}
	defer f.Close()

	runtime.GC() // Запускаем GC для актуальности данных
	if err := rpprof.WriteHeapProfile(f); err != nil {
		return fmt.Errorf("не удалось записать heap профиль: %w", err)
	}

	logger.Info("Profiler: память профилирована -> %s", filename)
	return nil
}

// SaveGoroutineProfile сохраняет профиль горутин
func (p *Profiler) SaveGoroutineProfile() error {
	if !p.enabled {
		return fmt.Errorf("профилирование отключено")
	}

	filename := filepath.Join(p.profilesDir, fmt.Sprintf("goroutine-%s.prof", time.Now().Format("2006-01-02-15-04-05")))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("не удалось создать файл профиля: %w", err)
	}
	defer f.Close()

	profile := rpprof.Lookup("goroutine")
	if profile == nil {
		return fmt.Errorf("goroutine профиль не найден")
	}

	if err := profile.WriteTo(f, 2); err != nil {
		return fmt.Errorf("не удалось записать goroutine профиль: %w", err)
	}

	logger.Info("Profiler: горутины профилированы -> %s (активных: %d)", filename, runtime.NumGoroutine())
	return nil
}

// SaveBlockProfile сохраняет профиль блокировок
func (p *Profiler) SaveBlockProfile() error {
	if !p.enabled {
		return fmt.Errorf("профилирование отключено")
	}

	runtime.SetBlockProfileRate(1) // Включаем отслеживание блокировок

	filename := filepath.Join(p.profilesDir, fmt.Sprintf("block-%s.prof", time.Now().Format("2006-01-02-15-04-05")))
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("не удалось создать файл профиля: %w", err)
	}
	defer f.Close()

	profile := rpprof.Lookup("block")
	if profile == nil {
		return fmt.Errorf("block профиль не найден")
	}

	if err := profile.WriteTo(f, 0); err != nil {
		return fmt.Errorf("не удалось записать block профиль: %w", err)
	}

	logger.Info("Profiler: блокировки профилированы -> %s", filename)
	return nil
}

// SaveAllProfiles сохраняет все доступные профили
func (p *Profiler) SaveAllProfiles() error {
	if !p.enabled {
		return fmt.Errorf("профилирование отключено")
	}

	logger.Info("Profiler: сохранение всех профилей...")

	if err := p.SaveMemoryProfile(); err != nil {
		logger.Warn("Profiler: ошибка сохранения memory профиля: %v", err)
	}

	if err := p.SaveGoroutineProfile(); err != nil {
		logger.Warn("Profiler: ошибка сохранения goroutine профиля: %v", err)
	}

	if err := p.SaveBlockProfile(); err != nil {
		logger.Warn("Profiler: ошибка сохранения block профиля: %v", err)
	}

	logger.Info("Profiler: все профили сохранены в %s", p.profilesDir)
	return nil
}

// startAutoSave запускает автоматическое сохранение профилей
func (p *Profiler) startAutoSave(interval time.Duration) {
	ctx, cancel := context.WithCancel(p.ctx)
	p.autoSaveFunc = cancel

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		logger.Info("Profiler: автосохранение профилей включено (интервал: %v)", interval)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.SaveAllProfiles(); err != nil {
					logger.Warn("Profiler: ошибка автосохранения профилей: %v", err)
				}
			}
		}
	}()
}

// GetStats возвращает текущую статистику
func (p *Profiler) GetStats() Stats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return Stats{
		Goroutines:      runtime.NumGoroutine(),
		AllocBytes:      m.Alloc,
		TotalAllocBytes: m.TotalAlloc,
		SysBytes:        m.Sys,
		NumGC:           m.NumGC,
		HeapObjects:     m.HeapObjects,
	}
}

// Stats статистика производительности
type Stats struct {
	Goroutines      int    `json:"goroutines"`
	AllocBytes      uint64 `json:"alloc_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	SysBytes        uint64 `json:"sys_bytes"`
	NumGC           uint32 `json:"num_gc"`
	HeapObjects     uint64 `json:"heap_objects"`
}

// Shutdown корректно завершает работу профилировщика
func (p *Profiler) Shutdown() error {
	if !p.enabled {
		return nil
	}

	logger.Info("Profiler: остановка профилировщика...")

	// Останавливаем автосохранение
	if p.autoSaveFunc != nil {
		p.autoSaveFunc()
	}

	// Останавливаем CPU профилирование
	if err := p.StopCPUProfile(); err != nil {
		logger.Warn("Profiler: ошибка остановки CPU профилирования: %v", err)
	}

	// Сохраняем финальные профили
	if err := p.SaveAllProfiles(); err != nil {
		logger.Warn("Profiler: ошибка сохранения финальных профилей: %v", err)
	}

	// Останавливаем pprof сервер
	if p.pprofServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := p.pprofServer.Shutdown(ctx); err != nil {
			logger.Warn("Profiler: ошибка остановки pprof сервера: %v", err)
		}
	}

	if p.cancel != nil {
		p.cancel()
	}

	logger.Info("Profiler: профилировщик остановлен")
	return nil
}
