package web

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// testRealtimeHandler обрабатывает GET /ws/test-realtime?tread_id=<N>
// tread_id — обязательный query-параметр, клиент получает его из GET /api/test/start.
// Передаётся явно т.к. TestSession может быть удалена из sessions после закрытия /ws/test-model,
// при этом RespModel (и AgentConfig) продолжают жить до TTL.
// Поддерживает OpenAI Realtime API и Google Live API.
func (w *Web) testRealtimeHandler(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	respId, ok := getRespId(c)
	if !ok {
		return
	}

	// tread_id обязателен — клиент получает его из /api/test/start в поле "tread_id"
	treadIdStr := c.Query("tread_id")
	if treadIdStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tread_id query parameter is required"})
		return
	}
	treadId, err := strconv.ParseUint(treadIdStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid tread_id: %s", treadIdStr)})
		return
	}

	// Upgrade HTTP ’ WebSocket (с поддержкой subprotocols для передачи токена)
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("testRealtimeHandler: Ошибка при upgrade WS respId=%d: %v", respId, err, userId)
		return
	}
	defer func() {
		_ = conn.Close()
		w.api.StopRealtimeSession(userId, respId)
		logger.Debug("testRealtimeHandler: соединение закрыто respId=%d", respId, userId)
	}()

	// Запускаем сессию реального времени (поддержка Realtime API: OpenAI и Google).
	// treadId используется для идентификации сессии в TestSession и sessions.
	if err = w.api.StartRealtimeSession(userId, respId, treadId); err != nil {
		errMsg, _ := json.Marshal(map[string]string{"type": "error", "text": err.Error()})
		_ = conn.WriteMessage(websocket.TextMessage, errMsg)
		logger.Error("testRealtimeHandler: Ошибка при StartRealtimeSession respId=%d treadId=%d: %v",
			respId, treadId, err, userId)
		return
	}

	// Получаем каналы аудио и событий
	audioCh, eventCh, err := w.api.GetRealtimeChannels(userId, respId)
	if err != nil {
		errMsg, _ := json.Marshal(map[string]string{"type": "error", "text": err.Error()})
		_ = conn.WriteMessage(websocket.TextMessage, errMsg)
		logger.Error("testRealtimeHandler: Ошибка при GetRealtimeChannels respId=%d: %v", respId, err, userId)
		return
	}
	defer w.api.UnsubscribeRealtimeEvents(userId, respId, eventCh)

	logger.Info("testRealtimeHandler: Получены каналы аудио и событий respId=%d treadId=%d", respId, treadId, userId)

	// Отправляем клиенту подтверждение готовности
	readyMsg, _ := json.Marshal(map[string]string{"type": "ready"})
	if err := conn.WriteMessage(websocket.TextMessage, readyMsg); err != nil {
		return
	}

	// Горутина: читаем аудио/события от провайдера Realtime (OpenAI или Google) ’ отправляем клиенту
	sendDone := make(chan struct{})
	go func() {
		//recorder, recorderErr := newRealtimeWAVRecorder(userId, respId, 24000)
		//if recorderErr != nil {
		//	logger.Warn("testRealtimeHandler: не удалось открыть WAV recorder: %v", recorderErr, userId)
		//}
		//if recorder != nil {
		//	defer func() {
		//		if err := recorder.Close(); err != nil {
		//			logger.Warn("testRealtimeHandler: ошибка закрытия WAV recorder: %v", err, userId)
		//		}
		//	}()
		//}
		defer func() {
			close(sendDone)
			// Закрываем WS-соединение чтобы разблокировать ReadMessage() в основном цикле.
			// Без этого основной цикл продолжает получать аудио от клиента и спамит
			// предупреждениями "сессия завершена" пока клиент сам не закроет соединение.
			_ = conn.Close()
		}()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		var audioFramesSent int
		var audioBytesSent int

		// Инициализация переменных для отслеживания времени первого дельта-сообщения
		var firstDeltaTime time.Time
		var firstDeltaSent bool

		for {
			select {
			case pcm16, ok := <-audioCh:
				if !ok {
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, pcm16); err != nil {
					logger.Debug("testRealtimeHandler: ошибка отправки audio: %v", err, userId)
					return
				}
				//if recorder != nil {
				//	if err := recorder.Write(pcm16); err != nil {
				//		logger.Warn("testRealtimeHandler: ошибка записи WAV: %v", err, userId)
				//	}
				//}
				// Инициализация переменных для отслеживания времени первого дельта-сообщения
				if !firstDeltaSent {
					firstDeltaTime = time.Now()
					firstDeltaSent = true
					logger.Info("[Realtime] Первая аудио-дельта отправлена клиенту в %s respId=%d",
						firstDeltaTime.Format("15:04:05.000"), respId, userId)
				}
				audioFramesSent++
				audioBytesSent += len(pcm16)

			case ev, ok := <-eventCh:
				if !ok {
					return
				}

				// Raw PCM for audio_delta is delivered through audioCh above as a
				// binary WebSocket frame. Mistral also publishes the same PCM in
				// RealtimeEvent.Data; never send that raw byte slice as a text
				// frame, otherwise the client closes the socket with Invalid UTF-8.
				// Other providers' text events continue through the normal branch.
				if ev.Type == "audio_delta" {
					continue
				}

				// barge-in: обработка прерывания текущей сессии
				if ev.Type == "interrupted" {
					// Сбрасываем аудио-каналы и очищаем очередь аудио
					drained := false
					for !drained {
						select {
						case <-audioCh:
						default:
							drained = true
						}
					}
					// Сбрасываем счетчики аудио
					audioFramesSent = 0
					audioBytesSent = 0
					firstDeltaSent = false
					// Отправляем сообщение о завершении аудио
					stopMsg, _ := json.Marshal(map[string]string{"type": "audio_stop"})
					if err := conn.WriteMessage(websocket.TextMessage, stopMsg); err != nil {
						logger.Debug("testRealtimeHandler: ошибка отправки audio_stop: %v", err, userId)
						return
					}
					logger.Info("[Realtime] barge-in: audio_stop отправлено клиенту respId=%d", respId, userId)
					continue
				}

				if ev.Type == "response_done" {
					finalTime := time.Now()
					durStr := ""
					if firstDeltaSent {
						durStr = fmt.Sprintf(", время до первого дельта-сообщения: %v", finalTime.Sub(firstDeltaTime))
					}
					logger.Info("[Realtime] response_done в сессии: frames=%d, bytes=%d (~%.1f кБ PCM16@24kHz)%s respId=%d",
						audioFramesSent, audioBytesSent,
						float64(audioBytesSent)/float64(24000*2),
						durStr, respId, userId)
					audioFramesSent = 0
					audioBytesSent = 0
					firstDeltaSent = false
				}

				var data []byte
				if ev.Data != nil {
					// Data уже содержит готовый JSON (token_usage и др.)
					data = ev.Data

					// Обработка token_usage аналогично testWebSocketHandler
					if ev.Type == "token_usage" {
						var tu map[string]any
						if err := json.Unmarshal(ev.Data, &tu); err == nil {
							if usage, ok := tu["usage"].(map[string]any); ok {
								inputTokens, _ := usage["input_tokens"].(float64)
								outputTokens, _ := usage["output_tokens"].(float64)
								totalTokens, _ := usage["total_tokens"].(float64)
								cachedTokens := 0.0
								if details, ok := usage["input_tokens_details"].(map[string]any); ok {
									cachedTokens, _ = details["cached_tokens"].(float64)
								}
								audioIn, audioOut := 0.0, 0.0
								if details, ok := usage["input_tokens_details"].(map[string]any); ok {
									audioIn, _ = details["audio_tokens"].(float64)
								}
								if details, ok := usage["output_tokens_details"].(map[string]any); ok {
									audioOut, _ = details["audio_tokens"].(float64)
								}
								if cachedTokens > 0 {
									logger.Info("и [Realtime TOKEN USAGE] Input: %d (audio:%d, cached:%d °) | Output: %d (audio:%d) | Total: %d respId=%d",
										int(inputTokens), int(audioIn), int(cachedTokens),
										int(outputTokens), int(audioOut),
										int(totalTokens), respId, userId)
								} else {
									logger.Info("и [Realtime TOKEN USAGE] Input: %d (audio:%d) | Output: %d (audio:%d) | Total: %d respId=%d",
										int(inputTokens), int(audioIn),
										int(outputTokens), int(audioOut),
										int(totalTokens), respId, userId)
								}
							}
						}
					}
				} else {
					payload := map[string]any{
						"type": ev.Type,
						"text": ev.Text,
					}
					if ev.Type == "response_done" && len(ev.Files) > 0 {
						// Преобразуем []model.File в формат, подходящий для отправки клиенту
						files := make([]map[string]any, 0, len(ev.Files))
						for _, f := range ev.Files {
							files = append(files, map[string]any{
								"type":      string(f.Type),
								"url":       f.URL,
								"file_name": f.FileName,
								"caption":   f.Caption,
							})
						}
						payload["type"] = "assist"
						// Непустой message — иначе клиент может проигнорировать сообщение
						payload["message"] = ev.Files[0].FileName
						payload["files"] = files
						first := ev.Files[0]
						if first.Type == "photo" {
							payload["image_url"] = first.URL
							payload["file_type"] = "image"
						} else {
							payload["file_url"] = first.URL
							payload["file_type"] = "file"
						}
						payload["file_name"] = first.FileName
						logger.Info("[Realtime] response_done → assist: count=%d file=%s respId=%d",
							len(ev.Files), ev.Files[0].FileName, respId, userId)
					}
					if ev.Err != nil {
						payload["error"] = ev.Err.Error()
					}
					data, _ = json.Marshal(payload)
				}

				if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
					logger.Debug("testRealtimeHandler: ошибка отправки сообщения: %v", err, userId)
					return
				}
				if ev.Type == "error" {
					// Обработка ошибок, полученных от внешних сервисов (Google/OpenAI)
					errText := ev.Text
					if ev.Err != nil {
						errText = ev.Err.Error()
					}
					logger.Error("testRealtimeHandler: ошибка обработки сообщения respId=%d: %s", respId, errText, userId)
					return
				}

			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// Обработка входящих сообщений: чтение сообщений от клиента и отправка их в OpenAI
	_ = conn.SetReadDeadline(time.Time{})
	conn.SetReadLimit(1 << 20) // 1MB
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				logger.Debug("testRealtimeHandler: WS ошибка respId=%d: %v", respId, err, userId)
			}
			break
		}

		switch msgType {
		case websocket.BinaryMessage:
			if err := w.api.SendRealtimeAudio(userId, respId, data); err != nil {
				logger.Warn("testRealtimeHandler: ошибка SendRealtimeAudio: %v", err, userId)
			}
		case websocket.TextMessage:
			var cmd map[string]string
			if err := json.Unmarshal(data, &cmd); err == nil {
				if cmd["type"] == "stop" {
					logger.Info("testRealtimeHandler: команда stop от клиента", userId)
					return
				}
			}
		}
	}

	<-sendDone
}

