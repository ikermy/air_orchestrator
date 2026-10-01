package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/ikermy/air-common/pkg/comdom"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// voiceProvider возвращает голосового провайдера из ?provider= (проставляет
// authAllowMiddleware). Если параметр не передан — используется Mistral для
// обратной совместимости со старыми клиентами /model/voices.
func voiceProvider(c *gin.Context) (comdom.ProviderType, bool) {
	if prov, ok := c.Get("provider"); ok && prov != nil {
		return getProvider(c)
	}
	return comdom.ProviderMistral, true
}

// ListVoices godoc
// @Summary Получить список голосов провайдера
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Router /model/voices [get]
func (w *Web) ListVoices(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit < 1 || limit > 1000 || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid pagination"})
		return
	}

	result, err := w.mod.ListVoices(userID, provider, limit, offset, c.DefaultQuery("type", "custom"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, result)
}

// GetVoice godoc
// @Summary Получить голос провайдера
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Router /model/voices/{voiceID} [get]
func (w *Web) GetVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}
	voice, err := w.mod.GetVoice(userID, provider, c.Param("voiceID"))
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// UpdateVoice godoc
// @Summary Обновить пользовательский голос
// @Tags model
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Param body body object true "Данные голоса"
// @Router /model/voices/{voiceID} [patch]
func (w *Web) UpdateVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getEditableVoice(userID, provider, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	var request comdom.UpdateVoiceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	voice, err := w.mod.UpdateVoice(userID, provider, voiceID, request)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// DeleteVoice godoc
// @Summary Удалить пользовательский голос
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Router /model/voices/{voiceID} [delete]
func (w *Web) DeleteVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getEditableVoice(userID, provider, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	voice, err := w.mod.DeleteVoice(userID, provider, voiceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, voice)
}

// GetVoiceSample godoc
// @Summary Получить аудиосэмпл голоса
// @Tags model
// @Produce audio/mpeg
// @Security BearerAuth
// @Param voiceID path string true "ID голоса"
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Router /model/voices/{voiceID}/sample [get]
func (w *Web) GetVoiceSample(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}
	voiceID := strings.TrimSpace(c.Param("voiceID"))
	if voiceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "voice_id is required"})
		return
	}
	if _, err := w.getEditableVoice(userID, provider, voiceID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		return
	}
	sample, contentType, err := w.mod.GetVoiceSample(userID, provider, voiceID)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	defer sample.Close()
	c.DataFromReader(http.StatusOK, -1, contentType, sample, nil)
}

// getEditableVoice проверяет, что голос существует и может изменяться текущим
// пользователем. Пресеты Mistral читаются, но не редактируются; для ElevenLabs
// ограничения на стороне API провайдера.
func (w *Web) getEditableVoice(userID uint32, provider comdom.ProviderType, voiceID string) (comdom.Voice, error) {
	voice, err := w.mod.GetVoice(userID, provider, voiceID)
	if err != nil {
		return comdom.Voice{}, err
	}
	if provider == comdom.ProviderMistral {
		if voice.UserID == nil || strings.TrimSpace(*voice.UserID) == "" {
			return comdom.Voice{}, fmt.Errorf("preset voice cannot be modified")
		}
	}
	return voice, nil
}

