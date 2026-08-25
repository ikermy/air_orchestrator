package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// OperatorsList godoc
// @Summary Получить список операторов пользователя
// @Tags operators
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /operators [get]
func (w *Web) OperatorsList(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	rawJSON, err := w.db.OperatorsList(w.ctx, userId)
	if err != nil {
		logger.Error("'OperatorsList' Ошибка чтения из БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json", rawJSON)
}

// SaveOperators godoc
// @Summary Сохранить список операторов
// @Tags operators
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Список операторов"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /operators [post]
func (w *Web) SaveOperators(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Data json.RawMessage `json:"data"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'SaveOperators' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Парсим JSON массив в слайс интерфейсов
	var operators []any
	if err := json.Unmarshal(requestData.Data, &operators); err != nil {
		logger.Error("Ошибка парсинга списка операторов: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid operators format"})
		return
	}

	// Преобразуем в массив чисел
	var numericOperators []int64
	for _, op := range operators {
		switch v := op.(type) {
		case float64:
			numericOperators = append(numericOperators, int64(v))
		case string:
			// Пытаемся преобразовать строку в число
			if num, err := strconv.ParseInt(v, 10, 64); err == nil {
				numericOperators = append(numericOperators, num)
			} else {
				logger.Error("Некорректное значение оператора: %s", v, userId)
				c.JSON(http.StatusBadRequest, gin.H{"error": "All operators must be numeric"})
				return
			}
		default:
			logger.Error("Неподдерживаемый тип данных для оператора: %T", v, userId)
			c.JSON(http.StatusBadRequest, gin.H{"error": "All operators must be numeric"})
			return
		}
	}

	// Преобразуем обратно в JSON без кавычек для чисел
	// Преобразуем обратно в JSON без кавычек для чисел
	var cleanData []byte

	if len(numericOperators) == 0 {
		cleanData = []byte("[]") // Пустой JSON массив
	} else {
		var err error
		cleanData, err = json.Marshal(numericOperators)
		if err != nil {
			logger.Error("Ошибка преобразования операторов в JSON: %v", err, userId)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
			return
		}
	}

	// Сохраняю список операторов
	if err := w.db.SaveOperators(w.ctx, userId, "Telegram", cleanData); err != nil {
		logger.Error("'SaveOperators' Ошибка при сохранении в БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}
