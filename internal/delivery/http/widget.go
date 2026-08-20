package web

import (
	"air_orchestrator/internal/config"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// proxyWidgetEvents godoc
// @Summary Проксировать события виджета
// @Tags widget
// @Produce text/event-stream
// @Router /widget/events [get]
func (w *Web) proxyWidgetEvents(c *gin.Context) {
	logger.Debug(
		"proxyWidgetEvents: проксирование запроса %s %s",
		c.Request.Method,
		c.Request.URL.String(),
	)

	ctx := c.Request.Context()

	requestURL := *c.Request.URL
	targetURL := strings.TrimRight(config.WidgetBotURL, "/") +
		requestURL.RequestURI()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		targetURL,
		nil,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "failed to create SSE request",
		})
		return
	}

	for key, values := range c.Request.Header {
		if strings.EqualFold(key, "Host") ||
			strings.EqualFold(key, "Content-Length") {
			continue
		}

		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := proxyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}

		logger.Error(
			"proxyWidgetEvents: ошибка подключения к widget: %v",
			err,
		)
		c.JSON(http.StatusBadGateway, gin.H{
			"error": "failed to connect to widget service",
		})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Status(resp.StatusCode)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		logger.Error("proxyWidgetEvents: response writer не поддерживает Flush")
		return
	}

	buffer := make([]byte, 32*1024)

	for {
		n, readErr := resp.Body.Read(buffer)

		if n > 0 {
			if _, writeErr := c.Writer.Write(buffer[:n]); writeErr != nil {
				logger.Debug(
					"proxyWidgetEvents: клиент закрыл SSE: %v",
					writeErr,
				)
				return
			}

			flusher.Flush()
		}

		if readErr != nil {
			if readErr != io.EOF {
				logger.Error(
					"proxyWidgetEvents: ошибка чтения SSE: %v",
					readErr,
				)
			}
			return
		}

		if ctx.Err() != nil {
			return
		}
	}
}

// proxyWidgetRequest проксирует запросы /v1/widget/... в widget-сервис.
//
// Прокси сохраняет HTTP-метод, путь, query-параметры, тело запроса
// и заголовки клиента.
//
// @Summary Widget API proxy
// @Description Проксирует запросы к API widget-сервиса.
// @Tags widget
// @Accept json
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /v1/widget/available [get]
// @Router /v1/widget/code [get]
// @Router /v1/widget/exam [post]
// @Router /v1/widget/validate [get]
// @Router /v1/widget/refresh [post]
// @Router /v1/widget/username [get]
// @Router /v1/widget/dialog [get]
// @Router /v1/widget/data [post]
// @Router /v1/widget/events [get]
// @Router /v1/widget/enable [get]
// @Router /v1/widget/disable [get]
// @Router /v1/widget/restart [get]
// @Router /v1/widget/events-ticket [post]
func (w *Web) proxyWidgetRequest(c *gin.Context) {
	logger.Debug("proxyWidgetRequest: проксирование запроса %s %s", c.Request.Method, c.Request.URL.String())

	// Создаем контекст с таймаутом на 5 секунд, привязанный к контексту запроса клиента
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	// Выполняем проксирование синхронно, но с таймаутом через контекст
	w.universalProxyWidgetRequest(c, nil, ctx)
}

// Создаем выделенный HTTP-клиент для прокси с жесткими таймерами на уровне транспорта
var proxyClient = &http.Client{
	Transport: func() *http.Transport {
		// Клонируем дефолтный транспорт со всеми правильными DialContext и TLS таймаутами
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = http.ProxyFromEnvironment
		t.IdleConnTimeout = 10 * time.Minute
		t.ResponseHeaderTimeout = 10 * time.Second
		return t
	}(),
}

func (w *Web) universalProxyWidgetRequest(c *gin.Context, uid *uint32, ctx context.Context) {
	logger.Debug("universalProxyWidgetRequest: проксирование запроса %s %s", c.Request.Method, c.Request.URL.String())
	requestURL := *c.Request.URL
	query := requestURL.Query()
	if uid != nil {
		query.Set("uid", fmt.Sprintf("%d", *uid))
	}
	requestURL.RawQuery = query.Encode()
	targetURL := strings.TrimRight(config.WidgetBotURL, "/") + requestURL.RequestURI()

	// Обязательно защищаем чтение тела запроса от клиента, если оно зависло
	// Обернем тело в контекст, чтобы оно тоже прерывалось
	req, err := http.NewRequestWithContext(ctx, c.Request.Method, targetURL, c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create proxy request"})
		return
	}

	req.ContentLength = c.Request.ContentLength

	for key, values := range c.Request.Header {
		if strings.EqualFold(key, "Host") || strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	// Используем наш прокси-клиент с ResponseHeaderTimeout
	resp, err := proxyClient.Do(req)
	if err != nil {
		// Проверяем контекст или ошибки таймаута
		if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
			logger.Error("universalProxyWidgetRequest: таймаут запроса к widget (превышено 5 сек)")
			c.JSON(http.StatusRequestTimeout, gin.H{"error": "Request timeout"})
			return
		}
		logger.Error("universalProxyWidgetRequest: ошибка запроса к widget: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to connect to widget service"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	// Читаем тело ответа тоже с учетом контекста (на случай если сервер начал отдавать данные и завис)
	bodyChan := make(chan struct {
		data []byte
		err  error
	}, 1)

	go func() {
		b, readErr := io.ReadAll(resp.Body)
		bodyChan <- struct {
			data []byte
			err  error
		}{b, readErr}
	}()

	select {
	case <-ctx.Done():
		logger.Error("universalProxyWidgetRequest: таймаут при чтении тела ответа от widget")
		c.JSON(http.StatusRequestTimeout, gin.H{"error": "Request timeout during read"})
		return
	case res := <-bodyChan:
		if res.err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Failed to read widget response"})
			return
		}
		logger.Debug("universalProxyWidgetRequest: успешно проксирован запрос %s %s, статус %d", c.Request.Method, c.Request.URL.String(), resp.StatusCode)
		c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), res.data)
	}
}

