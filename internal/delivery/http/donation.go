package web

import (
	"air_orchestrator/internal/config"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// donationCurrencies godoc
// @Summary Получение информации о доступных валютах
// @Tags pay
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /pay/donation/donation-currencies [get]
func (w *Web) donationCurrencies(c *gin.Context) {
	// Формируем запрос для pay сервиса с userId
	baseURL := fmt.Sprintf("%s/pay/donation-currencies", config.PAYURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		logger.Error("'donationCurrencies' ошибка построения URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to build URL"})
		return
	}

	// GET без тела
	resp, err := sendRESP(c.Request.Context(), http.MethodGet, u.String())
	if err != nil {
		logger.Error("'donationCurrencies' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в donationCurrencies: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'donationCurrencies' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}

// donationAddress godoc
// @Summary Создание платежа Bybit
// @Tags pay
// @Accept json
// @Produce json
// @Param body body object true "Данные платежа"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /pay/donation/donation-address [post]
func (w *Web) donationAddress(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	payUrl := fmt.Sprintf("%s/pay/donation-address", config.PAYURL)

	resp, err := sendRESP(c.Request.Context(), http.MethodPost, payUrl, body)
	if err != nil {
		logger.Error("'donationAddress' ошибка проксирования запроса: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to proxy request"})
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Error("ошибка закрытия response body в donationAddress: %v", err)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("'donationAddress' ошибка чтения ответа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read response"})
		return
	}

	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), respBody)
}
