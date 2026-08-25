package web

import (
	"air_orchestrator/internal/config"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// CRMAuthMiddleware перемещён в middleware.go

// proxyCRMPublicRequest godoc
// @Summary Проверить здоровье CRM (публичный)
// @Tags crm
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 502 {object} map[string]string
// @Router /open/crm/health [get]
func (w *Web) proxyCRMPublicRequest(c *gin.Context) {
	var targetURL string
	var client *http.Client

	targetURL = config.CrmServiceURL
	client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Формируем URL для проксирования - убираем префикс /crm
	path := strings.TrimPrefix(c.Request.URL.Path, "/crm")
	proxyURL := targetURL + path
	if c.Request.URL.RawQuery != "" {
		proxyURL += "?" + c.Request.URL.RawQuery
	}

	// Читаем тело запроса если есть
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
	}

	// Создаем новый запрос
	req, err := http.NewRequestWithContext(w.ctx, c.Request.Method, proxyURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	// Копируем заголовки
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Ошибка при проксировании публичного запроса к CRM: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к CRM сервису"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в proxyPublicCRMRequest: %v", err)
		}
	}()

	// Читаем ответ
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	// Копируем заголовки ответа, КРОМЕ CORS (их управляет Landing middleware)
	for key, values := range resp.Header {
		// Пропускаем все CORS заголовки
		if strings.HasPrefix(strings.ToLower(key), "access-control-") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	// Отправляем ответ клиенту
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// proxyAmoCRMOAuthCallback godoc
// @Summary OAuth callback для AmoCRM
// @Tags crm
// @Produce json
// @Param code query string true "Authorization code"
// @Success 200 {object} map[string]any
// @Failure 502 {object} map[string]string
// @Router /open/crm/oauth/amocrm/callback [get]
func (w *Web) proxyAmoCRMOAuthCallback(c *gin.Context) {
	state := c.Query("state")
	if state == "" {
		logger.Error("proxyAmoCRMOAuthCallback: отсутствует параметр state")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Отсутствует параметр state"})
		return
	}

	// State имеет формат "userID_timestamp" - извлекаем userID
	parts := strings.Split(state, "_")
	if len(parts) < 2 {
		logger.Error("proxyAmoCRMOAuthCallback: неверный формат state: %s", state)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Неверный формат state"})
		return
	}
	userIDStr := parts[0]

	// Формируем URL для проксирования на CRM
	targetURL := config.CrmServiceURL + "/oauth/amocrm/callback"
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Создаем GET запрос
	req, err := http.NewRequestWithContext(w.ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		logger.Error("proxyAmoCRMOAuthCallback: ошибка создания запроса - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	// Копируем заголовки
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// ВАЖНО: Передаем userID извлеченный из state в заголовке X-User-ID
	req.Header.Set("X-User-ID", userIDStr)

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("proxyAmoCRMOAuthCallback: ошибка выполнения запроса - %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к CRM сервису"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в proxyAmoCRMOAuthCallback: %v", err)
		}
	}()

	// Если CRM вернул редирект, перенаправляем браузер
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		location := resp.Header.Get("Location")
		if location != "" {
			c.Redirect(resp.StatusCode, location)
			return
		}
		logger.Error("proxyAmoCRMOAuthCallback: получен статус редиректа %d, но Location отсутствует", resp.StatusCode)
	}

	// Читаем ответ
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("proxyAmoCRMOAuthCallback: ошибка чтения ответа - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	// Копируем заголовки ответа, КРОМЕ CORS
	for key, values := range resp.Header {
		if strings.HasPrefix(strings.ToLower(key), "access-control-") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	// Проксируем ответ от CRM без изменений
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// proxyCRMRequest godoc
// @Summary CRM API proxy (требует авторизации)
// @Tags crm
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param path path string true "CRM API path"
// @Param body body object false "Request body"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /crm/api/{path} [post]
func (w *Web) proxyCRMRequest(c *gin.Context) {
	var targetURL string
	var client *http.Client

	targetURL = config.CrmServiceURL
	client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Убираем префикс /crm/api для CRM сервиса
	path := strings.TrimPrefix(c.Request.URL.Path, "/crm/api")
	proxyURL := targetURL + path
	if c.Request.URL.RawQuery != "" {
		proxyURL += "?" + c.Request.URL.RawQuery
	}

	// Читаем тело запроса
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
		// ⚠️ ВАЖНО: Восстанавливаем тело для последующих middleware
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	// Создаем новый запрос с КОПИЕЙ тела
	req, err := http.NewRequestWithContext(w.ctx, c.Request.Method, proxyURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		logger.Error("proxyCRMRequest: ошибка создания запроса - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	// Копируем необходимые заголовки
	req.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	req.Header.Set("Authorization", c.GetHeader("Authorization"))

	// Передаем userID из токена (используем "user_id" для совместимости с CRM)
	userID, exists := getUserID(c)
	if exists {
		req.Header.Set("X-User-ID", fmt.Sprintf("%d", userID))
	} else {
		logger.Error("proxyCRMRequest: user_id не найден в контексте для %s %s", c.Request.Method, proxyURL)
	}

	// Выполняем запос
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("proxyCRMRequest: ошибка при выполнении запроса - %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к CRM сервису"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в proxyCRMRequest: %v", err)
		}
	}()

	// Читаем ответ
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("proxyCRMRequest: ошибка чтения ответа - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	// Логируем только ошибки
	if resp.StatusCode >= 400 {
		logger.Error("proxyCRMRequest: CRM вернул ошибку %d для %s %s: %s", resp.StatusCode, c.Request.Method, proxyURL, string(respBody))
	}

	// Копируем заголовки ответа, КРОМЕ CORS (их управляет Landing middleware)
	for key, values := range resp.Header {
		// Пропускаем все CORS заголовки
		if strings.HasPrefix(strings.ToLower(key), "access-control-") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	// Отправляем ответ клиенту
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}
