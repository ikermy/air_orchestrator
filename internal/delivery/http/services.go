package web

import (
	"air_orchestrator/internal/config"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// GetServicesList godoc
// @Summary Получить список сервисов пользователя
// @Tags services
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /services/list [get]
func (w *Web) GetServicesList(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	list, err := w.db.ServiceList(userId)
	if err != nil {
		logger.Error("'GetServicesList' Ошибка при получении списка сервисов: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"services": list})
}

// AddService godoc
// @Summary Добавить сервис пользователю
// @Tags services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Имя сервиса"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /services-add [post]
func (w *Web) AddService(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Service string `json:"service"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'AddService' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.AddService(userId, requestData.Service)
	if err != nil {
		logger.Error("'AddService' Ошибка при добавлении сервиса: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.Status(http.StatusOK)
}

// DeleteService godoc
// @Summary Удалить сервис
// @Tags services
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Имя сервиса"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /services-delete [delete]
func (w *Web) DeleteService(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Service string `json:"service"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'DeleteService' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.DeleteService(userId, requestData.Service)
	if err != nil {
		logger.Error("'DeleteService' Ошибка при удалении сервиса: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.Status(http.StatusOK)
}

// closeResponseBody безопасно закрывает response body с логированием ошибки
func closeResponseBody(body io.ReadCloser, context string) {
	if err := body.Close(); err != nil {
		logger.Error("Ошибка закрытия response body в %s: %v", context, err)
	}
}

// ServiceAddTgAk godoc
// @Summary WebSocket для добавления Telegram API ключа
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/lead/tg-auth [get]
func (w *Web) ServiceAddTgAk(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Проксируем WebSocket на внешний сервис
	proxyWebSocket(c, fmt.Sprintf("%s/lead/ws/telegram/auth?uid=%d", config.LeadServiceWS, userId), userId)
}

// ServiceAddWaAk godoc
// @Summary WebSocket для добавления WhatsApp API ключа
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/lead/wa-auth [get]
func (w *Web) ServiceAddWaAk(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Проксируем WebSocket на внешний сервис
	proxyWebSocket(c, fmt.Sprintf("%s/lead/ws/whatsapp/auth?uid=%d", config.LeadServiceWS, userId), userId)
}

// ServiceEventWSS godoc
// @Summary WebSocket для событий сервиса-бота
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/lead/events [get]
func (w *Web) ServiceEventWSS(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Проксируем WebSocket на внешний сервис
	proxyWebSocket(c, fmt.Sprintf("%s/lead/ws/events?uid=%d", config.LeadServiceWS, userId), userId)
}

func proxyWebSocket(c *gin.Context, targetURL string, userId uint32) {
	// Устанавливаем WebSocket с клиентом
	clientConn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("Ошибка установки WebSocket с клиентом: %v", err, userId)
		return
	}
	defer func() {
		if err := clientConn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения клиента: %v", err, userId)
		}
	}()

	// Подключаемся к целевому WebSocket серверу
	targetConn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		logger.Error("Ошибка подключения к целевому WebSocket: %v", err, userId)
		if writeErr := clientConn.WriteJSON(gin.H{"error": "Service unavailable"}); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке: %v", writeErr, userId)
		}
		return
	}
	defer func() {
		if err := targetConn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения с целевым сервером: %v", err, userId)
		}
	}()

	// Канал для завершения
	done := make(chan struct{})

	// Проксируем: клиент -> целевой сервер
	go func() {
		defer close(done)
		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				return
			}

			// Подменяем user_id для протоколов auth_data и init
			var msgData map[string]any
			if err = json.Unmarshal(message, &msgData); err == nil {
				if t, _ := msgData["type"].(string); t == "auth_data" || t == "init" {
					msgData["user_id"] = userId
					if modifiedMessage, err := json.Marshal(msgData); err == nil {
						message = modifiedMessage
					}
				}
			}

			// Логируем сообщение от клиента
			//logger.Debug("Сообщение от клиента (userId=%d): %s", userId, string(message))

			if err = targetConn.WriteMessage(messageType, message); err != nil {
				logger.Error("Ошибка отправки на целевой сервер: %v", err, userId)
				return
			}
		}
	}()

	// Проксируем: целевой сервер -> клиент
	go func() {
		for {
			messageType, message, err := targetConn.ReadMessage()
			if err != nil {
				return
			}
			if err = clientConn.WriteMessage(messageType, message); err != nil {
				logger.Error("Ошибка отправки клиенту: %v", err, userId)
				return
			}
		}
	}()

	<-done
}

type CreateModelRequest struct {
	ModelId int    `json:"model_id"`
	Start   string `json:"start"`
	Tg      string `json:"tg"`
}

