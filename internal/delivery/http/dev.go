package web

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-common/pkg/mode"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// RestartApp — graceful перезапуск: ждёт доставки ответа клиенту, затем завершает процесс.
func RestartApp() {
	time.Sleep(3 * time.Second)
	syscall.Exit(0)
}

// GET /dev/getdata

// GetDev godoc
// @Summary Получить параметры разработки
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/getdata [post]
// GetDev devuelve текущие операционные настройки: email рассылки,
// Google OAuth Client ID и имя Telegram-бота (Carpintero).
func (w *Web) GetDev(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	configs, err := w.adminUC.GetConfigs(w.ctx, []string{
		"smtp.mail", "google_oauth.client_id", "tg.bot", "oper.tg.bot", "widg.ed25519_keys"})
	if err != nil {
		logger.Error("'GetDev' ошибка чтения конфигов: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"email":           configs["smtp.mail"],
		"google_id":       configs["google_oauth.client_id"],
		"bot_name":        configs["tg.bot"],
		"operbot_name":    configs["oper.tg.bot"],
		"session_created": w.exam.GetTimeCreatedSessionKey(),
		"widg_keys":       configs["widg.ed25519_keys"] != "",
	})
}

// POST /dev/setdistribmail

// SetDistribMail godoc
// @Summary Установить параметры SMTP
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "SMTP параметры (resp_id, mail, pass, host, port)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/setdistribmail [post]
// SetDistribMail сохраняет SMTP-настройки в app_config.
// Пароль приходит зашифрованным через DecryptPassword.
func (w *Web) SetDistribMail(c *gin.Context) {
	var req struct {
		RespID uint64 `json:"resp_id"`
		Mail   string `json:"mail"`
		Pass   string `json:"pass"` // зашифрован на клиенте
		Host   string `json:"host"`
		Port   string `json:"port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("'SetDistribMail' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userId, ok := getUserID(c)
	if !ok {
		return
	}

	pass, err := w.exam.DecryptPassword(req.RespID, req.Pass)
	if err != nil {
		logger.Error("'SetDistribMail' Ошибка расшифровки пароля: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err = w.adminUC.SetConfigs(w.ctx, map[string]string{
		"smtp.mail": req.Mail,
		"smtp.host": req.Host,
		"smtp.port": req.Port,
		"smtp.pass": pass,
	}); err != nil {
		logger.Error("'SetDistribMail' Ошибка сохранения: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("'SetDistribMail' SMTP настройки обновлены", userId)
	c.JSON(http.StatusOK, gin.H{})
}

// POST /dev/setgauth

// SetGAUTH godoc
// @Summary Установить параметры Google OAuth
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Google OAuth параметры (resp_id, url, id, sec)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/setgauth [post]
// SetGAUTH сохраняет Google OAuth настройки в app_config.
// ClientSecret приходит зашифрованным через DecryptPassword.
func (w *Web) SetGAUTH(c *gin.Context) {
	var req struct {
		RespID       uint64 `json:"resp_id"`
		RedirectURL  string `json:"url"`
		ClientID     string `json:"id"`
		ClientSecret string `json:"sec"` // зашифрован на клиенте
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("'SetGAUTH' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userId, ok := getUserID(c)
	if !ok {
		return
	}

	secret, err := w.exam.DecryptPassword(req.RespID, req.ClientSecret)
	if err != nil {
		logger.Error("'SetGAUTH' Ошибка расшифровки секрета: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err = w.adminUC.SetConfigs(w.ctx, map[string]string{
		"google_oauth.redirect_uri":  req.RedirectURL,
		"google_oauth.client_id":     req.ClientID,
		"google_oauth.client_secret": secret,
	}); err != nil {
		logger.Error("'SetGAUTH' Ошибка сохранения: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("'SetGAUTH' Google OAuth настройки обновлены", userId)
	c.Status(http.StatusOK)
}

// POST /dev/setcarpintero

// SetCarpintero godoc
// @Summary Установить параметры Telegram бота
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Параметры Telegram бота (resp_id, bot-token, bot-name)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/setcarpintero [post]
// SetCarpintero сохраняет настройки Telegram-бота Carpintero.
func (w *Web) SetCarpintero(c *gin.Context) {
	var req struct {
		RespID   uint64 `json:"resp_id"`
		BotToken string `json:"bot-token"` // зашифрован на клиенте
		BotName  string `json:"bot-name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("'SetCarpintero' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userId, ok := getUserID(c)
	if !ok {
		return
	}

	botToken, err := w.exam.DecryptPassword(req.RespID, req.BotToken)
	if err != nil {
		logger.Error("Ошибка расшифровки токена: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := w.adminUC.SetConfigs(w.ctx, map[string]string{
		"tg.token": botToken,
		"tg.bot":   req.BotName,
	}); err != nil {
		logger.Error("Ошибка сохранения: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("Carpintero настройки обновлены", userId)
	c.Status(http.StatusOK)
}

// SetOperBot godoc
// @Summary Установить параметры Telegram бота
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body object true "Параметры Telegram бота (resp_id, bot-token, bot-name)"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/setoperbot [post]
// SetOperBot сохраняет настройки Telegram-бота OperBot.
func (w *Web) SetOperBot(c *gin.Context) {
	var req struct {
		RespID   uint64 `json:"resp_id"`
		BotToken string `json:"bot-token"` // зашифрован на клиенте
		BotName  string `json:"bot-name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error("Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userId, ok := getUserID(c)
	if !ok {
		return
	}

	botToken, err := w.exam.DecryptPassword(req.RespID, req.BotToken)
	if err != nil {
		logger.Error("Ошибка расшифровки токена: %v", err, userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := w.adminUC.SetConfigs(w.ctx, map[string]string{
		"oper.tg.token": botToken,
		"oper.tg.bot":   req.BotName,
	}); err != nil {
		logger.Error("Ошибка сохранения: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("OperBot настройки обновлены", userId)
	c.Status(http.StatusOK)
}

// POST /dev/setsessionkey

// CreateNewSessionKey godoc
// @Summary Создать новый сеансовый ключ
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/setsessionkey [post]
// CreateNewSessionKey генерирует новый JWT session key, сохраняет в app_config
// и инвалидирует все refresh cookie.
// Email ключ (auth.email_key) при этом НЕ сбрасывается — пользователи сохраняют доступ.
func (w *Web) CreateNewSessionKey(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	if err := w.adminUC.ResetSessionKeys(w.ctx); err != nil {
		logger.Error("'CreateNewSessionKey' Ошибка сброса ключа: %", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Инвалидируем refresh cookie
	devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEVELOPMENT")), "true")
	cookie := http.Cookie{
		Name:     "MarusiaRefreshToken",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		Secure:   !devMode,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Domain:   mode.GetRealHost(),
	}
	if !devMode {
		cookie.Path = "/v1/auth/token/refresh"
		cookie.Domain = mode.GetRealHost()
	}
	http.SetCookie(c.Writer, &cookie)

	logger.Info("'CreateNewSessionKey' auth.session сброшен, перезапускаю", userId)
	c.JSON(http.StatusOK, gin.H{})
	go RestartApp()
}

// POST /dev/checksettings

// CheckSettings godoc
// @Summary Проверить параметры конфигурации
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/checksettings [post]
func (w *Web) CheckSettings(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	configs, err := w.adminUC.GetAllConfigs(w.ctx)
	if err != nil {
		logger.Error("'CheckSettings' Ошибка чтения конфигурации: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"smtp":         configs["smtp.mail"] != "",
		"google_oauth": configs["google_oauth.client_id"] != "",
		"carpintero":   configs["tg.token"] != "",
	})
}

// POST /dev/getsvckey

// GetServiceKey godoc
// @Summary Получить текущий gRPC service ключ
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/getsvckey [post]
func (w *Web) GetServiceKey(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	configs, err := w.adminUC.GetConfigs(w.ctx, []string{"svc.service_key"})
	if err != nil {
		logger.Error("'GetServiceKey' Ошибка чтения: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	key := configs["svc.service_key"]
	if key == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "service key not configured, use POST /dev/generatesvckey"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"service_key": key})
}

// POST /dev/generatesvckey

// GenerateServiceKey godoc
// @Summary Генерировать новый gRPC service ключ
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Router /dev/generatesvckey [post]
func (w *Web) GenerateServiceKey(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Генерируем новый 32-байтный hex-ключ
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		logger.Error("'GenerateServiceKey' Ошибка генерации ключа: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key generation failed"})
		return
	}
	newKey := hex.EncodeToString(b)

	if err := w.adminUC.SetConfigs(w.ctx, map[string]string{"svc.service_key": newKey}); err != nil {
		logger.Error("'GenerateServiceKey' Ошибка сохранения svc.service_key: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("'GenerateServiceKey' svc.service_key сгенерирован, перезапускаю", userId)

	// Возвращаем ключ администратору — единственный момент, когда он виден в plaintext.
	// Далее в БД хранится только зашифрованное значение.
	c.JSON(http.StatusOK, gin.H{
		"service_key": newKey,
		"note":        "Сохраните ключ в secrets/service_key.txt каждого микросервиса. Повторно получить plaintext можно через POST /dev/getsvckey.",
	})
	go RestartApp()
}

// GenerateWidgetKey godoc
// @Summary Генерация новой пары Ed25519 ключей для виджета
// @Tags dev
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {string} string "OK"
// @Failure 401 {object} map[string]string
// @Failure 403 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /dev/generate-widget-key [post]
func (w *Web) GenerateWidgetKey(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Генерируем новые ключи
	pub, priv, err := w.exam.GenerateEd25519KeyPair()
	if err != nil {
		logger.Error("'GenerateWidgetKey' Ошибка генерации ключа: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key generation failed"})
		return
	}
	publicDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		logger.Error("'GenerateWidgetKey' Ошибка сериализации публичного ключа: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "public key serialization failed"})
		return
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		logger.Error("'GenerateWidgetKey' Ошибка сериализации приватного ключа: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "private key serialization failed"})
		return
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})

	// PEM-ключи содержат переводы строк. json.Marshal корректно экранирует
	// их, в отличие от ручной сборки JSON через fmt.Sprintf.
	edKeysBytes, err := json.Marshal(struct {
		Public  string `json:"public"`
		Private string `json:"private"`
	}{
		Public:  string(publicPEM),
		Private: string(privatePEM),
	})
	if err != nil {
		logger.Error("'GenerateWidgetKey' Ошибка сериализации ключей: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "key serialization failed"})
		return
	}
	edKeys := string(edKeysBytes)

	if err = w.adminUC.SetConfigs(w.ctx, map[string]string{"widg.ed25519_keys": edKeys}); err != nil {
		logger.Error("'GenerateWidgetKey' Ошибка сохранения widg.ed25519_keys: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("'GenerateWidgetKey' widg.ed25519_keys сгенерированы", userId)

	// Ключи не нужны возвращаю только успешный ответ
	c.Status(http.StatusOK)
}
