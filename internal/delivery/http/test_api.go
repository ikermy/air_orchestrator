package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air_common/pkg/comerrors"
	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// providerErrorResponse writes a stable, safe API response for errors returned
// by an AI provider. The provider's raw response is intentionally kept out of
// the client response because it may contain internal details or credentials.
func providerErrorResponse(c *gin.Context, err error) bool {
	var providerErr *comerrors.ProviderError
	if !errors.As(err, &providerErr) {
		return false
	}

	status := http.StatusBadRequest
	switch providerErr.Kind {
	case comerrors.ProviderLimitErrorKind:
		status = http.StatusTooManyRequests
	case comerrors.ProviderAuthErrorKind:
		status = http.StatusUnauthorized
	case comerrors.ProviderPermissionErrorKind:
		status = http.StatusForbidden
	case comerrors.ProviderTimeoutErrorKind:
		status = http.StatusGatewayTimeout
	case comerrors.ProviderUnavailableErrorKind:
		status = http.StatusServiceUnavailable
	case comerrors.ProviderRequestErrorKind, comerrors.ProviderContentBlockedErrorKind:
		status = http.StatusBadRequest
	default:
		status = http.StatusInternalServerError
	}

	c.JSON(status, gin.H{
		"error":    string(providerErr.Kind),
		"provider": providerErr.Provider.String(),
	})
	return true
}

// wsPingSignal — маркер-сигнал для отправки WebSocket Ping через sendBuffer.
// Позволяет избежать concurrent write: все записи в conn проходят только через ГОРУТИНУ 2.
type wsPingSignal struct{}

// testStartSessionHandler godoc
// @Summary Запустить тестовую сессию
// @Tags api
// @Produce json
// @Security BearerAuth
// @Param provider query string true "Тип провайдера"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /api/test/start [get]
// testStartSessionHandler обрабатывает POST /api/test/start
// userId и respId извлекаются из токена в middleware
func (w *Web) testStartSessionHandler(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	respId, ok := getRespId(c)
	if !ok {
		return
	}

	// Получаем provider из query-параметров
	prov := c.Query("provider")
	if prov == "" || prov == "undefined" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider parameter is required"})
		return
	}

	// Преобразуем строковый provider в commdom.ProviderType
	provider, err := commdom.FromString(prov)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid provider: %s", prov)})
		return
	}

	session, activeModel, err := w.api.StartSession(w.ctx, userId, respId, provider)
	if err != nil {
		logger.Error("Ошибка запуска тестовой сессии, respId=%d, provider=%s: %v", respId, provider.String(), err, userId)
		if providerErrorResponse(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start test session"})
		return
	}
	logger.Debug("TestAPI start: userId=%d respId=%d provider=%s model=%s web_search=%v",
		userId, respId, provider.String(), activeModel.Name, activeModel.WebSearch)

	// Формируем базовый ответ
	response := gin.H{
		"user_id":    session.UserId,
		"resp_id":    session.RespId,
		"tread_id":   session.TreadId,
		"started_at": session.StartedAt.Format(time.RFC3339),
	}

	// Добавляем информацию о модели если она доступна
	if activeModel != nil {
		response["provider"] = activeModel.Provider.String()
		response["model_name"] = activeModel.Name
		response["s3_files"] = activeModel.S3
		response["image_generation"] = activeModel.Image
		response["code_interpreter"] = activeModel.Interpreter
		response["web_search"] = activeModel.WebSearch
		response["video_generation"] = activeModel.Video
		response["calendar"] = activeModel.GOAuth.Calendar
		response["sheets"] = activeModel.GOAuth.Sheets
		response["ignore"] = activeModel.Espero.Ignore
		response["realtime"] = activeModel.Realtime // Голосовой режим доступен
		if activeModel.Realtime {
			// Все realtime-провайдеры, включая Mistral cascade STT→LLM→TTS,
			// подключаются через общий /ws/test-realtime transport.
			response["realtime_provider"] = activeModel.Provider.String()
			response["realtime_transport"] = "websocket"
		}
		if activeModel.RealtimeVAD != nil {
			response["greeting"] = activeModel.RealtimeVAD.InitialGreeting // Модель приветствует первой
		}
	}

	c.JSON(http.StatusCreated, response)
}

