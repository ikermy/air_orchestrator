package web

import (
	"air_orchestrator/internal/config"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// proxyAvitoRequest godoc
// @Summary Avito API proxy
// @Description Проксирует запросы к Avito Bot API. Обрабатывает как публичные, так и защищённые маршруты.
// @Tags avito
// @Accept json
// @Produce json
// @Param body body object false "Request body"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 502 {object} map[string]string "Ошибка подключения к Avito"
// Публичные маршруты (без авторизации)
// @Router /open/avito/available [get]
// @Router /open/avito/auth/callback [get]
// @Router /open/avito/webhook [post]
// Защищённые маршруты (требуют Bearer token)
// @Router /v1/avito/status [get]
// @Router /v1/avito/auth/url [post]
// @Router /v1/avito/disconnect [post]
// @Router /v1/avito/chats [get]
// @Router /v1/avito/subscriptions [get]
// @Router /v1/avito/subscribe [post]
// @Router /v1/avito/unsubscribe [post]
func (w *Web) proxyAvitoRequest(c *gin.Context) {
	targetURL := config.AvitoBotURL

	// Сохраняем /v1 для защищённых маршрутов: avitobot регистрирует их
	// как /v1/avito/.... Публичные маршруты /open/... проксируем без
	// внутреннего префикса /open.
	path := c.Request.URL.Path
	path = strings.TrimPrefix(path, "/open")
	// Формируем URL для проксирования
	proxyURL := targetURL + path

	// Копируем query параметры из оригинального запроса
	queryParams := c.Request.URL.Query()

	// Если в контексте есть userId (установлен middleware), добавляем uid
	if userId, exists := c.Get("userId"); exists {
		uid := userId.(uint32)
		queryParams.Set("uid", fmt.Sprintf("%d", uid))
	}

	// Формируем строку query параметров
	queryString := queryParams.Encode()
	if queryString != "" {
		proxyURL += "?" + queryString
	}

	// Читаем тело запроса если есть
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	// Создаем новый HTTP запрос
	req, err := http.NewRequestWithContext(w.ctx, c.Request.Method, proxyURL, bytes.NewReader(bodyBytes))
	if err != nil {
		logger.Error("proxyAvitoRequest: ошибка создания запроса - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	// Копируем заголовки из оригинального запроса
	for key, values := range c.Request.Header {
		// Пропускаем заголовки, которые будут установлены автоматически
		if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Выполняем запрос
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		logger.Error("proxyAvitoRequest: ошибка при выполнении запроса - %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к сервису Avito"})
		return
	}
	defer closeResponseBody(resp.Body, "proxyAvitoRequest")

	// ВАЖНО: Если Avito сервер вернул редирект, перенаправляем браузер
	// Это критично для OAuth callback - сервер возвращает 302 с Location
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusTemporaryRedirect || resp.StatusCode == http.StatusSeeOther {
		location := resp.Header.Get("Location")
		if location != "" {
			logger.Debug("proxyAvitoRequest: перенаправление на %s (статус %d)", location, resp.StatusCode)
			c.Redirect(resp.StatusCode, location)
			return
		}
		logger.Error("proxyAvitoRequest: получен статус редиректа %d, но Location отсутствует", resp.StatusCode)
	}

	// Читаем ответ
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("proxyAvitoRequest: ошибка чтения ответа - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	// Логируем только ошибки (кроме ожидаемых 400/404 для маршрутов статуса)
	if resp.StatusCode >= 400 {
		// Не логируем 400/404 для маршрутов статуса/available - это нормально когда сервис не запущен или не подключен
		if !((resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusNotFound) &&
			(strings.Contains(path, "/status") || strings.Contains(path, "/available"))) {
			logger.Error("proxyAvitoRequest: сервис Avito вернул ошибку %d для %s %s: %s",
				resp.StatusCode, c.Request.Method, proxyURL, string(respBody))
		}
	}

	// Копируем заголовки ответа, КРОМЕ CORS (их управляет Landing middleware)
	for key, values := range resp.Header {
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