// GetVoiceSettings godoc
// @Summary Голосовые настройки провайдера одним запросом (capabilities + модели + голоса)
// @Tags model
// @Produce json
// @Security BearerAuth
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Router /model/voice/settings [get]
func (w *Web) GetVoiceSettings(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	provider, ok := voiceProvider(c)
	if !ok {
		return
	}

	// capabilities провайдера и доступность ключа.
	capabilities := w.mod.GetProviderCapabilities()[provider.String()]
	availability := w.mod.ProvidersWithApiKeys(userID)
	available := false
	for _, name := range availability.Available {
		if name == provider.String() {
			available = true
			break
		}
	}

	// Голосовые модели. Голосовая панель ходит только сюда, поэтому каталог
	// нужно синхронизировать: GetTypesGPT лишь читает БД. UpdateModelsListByProvider
	// для voice-only провайдера игнорирует ModelType и дергает
	// ensureVoiceCatalogFresh → syncVoiceModels (под in-memory throttle).
	var models json.RawMessage
	apiKey, keyErr := w.db.GetUserAPIKey(userID, provider)
	if keyErr != nil {
		logger.Warn("GetVoiceSettings: не удалось получить API-ключ %s: %v", provider, keyErr, userID)
	}
	syncCtx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	if res, err := w.mod.UpdateModelsListByProvider(syncCtx, comdom.Union{
		Provider:  provider,
		ModelType: comdom.General,
	}, apiKey); err == nil {
		if raw, mErr := json.Marshal(res); mErr == nil {
			models = raw
		}
	} else {
		logger.Warn("GetVoiceSettings: не удалось синхронизировать голосовые модели: %v", err, userID)
	}
	// Фолбэк: если синк не дал результата — читаем каталог из БД.
	if models == nil {
		if raw, err := w.db.GetTypesGPT(provider, 0); err == nil {
			models = raw
		} else {
			logger.Warn("GetVoiceSettings: не удалось получить голосовые модели: %v", err, userID)
		}
	}

	// Голоса провайдера (per-account). Ошибка не должна ломать ответ: фронт
	// отрисует модели/capabilities и покажет сообщение.
	limit := 100
	voices, voicesErr := w.mod.ListVoices(userID, provider, limit, 0, c.DefaultQuery("type", "custom"))
	if voicesErr != nil {
		logger.Warn("GetVoiceSettings: не удалось получить голоса: %v", voicesErr, userID)
	}

	resp := gin.H{
		"provider":     provider.String(),
		"voice_only":   provider.IsVoiceOnly(),
		"available":    available,
		"capabilities": capabilities,
	}
	if models != nil {
		resp["models"] = models
	}
	if voicesErr != nil {
		resp["voices_error"] = voicesErr.Error()
	} else {
		resp["voices"] = voices
	}
	c.JSON(http.StatusOK, resp)
}

