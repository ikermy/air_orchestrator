package web

import (
	"air_orchestrator/internal/config"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// totpPendingSession — временная запись между первым и вторым фактором входа.
type totpPendingSession struct {
	userId    uint32
	respId    uint64
	confirmed int
	disabled  int
	auto      bool
	password  string // plaintext пароль для загрузки MasterKey после 2FA
	expiresAt time.Time
}

// totpPendingTTL — время жизни pending-сессии (пока пользователь ищет телефон).
const totpPendingTTL = 5 * time.Minute

// newTOTPToken генерирует случайный hex-токен для pending-сессии.
func newTOTPToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// storeTOTPPending сохраняет pending-сессию и запускает автоудаление по TTL.
func (w *Web) storeTOTPPending(token string, sess totpPendingSession) {
	w.totpPending.Store(token, sess)
	time.AfterFunc(totpPendingTTL+time.Second, func() {
		w.totpPending.Delete(token)
	})
}

// loadAndDeleteTOTPPending атомарно извлекает и удаляет pending-сессию.
func (w *Web) loadAndDeleteTOTPPending(token string) (totpPendingSession, bool) {
	val, ok := w.totpPending.LoadAndDelete(token)
	if !ok {
		return totpPendingSession{}, false
	}
	sess := val.(totpPendingSession)
	if time.Now().After(sess.expiresAt) {
		return totpPendingSession{}, false
	}
	return sess, true
}

// cleanupTOTPPending периодически удаляет устаревшие pending-сессии.
// Вызывается при старте Web в отдельной горутине. (опционально — AfterFunc уже чистит)
func (w *Web) cleanupTOTPPending() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			w.totpPending.Range(func(k, v any) bool {
				if now.After(v.(totpPendingSession).expiresAt) {
					w.totpPending.Delete(k)
				}
				return true
			})
		}
	}
}

// totpSetupEntry — временная запись сырого секрета до подтверждения кодом.
type totpSetupEntry struct {
	rawSecret string
	expiresAt time.Time
}

// storeSetupPending сохраняет сырой TOTP secret в pending (до подтверждения).
func (w *Web) storeSetupPending(userId uint32, rawSecret string) {
	entry := totpSetupEntry{rawSecret: rawSecret, expiresAt: time.Now().Add(totpPendingTTL)}
	w.totpSetupPending.Store(userId, entry)
	time.AfterFunc(totpPendingTTL+time.Second, func() {
		w.totpSetupPending.Delete(userId)
	})
}

// loadAndDeleteSetupPending атомарно извлекает и удаляет pending setup-запись.
func (w *Web) loadAndDeleteSetupPending(userId uint32) (string, bool) {
	val, ok := w.totpSetupPending.LoadAndDelete(userId)
	if !ok {
		return "", false
	}
	entry := val.(totpSetupEntry)
	if time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.rawSecret, true
}

