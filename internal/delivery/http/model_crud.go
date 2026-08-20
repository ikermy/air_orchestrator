package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air_common/pkg/model/commdom"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// ListMistralVoices godoc
// @Summary Получить список голосов Mistral
// @Tags model
// @Produce json
// @Security BearerAuth
// @Router /model/voices [get]
func (w *Web) ListMistralVoices(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 1000 || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination"})
		return
	}

	result, err := w.mod.ListMistralVoices(userID, limit, offset, c.DefaultQuery("type", "custom"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetMistralVoice godoc
// @Summary Получить голос Mistral
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Router /model/voices/{voiceID} [get]
func (w *Web) GetMistralVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	voice, err := w.mod.GetMistralVoice(userID, c.Param("voiceID"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// UpdateMistralVoice godoc
// @Summary Обновить пользовательский голос Mistral
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Param body body object true "Данные голоса"
// @Router /model/voices/{voiceID} [patch]
func (w *Web) UpdateMistralVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getOwnedCustomVoice(userID, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var request commdom.UpdateVoiceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	voice, err := w.mod.UpdateMistralVoice(userID, voiceID, request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// DeleteMistralVoice godoc
// @Summary Удалить пользовательский голос Mistral
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Router /model/voices/{voiceID} [delete]
func (w *Web) DeleteMistralVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getOwnedCustomVoice(userID, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	voice, err := w.mod.DeleteMistralVoice(userID, voiceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// GetMistralVoiceSample godoc
// @Summary Получить аудиосэмпл голоса Mistral
// @Tags model
// @Produce audio/mpeg
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Router /model/voices/{voiceID}/sample [get]
func (w *Web) GetMistralVoiceSample(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getOwnedCustomVoice(userID, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	sample, contentType, err := w.mod.GetMistralVoiceSample(userID, voiceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer sample.Close()
	c.DataFromReader(http.StatusOK, -1, contentType, sample, nil)
}

func (w *Web) getOwnedCustomVoice(userID uint32, voiceID string) (commdom.Voice, error) {
	_ = userID // Router scopes the lookup to the authenticated user's Mistral account.
	voice, err := w.mod.GetMistralVoice(userID, voiceID)
	if err != nil {
		return commdom.Voice{}, err
	}
	// Preset voices are readable, but cannot be changed or deleted through the
	// custom voice API. Mistral custom voices carry the owning user ID.
	if voice.UserID == nil || strings.TrimSpace(*voice.UserID) == "" {
		return commdom.Voice{}, fmt.Errorf("preset voice cannot be modified")
	}
	return voice, nil
}

// CloneMistralVoice godoc
// @Summary Клонировать голос Mistral
// @Tags model
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param provider formData string true "Провайдер"
// @Param file formData file true "Аудиофайл"
// @Router /model/voice/clone [post]
func (w *Web) CloneMistralVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	providerName := strings.TrimSpace(c.PostForm("provider"))
	if providerName == "" {
		providerName = strings.TrimSpace(c.Query("provider"))
	}

	provider, err := commdom.FromString(providerName)
	if err != nil || provider != commdom.ProviderMistral {
		logger.Error("Неподдерживаемый провайдер", userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "provider=mistral is required"})
		return
	}
	model, err := w.db.GetModelByProvider(userID, provider)
	if err != nil {
		logger.Error("Ошибка получения модели пользователя для voice clone: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Mistral model not found"})
		return
	}
	if model == nil || model.ModelId == 0 {
		logger.Error("Ошибка model == nil || model.ModelId == 0", userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Mistral model not found"})
		return
	}
	modelID := model.ModelId
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()
	if header.Size > 25<<20 {
		logger.Error("Размер файла слишком большой", userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "audio file is too large"})
		return
	}
	audio, err := io.ReadAll(io.LimitReader(file, 25<<20))
	if err != nil || len(audio) == 0 {
		logger.Error("Файл не читается или = 0")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid audio file"})
		return
	}
	if err := validateVoiceAudio(header, audio); err != nil {
		logger.Error("Файл не валидирован %v", err, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if name == "" {
		logger.Error("Name is required", userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	languages := splitVoiceMetadata(c.PostForm("languages"))
	tags := splitVoiceMetadata(c.PostForm("tags"))
	var gender, description *string
	if value := strings.TrimSpace(c.PostForm("gender")); value != "" {
		gender = &value
	}
	if value := strings.TrimSpace(c.PostForm("description")); value != "" {
		description = &value
	}
	data, err := w.mod.GetUserModelByProvider(userID, commdom.ProviderMistral)
	if err != nil || data == nil {
		logger.Error("Ошибка err != nil || data == nil")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Mistral model not found"})
		return
	}
	oldVoiceID := ""
	if data.RealtimeVAD != nil && data.RealtimeVAD.Mistral != nil {
		if data.RealtimeVAD.Mistral.VoiceID != nil {
			oldVoiceID = *data.RealtimeVAD.Mistral.VoiceID
		}
		if data.RealtimeVAD.Mistral.VoiceClone != nil && data.RealtimeVAD.Mistral.VoiceClone.ProfileID != "" {
			oldVoiceID = data.RealtimeVAD.Mistral.VoiceClone.ProfileID
		}
	}
	voice, err := w.mod.CreateMistralVoice(userID, commdom.CreateVoiceRequest{Name: name, SampleAudio: base64.StdEncoding.EncodeToString(audio), SampleFilename: &header.Filename, Languages: languages, Tags: tags, Gender: gender, Description: description})
	if err != nil {
		logger.Error("Ошибка создания голоса %v", err, userID)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	if data.RealtimeVAD == nil {
		data.RealtimeVAD = &commdom.RealtimeVAD{}
	}
	if data.RealtimeVAD.Mistral == nil {
		data.RealtimeVAD.Mistral = &commdom.MistralRealtimeVAD{}
	}
	data.RealtimeVAD.Mistral.VoiceID = &voice.ID
	if data.RealtimeVAD.Mistral.VoiceClone == nil {
		data.RealtimeVAD.Mistral.VoiceClone = &commdom.MistralVoiceCloneConfig{}
	}
	data.RealtimeVAD.Mistral.VoiceClone.Enabled = true
	data.RealtimeVAD.Mistral.VoiceClone.ProfileID = voice.ID
	if err := w.mod.UpdateModelEveryWhere(userID, data); err != nil {
		if _, cleanupErr := w.mod.DeleteMistralVoice(userID, voice.ID); cleanupErr != nil {
			logger.Error("Ошибка cleanup нового voice profile %s: %v", voice.ID, cleanupErr, userID)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if oldVoiceID != "" && oldVoiceID != voice.ID {
		if _, cleanupErr := w.mod.DeleteMistralVoice(userID, oldVoiceID); cleanupErr != nil {
			logger.Warn("Не удалось удалить старый voice profile %s: %v", oldVoiceID, cleanupErr, userID)
		}
	}
	c.JSON(http.StatusCreated, gin.H{"voice": voice, "model_id": modelID})
}

func splitVoiceMetadata(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '\n' })
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func validateVoiceAudio(header *multipart.FileHeader, audio []byte) error {
	if header.Filename == "" {
		return fmt.Errorf("audio filename is required")
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]map[string]bool{
		".mp3":  {"audio/mpeg": true, "audio/mp3": true, "application/octet-stream": true},
		".wav":  {"audio/wav": true, "audio/x-wav": true, "audio/wave": true, "application/octet-stream": true},
		".ogg":  {"audio/ogg": true, "application/ogg": true, "application/octet-stream": true},
		".flac": {"audio/flac": true, "audio/x-flac": true, "application/octet-stream": true},
		".m4a":  {"audio/mp4": true, "video/mp4": true, "application/mp4": true, "application/octet-stream": true},
	}
	types, ok := allowed[ext]
	if !ok {
		return fmt.Errorf("unsupported audio format: %s", ext)
	}
	contentType := http.DetectContentType(audio)
	if !types[contentType] {
		return fmt.Errorf("audio format does not match extension: extension=%s content_type=%s", ext, contentType)
	}
	return nil
}

// getProvider извлекает provider из контекста Gin
// Возвращает provider и true при успехе, 0 и false при ошибке
// При ошибке автоматически отправляет HTTP ответ и вызывает c.Abort()
func getProvider(c *gin.Context) (commdom.ProviderType, bool) {
	prov, ok := c.Get("provider")
	if !ok || prov == nil {
		logger.Error("Ошибка получения провайдера из контекста: %s", c.Request.RequestURI)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider not specified"})
		return 0, false
	}

	switch provider := prov.(type) {
	case commdom.ProviderType:
		return provider, true
	case string:
		parsed, err := commdom.FromString(provider)
		if err != nil {
			logger.Error("Неверный тип провайдера: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider format"})
			return 0, false
		}
		return parsed, true
	default:
		logger.Error("Неверный тип провайдера в контексте: %T", prov)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider format"})
		return 0, false
	}
}

// SetModelActive godoc
// @Summary Установить модель активной
// @Tags model
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/setactive [get]
// SetModelActive переключает модель в активный режим
func (w *Web) SetModelActive(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	provider, ok := getProvider(c)
	if !ok {
		return
	}

	err := w.mod.SetActiveUserModel(userId, provider)
	if err != nil {
		logger.Error("Ошибка при установке активной модели: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	active, err := w.db.CheckActiveChannels(userId)
	if err != nil {
		logger.Error("'ReadUserModel' Ошибка: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"active_channels": active})
}

// List godoc
// @Summary Получить список моделей провайдера
// @Tags model
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /model/list [get]
// List возвращает список доступных моделей для текущего провайдера
func (w *Web) List(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	provider, ok := getProvider(c)
	if !ok {
		return
	}

	// Быстро обновляю список моделей провайдера
	shortCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	var (
		err       error
		modelType commdom.ModelType
		done      bool
	)
	// В горутине только для обработки ошибок что бы выйти из неё и продолжить если что
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		modelTypeStr := c.Query("type") // "general" или "realtime"
		modelType, err = commdom.ModelTypeFromString(modelTypeStr)
		if err != nil {
			// обработка ошибки
			return
		}
		apiKey, err := w.db.GetUserAPIKey(userID, provider)
		if err != nil {
			return
		}

		res, err := w.mod.UpdateModelsListByProvider(shortCtx, commdom.Union{
			Provider:  provider,
			ModelType: modelType,
		}, apiKey)
		if err != nil {
			logger.Error("Ошибка обновления и получения списка моделей type=%d для провайдера %s, err: %v", modelType, provider, err, userID)
			return
		}

		// В таком случае отправляю запрос прямо из горутины и выхожу
		c.JSON(http.StatusOK, gin.H{"models": res})
		done = true
		return
	}()

	wg.Wait()
	if done {
		return
	}

	// Для Mistral TSS модель не хранятся в таблице БД выходим с ошибкой
	if provider == commdom.ProviderMistral {
		logger.Error("Для Мистраль не получены Realtime модели")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "mistral realtime models not found"})
		return
	}

	if modelType == 0 {
		logger.Error("ModelType not specified", userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid model type"})
		return
	}
	rawJSON, err := w.db.GetTypesGPT(provider, modelType)
	if err != nil {
		logger.Error("'GetTypesGPT' Ошибка получения данных: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": rawJSON})
}

// FileUpload godoc
// @Summary Загрузить файл в модель
// @Tags model
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param file formData file true "Файл для загрузки"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/upfile [post]
// FileUpload загрузка файла провайдеру
func (w *Web) FileUpload(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем файл из запроса
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		logger.Error("'FileUpload' Ошибка получения файла: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "File is required"})
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Error("Ошибка закрытия файла в FileUpload: %v", err, userId)
		}
	}()

	// Читаем содержимое файла
	fileBytes, err := io.ReadAll(file)
	if err != nil {
		logger.Error("'FileUpload' Ошибка чтения файла: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading file"})
		return
	}

	// Получаем provider из контекста (установлен в middleware)
	provider, ok := getProvider(c)
	if !ok {
		return
	}

	// Загружаем файл в провайдера
	fileID, err := w.mod.UploadFileToProvider(userId, provider, header.Filename, fileBytes)
	if err != nil {
		logger.Error("'FileUpload' Ошибка при загрузке файла: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id": fileID,
	})
}

// FileDelete godoc
// @Summary Удалить файл из модели
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "ID файла для удаления"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/delfile [post]
// FileDelete Удаление файла из OpenAI и БД
func (w *Web) FileDelete(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var requestData struct {
		FileID string `json:"file_id"` // ID файла для удаления
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'FileDelete' Ошибка парсинга JSON: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	provider, ok := getProvider(c)
	if !ok {
		return
	}

	// Удаляем файл из провайдера
	err := w.mod.DeleteFileFromProvider(userId, provider, requestData.FileID)
	if err != nil {
		logger.Error("'FileDelete' Ошибка при удалении файла из %s: %v", provider, err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Для OpenAI дополнительно удаляем из старого формата БД
	// Для Mistral удаление из БД уже выполнено в DeleteDocumentFromLibrary
	if provider == commdom.ProviderOpenAI {
		err = w.db.DeleteFileFromUserGPT(userId, requestData.FileID)
		if err != nil {
			logger.Error("'FileDelete' Ошибка при удалении файла из БД: %v", err, userId)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// FileAdd godoc
// @Summary Добавить файлы в модель
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Список файлов"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/addfile [post]
// FileAdd Добавление файлов в провайдера и БД
func (w *Web) FileAdd(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	if _, ok := getProvider(c); !ok {
		return
	}

	var requestData struct {
		Files []struct {
			FileID   string `json:"fileid"`
			FileName string `json:"filename"`
		} `json:"files"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'FileAdd' Ошибка парсинга JSON: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Проверяем, что массив файлов не пустой
	if len(requestData.Files) == 0 {
		logger.Error("'FileAdd' Массив файлов пуст", userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Files array is empty"})
		return
	}

	// Массивы для отслеживания успешных и неудачных операций
	var successFiles []string
	var failedFiles []map[string]string

	// Обрабатываем каждый файл
	for _, file := range requestData.Files {
		if err := w.db.AddFileFromUserGPT(userId, file.FileID, file.FileName); err != nil {
			logger.Error("'FileAdd' Ошибка добавления файла в БД: %v", err, userId)
			continue
		}
		successFiles = append(successFiles, file.FileName)
	}

	// Формируем ответ в зависимости от результатов
	response := gin.H{
		"status":        "completed",
		"success_count": len(successFiles),
		"failed_count":  len(failedFiles),
	}

	if len(successFiles) > 0 {
		response["success_files"] = successFiles
	}

	if len(failedFiles) > 0 {
		response["failed_files"] = failedFiles
	}

	// Если все файлы обработались с ошибками
	if len(successFiles) == 0 && len(failedFiles) > 0 {
		c.JSON(http.StatusInternalServerError, response)
		return
	}

	c.JSON(http.StatusOK, response)
}

// CreateModel godoc
// @Summary Создать новую модель
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Данные новой модели"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/create [post]
// CreateModel Создание модели ассистента
func (w *Web) CreateModel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		logger.Error("Ошибка получения userId из контекста")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found"})
		return
	}

	// Получаем provider из контекста (установлен в middleware)
	prov, ok := c.Get("provider")
	if !ok || prov == nil {
		logger.Error("'CreateModelRequest' Ошибка получения провайдера", userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider not specified"})
		return
	}

	provider, ok := prov.(commdom.ProviderType)
	if !ok {
		logger.Error("'CreateModelRequest' неверный тип провайдера", userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider format"})
		return
	}

	var requestData commdom.UniversalModelData

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'CreateModelRequest' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Устанавливаем UseModelName внутреннее имя resp и realtime модели для провайдера например gpt-4 и gpt-realtime-mini
	// по умолчанию в зависимости от провайдера
	if requestData.UseModelName == nil {
		// Маппинг провайдеров на их имена в БД
		providerNames := map[commdom.ProviderType]string{
			commdom.ProviderOpenAI:  "OpenAI",
			commdom.ProviderMistral: "Mistral",
			commdom.ProviderGoogle:  "Google",
		}

		providerName, exists := providerNames[provider]
		if !exists {
			logger.Error("'CreateModelRequest' Неизвестный провайдер: %d", provider, userId)
			c.JSON(http.StatusBadRequest, gin.H{"error": "неподдерживаемый провайдер"})
			return
		}

		// Получаем модель по умолчанию из БД
		def, err := w.db.DefaultProvidersModels(providerName)
		if err != nil {
			logger.Error("'CreateModelRequest' Ошибка получения имени модели по умолчанию для %s: %v", providerName, err, userId)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Не проверяю данные реалтайм моделей если в будущем появятся провайдеры без их поддержки
		if def.GeneralModelName == "" || def.GeneralModelID == 0 {
			logger.Error("'CreateModelRequest' Модель по умолчанию не найдена для провайдера %s", providerName, userId)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "модель по умолчанию не настроена"})
			return
		}

		requestData.UseModelName = &commdom.UseModelName{
			GptType: &commdom.GptType{
				Name: def.GeneralModelName,
				ID:   def.GeneralModelID,
			},
			Realtime: &commdom.Realtime{
				Name: def.RealTimeModelName,
				ID:   def.RealTimeModelID,
			},
		}
	}

	// Преобразуем FileIDsWrapper в []commdom.Ids
	var fileIDs []commdom.Ids
	if len(requestData.FileIds) > 0 {
		for _, file := range requestData.FileIds {
			fileIDs = append(fileIDs, commdom.Ids{ID: file.ID, Name: file.Name})
		}
	}
	// Создаём модель у провайдера
	umcr, err := w.mod.CreateModel(userId, provider, &requestData, fileIDs)
	if err != nil {
		logger.Error("'CreateModelRequest' Ошибка при создании модели у провайдера: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Сохраняем модель в БД
	universalData := &commdom.UniversalModelData{
		Name:         requestData.Name,
		Prompt:       requestData.Prompt,
		MetaAction:   requestData.MetaAction,
		Triggers:     requestData.Triggers,
		FileIds:      requestData.FileIds,
		Operator:     requestData.Operator,
		Search:       requestData.Search,
		Interpreter:  requestData.Interpreter,
		Image:        requestData.Image,
		WebSearch:    requestData.WebSearch,
		Realtime:     requestData.Realtime,
		RealtimeVAD:  requestData.RealtimeVAD,
		S3:           requestData.S3,
		Haunter:      requestData.Haunter,
		Espero:       requestData.Espero,
		UseModelName: requestData.UseModelName,
		Provider:     provider,
		GOAuth:       requestData.GOAuth,
	}

	err = w.mod.SaveModel(userId, umcr, universalData)
	if err != nil {
		logger.Error("'CreateModelRequest' Ошибка при сохранении модели в БД: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "provider": provider.String()})
}

// UpdateModel godoc
// @Summary Обновить модель
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Обновленные данные модели"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /model/update [post]
// UpdateModel Обновление модели (измененная версия)
func (w *Web) UpdateModel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	provider, ok := getProvider(c)
	if !ok {
		return
	}

	var requestData commdom.UniversalModelData

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'UpdateModel' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Устанавливаем UseModelName внутреннее имя resp и realtime модели для провайдера например gpt-4 и gpt-realtime-mini
	// по умолчанию в зависимости от провайдера
	if requestData.UseModelName == nil {
		// Маппинг провайдеров на их имена в БД
		providerNames := map[commdom.ProviderType]string{
			commdom.ProviderOpenAI:  "OpenAI",
			commdom.ProviderMistral: "Mistral",
			commdom.ProviderGoogle:  "Google",
		}

		providerName, exists := providerNames[provider]
		if !exists {
			logger.Error("'UpdateModel' Неизвестный провайдер: %d", provider, userId)
			c.JSON(http.StatusBadRequest, gin.H{"error": "неподдерживаемый провайдер"})
			return
		}

		// Получаем модель по умолчанию из БД
		// Для UpdateModel это КРИТИЧНО - модель должна существовать!
		def, err := w.db.DefaultProvidersModels(providerName)
		if err != nil {
			logger.Error("'UpdateModel' Ошибка получения модели по умолчанию для %s: %v", providerName, err, userId)
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("модель по умолчанию для %s не настроена в БД: %v", providerName, err)})
			return
		}

		logger.Debug("Устанавливаю значения по умолчанию %w", def, userId)

		requestData.UseModelName = &commdom.UseModelName{
			GptType: &commdom.GptType{
				Name: def.GeneralModelName,
				ID:   def.GeneralModelID,
			},
			Realtime: &commdom.Realtime{
				Name: def.RealTimeModelName,
				ID:   def.RealTimeModelID,
			},
		}

		logger.Debug("'UpdateModel' Используется модель по умолчанию для %s: %s (ID: %d)",
			providerName, requestData.UseModelName.GptType.Name, requestData.UseModelName.GptType.ID, userId)
	}

	// Преобразуем FileIDsWrapper в []commdom.Ids
	var fileIDs []commdom.Ids
	if len(requestData.FileIds) > 0 {
		for _, file := range requestData.FileIds {
			fileIDs = append(fileIDs, commdom.Ids{ID: file.ID, Name: file.Name})
		}
	}
	// Создаём UniversalModelData для обновления
	universalData := &commdom.UniversalModelData{
		Name:         requestData.Name,
		Prompt:       requestData.Prompt,
		MetaAction:   requestData.MetaAction,
		Triggers:     requestData.Triggers,
		FileIds:      requestData.FileIds,
		Operator:     requestData.Operator,
		Search:       requestData.Search,
		Interpreter:  requestData.Interpreter,
		Image:        requestData.Image,
		WebSearch:    requestData.WebSearch,
		Realtime:     requestData.Realtime,
		RealtimeVAD:  requestData.RealtimeVAD,
		S3:           requestData.S3,
		Haunter:      requestData.Haunter,
		Espero:       requestData.Espero,
		UseModelName: requestData.UseModelName,
		Provider:     provider,
		GOAuth:       requestData.GOAuth,
	}

	// Полное обновление модели (API провайдера + БД)
	if err := w.mod.UpdateModelEveryWhere(userId, universalData); err != nil {
		logger.Error("'UpdateModel' Ошибка при обновлении модели: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Инвалидируем кэш конфигурации модели для пользователя
	// Чтобы новые сессии получили обновленные настройки
	w.mod.InvalidateUserAgentConfigCache(userId)

	c.JSON(http.StatusOK, gin.H{"message": "ok", "provider": provider})
}

// DeleteModelWSSHandler godoc
// @Summary WebSocket для удаления модели
// @Tags ws
// @Produce text/event-stream
// @Security BearerAuth
// @Router /ws/delete-model [get]
func (w *Web) DeleteModelWSSHandler(c *gin.Context) {
	conn, err := upgradeWebSocket(c)
	if err != nil {
		logger.Error("WebSocket upgrade error: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Ошибка закрытия WebSocket соединения в DeleteModelWSSHandler: %v", err)
		}
	}()

	// Получаем provider из контекста (установлен в middleware)
	prov, ok := c.Get("provider")
	if !ok || prov == nil {
		logger.Error("DeleteModelWSSHandler провайдер не найден в контексте")
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Провайдер не указан")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	provider, ok := prov.(commdom.ProviderType)
	if !ok {
		logger.Error("DeleteModelWSSHandler неверный тип провайдера: %T", prov)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Неверный формат провайдера")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	// Проверяем валидность провайдера
	if provider != commdom.ProviderOpenAI && provider != commdom.ProviderMistral && provider != commdom.ProviderGoogle {
		logger.Error("DeleteModelWSSHandler неподдерживаемый провайдер: %v", provider)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Неверный провайдер")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	logger.Debug("DeleteModelWSSHandler получен провайдер: %s", provider.String())

	userId, ok := getUserID(c)
	if !ok {
		logger.Error("Ошибка получения userId из контекста")
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Недействительный токен")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	// Вызываем функцию для удаления модели через WebSocket
	w.DeleteModelWSS(conn, userId, provider)
}

func (w *Web) DeleteModelWSS(conn *websocket.Conn, userId uint32, provider commdom.ProviderType) {
	voiceIDs := make([]string, 0, 2)
	if provider == commdom.ProviderMistral {
		if data, err := w.mod.GetUserModelByProvider(userId, provider); err == nil && data != nil {
			voiceIDs = mistralVoiceIDs(data)
		} else if err != nil {
			logger.Warn("Не удалось прочитать Mistral voice profile перед удалением модели: %v", err, userId)
		}
	}

	// Создаем callback функцию для отправки сообщений
	progressCallback := func(message string) {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err, userId)
		}
	}

	// Удаляем модель пользователя с callback (универсальный метод)
	err := w.mod.DeleteModel(userId, provider, true, progressCallback)
	if err != nil {
		errorMsg := fmt.Sprintf("❌ Ошибка при удалении модели: %v", err)
		logger.Error("'DeleteModelWSS' %s", errorMsg, userId)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения об ошибке: %v", err, userId)
		}
		return
	}

	// Модель уже удалена. Best-effort удаляем связанные Mistral voice profiles
	// тремя попытками и сообщаем о проблеме клиенту без отката удаления модели.
	if provider == commdom.ProviderMistral {
		for _, voiceID := range voiceIDs {
			if err := w.deleteMistralVoiceWithRetry(userId, voiceID); err != nil {
				errorMsg := fmt.Sprintf("❌ Модель удалена, но voice profile %s не удалён: %v", voiceID, err)
				logger.Error("'DeleteModelWSS' %s", errorMsg, userId)
				if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(errorMsg)); writeErr != nil {
					logger.Error("Ошибка отправки ошибки удаления voice profile: %v", writeErr, userId)
				}
				return
			}
		}
	}

	// Закрываем соединение после завершения операции
	if err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")); err != nil {
		logger.Error("Ошибка отправки WebSocket close message: %v", err, userId)
	}
}

func mistralVoiceIDs(data *commdom.UniversalModelData) []string {
	if data == nil || data.RealtimeVAD == nil || data.RealtimeVAD.Mistral == nil {
		return nil
	}
	mistral := data.RealtimeVAD.Mistral
	ids := make([]string, 0, 2)
	seen := make(map[string]struct{}, 2)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if mistral.VoiceID != nil {
		add(*mistral.VoiceID)
	}
	if mistral.VoiceClone != nil {
		add(mistral.VoiceClone.ProfileID)
	}
	return ids
}

func (w *Web) deleteMistralVoiceWithRetry(userID uint32, voiceID string) error {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := w.mod.DeleteMistralVoice(userID, voiceID); err == nil {
			return nil
		} else {
			lastErr = err
			logger.Warn("Ошибка удаления Mistral voice profile %s, попытка %d/3: %v", voiceID, attempt, err, userID)
		}
		if attempt < 3 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return lastErr
}

// ReadUserModel godoc
// @Summary Получить модели пользователя
// @Tags model
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /model [get]
// ReadUserModel Чтение модели пользователя
func (w *Web) ReadUserModel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	modelsData, err := w.mod.GetAllModelAsJSON(userId)
	if err != nil {
		logger.Error("Ошибка при получении модели пользователя: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Data(http.StatusOK, "application/json; charset=utf-8", modelsData)
}

// CheckDemoUser godoc
// @Summary Проверить статус демо пользователя
// @Tags model
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /model/demo [get]
func (w *Web) CheckDemoUser(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	status, err := w.db.CheckDemo(userId)
	if err != nil {
		logger.Error("'Ошибка проверки статуса демо пользователя: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status})
}
