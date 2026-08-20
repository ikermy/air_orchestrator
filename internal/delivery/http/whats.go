package web

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WaAuthWebSocket godoc
// @Summary WebSocket для WhatsApp авторизации
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/whats [get]
func (w *Web) WaAuthWebSocket(c *gin.Context) {
	// Получаем userId из контекста (middleware сохраняет как "userId")
	userID, exists := c.Get("userId")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// Апгрейдим входящее соединение до WebSocket
	clientConn, err := upgradeWebSocket(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upgrade connection"})
		return
	}
	defer func() {
		if err := clientConn.Close(); err != nil {
			// Ошибка закрытия клиентского WebSocket соединения (не логируем, т.к. это нормально при отключении клиента)
		}
	}()

	// Формируем URL внешнего сервиса с параметром uid
	targetURL := fmt.Sprintf("ws://whatsbot:8080/whats/ws?uid=%v", userID)
	// Подключаемся к внешнему WebSocket-сервису
	serverConn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		if writeErr := clientConn.WriteJSON(map[string]string{"error": "failed to connect to backend service"}); writeErr != nil {
			// Ошибка отправки сообщения клиенту (не логируем)
		}
		return
	}
	defer func() {
		if err := serverConn.Close(); err != nil {
			// Ошибка закрытия серверного WebSocket соединения (не логируем)
		}
	}()

	// Создаем каналы для завершения горутин
	done := make(chan struct{})

	// Проксируем сообщения от сервера к клиенту
	go func() {
		defer close(done)
		for {
			messageType, message, err := serverConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(messageType, message); err != nil {
				return
			}
		}
	}()

	// Проксируем сообщения от клиента к серверу
	go func() {
		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := serverConn.WriteMessage(messageType, message); err != nil {
				return
			}
		}
	}()

	// Ждем завершения
	<-done
}

// WaGetContactsWS godoc
// @Summary WebSocket для получения контактов WhatsApp
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/whats/contacts [get]
func (w *Web) WaGetContactsWS(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	// Апгрейдим входящее соединение до WebSocket
	clientConn, err := upgradeWebSocket(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upgrade connection"})
		return
	}
	defer func() {
		if err := clientConn.Close(); err != nil {
			// Ошибка закрытия клиентского WebSocket соединения (не логируем, т.к. это нормально при отключении клиента)
		}
	}()

	// Формируем URL внешнего сервиса с параметром uid
	targetURL := fmt.Sprintf("ws://whatsbot:8080/whats/contacts/ws?uid=%v", userID)

	// Подключаемся к внешнему WebSocket-сервису
	serverConn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		if writeErr := clientConn.WriteJSON(map[string]string{"error": "failed to connect to backend service"}); writeErr != nil {
			// Ошибка отправки сообщения клиенту (не логируем)
		}
		return
	}
	defer func() {
		if err := serverConn.Close(); err != nil {
			// Ошибка закрытия серверного WebSocket соединения (не логируем)
		}
	}()

	// Создаем каналы для завершения горутин
	done := make(chan struct{})

	// Проксируем сообщения от сервера к клиенту
	go func() {
		defer close(done)
		for {
			messageType, message, err := serverConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(messageType, message); err != nil {
				return
			}
		}
	}()

	// Проксируем сообщения от клиента к серверу
	go func() {
		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := serverConn.WriteMessage(messageType, message); err != nil {
				return
			}
		}
	}()

	// Ждем завершения
	<-done
}
