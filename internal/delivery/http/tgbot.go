package web

import (
	"air_orchestrator/internal/config"
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// proxyTgBotWebhook godoc
// @Summary Проксировать Telegram webhook
// @Tags tgbot
// @Router /open/tgbot/webhook [post]
//
// proxyTgBotWebhook проксирует Telegram webhook-обновления на внутренний сервис tgbot.
// Важно: сохраняет тело запроса без изменений и не добавляет лишних таймаутов для webhook.
func (w *Web) proxyTgBotWebhook(c *gin.Context) {
	w.proxyTgBotRequest(c, 5*time.Second)
}

// proxyTgBotSetWebhook godoc
// @Summary Настроить Telegram webhook
// @Tags tgbot
// @Router /open/tgbot/setwebhook [post]
//
// proxyTgBotSetWebhook проксирует запросы на привязку webhook к внутреннему сервису tgbot.
func (w *Web) proxyTgBotSetWebhook(c *gin.Context) {
	w.proxyTgBotRequest(c, 10*time.Second)
}

func (w *Web) proxyTgBotRequest(c *gin.Context, timeout time.Duration) {
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	proxyURL := strings.TrimRight(config.TgBotURL, "/") + c.Request.URL.RequestURI()
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, proxyURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		logger.Error("proxyTgBotRequest: ошибка создания запроса к tgbot: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Host = c.Request.Host

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("proxyTgBotRequest: ошибка при проксировании запроса к tgbot: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к сервису tgbot"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("proxyTgBotRequest: ошибка закрытия response body: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("proxyTgBotRequest: ошибка чтения ответа от tgbot: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	for key, values := range resp.Header {
		if strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Connection") {
			continue
		}
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}
