package web

import (
	"air_orchestrator/internal/config"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// SaveChannel godoc
// @Summary Сохранить данные канала
// @Tags channel
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Данные канала"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /channel [post]
func (w *Web) SaveChannel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Type    string `json:"type"` // "tgbot", "widget", "tgubot", "whatsbot"
		Data    string `json:"data"`
		Enabled bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SaveChannel' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.SaveChannelData(userId, requestData.Type, requestData.Data, requestData.Enabled)
	if err != nil {
		logger.Error("'SaveChannel' Ошибка при сохранении данных канала: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Сразу отправляю запрос на включение/выключение в соответствующий сервис
	go w.stopChannel(requestData.Type, userId, requestData.Enabled)

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

// ReadChannel godoc
// @Summary Читать данные канала
// @Tags channel
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /channel [get]
func (w *Web) ReadChannel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем каналы пользователя
	channels, err := w.db.GetChannelsData(userId)
	if err != nil {
		logger.Error("'ReadChannel' Ошибка получения данных каналов: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Форматируем JSON для красивого вывода
	var prettyJSON bytes.Buffer
	err = json.Indent(&prettyJSON, channels, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, json.RawMessage(prettyJSON.Bytes()))
}

// DeleteChannel godoc
// @Summary Удалить канал
// @Tags channel
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Тип канала"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /channel [delete]
func (w *Web) DeleteChannel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Type string `json:"type"` // "tgbot" или "widget"
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'DeleteChannel' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.DeleteChannelData(userId, requestData.Type)
	if err != nil {
		logger.Error("'DeleteChannel' Ошибка при удалении данных канала: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Отключаем канал в соответствующем сервисе
	err = w.disableChannels(userId, requestData.Type)
	if err != nil {
		logger.Error("'DeleteChannel' Ошибка при отключении канала: %v", err, userId)
		// Не возвращаем ошибку клиенту, т.к. данные в БД уже удалены
	}

	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}

func (w *Web) disableChannels(userId uint32, channel string) error {
	var url string
	switch channel {
	case "tgbot":
		url = fmt.Sprintf("%s/tgbot/disable?uid=%d", config.TgBotURL, userId)
	case "tgubot", "tguserbot":
		url = fmt.Sprintf("%s/tguser/disable?uid=%d", config.TgUserBotURL, userId)
	case "whats", "whatsbot":
		url = fmt.Sprintf("%s/whats/disable?uid=%d", config.WhatsBotURL, userId)
	case "widg", "widget":
		url = fmt.Sprintf("%s/widget/disable?uid=%d", config.WidgetBotURL, userId)
	case "avito":
		url = fmt.Sprintf("%s/avito/disable?uid=%d", config.AvitoBotURL, userId)
	//case "insta":
	//	port = w.conf.WEB.Insta
	//	channelPath = "insta"
	default:
		return fmt.Errorf("invalid channel name: %s", channel)
	}

	// Проверяем что port не пустой
	if url == "" {
		return fmt.Errorf("url is empty for channel %s", channel)
	}

	respCtx, cancel := context.WithTimeout(context.Background(), config.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(respCtx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("disableChannels Ошибка при создании запроса: %v", err)
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("disableChannels Ошибка при выполнении запроса: %v", err)
		return err
	}

	defer func() {
		if resp.Body != nil {
			err = resp.Body.Close()
			if err != nil {
				logger.Error("disableChannels Ошибка при закрытии тела ответа: %v", err)
			}
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("некорректный статус ответа: %d", resp.StatusCode)
	}

	return nil
}

// availableHandler godoc
// @Summary Проверка доступности сервиса
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param ch_type path string true "Тип канала"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /channel/available/{ch_type} [get]
// availableHandler проверяет доступность сервиса канала по его типу
func (w *Web) availableHandler(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	chType := c.Param("ch_type")
	if chType == "" {
		logger.Error("'availableHandler' Не указан тип канала", userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Channel type required"})
		return
	}

	var url string
	switch chType {
	case "tgbot":
		url = fmt.Sprintf("%s/%s/available", config.TgBotURL, chType)
	case "tguser":
		url = fmt.Sprintf("%s/%s/available", config.TgUserBotURL, chType)
	case "whats":
		url = fmt.Sprintf("%s/%s/available", config.WhatsBotURL, chType)
	case "widget":
		url = fmt.Sprintf("%s/%s/available", config.WidgetBotURL, chType)
	case "oper":
		url = fmt.Sprintf("%s/%s/available", config.OPERURL, chType)
	case "crm":
		url = fmt.Sprintf("%s/%s/available", config.CRMURL, chType)
	case "pay":
		url = fmt.Sprintf("%s/%s/available", config.PAYURL, chType)
	case "avito":
		url = fmt.Sprintf("%s/%s/available", config.AvitoBotURL, chType)
	case "insta":
		url = fmt.Sprintf("%s/%s/available", config.InstaBotURL, chType)
	default:
		logger.Error("'availableHandler' Неизвестный тип канала: %s", chType, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown channel type"})
		return
	}

	respCtx, cancel := context.WithTimeout(w.ctx, 2*time.Second)
	defer cancel()
	resp, err := sendRESP(respCtx, http.MethodGet, url, nil)
	if err != nil {
		logger.Debug("sendRESP %s вернул ошибку: %v", chType, err, userId)
		c.Status(http.StatusServiceUnavailable)
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body: %v", err, userId)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("Сервис %s вернул статус: %d", chType, resp.StatusCode, userId)
		c.Status(http.StatusServiceUnavailable)
		return
	}

	c.JSON(http.StatusOK, gin.H{"available": true})
}

// GetChannelName godoc
// @Summary Получить имя канала
// @Tags channel
// @Produce json
// @Security BearerAuth
// @Param name query string true "Имя канала"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /channel/name [get]
func (w *Web) GetChannelName(c *gin.Context) {
	chName := c.Query("name")
	//logger.Warn("name %s", chName)
	if chName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing name"})
		return
	}

	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем каналы пользователя
	var url string
	switch chName {
	case "tbot":
		url = fmt.Sprintf("%s/tgbot/getname", config.TgBotURL)
	case "tguserbot":
		url = fmt.Sprintf("%s/tguser/getname", config.TgUserBotURL)
	case "whatsbot":
		url = fmt.Sprintf("%s/whats/getname", config.WhatsBotURL)
	case "insta":
		url = fmt.Sprintf("%s/insta/getname", config.InstaBotURL)
	case "widg":
		url = fmt.Sprintf("%s/widget/getname", config.WidgetBotURL)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid channel name"})
		return
	}

	// Добавляем uid как query-параметр
	url = fmt.Sprintf("%s?uid=%d", url, userId)
	respCtx, cancel := context.WithTimeout(context.Background(), config.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(respCtx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("Ошибка при создании запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("Ошибка при выполнении запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	defer func() {
		err = resp.Body.Close()
		if err != nil {
			logger.Error("Ошибка при закрытии тела ответа: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		logger.Error("Некорректный статус ответа: %d", resp.StatusCode)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid response from channel service"})
		return
	}

	// Отправляю название канала обратно клиенту
	var respData struct {
		BotName string `json:"bot_name"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		logger.Error("Ошибка при декодировании ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"name": respData.BotName})
}

// RestartChannels godoc
// @Summary Перезапустить активные каналы
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/restart [get]
func (w *Web) RestartChannels(c *gin.Context) {
	uid, ok := c.Get("userId")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userId := uid.(uint32)

	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err = conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения: %v", err)
		}
	}()

	w.RestartActiveServicesWSS(conn, userId)
}

func (w *Web) RestartActiveServicesWSS(conn *websocket.Conn, userID uint32) {
	// Создаем callback функцию для отправки сообщений
	progressCallback := func(message string) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
	}

	progressCallback("Начинаю перезапуск активных сервисов...")

	activeChannels, err := w.db.GetActiveChannels(userID)
	if err != nil {
		logger.Error("'RestartActiveServicesWSS' Ошибка: %v", err, userID)
		progressCallback("❌ Ошибка получения активных каналов")
		return
	}

	for _, chType := range activeChannels {
		err = w.restartChannel(chType, userID, progressCallback)
		if err != nil {
			logger.Error("Ошибка при перезапуске канала: %v", err, userID)
			errorMsg := fmt.Sprintf("Ошибка при перезапуске канала: %v", err)
			if err = conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); err != nil {
				logger.Error("Ошибка отправки WebSocket сообщения об ошибке: %v", err)
			}

			continue
		}
	}

	progressCallback("Все активные сервисы успешно перезапущены.")
	if err = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		logger.Error("Ошибка отправки WebSocket close message: %v", err)
	}
}

func (w *Web) restartChannel(channel string, userId uint32, progressCallback func(string)) error {
	var url, realName string
	switch channel {
	case "tgbot":
		url = fmt.Sprintf("%s/%s/restart?uid=%d", config.TgBotURL, channel, userId)
		realName = "Telegram Бот"
	case "tguserbot":
		url = fmt.Sprintf("%s/%s/restart?uid=%d", config.TgUserBotURL, channel, userId)
		realName = "Telegram UserBot"
	case "whatsbot":
		url = fmt.Sprintf("%s/%s/restart?uid=%d", config.WhatsBotURL, channel, userId)
		realName = "WhatsApp Бот"
	case "widget":
		url = fmt.Sprintf("%s/%s/restart?uid=%d", config.WidgetBotURL, channel, userId)
		realName = "Виджет на сайте"
	case "insta":
		url = fmt.Sprintf("%s/%s/restart?uid=%d", config.InstaBotURL, channel, userId)
		realName = "Instagram Бот"
	default:
		return fmt.Errorf("unknown channels type: %s", channel)
	}

	if progressCallback != nil {
		progressCallback("Начинаю перезапуск канала " + realName + "...")
	}

	respCtx, cancel := context.WithTimeout(w.ctx, 10*time.Second)
	defer cancel()

	resp, err := sendRESP(respCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("error restarting channel %s: %v", channel, err)
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body при перезапуске канала %s: %v", channel, err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("channel %s restart returned status: %d", channel, resp.StatusCode)
	}

	if progressCallback != nil {
		progressCallback("Канал " + realName + " успешно перезапущен.")
	}

	return nil
}

func (w *Web) stopChannel(channel string, userId uint32, start bool) {
	metod := "enable"
	if !start {
		metod = "disable"
	}

	var url string
	switch channel {
	case "tgbot":
		url = fmt.Sprintf("%s/tgbot/%s?uid=%d", config.TgBotURL, metod, userId)

	case "tgubot":
		url = fmt.Sprintf("%s/tguser/%s?uid=%d", config.TgUserBotURL, metod, userId)

	case "whatsbot":
		url = fmt.Sprintf("%s/whats/%s?uid=%d", config.WhatsBotURL, metod, userId)

	case "widg", "widget":
		url = fmt.Sprintf("%s/widget/%s?uid=%d", config.WidgetBotURL, metod, userId)

	case "insta":
		url = fmt.Sprintf("%s/insta/%s?uid=%d", config.InstaBotURL, metod, userId)

	default:
		logger.Warn("unknown channels type: %s", channel)
		return // Выходим, если тип канала неизвестен
	}

	// Проверяем что port не пустой
	if url == "" {
		logger.Error("url is empty for channel %s", channel)
		return
	}

	respCtx, cancel := context.WithTimeout(w.ctx, 2*time.Second)
	defer cancel()

	resp, err := sendRESP(respCtx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("error disable channel %s: %v", channel, err)
		return // Выходим при ошибке запроса
	}

	// Безопасное закрытие response body
	if resp != nil && resp.Body != nil {
		defer func() {
			if err = resp.Body.Close(); err != nil {
				logger.Error("Ошибка закрытия response body при отключении канала %s: %v", channel, err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			logger.Error("channel %s disable status: %d", channel, resp.StatusCode)
		}
	}
}
