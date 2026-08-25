package web

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// TODO сделать передачу не значений а value для поддержки мультиязычности

// CreateMasterKeyWSS GET /ws/create-master-key?respId=...&pass=...
// Создаёт MasterKey для пользователя через WebSocket с прогрессом шифрования.
// Возвращает raw MasterKey (base64) ОДИН РАЗ. Если MasterKey уже создан — ошибка.
// userId берётся из токена (authAllowMiddleware).
// respId и pass передаются через query параметры (для WebSocket GET).
//
// Query: ?respId={respId из POST /key}&pass={AES-зашифрованный пароль}
// WebSocket Messages:
//
//	{"type":"progress","message":"🔄 Проверка..."}
//	{"type":"progress","message":"🔐 Генерация MasterKey..."}
//	{"type":"progress","message":"🔐 Шифрование API-ключей..."}
//	{"type":"success","message":"✅ MasterKey создан","data":{"raw_master_key":"...","warning":"..."}}
func (w *Web) CreateMasterKeyWSS(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("'CreateMasterKeyWSS' WebSocket upgrade error: %v", err, userId)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("'CreateMasterKeyWSS' error closing WebSocket: %v", err, userId)
		}
	}()

	// Получаем параметры из query (для WebSocket GET запроса)
	respIdStr := c.Query("respId")
	encPass := c.Query("pass")

	if respIdStr == "" || encPass == "" {
		w.sendWSError(conn, "Отсутствуют required параметры respId или pass")
		return
	}

	var respId uint64
	if _, err := fmt.Sscanf(respIdStr, "%d", &respId); err != nil {
		logger.Error("'CreateMasterKeyWSS' некорректный respId: %v", err, userId)
		w.sendWSError(conn, "Некорректный формат respId")
		return
	}

	// Callback для отправки прогресса (с ручной локализацией технических кодов)
	sendProgress := func(message string) {
		translated := message
		switch {
		case message == "NO_KEYS_TO_ENCRYPT":
			translated = "ℹ️ API-ключи отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_KEY:"):
			provider := strings.TrimPrefix(message, "ENCRYPTING_KEY:")
			translated = fmt.Sprintf("🔐 Шифрование API-ключа %s...", provider)
		case strings.HasPrefix(message, "ENCRYPTED_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано API-ключей: %s", count)
		case message == "NO_CHANNELS_TO_ENCRYPT":
			translated = "ℹ️ Данные каналов отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_CHANNEL:"):
			ch := strings.TrimPrefix(message, "ENCRYPTING_CHANNEL:")
			translated = fmt.Sprintf("🔐 Шифрование данных канала %s...", ch)
		case strings.HasPrefix(message, "ENCRYPTED_CHANNELS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_CHANNELS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано каналов: %s", count)
		case message == "NO_DIALOGS_TO_ENCRYPT":
			translated = "ℹ️ Диалоги отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_DIALOGS:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_DIALOGS:")
			translated = fmt.Sprintf("🔐 Шифрование %s диалогов...", count)
		case strings.HasPrefix(message, "ENCRYPTED_DIALOGS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_DIALOGS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано диалогов: %s", count)
		case message == "NO_GOOGLE_TOKEN_TO_ENCRYPT":
			translated = "ℹ️ Google токен отсутствует или уже зашифрован"
		case message == "ENCRYPTING_GOOGLE_TOKEN":
			translated = "🔐 Шифрование Google OAuth токена..."
		case message == "ENCRYPTED_GOOGLE_TOKEN":
			translated = "✅ Google OAuth токен зашифрован"
		case message == "NO_VECTORS_TO_ENCRYPT":
			translated = "ℹ️ Векторные документы отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_VECTORS:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_VECTORS:")
			translated = fmt.Sprintf("🔐 Шифрование %s документов...", count)
		case strings.HasPrefix(message, "ENCRYPTED_VECTORS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_VECTORS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано документов: %s", count)
		case message == "NO_CRM_CONFIGS_TO_ENCRYPT":
			translated = "ℹ️ CRM‑конфигурации отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_CRM_CONFIGS:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_CRM_CONFIGS:")
			translated = fmt.Sprintf("🔐 Шифрование %s CRM‑конфигураций...", count)
		case strings.HasPrefix(message, "ENCRYPTED_CRM_CONFIGS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_CRM_CONFIGS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано CRM‑конфигураций: %s", count)
		case message == "NO_OAUTH_STATES_TO_ENCRYPT":
			translated = "ℹ️ OAuth‑состояния отсутствуют или уже зашифрованы"
		case strings.HasPrefix(message, "ENCRYPTING_OAUTH_STATES:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_OAUTH_STATES:")
			translated = fmt.Sprintf("🔐 Шифрование %s OAuth‑состояний...", count)
		case strings.HasPrefix(message, "ENCRYPTED_OAUTH_STATES_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_OAUTH_STATES_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано OAuth‑состояний: %s", count)
		case strings.HasPrefix(message, "NO_USER_STORAGE_CONFIGS_TO_ENCRYPT:"):
			count := strings.TrimPrefix(message, "NO_USER_STORAGE_CONFIGS_TO_ENCRYPT:")
			translated = fmt.Sprintf("ℹ️ Конфиги хранилища отсутствуют или уже зашифрованы: %s", count)
		case strings.HasPrefix(message, "ENCRYPTED_USER_STORAGE_CONFIGS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_USER_STORAGE_CONFIGS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано конфигов хранилища: %s", count)

			// События шифрования таблиц с данными ботов в service
		case strings.HasPrefix(message, "NO_SERVICE_TGBOTS_TO_ENCRYPT"):
			translated = "ℹ️ Нет Telegram‑ботов для шифрования"
		case strings.HasPrefix(message, "NO_SERVICE_WABOTS_TO_ENCRYPT"):
			translated = "ℹ️ Нет WhatsApp‑ботов для шифрования"
		case strings.HasPrefix(message, "ENCRYPTING_SERVICE_TGBOTS:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_SERVICE_TGBOTS:")
			translated = fmt.Sprintf("🔒 Начато шифрование Telegram‑ботов: %s", count)
		case strings.HasPrefix(message, "ENCRYPTING_SERVICE_WABOTS:"):
			count := strings.TrimPrefix(message, "ENCRYPTING_SERVICE_WABOTS:")
			translated = fmt.Sprintf("🔒 Начато шифрование WhatsApp‑ботов: %s", count)
		case strings.HasPrefix(message, "ENCRYPTED_SERVICE_TGBOTS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_SERVICE_TGBOTS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано Telegram‑ботов: %s", count)
		case strings.HasPrefix(message, "ENCRYPTED_SERVICE_WABOTS_COUNT:"):
			count := strings.TrimPrefix(message, "ENCRYPTED_SERVICE_WABOTS_COUNT:")
			translated = fmt.Sprintf("✅ Зашифровано WhatsApp‑ботов: %s", count)
		}

		w.sendWSMessage(conn, "progress", translated, nil)
	}

	sendProgress("🔄 Создание MasterKey...")

	rawB64, err := w.masterKeyUC.CreateMasterKey(userId, respId, encPass, sendProgress)
	if err != nil {
		if err.Error() == "MASTER_KEY_ALREADY_EXISTS" {
			w.sendWSError(conn, "MasterKey уже существует")
			return
		}
		if err.Error() == "INVALID_PASSWORD" {
			w.sendWSError(conn, "Неверный пароль")
			return
		}
		logger.Error("'CreateMasterKeyWSS' failed: %v", err, userId)
		w.sendWSError(conn, "Внутренняя ошибка сервера")
		return
	}

	logger.Debug("'CreateMasterKeyWSS' MasterKey created", userId)

	// Отправляем успешный результат с raw MasterKey
	w.sendWSMessage(conn, "success", "✅ MasterKey создан и данные зашифрованы", map[string]any{
		"raw_master_key": rawB64,
		"warning":        "Сохраните этот ключ в надёжном месте — он больше не будет показан. Он потребуется при смене или сбросе пароля.",
	})

	// Закрываем соединение
	if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		logger.Error("'CreateMasterKeyWSS' error sending close message: %v", err, userId)
	}
}