// CloneVoice godoc
// @Summary Клонировать голос провайдера
// @Tags model
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param provider query string false "Провайдер (mistral|elevenlabs)"
// @Param name formData string true "Имя голоса"
// @Param clone_mode formData string false "instant|professional (только ElevenLabs)"
// @Param language formData string false "Язык (обязателен для professional/PVC)"
// @Param model_id formData string false "Модель для обучения PVC"
// @Param file formData file true "Аудиообразец(ы): file/files/samples"
// @Router /model/voice/clone [post]
func (w *Web) CloneVoice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	// Читаем тело один раз и восстанавливаем reader: это защищает от случая,
	// когда тело уже было частично прочитано, и даёт возможность залогировать
	// реальный boundary/префикс при ошибке разбора multipart.
	const maxVoiceCloneBody = 128 << 20 // 128 MiB
	body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, maxVoiceCloneBody+1))
	if readErr != nil {
		logger.Error("CloneVoice: не удалось прочитать тело: %v", readErr, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read request body"})
		return
	}
	if len(body) > maxVoiceCloneBody {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body is too large"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	form, formErr := c.MultipartForm()
	if formErr != nil {
		boundary := ""
		if _, params, mErr := mime.ParseMediaType(c.Request.Header.Get("Content-Type")); mErr == nil {
			boundary = params["boundary"]
		}
		logger.Warn("CloneVoice: multipart не распознан (boundary=%q, len=%d, prefix=%s): %v",
			boundary, len(body), bodyPreview(body, 240), formErr, userID)
	}
	getField := func(key string) string {
		if form != nil {
			if vs := form.Value[key]; len(vs) > 0 {
				return strings.TrimSpace(vs[0])
			}
		}
		return strings.TrimSpace(c.PostForm(key))
	}

	provider, ok := cloneProvider(c, getField)
	if !ok {
		return
	}
	if !provider.Supports(comdom.CapabilityVoiceClone) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("провайдер %s не поддерживает клонирование голоса", provider)})
		return
	}

	name := getField("name")
	if name == "" {
		logger.Warn("CloneVoice: пустой name (content-type=%s, поля=%v, formErr=%v)",
			c.ContentType(), multipartKeys(form), formErr, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	cloneMode := getField("clone_mode")
	if cloneMode == "" {
		cloneMode = string(comdom.CloneModeInstant)
	}
	if !comdom.CloneMode(cloneMode).IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "clone_mode must be 'instant' or 'professional'"})
		return
	}

	samples, filenames, err := collectVoiceSamples(form)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	oldVoiceID := ""
	if provider == comdom.ProviderMistral {
		oldVoiceID = w.currentMistralVoiceID(userID)
	}

	request := comdom.CreateVoiceRequest{
		Name:        name,
		Samples:     samples,
		SampleAudio: samples[0],
		CloneMode:   cloneMode,
		Language:    getField("language"),
		ModelID:     getField("model_id"),
		Languages:   splitVoiceMetadata(getField("languages")),
		Tags:        splitVoiceMetadata(getField("tags")),
	}
	if len(filenames) > 0 {
		fn := filenames[0]
		request.SampleFilename = &fn
	}
	if value := getField("gender"); value != "" {
		request.Gender = &value
	}
	if value := getField("description"); value != "" {
		request.Description = &value
	}

	voice, err := w.mod.CreateVoice(userID, provider, request)
	if err != nil {
		logger.Error("Ошибка создания голоса: %v", err, userID)
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// Mistral: сохраняем голос в модели (RealtimeVAD), обратная совместимость.
	if provider == comdom.ProviderMistral {
		if err := w.linkMistralVoice(userID, voice.ID, oldVoiceID); err != nil {
			if _, cleanupErr := w.mod.DeleteVoice(userID, provider, voice.ID); cleanupErr != nil {
				logger.Error("Ошибка cleanup нового voice profile %s: %v", voice.ID, cleanupErr, userID)
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusCreated, gin.H{"voice": voice})
}

// cloneProvider определяет провайдера для клонирования: сначала form-поле
// provider (его отправляет фронтенд), затем ?provider= (контекст), иначе Mistral.
func cloneProvider(c *gin.Context, getField func(string) string) (comdom.ProviderType, bool) {
	if name := getField("provider"); name != "" {
		p, err := comdom.FromString(name)
		if err != nil || !p.IsValid() {
			logger.Error("CloneVoice: неверный provider=%q: %v", name, err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid provider: " + name})
			return 0, false
		}
		return p, true
	}
	if prov, ok := c.Get("provider"); ok && prov != nil {
		return getProvider(c)
	}
	return comdom.ProviderMistral, true
}

// bodyPreview возвращает безопасный для логов ASCII-превью начала тела.
func bodyPreview(body []byte, n int) string {
	if n > len(body) {
		n = len(body)
	}
	return strconv.QuoteToASCII(string(body[:n]))
}

// multipartKeys возвращает отсортированный список полей формы (диагностика).
func multipartKeys(form *multipart.Form) []string {
	if form == nil {
		return nil
	}
	keys := make([]string, 0, len(form.Value)+len(form.File))
	for k := range form.Value {
		keys = append(keys, k)
	}
	for k := range form.File {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collectVoiceSamples собирает аудиообразцы из multipart-полей file/files/samples
// и кодирует их в base64 (как ожидает comdom.CreateVoiceRequest).
func collectVoiceSamples(form *multipart.Form) ([]string, []string, error) {
	if form == nil {
		return nil, nil, fmt.Errorf("multipart form is required")
	}
	var headers []*multipart.FileHeader
	for _, field := range []string{"file", "files", "samples"} {
		headers = append(headers, form.File[field]...)
	}
	if len(headers) == 0 {
		return nil, nil, fmt.Errorf("at least one audio file is required")
	}
	const maxSamples = 25
	if len(headers) > maxSamples {
		return nil, nil, fmt.Errorf("too many audio files (max %d)", maxSamples)
	}

	encoded := make([]string, 0, len(headers))
	names := make([]string, 0, len(headers))
	for _, header := range headers {
		if header.Size > 25<<20 {
			return nil, nil, fmt.Errorf("audio file is too large: %s", header.Filename)
		}
		file, err := header.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("cannot open audio file: %s", header.Filename)
		}
		audio, err := io.ReadAll(io.LimitReader(file, 25<<20))
		_ = file.Close()
		if err != nil || len(audio) == 0 {
			return nil, nil, fmt.Errorf("invalid audio file: %s", header.Filename)
		}
		if err := validateVoiceAudio(header, audio); err != nil {
			return nil, nil, err
		}
		encoded = append(encoded, base64.StdEncoding.EncodeToString(audio))
		names = append(names, header.Filename)
	}
	return encoded, names, nil
}

// currentMistralVoiceID возвращает текущий выбранный голос Mistral, если задан.
func (w *Web) currentMistralVoiceID(userID uint32) string {
	data, err := w.mod.GetUserModelByProvider(userID, comdom.ProviderMistral)
	if err != nil || data == nil || data.RealtimeVAD == nil || data.RealtimeVAD.Mistral == nil {
		return ""
	}
	mistral := data.RealtimeVAD.Mistral
	if mistral.VoiceClone != nil && strings.TrimSpace(mistral.VoiceClone.ProfileID) != "" {
		return mistral.VoiceClone.ProfileID
	}
	if mistral.VoiceID != nil {
		return strings.TrimSpace(*mistral.VoiceID)
	}
	return ""
}

// linkMistralVoice привязывает созданный профиль к realtime-модели Mistral,
// сохраняя обратную совместимость (RealtimeVAD.Mistral.VoiceID/VoiceClone).
func (w *Web) linkMistralVoice(userID uint32, newVoiceID, oldVoiceID string) error {
	data, err := w.mod.GetUserModelByProvider(userID, comdom.ProviderMistral)
	if err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("Mistral model not found")
	}
	if data.RealtimeVAD == nil {
		data.RealtimeVAD = &comdom.RealtimeVAD{}
	}
	if data.RealtimeVAD.Mistral == nil {
		data.RealtimeVAD.Mistral = &comdom.MistralRealtimeVAD{}
	}
	data.RealtimeVAD.Mistral.VoiceID = &newVoiceID
	if data.RealtimeVAD.Mistral.VoiceClone == nil {
		data.RealtimeVAD.Mistral.VoiceClone = &comdom.MistralVoiceCloneConfig{}
	}
	data.RealtimeVAD.Mistral.VoiceClone.Enabled = true
	data.RealtimeVAD.Mistral.VoiceClone.ProfileID = newVoiceID
	if err := w.mod.UpdateModelEveryWhere(userID, data); err != nil {
		return err
	}
	if oldVoiceID != "" && oldVoiceID != newVoiceID {
		if _, cleanupErr := w.mod.DeleteVoice(userID, comdom.ProviderMistral, oldVoiceID); cleanupErr != nil {
			logger.Warn("Не удалось удалить старый voice profile %s: %v", oldVoiceID, cleanupErr, userID)
		}
	}
	return nil
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
func getProvider(c *gin.Context) (comdom.ProviderType, bool) {
	prov, ok := c.Get("provider")
	if !ok || prov == nil {
		logger.Error("Ошибка получения провайдера из контекста: %s", c.Request.RequestURI)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Provider not specified"})
		return 0, false
	}

	switch provider := prov.(type) {
	case comdom.ProviderType:
		return provider, true
	case string:
		parsed, err := comdom.FromString(provider)
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
		modelType comdom.ModelType
		done      bool
	)
	// В горутине только для обработки ошибок что бы выйти из неё и продолжить если что
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()

		modelTypeStr := c.Query("type") // "general" или "realtime"
		modelType, err = comdom.ModelTypeFromString(modelTypeStr)
		if err != nil {
			// обработка ошибки
			return
		}
		apiKey, err := w.db.GetUserAPIKey(userID, provider)
		if err != nil {
			logger.Error("Ошибка получения API ключа для провайдера %s: %v", provider, err, userID)
			return
		}

		res, err := w.mod.UpdateModelsListByProvider(shortCtx, comdom.Union{
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

	// Fallback: каталог напрямую из БД (LLM + voice_models). Для voice-only
	// провайдеров (ElevenLabs) modelType игнорируется, поэтому отсутствие type
	// допустимо — вернутся только голосовые модели.
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
	// Фактически OpenAI уже не поддерживает этот режим работы
	// Для OpenAI дополнительно удаляем из старого формата БД
	// Для Mistral удаление из БД уже выполнено в DeleteDocumentFromLibrary
	//if provider == comdom.ProviderOpenAI {
	//	err = w.db.DeleteFileFromUserGPT(userId, requestData.FileID)
	//	if err != nil {
	//		logger.Error("'FileDelete' Ошибка при удалении файла из БД: %v", err, userId)
	//		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	//		return
	//	}
	//}

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
		// Проверяем входные значения
		if userId == 0 || file.FileID == "" || file.FileName == "" {
			logger.Warn("получены некорректные значения: userId, fileID или fileName пусты")
			continue
		}

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

	provider, ok := prov.(comdom.ProviderType)
	if !ok {
		logger.Error("'CreateModelRequest' неверный тип провайдера", userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider format"})
		return
	}

	if provider.IsVoiceOnly() {
		logger.Warn("'CreateModelRequest' провайдер %s — voice-only", provider, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "ElevenLabs — voice-only; настраивается в настройках голоса текущей модели"})
		return
	}

	var requestData comdom.UniversalModelData

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'CreateModelRequest' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Устанавливаем UseModelName внутреннее имя resp и realtime модели для провайдера например gpt-4 и gpt-realtime-mini
	// по умолчанию в зависимости от провайдера
	if requestData.UseModelName == nil {
		// Маппинг провайдеров на их имена в БД
		providerNames := map[comdom.ProviderType]string{
			comdom.ProviderOpenAI:  "OpenAI",
			comdom.ProviderMistral: "Mistral",
			comdom.ProviderGoogle:  "Google",
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

		requestData.UseModelName = &comdom.UseModelName{
			GptType: &comdom.GptType{
				Name: def.GeneralModelName,
				ID:   def.GeneralModelID,
			},
			Realtime: &comdom.Realtime{
				Name: def.RealTimeModelName,
				ID:   def.RealTimeModelID,
			},
		}
	}

	// Преобразуем FileIDsWrapper в []comdom.Ids
	var fileIDs []comdom.Ids
	if len(requestData.FileIds) > 0 {
		for _, file := range requestData.FileIds {
			fileIDs = append(fileIDs, comdom.Ids{ID: file.ID, Name: file.Name})
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
	universalData := &comdom.UniversalModelData{
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
		CreateMusic:  requestData.CreateMusic,
		Voice:        requestData.Voice,
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

	if provider.IsVoiceOnly() {
		logger.Warn("'UpdateModel' провайдер %s — voice-only", provider, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": "ElevenLabs — voice-only; настраивается в настройках голоса текущей модели"})
		return
	}

	var requestData comdom.UniversalModelData

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'UpdateModel' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Устанавливаем UseModelName внутреннее имя resp и realtime модели для провайдера например gpt-4 и gpt-realtime-mini
	// по умолчанию в зависимости от провайдера
	if requestData.UseModelName == nil {
		// Маппинг провайдеров на их имена в БД
		providerNames := map[comdom.ProviderType]string{
			comdom.ProviderOpenAI:  "OpenAI",
			comdom.ProviderMistral: "Mistral",
			comdom.ProviderGoogle:  "Google",
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

		requestData.UseModelName = &comdom.UseModelName{
			GptType: &comdom.GptType{
				Name: def.GeneralModelName,
				ID:   def.GeneralModelID,
			},
			Realtime: &comdom.Realtime{
				Name: def.RealTimeModelName,
				ID:   def.RealTimeModelID,
			},
		}

		logger.Debug("'UpdateModel' Используется модель по умолчанию для %s: %s (ID: %d)",
			providerName, requestData.UseModelName.GptType.Name, requestData.UseModelName.GptType.ID, userId)
	}

	// Преобразуем FileIDsWrapper в []comdom.Ids
	var fileIDs []comdom.Ids
	if len(requestData.FileIds) > 0 {
		for _, file := range requestData.FileIds {
			fileIDs = append(fileIDs, comdom.Ids{ID: file.ID, Name: file.Name})
		}
	}
	// Создаём UniversalModelData для обновления
	universalData := &comdom.UniversalModelData{
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
		CreateMusic:  requestData.CreateMusic,
		Voice:        requestData.Voice,
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

	provider, ok := prov.(comdom.ProviderType)
	if !ok {
		logger.Error("DeleteModelWSSHandler неверный тип провайдера: %T", prov)
		if err := conn.WriteMessage(websocket.TextMessage, []byte("❌ Неверный формат провайдера")); err != nil {
			logger.Error("Ошибка отправки WebSocket сообщения: %v", err)
		}
		return
	}

	// Проверяем валидность провайдера
	if provider != comdom.ProviderOpenAI && provider != comdom.ProviderMistral && provider != comdom.ProviderGoogle {
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

func (w *Web) DeleteModelWSS(conn *websocket.Conn, userId uint32, provider comdom.ProviderType) {
	voiceIDs := make([]string, 0, 2)
	if provider == comdom.ProviderMistral {
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
	if provider == comdom.ProviderMistral {
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

func mistralVoiceIDs(data *comdom.UniversalModelData) []string {
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
		logger.Error("Ошибка проверки статуса демо пользователя: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status})
}

// FastCheckUserModel godoc
// @Summary Проверить наличие модели у пользователя
// @Tags model
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /fast-chek [get]
func (w *Web) FastCheckUserModel(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	status, err := w.db.FastCheckActiveUserModel(userId)
	if err != nil {
		logger.Error("Ошибка проверки наличия модели у пользователя: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": status})
}
