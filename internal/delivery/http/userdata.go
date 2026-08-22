package web

import (
	"air_orchestrator/internal/config"
	"air_orchestrator/internal/domain/state"
	storage "air_orchestrator/internal/infrastructure/storage"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// UserData godoc
// @Summary Получить данные пользователя
// @Tags user
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /user/data [get]
func (w *Web) UserData(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	rawData, err := w.db.UserInfo(userId)
	if err != nil {
		logger.Error("'UserData' Ошибка чтения из БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Расшифровываем поле Email (может быть зашифровано после lazy migration)
	userData, err := w.exam.DecryptEmailInJSON(rawData, "Email")
	if err != nil {
		logger.Error("'UserData' Ошибка при расшифровке email: %v", err, userId)
		// Не критично — оставляем данные как есть
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", userData)
}

// DeleteAllUserDataWSSHandler godoc
// @Summary WebSocket для удаления всех данных пользователя
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/delete-all [get]
func (w *Web) DeleteAllUserDataWSSHandler(c *gin.Context) {
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения в DeleteAllUserDataWSSHandler: %v", err)
		}
	}()

	uid, ok := c.Get("userId")
	if !ok {
		logger.Error("Ошибка получения userId из контекста")
		if err := conn.WriteMessage(websocket.TextMessage, []byte("Токен не найден")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}
	userID := uid.(uint32)

	active, err := w.db.CheckActiveChannels(userID)
	if err != nil {
		logger.Error("DeleteAllUserDataWSSHandler: ошибка проверки активных каналов: %v", err, userID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("Внутренняя ошибка сервера")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}
	if active {
		logger.Error("DeleteAllUserDataWSSHandler: попытка удаления данных активного пользователя %d", userID)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("Невозможно удалить данные активного пользователя")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	go w.SendAdminNotification(UserDelete, fmt.Sprintf("userID: %d\n", userID))

	// Вызываем функцию для удаления данных через WebSocket
	w.DeleteAllUserDataWSS(conn, userID)
}

func (w *Web) DeleteAllUserDataWSS(conn *websocket.Conn, userID uint32) {
	// Создаем callback функцию для отправки сообщений
	progressCallback := func(message string) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
	}

	// 1. Отправляем начальное сообщение
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Начинаю удаление всех данных пользователя...")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}

	// 2. Отключаем все каналы в БД
	if err := w.db.DisableAllUserChannel(userID); err != nil {
		progressCallback("❌ Ошибка отключения каналов")
		logger.Warn("Ошибка отключения каналов при удалении всех данных пользователя:%v", err, userID)
	}

	// 2.1 Отправляю команду на остановку сервисов
	if err := CallStopForAllUserServices(userID); err != nil {
		progressCallback("❌ Ошибка остановки сервисов")
		logger.Warn("Ошибка остановки сервисов при удалении всех данных пользователя:%v", err, userID)
	}

	time.Sleep(10 * time.Second) // Жду чтобы в кроне успела сработать _scheduler_ResetUserFlags
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Начинаю остановку сервисов пользователя...")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}
	time.Sleep(10 * time.Second)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Жду остановки сервисов пользователя...")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}
	time.Sleep(10 * time.Second)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Все сервисы пользователя остановлены")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}
	time.Sleep(5 * time.Second)
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Начинаю удаление пользовательских данных...")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}

	// 3. Удаляю все модели пользователя
	allModels, err := w.mod.GetUserModels(userID)
	if err != nil {
		errorMsg := "Ошибка при получении списка моделей"
		logger.Error("Ошибка при получении списка моделей: %v", err)
		if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
		}
	} else {
		for _, modelData := range allModels {
			if err := w.mod.DeleteModel(userID, modelData.Provider, true, progressCallback); err != nil {
				errorMsg := fmt.Sprintf("Ошибка при удалении модели %s", modelData.Provider)
				logger.Error("Ошибка при удалении модели %s: %v", modelData.Provider, err)
				if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
					logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
				}
			}
		}
	}

	// 4. Удаляю все данные через актуальную хранимую процедуру.
	if err := w.db.DeleteAllUserData(userID); err != nil {
		progressCallback("❌ Ошибка удаления данных из БД")
		return
	}

	// 5. Удаляю файлы пользователя из storage
	err = w.DeleteUserS3Directory(w.ctx, userID)
	if err != nil {
		errorMsg := "Ошибка при удалении файлов пользователя в S3"
		logger.Error("'DeleteAllUserData' %s", errorMsg)
		if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
		}
		//return // Продолжаю даже если ошибка
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("Файлы пользователя удалены")); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}

	// 6. Удаляю данные сервисов TG-lead если они есть у пользователя
	url := fmt.Sprintf("%s/service/lead/deletall?uid=%d", config.LeadServiceURL, userID)

	resp, err := sendRESP(w.ctx, http.MethodGet, url, nil)
	if err != nil {
		errorMsg := "Ошибка отправки запроса на удаление в services"
		logger.Error("'DeleteAllUserDataWSS' ошибка запроса к services: %v", err)
		if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
		}
		//return // Продолжаю даже если ошибка
	} else {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Error("Ошибка закрытия response body в DeleteAllUserDataWSS: %v", err)
			}
		}()

		// Проверяем статус-код ответа
		if resp.StatusCode == http.StatusNotFound {
			// Пользователь не найден в services - это нормально, просто у него нет сервисов
			logger.Debug("'DeleteAllUserDataWSS' пользователь не найден в services (нет данных для удаления)")
			if writeErr := conn.WriteMessage(websocket.TextMessage, []byte("Данные в services не найдены")); writeErr != nil {
				logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
			}
		} else if resp.StatusCode != http.StatusOK {
			errorMsg := "Сервис вернул ошибку при удалении данных"
			logger.Error("'DeleteAllUserDataWSS' services вернул статус %d", resp.StatusCode)
			if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
				logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
			}
			//return // Продолжаю даже если ошибка
		} else {
			if writeErr := conn.WriteMessage(websocket.TextMessage, []byte("Данные в services успешно удалены")); writeErr != nil {
				logger.Error("Ошибка отправки WebSocket сообщения: %v", writeErr)
			}
		}
	}

	// Отправляем сообщение об успешном завершении
	successMsg := "Все данные пользователя успешно удалены"
	logger.Info("'DeleteAllUserDataWSS' %s", successMsg, userID)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(successMsg)); err != nil {
		logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
	}

	// Закрываем соединение после завершения операции
	if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		logger.Error("Ошибка отправки WebSocket close message: %v", err)
	}
}