func (w *Web) proxyWidgetUIDRequest(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}

	// Создаем контекст с таймаутом на 5 секунд, привязанный к контексту запроса клиента
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	w.universalProxyWidgetRequest(c, &uid, ctx)
}

// TODO удалить старые методы после полноценной проверки proxyWidgetRequest

// widgetCode godoc
// @Summary Получить код виджета
// @Tags widget
// @Accept json
// @Produce json
// @Param body body object true "Параметры виджета"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/code [get]
func (w *Web) veryold_widgetCode(c *gin.Context) {
	const widgetUrl = "http://widget:8080/widget/code"

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	resp, err := sendRESP(c.Request.Context(), http.MethodGet, widgetUrl, body)
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetCode: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

func (w *Web) old_widgetCode(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}

	requestURL := *c.Request.URL
	query := requestURL.Query()
	query.Set("uid", fmt.Sprintf("%d", uid))
	requestURL.RawQuery = query.Encode()

	targetURL := strings.TrimRight(config.WidgetBotURL, "/") + requestURL.RequestURI()

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	// 3. Передаем динамически собранный URL
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, targetURL, body)
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetCode: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetExam godoc
// @Summary Экзамен виджета
// @Tags widget
// @Accept json
// @Produce json
// @Param body body object true "Данные экзамена"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/exam [post]
func (w *Web) widgetExam(c *gin.Context) {
	const widgetUrl = "http://widget:8080/widget/exam"

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	resp, err := sendRESP(c.Request.Context(), http.MethodPost, widgetUrl, body)
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetExam: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetValidate godoc
// @Summary Проверить токен виджета
// @Tags widget
// @Produce json
// @Param token query string true "Токен для проверки"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/validate [get]
func (w *Web) widgetValidate(c *gin.Context) {
	token := c.Query("token")

	baseURL := "http://widget:8080/widget/validate"
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Ошибка парсинга URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetValidate: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetRefresh godoc
// @Summary Обновить токен виджета
// @Tags widget
// @Accept json
// @Produce json
// @Param body body object true "Виджет код"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/refresh [post]
func (w *Web) widgetRefresh(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	const widgetUrl = "http://widget:8080/widget/refresh"

	resp, err := sendRESP(c.Request.Context(), http.MethodPost, widgetUrl, body)
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetRefresh: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetUserName godoc
// @Summary Получить имя пользователя в режиме виджета
// @Tags widget
// @Produce json
// @Param code query string true "Код виджета"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/username [get]
func (w *Web) widgetUserName(c *gin.Context) {
	token := c.Query("token")

	const baseURL = "http://widget:8080/widget/username"
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Ошибка парсинга URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetUserName: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetDialog godoc
// @Summary Получить диалог в режиме виджета
// @Tags widget
// @Produce json
// @Param code query string true "Код виджета"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/dialog [get]
func (w *Web) widgetDialog(c *gin.Context) {
	token := c.Query("token")
	name := c.Query("name")

	const baseURL = "http://widget:8080/widget/dialog"
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Ошибка парсинга URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("token", token)
	q.Set("name", name)
	u.RawQuery = q.Encode()

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetDialog: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetData godoc
// @Summary Получить данные в режиме виджета
// @Tags widget
// @Accept json
// @Produce json
// @Param body body object true "Данные виджета"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /widget/data [post]
func (w *Web) widgetData(c *gin.Context) {
	const widgetUrl = "http://widget:8080/widget/data"

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Ошибка чтения тела запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read request body"})
		return
	}

	resp, err := sendRESP(c.Request.Context(), http.MethodPost, widgetUrl, body)
	if err != nil {
		logger.Error("Ошибка отправки запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetData: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// widgetEvent godoc
// @Summary Получить события в режиме виджета
// @Tags widget
// @Produce event-stream
// @Param code query string true "Код виджета"
// @Success 200
// @Failure 400 {object} map[string]string
// @Router /widget/events [get]
func (w *Web) widgetEvent(c *gin.Context) {
	token := c.Query("token")

	const baseURL = "http://widget:8080/widget/events"
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("Ошибка парсинга URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		// Проверяем, что это не обычное закрытие соединения
		if errors.Is(c.Request.Context().Err(), context.Canceled) {
			//logger.Debug("Client closed SSE connection")
		} else {
			logger.Error("Ошибка отправки запроса: %v", err)
		}
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("Ошибка закрытия response body в widgetEvent: %v", err)
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.Status(resp.StatusCode)
		_, _ = c.Writer.Write(bodyBytes)
		return
	}

	writer := c.Writer
	flusher, ok := writer.(http.Flusher)
	if !ok {
		logger.Warn("Ошибка: невозможен сброс данных")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	reader := bufio.NewReader(resp.Body)
	for {
		select {
		case <-c.Request.Context().Done():
			logger.Debug("Client disconnected from /widget/events")
			return
		default:
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				if _, werr := writer.Write(line); werr != nil {
					logger.Debug("Client closed connection during write")
					return
				}
				flusher.Flush()
			}
			if err != nil {
				if err == io.EOF {
					logger.Debug("SSE stream ended")
					return
				}
				logger.Error("Ошибка чтения потока: %v", err)
				return
			}
		}
	}
}
