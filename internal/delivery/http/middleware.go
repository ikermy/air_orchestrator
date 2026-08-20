package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// extractToken извлекает токен из запроса в соответствии с RFC 6750 и WebSocket best practices
// Приоритет: Authorization Bearer header > Sec-WebSocket-Protocol > query parameter
// Для WebSocket проверяет Sec-WebSocket-Protocol header, затем query parameter
func extractToken(c *gin.Context) string {
	// Для WebSocket токен может быть в Sec-WebSocket-Protocol или query
	if websocket.IsWebSocketUpgrade(c.Request) {
		// Проверяем Sec-WebSocket-Protocol header (браузерные WebSocket API поддерживают subprotocols)
		protocols := c.Request.Header.Get("Sec-WebSocket-Protocol")
		if protocols != "" {
			// Токен может быть передан как subprotocol
			// Формат: new WebSocket(url, [token]) или new WebSocket(url, token)
			parts := strings.Split(protocols, ",")
			for _, protocol := range parts {
				token := strings.TrimSpace(protocol)
				if token != "" {
					return token
				}
			}
		}

		// Fallback для WebSocket: токен в query
		return c.Query("token")
	}

	// Для обычных HTTP запросов проверяем Authorization header (RFC 6750)
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		// Ожидаем формат: "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	// Fallback: токен в query parameter (для обратной совместимости)
	return c.Query("token")
}

// getFullEndpoint собирает полный путь запроса с методом и параметрами
func (w *Web) getFullEndpoint(c *gin.Context) string {
	requestPath := c.Request.URL.Path
	requestMethod := c.Request.Method
	queryParams := c.Request.URL.RawQuery

	fullEndpoint := requestMethod + " " + requestPath
	if queryParams != "" {
		fullEndpoint += "?" + queryParams
	}
	return fullEndpoint
}

// performRateLimit выполняет проверку лимита и логирует ошибку при превышении
func (w *Web) performRateLimit(c *gin.Context, respId uint64) bool {
	if !w.Allow(respId) {
		fullEndpoint := w.getFullEndpoint(c)
		logger.Error("'authAllowMiddleware' Лимит запросов превышен для respId=%d на маршруте: %s",
			respId, fullEndpoint)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Rate limit exceeded"})
		c.Abort()
		return false
	}
	return true
}

// verifyTokenAndCheckRateLimit проверяет токен и rate limit, устанавливает userId и respId в контексте
// Возвращает true если проверка прошла успешно, false если нужно прервать выполнение запроса
func (w *Web) verifyTokenAndCheckRateLimit(c *gin.Context, token string) bool {
	if token == "" {
		logger.Error("'authAllowMiddleware' Токен не предоставлен")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token required"})
		c.Abort()
		return false
	}

	userId, respId, err := w.exam.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		c.Abort()
		return false
	}

	c.Set("userId", userId)
	c.Set("respId", respId)

	return w.performRateLimit(c, respId)
}

// authAllowMiddleware проверяет лимиты запросов для респондера
// Поддерживает RFC 6750 (Authorization: Bearer <token>) для HTTP-запросов
// Для WebSocket использует query-параметр (браузерное ограничение)
// Для GET token в Authorization-header или query-параметре
// Для POST и DELETE без path-параметров проверка пропускается (будет выполнена после SetUserAuth)
// Для GET и DELETE с path-параметрами проверяем сразу
// Для POST с path-параметрами можно тоже сразу проверять (токен уже в query)
// Для POST без path-параметров проверяем token в Authorization-header или query (если есть), иначе отложенная проверка
func (w *Web) authAllowMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractToken(c)

		// Кейс 1: POST без параметров пути и БЕЗ токена в заголовке/query
		// Подразумевает, что токен придет в теле запроса (отложенная проверка в хендлере через SetUserAuth)
		if c.Request.Method == http.MethodPost && len(c.Params) == 0 && token == "" {
			c.Set("rateLimitCheck", true)
			c.Next()
			return
		}

		// Кейс 2: Все остальные запросы (GET, DELETE, POST с токеном или вложенным путем)
		// Выполняем немедленную проверку токена и rate limit.
		if !w.verifyTokenAndCheckRateLimit(c, token) {
			return
		}

		// Извлекаем провайдера из query-параметров для всех типов запросов (если он есть)
		if providerParam := c.Query("provider"); providerParam != "" {
			provider, err := commdom.FromString(providerParam)
			if err != nil {
				logger.Error("'authAllowMiddleware' Ошибка получения провайдера: %v", err)
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				c.Abort()
				return
			}
			c.Set("provider", provider)
		}

		c.Next()
	}
}