func CallStopForAllUserServices(userID uint32) error {
	// TODO реализовать для всех сервисов!

	url := fmt.Sprintf("%s/stopall?uid=%d", config.LeadServiceURL, userID)
	respCtx, cancel := context.WithTimeout(context.Background(), config.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(respCtx, http.MethodGet, url, nil)
	if err != nil {
		logger.Error("disableChannels Ошибка при создании запроса: %v", err)
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		logger.Error("disableChannels Ошибка при выполнении запроса: %v", err)
		return err
	}

	defer func() {
		if resp.Body != nil {
			err = resp.Body.Close()
			if err != nil {
				logger.Error("disableChannels Ошибка при закрытии тела ответа: %v", err)
			}
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("некорректный статус ответа: %d", resp.StatusCode)
	}

	return nil
}

func (w *Web) DeleteUserS3Directory(ctx context.Context, userID uint32) error {
	if w.storage.Factory == nil {
		logger.Info("storage factory not configured, skipping file deletion", userID)
		return nil
	}
	backend, err := w.storage.Factory.Resolve(ctx, userID)
	if err != nil {
		logger.Error("DeleteUserS3Directory: resolve storage backend: %v", err, userID)
		return fmt.Errorf("не удалось подключиться к хранилищу: %w", err)
	}
	prefix := fmt.Sprintf("users/%d/", userID)
	result, err := backend.ListObjects(ctx, prefix, storage.ListOptions{})
	if err != nil {
		logger.Error("DeleteUserS3Directory: list objects: %v", err, userID)
		return fmt.Errorf("не удалось получить список файлов: %w", err)
	}
	var lastErr error
	for _, object := range result.Objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := backend.DeleteObject(ctx, object.Key); err != nil {
			logger.Error("DeleteUserS3Directory: delete object %s: %v", object.Key, err, userID)
			lastErr = err
			continue
		}
	}

	// Clean up Redis reservations for this user
	if w.storage.Reservations != nil {
		_ = w.storage.Reservations.RecoverReservation(ctx, userID, 0)
	}

	// Reset storage quota
	if quota, ok := any(w.db).(interface {
		EnsureStorageQuota(context.Context, uint32) error
		ResetStorageQuota(context.Context, uint32) error
	}); ok {
		_ = quota.EnsureStorageQuota(ctx, userID)
		_ = quota.ResetStorageQuota(ctx, userID)
	}

	logger.Info("Хранилище пользователя очищено %d", userID)
	return lastErr
}

// UserTimeZone godoc
// @Summary Устновить часовой пояс пользователя
// @Tags user
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Часовой пояс"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /user/timezone [post]
func (w *Web) UserTimeZone(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Timezone string `json:"timezone"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'UserTimeZone' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := w.db.SaveUserTimeZone(userId, requestData.Timezone)
	if err != nil {
		logger.Error("'UserTimeZone' Ошибка при сохранении в БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// UserLanguage godoc
// @Summary Устновить Язык уведомлений пользователя
// @Tags user
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Язык уведомлений"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /user/timezone [post]
func (w *Web) UserLanguage(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		Language string `json:"language"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'UserLanguage' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ok = state.ValidateLanguage(requestData.Language)
	if !ok {
		logger.Error("Не поддерживаемый язык: %v", requestData.Language)
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported language"})
		return
	}

	err := w.db.SaveUserLanguage(userId, requestData.Language)
	if err != nil {
		logger.Error("'UserLanguage' Ошибка при сохранении в БД: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}
