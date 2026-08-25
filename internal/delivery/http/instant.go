package web

import (
	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// Instant godoc
// @Summary WebSocket для мгновенных уведомлений
// @Tags ws
// @Produce application/json
// @Security BearerAuth
// @Router /ws/instant [get]
func (w *Web) Instant(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Апгрейд соединения до WebSocket
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения в InstantNotificationHandler: %v", err)
		}
	}()

	// Канал для завершения работы
	done := make(chan struct{})

	// Горутина для чтения сообщений от клиента (для поддержания соединения)
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				logger.Debug("Клиент отключился", userId)
				return
			}
		}
	}()

	// Основной цикл чтения из канала и отправки сообщений
	for {
		select {
		case msg, ok := <-mode.GetInstantMsgCh():
			if !ok {
				logger.Error("InstantMsgCh закрыт")
				return
			}

			// Отправляем сообщение только если UID совпадает с userId клиента
			if msg.UID == userId {
				if err := conn.WriteJSON(msg); err != nil {
					logger.Error("Ошибка отправки сообщения: %v", err, userId)
					return
				}
			}
		case <-done:
			return
		case <-c.Request.Context().Done():
			return
		}
	}
}
