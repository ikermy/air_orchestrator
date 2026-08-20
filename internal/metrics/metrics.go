// Package metrics содержит Prometheus-метрики приложения AiR ORCHESTRATOR.
// Все метрики регистрируются в DefaultRegisterer при init() и доступны
// через стандартный обработчик promhttp.Handler().
package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ─── HTTP метрики ────────────────────────────────────────────────────────────

var (
	// HTTPRequestsTotal — счётчик всех HTTP-запросов.
	// Лейблы: method, path (нормализованный), status_code.
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Общее количество HTTP-запросов.",
		},
		[]string{"method", "path", "status_code"},
	)

	// HTTPRequestDuration — гистограмма времени обработки запросов.
	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "airorc",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Длительность обработки HTTP-запросов в секундах.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

// ─── Storage метрики ─────────────────────────────────────────────────────────

var (
	// StorageHealthy — 1 когда ReservationService и Redis доступны, 0 при деградации.
	StorageHealthy = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "airorc",
		Subsystem: "storage",
		Name:      "healthy",
		Help:      "Состояние хранилища: 1 — работает, 0 — деградировано (Redis недоступен).",
	})

	// StorageReservationTotal — счётчик попыток резервирования.
	StorageReservationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "storage",
			Name:      "reservation_total",
			Help:      "Количество попыток резервирования места под загрузку.",
		},
		[]string{"result"}, // ok | degraded | quota_exceeded | error
	)

	// StorageCommitTotal — счётчик успешных/неуспешных коммитов.
	StorageCommitTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "storage",
			Name:      "commit_total",
			Help:      "Количество операций подтверждения загрузки.",
		},
		[]string{"result"}, // ok | expired | not_found | error
	)

	// StorageSweepTotal — счётчик очищенных просроченных резервирований.
	StorageSweepTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "airorc",
		Subsystem: "storage",
		Name:      "sweep_total",
		Help:      "Количество просроченных резервирований, очищенных sweep-процессом.",
	})

	// StorageReleaseTotal — счётчик освобождённых резервирований.
	StorageReleaseTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "airorc",
		Subsystem: "storage",
		Name:      "release_total",
		Help:      "Количество освобождённых резервирований (при ошибке загрузки или отмене).",
	})

	// StorageSessionTotal — счётчик созданных STS-сессий.
	StorageSessionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "storage",
			Name:      "session_total",
			Help:      "Количество созданных storage-сессий.",
		},
		[]string{"mode"}, // sts | presigned
	)
)

var (
	// ActiveWSSessions — текущее число открытых WebSocket-сессий.
	ActiveWSSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "airorc",
		Subsystem: "ws",
		Name:      "active_sessions",
		Help:      "Текущее количество активных WebSocket-сессий.",
	})

	// ActiveTestSessions — текущее число активных тестовых AI-сессий.
	ActiveTestSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "airorc",
		Subsystem: "api",
		Name:      "active_test_sessions",
		Help:      "Текущее количество активных тестовых сессий AI-модели.",
	})

	// TODO сделать для них декоратор и подключить для использования
	// DBQueriesTotal — счётчик SQL-запросов с лейблом операции.
	DBQueriesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "db",
			Name:      "queries_total",
			Help:      "Общее количество SQL-запросов к БД.",
		},
		[]string{"operation"},
	)

	// DBQueryDuration — гистограмма длительности SQL-запросов.
	DBQueryDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "airorc",
			Subsystem: "db",
			Name:      "query_duration_seconds",
			Help:      "Длительность SQL-запросов в секундах.",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		},
		[]string{"operation"},
	)

	// AuthAttemptsTotal — счётчик попыток входа.
	AuthAttemptsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "airorc",
			Subsystem: "auth",
			Name:      "attempts_total",
			Help:      "Количество попыток аутентификации.",
		},
		[]string{"result"}, // success | failure
	)

	// RegistrationsTotal — счётчик новых регистраций.
	RegistrationsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "airorc",
		Subsystem: "auth",
		Name:      "registrations_total",
		Help:      "Общее количество новых регистраций пользователей.",
	})
)

// ─── Middleware ───────────────────────────────────────────────────────────────

// PrometheusMiddleware — gin-middleware для автоматической записи HTTP-метрик.
func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		isWebSocket := websocket.IsWebSocketUpgrade(c.Request)
		if isWebSocket {
			ActiveWSSessions.Inc()
			defer ActiveWSSessions.Dec()
		}

		// Пропускаем саму ручку /metrics чтобы не засорять данные
		if c.Request.URL.Path == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		path := c.FullPath() // нормализованный путь с параметрами как ":id"
		if path == "" {
			path = "unknown"
		}

		HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}

// Handler возвращает http.Handler для ручки GET /metrics.
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}