// ─── Вспомогательные функции ──────────────────────────────────────────────────

// getUserID извлекает userId из контекста Gin.
// Если userId не найден — записывает 401 Unauthorized и возвращает (0, false).
// Используется во всех хендлерах вместо дублирующегося boilerplate.
func getUserID(c *gin.Context) (uint32, bool) {
	val, exists := c.Get("userId")
	if !exists {
		// Для обратной совместимости с CRM-мидлварью пробуем "user_id"
		val, exists = c.Get("user_id")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return 0, false
		}
	}
	userId, ok := val.(uint32)
	if !ok || userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return 0, false
	}
	return userId, true
}

// getRespId извлекает respId из контекста Gin.
// Если respId не найден — записывает 401 Unauthorized и возвращает (0, false).
func getRespId(c *gin.Context) (uint64, bool) {
	val, exists := c.Get("respId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "respId required"})
		return 0, false
	}
	respId, ok := val.(uint64)
	if !ok || respId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid respId"})
		return 0, false
	}
	return respId, true
}

// CRMAuthMiddleware проверяет Bearer-токен для защищённых CRM-эндпоинтов.
// Перемещено из crm.go в middleware.go для централизации auth-мидлварей.
func (w *Web) CRMAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Отсутствует токен авторизации"})
			c.Abort()
			return
		}

		tokenString = strings.TrimPrefix(tokenString, "Bearer ")

		userID, respID, err := w.exam.VerifyAccessToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Недействительный токен"})
			c.Abort()
			return
		}

		if !w.performRateLimit(c, respID) {
			return
		}

		// Используем "user_id" для совместимости с CRM-сервисом
		c.Set("user_id", userID)
		c.Set("userId", userID)
		c.Set("respId", respID)
		c.Next()
	}
}

// devOnlyMiddleware проверяет, является ли пользователь разработчиком.
// Требует предварительного выполнения authAllowMiddleware для наличия userId в контексте.
func (w *Web) devOnlyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := getUserID(c)
		if !ok {
			return
		}

		isDev, err := w.adminUC.IsDevUser(userID)
		if err != nil {
			logger.Error("'devOnlyMiddleware' Ошибка проверки dev-статуса пользователя %d: %v", userID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			c.Abort()
			return
		}

		if !isDev {
			logger.Warn("'devOnlyMiddleware' Попытка доступа к dev-эндпоинту не-dev пользователем %d", userID)
			c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden: Developer access required"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// SetUserAuth устанавливает userId и respId в контексте после верификации токена из POST/DELETE body
func (w *Web) SetUserAuth(c *gin.Context, token string) (uint32, error) {
	userId, respId, err := w.exam.VerifyAccessToken(token)
	if err != nil {
		logger.Error("'SetUserAuth' ошибка парсинга токена: %v", err)
		return 0, errors.New("invalid token")
	}

	// Сохраняем userId и respId в контексте
	c.Set("userId", userId)
	c.Set("respId", respId)

	// Проверка rate limit если был установлен
	if _, ok := c.Get("rateLimitCheck"); ok {
		if !w.performRateLimit(c, respId) {
			return 0, errors.New("rate limited")
		}
	}

	return userId, nil
}
