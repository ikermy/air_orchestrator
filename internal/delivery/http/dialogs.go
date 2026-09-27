package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"air_orchestrator/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

type LogEntry struct {
	timestamp time.Time
	message   string
	fileName  string
}

type lokiQueryResponse struct {
	Data struct {
		Result []struct {
			Stream map[string]string `json:"stream"`
			Values [][2]string       `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// GetUserDialogs godoc
// @Summary Получить все диалоги пользователя
// @Tags dialog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /dialog/all [get]
func (w *Web) GetUserDialogs(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	dialogs, err := w.db.GetUserDialogs(userID)
	if err != nil {
		logger.Error("'GetUserDialogs' Ошибка чтения из БД: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Форматируем JSON
	var prettyJSON bytes.Buffer
	err = json.Indent(&prettyJSON, dialogs, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, json.RawMessage(prettyJSON.Bytes()))
}

// ViewDialog godoc
// @Summary Получить конкретный диалог
// @Tags dialog
// @Produce json
// @Security BearerAuth
// @Param id path integer true "ID диалога"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /dialog/view/{id} [post]
func (w *Web) ViewDialog(c *gin.Context) {
	dialogId, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Error("'ViewDialog' Ошибка парсинга ID диалога: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dialog ID"})
		return
	}

	userID, ok := getUserID(c)
	if !ok {
		return
	}

	data, err := w.db.ReadDialog(dialogId)
	if err != nil {
		logger.Error("'ViewDialog' Ошибка чтения диалога из БД: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Преобразуем структуру в JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Форматируем JSON
	var prettyJSON bytes.Buffer
	err = json.Indent(&prettyJSON, jsonData, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, json.RawMessage(prettyJSON.Bytes()))
}

// LogWSSHandler godoc
// @Summary WebSocket для логов
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/log [get]
func (w *Web) LogWSSHandler(c *gin.Context) {
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения в LogWSSHandler: %v", err)
		}
	}()

	// Проверяем параметры запроса для аутентификации
	userID, ok := getUserID(c)
	if !ok {
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Недействительный токен")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке токена: %v", err, userID)
		}
		return
	}

	// Вызываем функцию для работы с логами через WebSocket
	w.GetLogWSS(conn, userID)
}

func (w *Web) GetLogWSS(conn *websocket.Conn, userID uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	initialCtx, initialCancel := context.WithTimeout(ctx, 15*time.Second)
	logs, err := w.fetchLokiLogs(initialCtx, userID, 200)
	initialCancel()
	if err != nil {
		errMsg := fmt.Sprintf("❌ Ошибка чтения логов из Loki: %v", err)
		logger.Error(errMsg, userID)
		if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errMsg)); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке Loki: %v", writeErr, userID)
		}
		return
	}

	for _, logEntry := range logs {
		formattedMsg := fmt.Sprintf("[%s] %s", logEntry.fileName, logEntry.message)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(formattedMsg+"\n")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения с логом: %v", err, userID)
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastSent time.Time
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			queryCtx, queryCancel := context.WithTimeout(ctx, 10*time.Second)
			updates, err := w.fetchLokiLogsSince(queryCtx, userID, 100, lastSent)
			queryCancel()
			if err != nil {
				logger.Error("Ошибка чтения новых логов из Loki: %v", err, userID)
				return
			}

			for _, logEntry := range updates {
				formattedMsg := fmt.Sprintf("[%s] %s", logEntry.fileName, logEntry.message)
				if err := conn.WriteMessage(websocket.TextMessage, []byte(formattedMsg+"\n")); err != nil {
					logger.Error("Ошибка отправки нового WebSocket лога: %v", err, userID)
					return
				}
				if logEntry.timestamp.After(lastSent) {
					lastSent = logEntry.timestamp
				}
			}
		}
	}
}

func (w *Web) fetchLokiLogs(ctx context.Context, userID uint32, limit int) ([]LogEntry, error) {
	return w.fetchLokiLogsSince(ctx, userID, limit, time.Time{})
}

func (w *Web) fetchLokiLogsSince(ctx context.Context, userID uint32, limit int, since time.Time) ([]LogEntry, error) {
	// Запросим данные из VictoriaLogs (Elasticsearch-совместимый приём),
	// куда Vector отправляет логи через endpoint /insert/elasticsearch/.
	// Формируем DSL-запрос: фильтр по метке app=air и по вхождению [USER:ID] в поле message.
	must := []interface{}{
		map[string]interface{}{"match": map[string]interface{}{"app": "air"}},
		map[string]interface{}{"match_phrase": map[string]interface{}{"message": fmt.Sprintf("[USER:%d]", userID)}},
	}
	boolQuery := map[string]interface{}{"must": must}
	if !since.IsZero() {
		// Фильтр по времени; ожидаем, что поле timestamp хранится в ISO8601
		boolQuery["filter"] = []interface{}{
			map[string]interface{}{"range": map[string]interface{}{"timestamp": map[string]interface{}{"gt": since.Format(time.RFC3339Nano)}}},
		}
	}

	body := map[string]interface{}{
		"size":  limit,
		"query": map[string]interface{}{"bool": boolQuery},
		"sort":  []interface{}{map[string]interface{}{"timestamp": map[string]string{"order": "asc"}}},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("формирование запроса к VictoriaLogs: %w", err)
	}

	// Попробуем стандартный Elasticsearch-эндпоинт _search
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.VicLogsURL+"/_search", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("создание запроса в VictoriaLogs: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("запрос в VictoriaLogs: %w", err)
	}
	defer closeResponseBody(resp.Body, "fetchLokiLogsSince")

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("VictoriaLogs вернул статус %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Ожидаем структуру ответа, совместимую с Elasticsearch _search
	var esResp struct {
		Hits struct {
			Hits []struct {
				Source struct {
					Message       string      `json:"message"`
					Timestamp     interface{} `json:"timestamp"`
					ContainerName string      `json:"container_name"`
					App           string      `json:"app"`
					Service       string      `json:"service"`
					Tier          string      `json:"tier"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("декодирование ответа VictoriaLogs: %w", err)
	}

	var allLogs []LogEntry
	for _, hit := range esResp.Hits.Hits {
		src := hit.Source
		var ts time.Time
		switch v := src.Timestamp.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				ts = t
			} else if t, err := time.Parse(time.RFC3339, v); err == nil {
				ts = t
			}
		case float64:
			// возможно метка в секундах.millis
			sec := int64(v)
			ts = time.Unix(sec, 0)
		case int64:
			ts = time.Unix(v, 0)
		default:
			// оставляем нулевой ts — фильтрация по since не будет применена
		}

		if !since.IsZero() && !ts.After(since) {
			continue
		}

		serviceName := src.Service
		if serviceName == "" {
			serviceName = src.ContainerName
		}
		if serviceName == "" {
			serviceName = src.App
		}
		if serviceName == "" {
			serviceName = "unknown"
		}

		if src.Message == "" {
			continue
		}

		// Проверяем, содержит ли сообщение маркер пользователя (на случай, если match_phrase не сработал)
		if !strings.Contains(src.Message, fmt.Sprintf("[USER:%d]", userID)) {
			continue
		}

		allLogs = append(allLogs, LogEntry{
			timestamp: ts,
			message:   sanitizeUserLogLine(src.Message),
			fileName:  serviceName,
		})
	}

	sort.Slice(allLogs, func(i, j int) bool {
		return allLogs[i].timestamp.Before(allLogs[j].timestamp)
	})

	return allLogs, nil
}

