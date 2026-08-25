package profiler

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// HTTPTracingMiddleware middleware для трассировки HTTP запросов
func HTTPTracingMiddleware(slowThreshold time.Duration) gin.HandlerFunc {
	if slowThreshold == 0 {
		slowThreshold = 1 * time.Second // По умолчанию 1 секунда
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		// Обрабатываем запрос
		c.Next()

		// Вычисляем время выполнения
		duration := time.Since(start)
		statusCode := c.Writer.Status()

		// Логируем только медленные запросы или ошибки
		if duration > slowThreshold {
			logger.Warn("🐌 Медленный запрос: %s %s | Status: %d | Duration: %v",
				method, path, statusCode, duration)
		} else if statusCode >= 400 {
			logger.Warn("❌ Ошибка запроса: %s %s | Status: %d | Duration: %v",
				method, path, statusCode, duration)
		} else {
			logger.Debug("✅ %s %s | Status: %d | Duration: %v",
				method, path, statusCode, duration)
		}
	}
}

// DetailedHTTPTracingMiddleware расширенное middleware с детальной информацией
func DetailedHTTPTracingMiddleware(slowThreshold time.Duration) gin.HandlerFunc {
	if slowThreshold == 0 {
		slowThreshold = 1 * time.Second
	}

	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method
		clientIP := c.ClientIP()

		// Обрабатываем запрос
		c.Next()

		// Вычисляем время выполнения
		duration := time.Since(start)
		statusCode := c.Writer.Status()
		bodySize := c.Writer.Size()

		// Формируем детальное сообщение
		if duration > slowThreshold {
			logger.Warn("🐌 Медленный запрос | Method: %s | Path: %s | Status: %d | Duration: %v | IP: %s | Size: %d bytes",
				method, path, statusCode, duration, clientIP, bodySize)
		} else if statusCode >= 400 {
			logger.Warn("❌ Ошибка запроса | Method: %s | Path: %s | Status: %d | Duration: %v | IP: %s",
				method, path, statusCode, duration, clientIP)
		}
	}
}