type realtimeWAVRecorder struct {
	file       *os.File
	dataBytes  uint32
	sampleRate uint32
}

func newRealtimeWAVRecorder(userID uint32, respID uint64, sampleRate uint32) (*realtimeWAVRecorder, error) {
	dir := filepath.Join("realtime_audio", fmt.Sprintf("%d", userID))
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("%d.wav", respID))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	recorder := &realtimeWAVRecorder{file: file, sampleRate: sampleRate}
	if err := recorder.writeHeader(0); err != nil {
		_ = file.Close()
		return nil, err
	}
	logger.Info("testRealtimeHandler: WAV recording started path=%s sample_rate=%d", path, sampleRate, userID)
	return recorder, nil
}

func (r *realtimeWAVRecorder) Write(pcm []byte) error {
	if len(pcm)%2 != 0 {
		return fmt.Errorf("PCM16 chunk has odd byte length: %d", len(pcm))
	}
	n, err := r.file.Write(pcm)
	r.dataBytes += uint32(n)
	return err
}

func (r *realtimeWAVRecorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	if _, err := r.file.Seek(0, 0); err != nil {
		_ = r.file.Close()
		return err
	}
	if err := r.writeHeader(r.dataBytes); err != nil {
		_ = r.file.Close()
		return err
	}
	return r.file.Close()
}

func (r *realtimeWAVRecorder) writeHeader(dataSize uint32) error {
	byteRate := r.sampleRate * 2
	blockAlign := uint16(2)
	chunkSize := uint32(36) + dataSize
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], chunkSize)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], r.sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataSize)
	_, err := r.file.Write(header)
	return err
}