func sanitizeUserLogLine(line string) string {
	cleanLine := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(line, "")
	cleanLine = regexp.MustCompile(`\[USER:\d+]\s*`).ReplaceAllString(cleanLine, "")
	return strings.TrimSpace(cleanLine)
}

// GetUserDetails godoc
// @Summary Получить статистику пользователя
// @Tags user
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /user/details [get]
func (w *Web) GetUserDetails(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	jsonRaw, err := w.db.GetUserDetails(userID)
	if err != nil {
		logger.Error("'GetUserDetails' Ошибка получения данных: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var prettyJSON bytes.Buffer
	err = json.Indent(&prettyJSON, jsonRaw, "", "  ")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	}

	c.JSON(http.StatusOK, json.RawMessage(prettyJSON.Bytes()))
}

// DeleteDialog godoc
// @Summary Удалить диалог по ID
// @Tags dialog
// @Produce json
// @Security BearerAuth
// @Param id path integer true "ID диалога"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /dialog/{id} [delete]
func (w *Web) DeleteDialog(c *gin.Context) {
	dialogId, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		logger.Error("Ошибка парсинга ID диалога: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid dialog ID"})
		return
	}

	userID, ok := getUserID(c)
	if !ok {
		return
	}

	err = w.db.DeleteDialog(userID, dialogId)
	if err != nil {
		logger.Error("'DeleteDialog' Ошибка при удалении диалога id=%d: %v", dialogId, err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "Dialog deleted successfully"})
}

// DeleteDialogs godoc
// @Summary Удалить список диалогов
// @Tags dialog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Список ID диалогов"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /dialog/list [delete]
func (w *Web) DeleteDialogs(c *gin.Context) {
	type idsReq struct {
		IDs []uint64 `json:"ids"`
	}

	var ids []uint64

	// 1) Попытка прочитать JSON тело { "ids": [1,2,3] }
	var body idsReq
	if err := c.ShouldBindJSON(&body); err == nil && len(body.IDs) > 0 {
		ids = body.IDs
	} else {
		// 2) Фолбэк на query параметр ?ids=1,2,3
		q := c.Query("ids")
		if q == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "no ids provided"})
			return
		}
		parts := strings.Split(q, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			v, err := strconv.ParseUint(p, 10, 64)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid id: " + p})
				return
			}
			ids = append(ids, v)
		}
	}

	if len(ids) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "empty ids list"})
		return
	}

	// Получение userID как в DeleteDialog
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	// Выполняем удаление по каждому id, считаем успешные и собираем неудачные
	deleted := 0
	var failed []uint64
	var demoRestricted []uint64

	for _, id := range ids {
		if err := w.db.DeleteDialog(userID, id); err != nil {
			// Проверяем, является ли это ошибкой демо-пользователя
			if strings.Contains(err.Error(), "невозможно удалить диалог демо пользователя") {
				demoRestricted = append(demoRestricted, id)
				logger.Warn("Попытка удаления диалога %d демо пользователем: %v", id, userID)
			} else {
				logger.Error("Ошибка при удалении диалога %d: %v", id, err, userID)
				failed = append(failed, id)
			}
			continue
		}
		deleted++
	}

	result := gin.H{
		"status":  "ok",
		"ids":     ids,
		"deleted": deleted,
		"failed":  failed,
	}

	if len(demoRestricted) > 0 {
		result["demo_restricted"] = demoRestricted
		result["message"] = "Невозможно удалить диалоги демо пользователя"
	}

	c.JSON(http.StatusOK, result)
}