// TOTPSetup ============================================================================
// POST /totp/setup
// Требует: авторизованный пользователь (token в Authorization header или query).
// Генерирует TOTP secret, сохраняет во временном хранилище (НЕ в БД).
// Secret записывается в БД только после подтверждения кодом (/totp/confirm).
// Возвращает: { uri: "otpauth://..." }
// @Summary Генерация TOTP secret и URI QR-кода
// @Tags totp
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Router /totp/setup [post]
func (w *Web) TOTPSetup(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Получаем email для label в QR-коде
	email, err := w.db.GetUserEmail(userId)
	if err != nil {
		logger.Error("TOTPSetup: error getting email: %v", err, userId)
		email = "user"
	}
	// Расшифровываем если зашифрован
	if plain, dErr := w.exam.DecryptEmailIfNeeded(email); dErr == nil {
		email = plain
	}

	// Генерируем secret и URI
	rawSecret, uri, err := w.exam.GenerateTOTPSecret(email)
	if err != nil {
		logger.Error("'TOTPSetup' ошибка генерации TOTP: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "totp generation failed"})
		return
	}

	// Сохраняем сырой secret во временном хранилище — в БД пока не пишем.
	// TOTP считается активным только после записи в БД (TOTPSecret IS NOT NULL).
	w.storeSetupPending(userId, rawSecret)

	logger.Debug("'TOTPSetup' TOTP secret сгенерирован (ожидает подтверждения)", userId)
	c.JSON(http.StatusOK, gin.H{
		"uri": uri,
	})
}

// ============================================================================
// POST /totp/confirm
// Тело: { "code": "123456" }
// Проверяет код и активирует TOTP — записывает зашифрованный secret в БД.
// После этого TOTPSecret IS NOT NULL ’ TOTP считается включённым.
// ============================================================================
// @Summary Активация TOTP после проверки 6-значного кода
// @Tags totp
// @Security BearerAuth
// @Param body body object true "Код TOTP"
// @Success 200 {object} map[string]any
// @Router /totp/confirm [post]
func (w *Web) TOTPConfirm(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code required"})
		return
	}

	// Извлекаем сырой secret из временного хранилища
	rawSecret, found := w.loadAndDeleteSetupPending(userId)
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"error": "totp not configured or setup expired, call /totp/setup first"})
		return
	}

	// Шифруем secret для проверки кода (ValidateTOTPCode ожидает зашифрованный)
	encSecret, err := w.exam.EncryptTOTPSecret(rawSecret)
	if err != nil {
		logger.Error("'TOTPConfirm' ошибка шифрования TOTP secret: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if !w.exam.ValidateTOTPCode(encSecret, body.Code) {
		logger.Warn("'TOTPConfirm' неверный TOTP код", userId)
		// Возвращаем secret обратно в pending чтобы пользователь мог повторить попытку
		w.storeSetupPending(userId, rawSecret)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid code"})
		return
	}

	// Код верный — сохраняем зашифрованный secret в БД
	// После этого TOTPSecret IS NOT NULL ’ TOTP активен
	if err := w.db.SaveTOTPSecret(w.ctx, userId, encSecret); err != nil {
		logger.Error("'TOTPConfirm' ошибка сохранения TOTP secret: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "totp save failed"})
		return
	}

	logger.Debug("'TOTPConfirm' TOTP успешно активирован", userId)
	c.JSON(http.StatusOK, gin.H{"enabled": true})
}

// ============================================================================
// DELETE /totp
// Тело: { "code": "123456" }
// Отключает TOTP: проверяет одноразовый код и устанавливает TOTPSecret = NULL.
// ============================================================================
// @Summary Отключение TOTP с проверкой 6-значного одноразового кода
// @Tags totp
// @Security BearerAuth
// @Param body body object true "Код TOTP"
// @Success 200 {object} map[string]any
// @Router /totp [delete]
func (w *Web) TOTPDisable(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	var body struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "code required"})
		return
	}

	encSecret, enabled, err := w.db.GetTOTPData(w.ctx, userId)
	if err != nil {
		logger.Error("'TOTPDisable' ошибка получения TOTP данных: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if !enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "totp not enabled"})
		return
	}

	if !w.exam.ValidateTOTPCode(encSecret, body.Code) {
		logger.Warn("'TOTPDisable' неверный TOTP код", userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid code"})
		return
	}

	if err := w.db.ClearTOTPSecret(w.ctx, userId); err != nil {
		logger.Error("'TOTPDisable' ошибка удаления TOTP secret: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "totp disable failed"})
		return
	}

	logger.Debug("'TOTPDisable' TOTP отключён", userId)
	c.JSON(http.StatusOK, gin.H{"enabled": false})
}

// AuthTOTP ===================================================================
// POST /auth/totp
// Второй шаг входа. Тело: { "totp_token": "...", "code": "123456" }
// totp_token — временный токен полученный от POST /auth при TOTPEnabled=true.
// ============================================================================
// @Summary Второй шаг входа — ввод TOTP-кода
// @Tags totp
// @Param body body object true "Токен и код"
// @Success 200 {object} map[string]any
// @Router /auth/totp [post]
func (w *Web) AuthTOTP(c *gin.Context) {
	var body struct {
		TOTPToken string `json:"totp_token" binding:"required"`
		Code      string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "totp_token and code required"})
		return
	}

	sess, ok := w.loadAndDeleteTOTPPending(body.TOTPToken)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired totp_token"})
		return
	}

	encSecret, enabled, err := w.db.GetTOTPData(w.ctx, sess.userId)
	if err != nil || !enabled {
		logger.Error("'AuthTOTP' TOTP не активен", sess.userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "totp not enabled"})
		return
	}

	if !w.exam.ValidateTOTPCode(encSecret, body.Code) {
		logger.Warn("'AuthTOTP' неверный TOTP код", sess.userId)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid code"})
		return
	}

	// Генерируем access token — второй фактор пройден
	sta, lta, err := w.exam.GenerateAccessToken(sess.userId, sess.respId)
	if err != nil {
		logger.Error("'AuthTOTP' ошибка генерации токена: %v", err, sess.userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}

	if sess.auto {
		ltaExpiration := int(config.RefreshTokenTTL / time.Second)

		//cookie := &http.Cookie{
		//	Name:     "MarusiaRefreshToken",
		//	Value:    lta,
		//	Path:     "/v1/auth/token/refresh",
		//	Secure:   true,
		//	HttpOnly: true,
		//	SameSite: http.SameSiteLaxMode,
		//	Expires:  time.Now().Add(time.Duration(ltaExpiration) * time.Second),
		//	Domain:   mode.RealHost,
		//}

		devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEVELOPMENT")), "true")
		cookie := &http.Cookie{
			Name:     "MarusiaRefreshToken",
			Value:    lta,
			Path:     "/",
			Secure:   !devMode,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(time.Duration(ltaExpiration) * time.Second),
		}
		if !devMode {
			cookie.Path = "/v1/auth/token/refresh"
			cookie.Domain = mode.GetRealHost()
		}

		http.SetCookie(c.Writer, cookie)
	}

	logger.Debug("'AuthTOTP' вход выполнен (2FA)", sess.userId)

	// Загружаем MasterKey в RAM (если создан)
	if encMK, wrapSalt, hasMK, mkErr := w.masterKeyUC.GetMasterKeyData(sess.userId); mkErr != nil {
		logger.Error("'AuthTOTP' ошибка получения MasterKey: %v", mkErr, sess.userId)
	} else if hasMK && sess.password != "" {
		if loadErr := w.exam.LoadMasterKey(sess.userId, sess.password, encMK, wrapSalt); loadErr != nil {
			logger.Error("'AuthTOTP' ошибка загрузки MasterKey в cache: %v", loadErr, sess.userId)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"token":     sta,
		"confirmed": sess.confirmed,
		"disabled":  sess.disabled,
	})
}