func sendRESP(ctx context.Context, method, url string, data ...[]byte) (*http.Response, error) {
	var bodyData io.Reader
	if len(data) > 0 {
		bodyData = bytes.NewBuffer(data[0])
	} else {
		bodyData = nil
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyData)
	if err != nil {
		return nil, fmt.Errorf("ошибка при создании HTTP-запроса: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Transport: tr,
		// НЕ следовать за редиректами автоматически - возвращаем редирект как есть
		// Это критично для правильной работы OAuth callback'ов (Avito, AmoCRM и т.д.)
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка при выполнении HTTP-запроса: %v", err)
	}

	return resp, nil
}

// ServiceStartWSS godoc
// @Summary WebSocket для запуска сервиса
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/leed/start [get]
func (w *Web) ServiceStartWSS(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Устанавливаем WebSocket с клиентом
	clientConn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("'ServiceStartWSS' Ошибка установки WebSocket: %v", err, userId)
		return
	}
	defer func() {
		if err := clientConn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения клиента в ServiceStartWSS: %v", err, userId)
		}
	}()

	// Подключаемся к удалённому сервису
	targetURL := fmt.Sprintf("%s/lead/ws/start?uid=%d", config.LeadServiceWS, userId)
	targetConn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		logger.Error("'ServiceStartWSS' Ошибка подключения к удалённому сервису: %v", err, userId)
		if writeErr := clientConn.WriteJSON(gin.H{"error": "Service unavailable"}); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке: %v", writeErr, userId)
		}
		return
	}
	defer func() {
		if err = targetConn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения с удаленным сервисом в ServiceStartWSS: %v", err, userId)
		}
	}()

	// Читаем request_user_id от сервера
	var requestMsg map[string]any
	if err = targetConn.ReadJSON(&requestMsg); err != nil {
		logger.Error("'ServiceStartWSS' Ошибка чтения запроса userId: %v", err, userId)
		return
	}

	// Отправляем init с userId
	initMsg := map[string]any{
		"type":    "init",
		"user_id": userId,
	}
	if err = targetConn.WriteJSON(initMsg); err != nil {
		logger.Error("'ServiceStartWSS' Ошибка отправки init: %v", err, userId)
		return
	}

	// Канал завершения
	done := make(chan struct{})

	// Проксируем: удалённый сервер -> клиент
	go func() {
		defer close(done)
		for {
			var message map[string]any
			if err = targetConn.ReadJSON(&message); err != nil {
				return
			}
			if err = clientConn.WriteJSON(message); err != nil {
				logger.Error("'ServiceStartWSS' Ошибка отправки клиенту: %v", err, userId)
				return
			}
		}
	}()

	<-done
}

// CallLeadTarget вызывает Meta-сервис lead/target напрямую без gin.Context.
// Используется как колбэк в mcp.Handler через SetLeadTargetFn.
func (w *Web) CallLeadTarget(ctx context.Context, respId int64) error {
	targetURL := fmt.Sprintf("%s/lead/target?rid=%d", config.LeadServiceURL, respId)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("lead_target service unavailable: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("lead_target returned HTTP %d", resp.StatusCode)
	}
	defer closeResponseBody(resp.Body, "CallLeadTarget")
	logger.Info("CallLeadTarget: HTTP %d rid=%d", resp.StatusCode, respId)
	return nil
}

// proxyServicesRequest godoc
// @Summary Проксировать запрос к LeadService
// @Tags services
// @Security BearerAuth
// @Router /services/lead/{path} [get]
//
// proxyServicesRequest proxies /v1/services/lead/* to LeadService /lead/*.
func (w *Web) proxyServicesRequest(c *gin.Context) {
	var targetURL string
	var client *http.Client

	targetURL = config.LeadServiceURL
	client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// Получаем userId из контекста (установлен middleware)
	userId, ok := c.Get("userId")
	if !ok {
		logger.Error("proxyServicesRequest: userId не найден в контексте для %s %s", c.Request.Method, c.Request.URL.Path)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	userIdValue := userId.(uint32)

	// Gin stores the wildcard part without the /v1/services/lead prefix.
	path := c.Param("path")
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	proxyURL := targetURL + "/lead" + path

	// Обрабатываем query параметры - удаляем token если есть
	queryParams := c.Request.URL.Query()
	queryParams.Del("token") // Удаляем token, т.к. он уже обработан в middleware

	// Добавляем uid query параметр
	queryParams.Set("uid", fmt.Sprintf("%d", userIdValue))

	// Формируем строку query параметров
	queryString := queryParams.Encode()
	if queryString != "" {
		proxyURL += "?" + queryString
	}

	// Читаем тело запроса если есть
	var bodyBytes []byte
	if c.Request.Body != nil {
		bodyBytes, _ = io.ReadAll(c.Request.Body)
		// ВАЖНО: Восстанавливаем тело для последующих middleware
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	// Создаем новый запрос с КОПИЕЙ тела
	req, err := http.NewRequestWithContext(w.ctx, c.Request.Method, proxyURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		logger.Error("proxyServicesRequest: ошибка создания запроса - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка создания запроса"})
		return
	}

	// Копируем необходимые заголовки
	req.Header.Set("Content-Type", c.GetHeader("Content-Type"))

	// Выполняем запрос
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("proxyServicesRequest: ошибка при выполнении запроса - %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ошибка подключения к сервису"})
		return
	}
	defer closeResponseBody(resp.Body, "proxyServicesRequest")

	// Читаем ответ
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("proxyServicesRequest: ошибка чтения ответа - %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка чтения ответа"})
		return
	}

	// Логируем только ошибки
	// Исключение: для GET /status статус 400 - это нормально (сервис не запущен)
	if resp.StatusCode >= 400 {
		// Не логируем 400 для /status маршрута
		if !(resp.StatusCode == http.StatusBadRequest && c.Request.Method == http.MethodGet && strings.Contains(path, "/status")) {
			logger.Error("proxyServicesRequest: сервис вернул ошибку %d для %s %s: %s", resp.StatusCode, c.Request.Method, proxyURL, string(respBody))
		}
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