// VerifyUserPassword POST /auth/verify-password
// Проверяет пароль пользователя против сохранённого хеша в БД.
// userId берётся из токена (authAllowMiddleware).
// respId передаётся в теле — свежий respId из POST /key (TTL 30 сек).
//
// Request:  { "a": respId (из POST /key), "b": AES-зашифрованный пароль }
// Response: {} (200 OK) или 401 (неверный пароль)
func (w *Web) VerifyUserPassword(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		RespId uint64 `json:"a" binding:"required"`
		Pass   string `json:"b" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		logger.Error("'VerifyUserPassword' JSON parsing error: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.masterKeyUC.VerifyPassword(userId, body.RespId, body.Pass)
	if err != nil {
		if err.Error() == "INVALID_PASSWORD" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
			return
		}
		logger.Error("'VerifyUserPassword' error: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// RewrapMasterKey POST /auth/rewrap-master-key
// Перешифровывает (rewrap) MasterKey с новым паролем.
// Требует авторизации. userId берётся из токена (authAllowMiddleware).
// respId передаётся в теле — свежий respId из POST /key.
//
// Два сценария:
//   - "b" передан (raw MasterKey base64) → перешифровывает MasterKey с новым паролем
//   - "b" не передан → MasterKey утрачен: удаляет зашифрованные данные и сбрасывает MasterKey в NULL
//
// Request:  { "a": respId (из POST /key), "b": raw MasterKey base64 (опционально), "c": новый пароль AES-CBC }
// Response: {}
func (w *Web) RewrapMasterKey(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		RespId       uint64 `json:"a" binding:"required"`
		RawMasterKey string `json:"b"` // опционально: если не передан — MasterKey утрачен
		NewPass      string `json:"c" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		logger.Error("'RewrapMasterKey' JSON parsing error: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.masterKeyUC.RewrapOrReset(userId, body.RespId, body.RawMasterKey, body.NewPass)
	if err != nil {
		logger.Error("'RewrapMasterKey' error: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ============================================================================
// WebSocket Helpers
// ============================================================================

func (w *Web) sendWSMessage(conn *websocket.Conn, msgType, message string, data any) {
	msg := gin.H{
		"type":    msgType,
		"message": message,
	}
	if data != nil {
		msg["data"] = data
	}
	if err := conn.WriteJSON(msg); err != nil {
		logger.Error("Error sending WebSocket message: %v", err)
	}
}

func (w *Web) sendWSError(conn *websocket.Conn, message string) {
	w.sendWSMessage(conn, "error", message, nil)
}
