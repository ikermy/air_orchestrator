package web

import (
	"air_orchestrator/internal/config"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-common/pkg/com"
	"github.com/ikermy/air-common/pkg/endpoint"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// GetMail godoc
// @Summary Получить email пользователя
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota/mail [get]
func (w *Web) GetMail(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	rawEmail, err := w.db.GetUserEmail(userId)
	if err != nil {
		logger.Error("'GetMail' Ошибка получения почты из БД: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Расшифровываем если нужно
	email, err := w.exam.DecryptEmailIfNeeded(rawEmail)
	if err != nil {
		logger.Error("'GetMail' Ошибка при расшифровке email: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"email": email})
}

// SaveNotifications godoc
// @Summary Сохранить канал уведомления
// @Tags notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Канал и данные уведомления"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota [post]
func (w *Web) SaveNotifications(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Type    string `json:"type"` // "email" "telega" "instant"
		Data    string `json:"data"`
		Enabled bool   `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'save-notifications' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	logger.Infoln(requestData)
	var telegaId uint64 = 0
	if requestData.Type == "telega" && requestData.Data != "" {
		// Parse the string into uint64
		parsedId, err := strconv.ParseUint(requestData.Data, 10, 64)
		if err != nil {
			logger.Error("'save-notifications' Ошибка при конвертации ID телеграма: %v", err, userId)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Telegram ID format"})
			return
		}
		telegaId = parsedId
	}

	err := w.db.UpdateNotification(userId, requestData.Type, requestData.Enabled, telegaId)
	if err != nil {
		logger.Error("'SaveNotifications' Ошибка при сохранении данных уведомлений: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	active, err := w.db.CheckActiveChannels(userId)
	if err != nil {
		logger.Error("Ошибка при проверке активных каналов: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"active_channels": active})
}

// ReadNotifications godoc
// @Summary Получить каналы уведомлений
// @Tags notifications
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota [get]
func (w *Web) ReadNotifications(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	notifications, err := w.db.GetNotificationsData(userId)
	if err != nil {
		logger.Error("'ReadNotifications' Ошибка получения данных уведомлений: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Расшифровываем email перед отправкой клиенту
	notifications, err = w.exam.DecryptEmailInJSON(notifications, "email.data")
	if err != nil {
		logger.Error("'read-notifications' Ошибка расшифровки email: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
		return
	}

	// notifications — это []byte с JSON из базы
	var notificationsMap map[string]any
	if err := json.Unmarshal(notifications, &notificationsMap); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Добавляем BotName из Go-структуры
	notificationsMap["BotName"] = w.carpinteroName

	// Форматируем обратно в JSON
	prettyJSON, err := json.MarshalIndent(notificationsMap, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, json.RawMessage(prettyJSON))
}

// SendVerificationCode godoc
// @Summary Отправить верификационный код
// @Tags notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "ID и код"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota/code [post]
func (w *Web) SendVerificationCode(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Id  string `json:"id"`
		Pin string `json:"pin"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'sendverifcode' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var telegaId uint64 = 0
	// Parse the string into uint64
	parsedId, err := strconv.ParseUint(requestData.Id, 10, 64)
	if err != nil {
		logger.Error("'sendverifcode' Ошибка при конвертации ID телеграма: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Telegram ID format"})
		return
	}
	telegaId = parsedId

	// Проверяем, что ID телеграма не равен 0
	if telegaId == 0 {
		logger.Error("'sendverifcode' Ошибка при конвертации ID телеграма: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Telegram ID"})
		return
	}

	// Отправляем верификационный код
	err = w.smtp.SendCarpinteroVerification(telegaId, requestData.Pin)
	if err != nil {
		logger.Error("'sendverifcode' Ошибка при отправке верификационного кода: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"send": "ok"})
}

// SaveNotificationsEvents godoc
// @Summary Сохранить события уведомлений
// @Tags notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "События уведомлений"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota/events [post]
func (w *Web) SaveNotificationsEvents(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Start  bool `json:"start"`
		End    bool `json:"end"`
		Target bool `json:"target"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SaveNotificationsEvents' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.SaveNotificationEvent(userId, requestData.Start, requestData.End, requestData.Target)
	if err != nil {
		logger.Error("'SaveNotificationsEvents' Ошибка при сохранении событий уведомлений: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	active, err := w.db.CheckActiveChannels(userId)
	if err != nil {
		logger.Error("Ошибка при проверке активных каналов: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"active_channels": active})
}

// DeleteNotificationsChannel godoc
// @Summary Удалить канал уведомлений
// @Tags notifications
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Имя канала"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /nota [delete]
func (w *Web) DeleteNotificationsChannel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Chan string `json:"chan"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'DeleteNotificationsChannel' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.DeleteNotificationsChannel(userId, requestData.Chan)
	if err != nil {
		logger.Error("'DeleteNotificationsChannel' Ошибка при удалении данных канала: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// SendInstantNotification godoc
// @Summary Отправить мгновенное уведомление
// @Tags notification
// @Accept json
// @Produce json
// @Param body body object true "Данные уведомления"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /notification/instant [post]
func (w *Web) SendInstantNotification(c *gin.Context) {
	var requestData struct {
		UID        uint32 `json:"uid"`
		Event      string `json:"event"`
		UserName   string `json:"user"`
		AssistName string `json:"assist"`
		Target     string `json:"target"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SendInstantNotification' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lang := w.db.UserLanguage(requestData.UID)

	msg, err := endpoint.CreateMessageFromEvent(lang, requestData.Event, requestData.UserName, requestData.AssistName, requestData.Target)
	if err != nil {
		logger.Error("'SendInstantNotification' Ошибка при создании сообщения: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create message"})
		return
	}

	instantMsg := com.InstMsg{
		UID: requestData.UID,
		Msg: msg,
	}
	// Отправляю в канал InstantCh
	select {
	case mode.GetInstantMsgCh() <- instantMsg:
	default:
		logger.Warn("Канал InstantCh переполнен, сообщение пропущено", requestData.UID)
	}

	c.JSON(http.StatusOK, gin.H{})
}

// SendMailNotification godoc
// @Summary Отправить email уведомление
// @Tags notification
// @Accept json
// @Produce json
// @Param body body object true "Данные email уведомления"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /notification/mail [post]
func (w *Web) SendMailNotification(c *gin.Context) {
	var requestData struct {
		UserID     uint32 `json:"uid"`
		Email      string `json:"email"`
		Event      string `json:"event"`
		UserName   string `json:"user"`
		AssistName string `json:"assist"`
		Target     string `json:"target"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SendMailNotification' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lang := w.db.UserLanguage(requestData.UserID)

	msg, err := endpoint.CreateMessageFromEvent(lang, requestData.Event, requestData.UserName, requestData.AssistName, requestData.Target)
	if err != nil {
		logger.Error("'SendMailNotification' Ошибка при создании сообщения: %v", err, requestData.UserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create message"})
		return
	}

	err = w.smtp.SendNotificationMail(requestData.UserID, requestData.Email, msg)
	if err != nil {
		logger.Error("'SendMailNotification' Ошибка при отправке email: %v", err, requestData.UserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send email"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// SendTelegaNotification godoc
// @Summary Отправить Telegram уведомление
// @Tags notification
// @Accept json
// @Produce json
// @Param body body object true "Данные Telegram уведомления"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /notification/telega [post]
func (w *Web) SendTelegaNotification(c *gin.Context) {
	var requestData struct {
		UserID     uint32 `json:"uid"`
		TelegramId int64  `json:"tid"`
		Event      string `json:"event"`
		UserName   string `json:"user"`
		AssistName string `json:"assist"`
		Target     string `json:"target"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SendTelegaNotification' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	lang := w.db.UserLanguage(requestData.UserID)

	// Формируем URL внешнего сервиса
	url := fmt.Sprintf("%s/tgbot/notification", config.TgBotURL)

	// Подготавливаем данные для отправки (без токена)
	bodyData, err := json.Marshal(map[string]any{
		"lang":   lang,
		"tid":    requestData.TelegramId,
		"event":  requestData.Event,
		"user":   requestData.UserName,
		"assist": requestData.AssistName,
		"target": requestData.Target,
	})
	if err != nil {
		logger.Error("'SendTelegaNotification' Ошибка сериализации данных: %v", err, requestData.UserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
		return
	}

	// Отправляем запрос
	respCtx, cancel := context.WithTimeout(w.ctx, config.RequestTimeout)
	defer cancel()
	resp, err := sendRESP(respCtx, http.MethodPost, url, bodyData)
	if err != nil {
		logger.Error("'SendTelegaNotification' Ошибка при запросе к внешнему сервису: %v", err, requestData.UserID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Service unavailable"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в SendTelegaNotification: %v", err)
		}
	}()

	// Обрабатываем ответ
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		logger.Error("'SendTelegaNotification' Внешний сервис вернул ошибку: статус %d, ответ: %s", resp.StatusCode, string(bodyBytes), requestData.UserID)
		c.Data(resp.StatusCode, "application/json", bodyBytes)
		return
	}

	// Возвращаем успешный ответ
	c.JSON(http.StatusOK, gin.H{})
}
