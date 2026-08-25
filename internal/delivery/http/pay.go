package web

import (
	"air_orchestrator/internal/config"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// paySubscription godoc
// @Summary Проверка подписки пользователя
// @Tags pay
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /auth/check-subscription [get]
func (w *Web) paySubscription(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	err := w.db.CheckUserSubscription(w.db, userId)
	if err != nil {
		logger.Error("'paySubscription' Ошибка проверки подписки: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// payTariff godoc
// @Summary Получение информации о тарифах
// @Tags pay
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /pay/tariff [get]
func (w *Web) payTariff(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Формируем запрос для pay сервиса с userId
	baseURL := fmt.Sprintf("%s/pay/tariff", config.PAYURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("'payTariff' ошибка построения URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("uid", fmt.Sprintf("%v", userId))
	u.RawQuery = q.Encode()

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("'payTariff' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в payTariff: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'payTariff' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// payCurrencies godoc
// @Summary Получение информации о доступных валютах
// @Tags pay
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /pay/currencies [get]
func (w *Web) payCurrencies(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Формируем запрос для pay сервиса с userId
	baseURL := fmt.Sprintf("%s/pay/currencies", config.PAYURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("'payCurrencies' ошибка построения URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("uid", fmt.Sprintf("%v", userId))
	u.RawQuery = q.Encode()

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("'payCurrencies' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в payCurrencies: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'payCurrencies' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// payCreateBybitPayment godoc
// @Summary Создание платежа Bybit
// @Tags pay
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Данные платежа"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /pay/create-payment [post]
func (w *Web) payCreateBybitPayment(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var reqBody struct {
		Currency string  `json:"currency"`
		Network  string  `json:"network"`
		Amount   float64 `json:"amount"`
	}

	// Проверка корректности запроса
	if err := c.ShouldBindJSON(&reqBody); err != nil {
		logger.Error("'payCreateBybitPayment' ошибка привязки JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Формируем запрос для pay сервиса с userId
	payloadData := map[string]any{
		"user_id":  userId,
		"currency": reqBody.Currency,
		"network":  reqBody.Network,
		"amount":   reqBody.Amount,
	}

	jsonData, err := json.Marshal(payloadData)
	if err != nil {
		logger.Error("'payCreateBybitPayment' ошибка маршалинга JSON: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to marshal request"})
		return
	}

	payUrl := fmt.Sprintf("%s/pay/create-payment", config.PAYURL)

	resp, err := sendRESP(c.Request.Context(), http.MethodPost, payUrl, jsonData)
	if err != nil {
		logger.Error("'payCreateBybitPayment' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в payCreateBybitPayment: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'payCreateBybitPayment' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// payPaymentStatus returns the current payment status for fallback polling.
func (w *Web) payPaymentStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	orderID := c.Query("orderId")
	if orderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "orderId is required"})
		return
	}

	u, err := url.Parse(fmt.Sprintf("%s/pay/payment-status", config.PAYURL))
	if err != nil {
		logger.Error("'payPaymentStatus' ошибка построения URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}
	q := u.Query()
	q.Set("orderId", orderID)
	q.Set("uid", fmt.Sprintf("%d", userID))
	u.RawQuery = q.Encode()

	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("'payPaymentStatus' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'payPaymentStatus' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), body)
}

// payHandlePaymentStatusSSE godoc
// @Summary Проверка статуса платежа (SSE)
// @Tags pay
// @Produce event-stream
// @Security BearerAuth
// @Param orderId query string true "ID платежа"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /pay/payment-status-stream [get]
func (w *Web) payHandlePaymentStatusSSE(c *gin.Context) {
	orderId := c.Query("orderId")

	userID, ok := getUserID(c)
	if !ok {
		return
	}

	baseURL := fmt.Sprintf("%s/pay/payment-status-stream", config.PAYURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("'payHandlePaymentStatusSSE' ошибка построения URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	q := u.Query()
	q.Set("orderId", orderId)
	q.Set("uid", fmt.Sprintf("%v", userID))
	u.RawQuery = q.Encode()

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		if errors.Is(c.Request.Context().Err(), context.Canceled) {
			return
		}
		logger.Error("'payHandlePaymentStatusSSE' ошибка проксирования запроса: %v", err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в payHandlePaymentStatusSSE: %v", err)
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
		logger.Warn("'payHandlePaymentStatusSSE' предупреждение: потоковая передача не поддерживается")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming not supported"})
		return
	}

	c.Status(http.StatusOK)
	flusher.Flush()

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if _, writeErr := writer.Write(line); writeErr != nil {
				logger.Debug("Client closed connection during write")
				return
			}
			flusher.Flush()
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(c.Request.Context().Err(), context.Canceled) {
				return
			}
			logger.Error("'payHandlePaymentStatusSSE' ошибка чтения данных из потока: %v", err)
			return
		}
	}
}