// testAskHandler godoc
// @Summary Отправить вопрос в тестовую сессию
// @Tags api
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Вопрос пользователя"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /api/test/ask [post]
func (w *Web) testAskHandler(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	respId, ok := getRespId(c)
	if !ok {
		return
	}

	// Принимаем полное сообщение в формате model.Message
	var msg struct {
		Type      string               `json:"type"`                 // "user" или "user_voice"
		Content   model.AssistResponse `json:"content"`              // Содержимое сообщения
		Files     []model.FileUpload   `json:"files,omitempty"`      // Файлы если есть
		AudioData []byte               `json:"audio_data,omitempty"` // Данные аудио для user_voice
		AudioName string               `json:"audio_name,omitempty"` // Имя аудио файла
	}

	if err := c.ShouldBindJSON(&msg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	logger.Debug("Получено сообщение, respId=%d: Type=%s, Message=%s, Files=%d",
		respId, msg.Type, msg.Content.Message, len(msg.Files), userId)

	// Обработка голосового сообщения
	if msg.Type == "user_voice" {
		// Проверяем наличие аудио данных
		if len(msg.AudioData) == 0 {
			logger.Error("user_voice: отсутствуют аудио данные")
			c.JSON(http.StatusBadRequest, gin.H{"error": "audio_data required for user_voice type"})
			return
		}

		// Определяем MIME тип по расширению файла
		audioFileName := msg.AudioName
		if audioFileName == "" {
			audioFileName = "voice_message.ogg"
		}

		// Извлекаем расширение
		ext := ".ogg" // по умолчанию
		if idx := len(audioFileName) - 1; idx > 0 {
			for i := idx; i >= 0; i-- {
				if audioFileName[i] == '.' {
					ext = audioFileName[i:]
					break
				}
			}
		}

		// Определяем MIME тип по расширению
		mimeType := "audio/ogg" // по умолчанию
		switch ext {
		case ".mp3":
			mimeType = "audio/mpeg"
		case ".wav":
			mimeType = "audio/wav"
		case ".ogg":
			mimeType = "audio/ogg"
		case ".flac":
			mimeType = "audio/flac"
		case ".aac":
			mimeType = "audio/aac"
		case ".m4a":
			mimeType = "audio/mp4"
		case ".webm":
			mimeType = "audio/webm"
		default:
			logger.Warn("Неизвестное расширение аудио: %s, используем audio/ogg", ext)
		}

		logger.Debug("Транскрибация аудио: размер=%d байт, исходное имя=%s, MIME=%s",
			len(msg.AudioData), msg.AudioName, mimeType)

		// Вызываем транскрибацию с MIME типом
		transcribedText, err := w.mod.TranscribeAudio(userId, msg.AudioData, mimeType)
		if err != nil {
			logger.Error("Ошибка транскрибации аудио: %v", err)
			if providerErrorResponse(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("transcription failed: %v", err)})
			return
		}

		logger.Debug("Аудио успешно транскрибировано: %s", transcribedText)

		// Заменяем текст сообщения на транскрибированный текст
		msg.Content.Message = transcribedText

		// Убираю упоминание о файлах в сообщении, так как аудио уже обработано
		msg.Files = nil
	}

	// Проверяем что есть текст сообщения или файлы
	if msg.Content.Message == "" && len(msg.Files) == 0 {
		logger.Error("Пустое сообщение: нет текста и файлов")
		c.JSON(http.StatusBadRequest, gin.H{"error": "message content required"})
		return
	}

	// Устанавливаем тип сообщения по умолчанию если не указан
	if msg.Type == "" {
		msg.Type = "user"
	}

	// Создаем полное сообщение для отправки в канал
	message := model.Message{
		Type:      msg.Type,
		Content:   msg.Content,
		Operator:  model.Operator{SetOperator: false, Operator: false},
		Timestamp: time.Now(),
		Files:     msg.Files,
	}

	// Вызываем метод API для отправки сообщения
	if err := w.api.SendMessage(userId, respId, message); err != nil {
		logger.Error("Ошибка отправки сообщения: %v", err)
		if providerErrorResponse(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	logger.Debug("TestAPI message accepted: userId=%d respId=%d type=%s text_len=%d files=%d",
		userId, respId, message.Type, len(message.Content.Message), len(message.Files))

	c.JSON(http.StatusAccepted, gin.H{
		"status":    "message_sent",
		"timestamp": time.Now(),
	})
}

// testWebSocketHandler godoc
// @Summary WebSocket для получения ответов тестовой сессии
// @Tags api
// @Produces text/event-stream
// @Security BearerAuth
// @Router /ws/test-model [get]
func (w *Web) testWebSocketHandler(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	respId, ok := getRespId(c)
	if !ok {
		return
	}

	// Upgrade HTTP to WebSocket
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("Ошибка апгрейда до WebSocket: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения в TestEventHandler: %v", err)
		}
	}()

	logger.Debug("WebSocket соединение установлено, respId=%d", respId, userId)

	// Получаем канал из TestSession вместо ModelRouter.GetCh
	testAPI := w.api
	// Фактически сессия соза1тся в func (ta *TestAPI) StartSession а GetChannel просто ищет её в мапе
	ch, err := testAPI.GetChannel(userId, respId)
	if err != nil {
		logger.Error("Ошибка получения канала respId=%d: %v", respId, err, userId)
		if writeErr := conn.WriteJSON(gin.H{"error": err.Error()}); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке: %v", writeErr)
		}
		return
	}

	logger.Debug("Канал получен успешно для respId=%d, TxCh=%p, RxCh=%p", respId, ch.TxCh, ch.RxCh)

	// КРИТИЧЕСКИ ВАЖНО: Очищаем WebSocket сессию из sync.Map при закрытии соединения
	// Используем CleanupWebSocketSession чтобы сохранить канал в RespModel для возможности переподключения
	// Если пользователь не переподключится в течение TTL, респондент автоматически удалится через periodicFlush
	defer func() {
		if cleanupErr := testAPI.CleanupWebSocketSession(userId, respId); cleanupErr != nil {
			logger.Warn("Ошибка очистки WebSocket сессии: %v", cleanupErr, userId)
		} else {
			logger.Debug("WebSocket сессия успешно очищена, respId=%d", respId, userId)
		}
	}()

	// Пинг-понг для поддержания соединения
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				logger.Debug("WebSocket соединение закрыто клиентом: %v", err)
				return
			}
		}
	}()

	// Читаем ответы от модели и отправляем их в WebSocket
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	logger.Debug("Начинаем слушать канал TxCh для respId=%d, TxCh=%p, буфер=%d/%d",
		respId, ch.TxCh, len(ch.TxCh), cap(ch.TxCh))

	// КРИТИЧНО: Промежуточный буфер для максимально быстрого чтения из TxCh
	// Это предотвращает переполнение TxCh (размер 1) при быстром потоке от OpenAI Priority tier
	// Отдельная горутина непрерывно читает из TxCh и помещает в messageBuffer для обработки
	messageBuffer := make(chan model.Message, 2000)

	// Буфер для асинхронной отправки в WebSocket (предотвращает блокировку чтения из TxCh)
	// Увеличен до 2000 для максимально надёжной доставки дельт (даже при медленном клиенте)
	sendBuffer := make(chan any, 2000)
	sendBufferClosed := make(chan struct{})

	// Счётчики для мониторинга использования буфера
	var messageCount int
	var maxBufferUsage int
	var maxMessageBufferUsage int

	// Переменные для отслеживания времени отправки
	var firstDeltaTime time.Time
	var firstDeltaSent bool

	// ГОРУТИНА 1: КРИТИЧНО - Максимально быстрое чтение из TxCh -> messageBuffer
	// Используем блокирующий select для предотвращения высокой загрузки CPU
	go func() {
		defer close(messageBuffer)
		for {
			select {
			case <-done:
				return
			case <-c.Request.Context().Done():
				return
			case msg, ok := <-ch.TxCh:
				if !ok {
					return
				}
				// БЛОКИРУЮЩАЯ отправка в messageBuffer (буфер 2000)
				messageBuffer <- msg

				// Мониторинг
				currentUsage := len(messageBuffer)
				if currentUsage > maxMessageBufferUsage {
					maxMessageBufferUsage = currentUsage
				}
				if currentUsage > 1500 {
					logger.Warn("К [MONITOR] messageBuffer высокая загрузка: %d/2000 (%.1f%%), respId=%d",
						currentUsage, float64(currentUsage)/2000.0*100.0, respId, userId)
				}
			}
		}
	}()

	// ГОРУТИНА 2: Отправка в WebSocket (разделяем чтение и отправку)
	// Все записи в conn идут строго через эту горутину — исключаем concurrent write.
	go func() {
		defer close(sendBufferClosed)
		for payload := range sendBuffer {
			// Специальный маркер для ping — отправляем через WriteMessage, не JSON
			if _, isPing := payload.(wsPingSignal); isPing {
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					logger.Debug("Ошибка отправки ping в WebSocket: %v", err, userId)
					return
				}
				continue
			}
			if err := conn.WriteJSON(payload); err != nil {
				logger.Error("Ошибка отправки в WebSocket: %v", err, userId)
				return
			}
		}
	}()

	// ОСНОВНОЙ ЦИКЛ: Обработка сообщений из messageBuffer (а не напрямую из TxCh!)
	for {
		select {
		case <-done:
			logger.Debug("WebSocket соединение закрыто")
			close(sendBuffer)
			<-sendBufferClosed // Ждем завершения горутины отправки
			return

		case <-c.Request.Context().Done():
			logger.Debug("Контекст запроса завершен")
			close(sendBuffer)
			<-sendBufferClosed // Ждем завершения горутины отправки
			return

		case msg, ok := <-messageBuffer:
			if !ok {
				logger.Debug("Канал ответов закрыт")
				sendBuffer <- gin.H{"status": "channel_closed"}
				close(sendBuffer)
				return
			}

			// Фильтруем - отправляем только ответы от ассистента и потоковые дельты
			if msg.Type != "assistant" && msg.Type != "assist" && msg.Type != "assistant_delta" {
				continue
			}

			// Обработка потоковых дельт (частичные обновления для TRUE STREAMING)
			if msg.Type == "assistant_delta" {
				// Фиксируем время отправки первой дельты
				if !firstDeltaSent {
					firstDeltaTime = time.Now()
					firstDeltaSent = true
					logger.Info("Первая дельта отправлена в %s respId=%d",
						firstDeltaTime.Format("15:04:05.000"), respId, userId)
				}

				// Проверяем, является ли content JSON событием function call
				content := msg.Content.Message
				var deltaPayload any

				// Если content начинается с '{', это может быть JSON событие
				if len(content) > 0 && content[0] == '{' {
					var event map[string]any
					if err := json.Unmarshal([]byte(content), &event); err == nil {
						if eventType, ok := event["type"].(string); ok {
							// Проверяем типы событий function calls
							if strings.HasPrefix(eventType, "response.output_item.") ||
								strings.HasPrefix(eventType, "response.function_call_arguments.") ||
								eventType == "function_result" ||
								eventType == "token_usage" {
								// Это JSON событие function call - отправляем как есть
								deltaPayload = event

								// Детальное логирование для function calls
								if strings.HasPrefix(eventType, "response.function_call_arguments.") {
								} else if eventType == "token_usage" {
									logger.Debug("и [WebSocket] Token usage событие отправлено клиенту, respId=%d", respId, userId)
								}
							}
						}
					}
				}

				// Если это не JSON событие, формируем обычный payload
				if deltaPayload == nil {
					deltaPayload = gin.H{
						"type":      "delta",
						"content":   content,
						"timestamp": msg.Timestamp.Format(time.RFC3339),
					}
				}

				// Неблокирующая отправка через буфер для максимальной скорости чтения из TxCh
				select {
				case sendBuffer <- deltaPayload:
					// Успешно отправлено
					messageCount++
					// Мониторинг использования sendBuffer
					currentUsage := len(sendBuffer)
					if currentUsage > maxBufferUsage {
						maxBufferUsage = currentUsage
					}
					// Логируем при высокой загрузке
					if currentUsage > 1500 {
						logger.Warn("[MONITOR] sendBuffer высокая загрузка: %d/2000 (75%%), respId=%d",
							currentUsage, respId, userId)
					}
				default:
					// Буфер переполнен - пропускаем дельту (клиент увидит следующую)
					logger.Warn("sendBuffer переполнен, дельта пропущена, respId=%d", respId, userId)
				}
				continue
			}

			// Диагностический лог: что именно пришло от модели
			logger.Debug("[FINAL] msg.Type=%q, len(message)=%d, SendFiles=%d, UploadedFiles=%d, Meta=%v, respId=%d",
				msg.Type, len(msg.Content.Message),
				len(msg.Content.Action.SendFiles), len(msg.Files),
				msg.Content.Meta, respId, userId)

			// Предупреждение: пустое текстовое сообщение без файлов — клиент может показать ошибку
			if msg.Content.Message == "" && len(msg.Content.Action.SendFiles) == 0 && len(msg.Files) == 0 {
				logger.Warn("[FINAL] Пустой ответ от модели (нет текста и файлов), respId=%d, type=%q — клиент может показать ошибку", respId, msg.Type, userId)
			}

			// Нормализуем тип: "assist" → "assistant"
			// Фронтенд ожидает "assistant", внутренний тип "assist" не должен доходить до клиента
			msgTypeForClient := msg.Type
			if msgTypeForClient == "assist" {
				msgTypeForClient = "assistant"
			}

			// Формируем полный ответ со всеми полями Message
			finalTime := time.Now()

			// Если была первая дельта, выводим разницу во времени
			if firstDeltaSent {
				duration := finalTime.Sub(firstDeltaTime)
				logger.Info("Время от первой дельты до финального ответа: %v respId=%d",
					duration, respId, userId)
			}

			// Выводим статистику использования буферов
			logger.Info("[MONITOR] sendBuffer: отправлено=%d сообщений, макс.загрузка sendBuffer=%d/2000 (%.1f%%), макс.загрузка messageBuffer=%d/2000 (%.1f%%), respId=%d",
				messageCount, maxBufferUsage, float64(maxBufferUsage)/20.0, maxMessageBufferUsage, float64(maxMessageBufferUsage)/20.0, respId, userId)

			payload := gin.H{
				"type":       msgTypeForClient,
				"message":    msg.Content.Message,
				"operator":   msg.Operator.Operator,
				"created_at": msg.Timestamp,
				"name":       msg.Name,
			}

			// Добавляем метаданные если есть
			if msg.Content.Meta {
				payload["meta"] = true
			}

			// Добавляем файлы из Action.SendFiles если есть
			if len(msg.Content.Action.SendFiles) > 0 {
				files := make([]gin.H, 0, len(msg.Content.Action.SendFiles))
				for _, file := range msg.Content.Action.SendFiles {
					fileInfo := gin.H{
						"type":      file.Type,     // "photo", "file", "video", etc.
						"url":       file.URL,      // URL файла
						"file_name": file.FileName, // Имя файла
						"caption":   file.Caption,  // Подпись
					}
					files = append(files, fileInfo)
				}
				payload["files"] = files

				// Для удобства клиента добавляем прямые поля для первого файла
				firstFile := msg.Content.Action.SendFiles[0]
				if firstFile.Type == "photo" {
					payload["image_url"] = firstFile.URL
					payload["file_type"] = "image"
				} else {
					payload["file_url"] = firstFile.URL
					payload["file_type"] = "file"
				}
				payload["file_name"] = firstFile.FileName
			}

			// Добавляем загруженные файлы если есть
			if len(msg.Files) > 0 {
				uploadedFiles := make([]gin.H, 0, len(msg.Files))
				for _, file := range msg.Files {
					uploadedFiles = append(uploadedFiles, gin.H{
						"name":      file.Name,
						"mime_type": file.MimeType,
					})
				}
				payload["uploaded_files"] = uploadedFiles
			}

			// Отправляем через буфер (неблокирующая отправка)
			select {
			case sendBuffer <- payload:
				logger.Info("[FINAL] Финальный ответ отправлен клиенту в %s, type=%q, len(message)=%d, respId=%d",
					finalTime.Format("15:04:05.000"), msgTypeForClient, len(msg.Content.Message), respId, userId)
			default:
				logger.Warn("[FINAL] sendBuffer переполнен — финальное сообщение ПРОПУЩЕНО, respId=%d", respId, userId)
			}

		case <-ticker.C:
			// Ping роутим через sendBuffer — исключает concurrent write с ГОРУТИНОЙ 2
			select {
			case sendBuffer <- wsPingSignal{}:
			default:
				logger.Debug("sendBuffer занят, ping пропущен, respId=%d", respId, userId)
			}
		}
	}
}

// testStopSessionHandler godoc
// @Summary Остановить тестовую сессию
// @Tags api
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /api/test/stop [delete]
func (w *Web) testStopSessionHandler(c *gin.Context) {
	logger.Debug("Test session stop")
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	respId, ok := getRespId(c)
	if !ok {
		return
	}
	logger.Debug("Stopping test session for userId=%d, respId=%d", userId, respId)
	// Вызываем метод API для остановки сессии
	if err := w.api.StopSession(userId, respId); err != nil {
		logger.Error("Ошибка остановки тестовой сессии: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Ошибка остановки тестовой сессии"})
		return
	}
	logger.Debug("Test session stop ok")
	c.JSON(http.StatusOK, gin.H{"status": "session_stopped"})
}
