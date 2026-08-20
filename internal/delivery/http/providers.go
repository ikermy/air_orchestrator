package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// ProvidersWithApiKeys godoc
// @Summary Получить список провайдеров с установленными API-ключами
// @Tags provider
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /provider/available [get]
func (w *Web) ProvidersWithApiKeys(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	providers := w.mod.ProvidersWithApiKeys(userID)

	c.JSON(http.StatusOK, gin.H{
		"available":   providers.Available,
		"unavailable": providers.Unavailable,
	})
}

// RevokeUserAPIKey godoc
// @Summary Отозвать API-ключ пользователя
// @Tags provider
// @Produce json
// @Security BearerAuth
// @Param providerStr query string true "Имя провайдера"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /provider/revoke [delete]
func (w *Web) RevokeUserAPIKey(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем providerStr из query-параметров (строка)

	providerStr := c.Query("providerStr") // "google", "openai"

	if providerStr == "" {
		logger.Error("Provider не указан в запросе")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider query parameter is required"})
		return
	}

	provider, err := commdom.FromString(providerStr)
	if err != nil || provider == 0 {
		logger.Error("Неверный providerStr: %s", providerStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider"})
		return
	}

	needRestart, err := w.providerUC.RevokeUserAPIKey(userID, provider)
	if err != nil {
		logger.Error("Ошибка при отзыве API-ключа для providerStr %s: %v", providerStr, err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restart": needRestart,
	})
}

// SetUserAPIKey godoc
// @Summary Установить API-ключ провайдера
// @Tags provider
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "API-ключ для установки"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /provider/setkey [post]
func (w *Web) SetUserAPIKey(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем providerStr из query-параметров (строка)
	providerStr := c.Query("providerStr") // "google", "openai"

	if providerStr == "" {
		logger.Error("Provider не указан в запросе")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider query parameter is required"})
		return
	}

	var requestData struct {
		ApiKey string `json:"key"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("Ошибка при разборе JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider, err := commdom.FromString(providerStr)
	if err != nil || provider == 0 {
		logger.Error("Неверный provider: %s", providerStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider"})
		return
	}

	needRestart, err := w.providerUC.SetUserAPIKey(userID, provider, requestData.ApiKey)
	if err != nil {
		if err.Error() == "MASTER_KEY_REQUIRED" {
			logger.Warn("SetUserAPIKey: MasterKey требуется, но не был предоставлен", userID)
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "MASTER_KEY_REQUIRED"})
			return
		}
		logger.Error("Ошибка при установке API-ключа для provider %s: %v", providerStr, err)
		c.Status(http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"restart": needRestart,
	})
}
