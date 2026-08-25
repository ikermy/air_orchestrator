package profiler

import (
	"fmt"
	"runtime"
	"time"

	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// Analyzer анализатор производительности
type Analyzer struct {
	profiler *Profiler
}

// NewAnalyzer создает новый анализатор
func NewAnalyzer(p *Profiler) *Analyzer {
	return &Analyzer{profiler: p}
}

// LogMemoryStats логирует текущее состояние памяти
func (a *Analyzer) LogMemoryStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	logger.Info("📊 Memory Stats:")
	logger.Info("  Alloc       = %v MB", bToMb(m.Alloc))
	logger.Info("  TotalAlloc  = %v MB", bToMb(m.TotalAlloc))
	logger.Info("  Sys         = %v MB", bToMb(m.Sys))
	logger.Info("  NumGC       = %v", m.NumGC)
	logger.Info("  HeapObjects = %v", m.HeapObjects)
	logger.Info("  Goroutines  = %v", runtime.NumGoroutine())
}

// LogGoroutineStats логирует информацию о горутинах
func (a *Analyzer) LogGoroutineStats() {
	numGoroutines := runtime.NumGoroutine()
	logger.Info("🔄 Goroutines: %d активных", numGoroutines)

	if numGoroutines > 1000 {
		logger.Warn("⚠️  Большое количество горутин (%d) - возможна утечка!", numGoroutines)
	}
}

// AnalyzePerformance выполняет комплексный анализ производительности
func (a *Analyzer) AnalyzePerformance() {
	logger.Info("=== Анализ производительности ===")

	a.LogMemoryStats()
	a.LogGoroutineStats()

	// Проверяем потенциальные проблемы
	stats := a.profiler.GetStats()

	// Проверка утечки памяти
	allocMB := bToMb(stats.AllocBytes)
	if allocMB > 500 {
		logger.Warn("⚠️  Высокое потребление памяти: %d MB", allocMB)
	}

	// Проверка горутин
	if stats.Goroutines > 1000 {
		logger.Warn("⚠️  Аномальное количество горутин: %d", stats.Goroutines)
		if err := a.profiler.SaveGoroutineProfile(); err != nil {
			logger.Error("Ошибка сохранения goroutine профиля: %v", err)
		}
	}

	logger.Info("=================================")
}

// MeasureFunc измеряет время выполнения функции
func MeasureFunc(name string, fn func()) time.Duration {
	start := time.Now()
	fn()
	duration := time.Since(start)

	if duration > time.Second {
		logger.Warn("⚠️  Медленная функция '%s': %v", name, duration)
	} else {
		logger.Debug("⏱️  Функция '%s' выполнена за: %v", name, duration)
	}

	return duration
}

// MeasureFuncWithResult измеряет время выполнения функции с возвратом значения
func MeasureFuncWithResult[T any](name string, fn func() T) (T, time.Duration) {
	start := time.Now()
	result := fn()
	duration := time.Since(start)

	if duration > time.Second {
		logger.Warn("⚠️  Медленная функция '%s': %v", name, duration)
	} else {
		logger.Debug("⏱️  Функция '%s' выполнена за: %v", name, duration)
	}

	return result, duration
}

// bToMb конвертирует байты в мегабайты
func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}

// FormatBytes форматирует байты в читаемый формат
func FormatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
