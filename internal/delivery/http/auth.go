package web

import (
	"air_orchestrator/internal/config"
	"air_orchestrator/internal/metrics"
	authuc "air_orchestrator/internal/usecase/auth"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_common/pkg/mode"
	"github.com/ikermy/air_logger/v2/pkg/logger"
)

// CheckMail godoc
// @Summary Проверка занятости email и создание промежуточного ключа
// @Tags auth
// @Accept json
// @Produce json
// @Param request body object true "Email и RespId"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string
// @Router /auth/check-email [post]
func (w *Web) CheckMail(c *gin.Context) {
	var requestData struct {
		Mail   string `json:"a"`
		RespId uint64 `json:"b"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'CheckMail' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	userId, _, err := w.authUC.CheckEmail(requestData.Mail)
	if err != nil {
		logger.Error("'CheckMail' Ошибка при проверке почты: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var key string
	if userId == 0 {
		key, err = w.exam.AddRegUser(requestData.RespId)
		if err != nil {
			logger.Error("'CheckMail' Ошибка при добавлении пользователя: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"email": userId != 0, "key": key})
}

// RegNewUser godoc
// @Summary Регистрация нового пользователя
// @Tags auth
// @Accept json
// @Produce json
// @Param request body object true "Данные регистрации"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /auth/register [post]
func (w *Web) RegNewUser(c *gin.Context) {
	defer func() {
		if c.Writer.Status() < http.StatusBadRequest {
			metrics.RegistrationsTotal.Inc()
		}
	}()
	var requestData struct {
		RespId   uint64 `json:"a"`
		Name     string `json:"b"`
		Mail     string `json:"c"`
		Pass     string `json:"d"`
		Demo     bool   `json:"e"`
		Language string `json:"f"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'RegNewUser' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	// Расшифровываем пароль (транспортный слой)
	password, err := w.exam.DecryptPassword(requestData.RespId, requestData.Pass)
	if err != nil {
		logger.Error("'RegNewUser' Ошибка при расшифровке пароля: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Делегируем бизнес-логику в use case
	regData, err := w.authUC.Register(authuc.RegisterInput{
		Name:     requestData.Name,
		Mail:     requestData.Mail,
		Password: password,
		Demo:     requestData.Demo,
		Language: requestData.Language,
	})
	if err != nil {
		logger.Error("'RegNewUser' Ошибка регистрации: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})

	go w.SendAdminNotification(NewReg, fmt.Sprintf("name: %s\ndemo: %v\nlang: %s\nemail: %s\n",
		requestData.Name, requestData.Demo, requestData.Language, requestData.Mail))

	go func(email, confirmToken string) {
		if err := w.smtp.SendConfirmMail(requestData.Language, email, confirmToken); err != nil {
			logger.Error("'RegNewUser' Ошибка при отправке письма (асинхронно): %v", err)
		}
	}(requestData.Mail, regData.ConfirmToken)
}

// GetAuthKey godoc
// @Summary Получить промежуточный ключ авторизации
// @Tags auth
// @Accept json
// @Produce json
// @Param body body object true "RespId"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /auth/session-key [post]
func (w *Web) GetAuthKey(c *gin.Context) {
	var requestData struct {
		RespId uint64 `json:"a"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'GetAuthKey' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	key, err := w.exam.AddRegUser(requestData.RespId)
	if err != nil {
		logger.Error("'GetAuthKey' Ошибка при создании промежуточного ключа: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key})
}

// HandleResetPass godoc
// @Summary Запросить сброс пароля
// @Tags auth
// @Accept json
// @Produce json
// @Param body body object true "Email RespId Lang"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /auth/reset-password/request [post]
func (w *Web) HandleResetPass(c *gin.Context) {
	var requestData struct {
		RespId uint64 `json:"a"`
		Mail   string `json:"b"`
		Lang   string `json:"lang"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'HandleResetPass' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	token, found, err := w.authUC.RequestPasswordReset(requestData.Mail)
	if err != nil {
		logger.Error("'HandleResetPass' Ошибка генерации токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"a": found})

	if found {
		go func(email, confirmToken string) {
			if err := w.smtp.SendResetPasswordMail(requestData.Lang, email, confirmToken); err != nil {
				logger.Error("'HandleResetPass' Ошибка при отправке письма (асинхронно): %v", err)
			}
		}(requestData.Mail, token)
	}
}

// CheckResetPass godoc
// @Summary Проверить токен сброса пароля
// @Tags auth
// @Accept json
// @Produce json
// @Param body body object true "Токен сброса пароля"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /auth/reset-password/validate [post]
func (w *Web) CheckResetPass(c *gin.Context) {
	var requestData struct {
		RespId uint64 `json:"a"`
		Key    string `json:"b"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'CheckResetPass' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	userId, email, err := w.exam.ParseMailConfirmationToken(requestData.Key)
	if err != nil {
		logger.Error("'CheckResetPass' Ошибка при парсинге токена подтверждения email: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"status":  "error",
			"message": "Недействительный или истёкший токен подтверждения",
		})
		return
	}

	sta, _, err := w.exam.GenerateAccessToken(userId, requestData.RespId)
	if err != nil {
		logger.Error("'CheckResetPass' Ошибка при генерации токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "success",
		"email":  email,
		"token":  sta,
	})
}

// ResetPass godoc
// @Summary Установить новый пароль
// @Tags auth
// @Accept json
// @Produce json
// @Param body body object true "Новый пароль и RespId"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /auth/reset-password/confirm [post]
func (w *Web) ResetPass(c *gin.Context) {
	var requestData struct {
		Token        string `json:"a"`
		Mail         string `json:"b"`
		Pass         string `json:"c"`
		RawMasterKey string `json:"d"` // опционально: base64 raw MasterKey для re-wrap
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'ResetPass' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userId, respId, err := w.exam.ParseAuthToken(&requestData.Token)
	if err != nil {
		logger.Error("'ResetPass' Ошибка при проверке токена: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	if !w.performRateLimit(c, *respId) {
		return
	}

	password, err := w.exam.DecryptPassword(*respId, requestData.Pass)
	if err != nil {
		logger.Error("'ResetPass' Ошибка при расшифровке пароля: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := w.authUC.ResetPassword(*userId, authuc.ResetPasswordInput{
		Mail:         requestData.Mail,
		Password:     password,
		RawMasterKey: requestData.RawMasterKey,
	}); err != nil {
		logger.Error("'ResetPass' Ошибка при сбросе пароля: %v", err, *userId)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// ConfirmEmail godoc
// @Summary Подтвердить email адрес
// @Tags auth
// @Produce json
// @Param token query string true "Токен подтверждения"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /auth/email/confirm [get]
func (w *Web) ConfirmEmail(c *gin.Context) {
	host := publicWebHost()

	tokenStr := c.Query("key")
	if tokenStr == "" {
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/?confirm=error&reason=no-token", host))
		return
	}

	_, email, err := w.authUC.ConfirmEmail(tokenStr)
	if err != nil {
		logger.Error("'ConfirmEmail' Ошибка при подтверждении email: %v", err)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/?confirm=error&reason=invalid-token", host))
		return
	}

	go w.SendAdminNotification(MailConfirm, fmt.Sprintf("email: %s\n", email))
	c.Redirect(http.StatusFound, fmt.Sprintf("%s/?confirm=success&email=%s", host, url.QueryEscape(email)))
}

// publicWebHost returns the public HTTPS origin used for browser redirects.
// GetRealHost normally contains only a hostname, but keeping this centralized
// avoids unsafe splitting of hostnames with or without a port.
func publicWebHost() string {
	host := strings.TrimRight(mode.GetRealHost(), "/")
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

// RefreshToken godoc
// @Summary Обновить access token
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /auth/token/refresh [get]
func (w *Web) RefreshToken(c *gin.Context) {
	refreshToken, err := c.Cookie("MarusiaRefreshToken")
	if err != nil {
		logger.Error("'RefreshToken', cookie not found: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No refresh token"})
		return
	}

	// 1. Проверяем токен в блэклисте (с учетом 10s Grace Period)
	blacklisted, err := w.exam.IsBlacklisted(c.Request.Context(), refreshToken)
	if err != nil {
		logger.Error("'RefreshToken', error checking blacklist: %v", err)
	}
	if blacklisted {
		logger.Warn("'RefreshToken', token is blacklisted")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token revoked"})
		return
	}

	// 2. Проверяем валидность refreshToken
	userId, respId, err := w.exam.VerifyAccessToken(refreshToken)
	if err != nil {
		logger.Error("'RefreshToken', invalid refresh token: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	// 3. Генерируем новую пару токенов (Ротация)
	sta, lta, err := w.exam.GenerateAccessToken(userId, respId)
	if err != nil {
		logger.Error("'RefreshToken', error generating tokens: %v", err, userId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error generating token"})
		return
	}

	// 4. Добавляем старый токен в блэклист (на срок его жизни)
	// Для простоты берем TTL из конфига, в идеале — высчитать оставшееся время до exp
	ltaExpiration := int(config.RefreshTokenTTL / time.Second)
	if err := w.exam.BlacklistToken(c.Request.Context(), refreshToken, time.Duration(ltaExpiration)*time.Second); err != nil {
		logger.Error("'RefreshToken', failed to blacklist old token: %v", err)
	}

	// 5. Инвалидируем user_key в Redis перед обновлением
	err = w.exam.RefreshMasterKeyTTL(userId)
	if err != nil {
		logger.Error("'RefreshToken', failed to refresh master key in redis: %v", err)
		// Вообще здесь можно продолжать НО сервисы не получат mk пользователя и всё равно не смогут работать
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Error refreshing token"})
	}

	// 6. Устанавливаем новую куку с HttpOnly и ограниченным Path
	//cookie := &http.Cookie{
	//	Name:     "MarusiaRefreshToken",
	//	Value:    lta,
	//	Path:     "/v1/auth/token/refresh",
	//	Secure:   true,
	//	HttpOnly: true,
	//	SameSite: http.SameSiteLaxMode,
	//	Expires:  time.Now().Add(time.Duration(ltaExpiration) * time.Second),
	//}
	//http.SetCookie(c.Writer, cookie)

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

	c.JSON(http.StatusOK, gin.H{"s": sta})
}

// Login godoc
// @Summary Аутентификация пользователя
// @Tags auth
// @Accept json
// @Produce json
// @Param request body object true "Учётные данные"
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Router /auth/login [post]
func (w *Web) Login(c *gin.Context) {
	defer func() {
		result := "success"
		if c.Writer.Status() >= http.StatusBadRequest {
			result = "failure"
		}
		metrics.AuthAttemptsTotal.WithLabelValues(result).Inc()
	}()
	var requestData struct {
		RespId uint64 `json:"a"`
		Mail   string `json:"b"`
		Pass   string `json:"c"`
		Auto   bool   `json:"d"` // Вход через куки
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		logger.Error("'Login' Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !w.performRateLimit(c, requestData.RespId) {
		return
	}

	// Расшифровываем пароль (транспортный слой)
	password, err := w.exam.DecryptPassword(requestData.RespId, requestData.Pass)
	if err != nil {
		logger.Error("'Login' Ошибка при расшифровке пароля: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Делегируем аутентификацию в use case
	result, err := w.authUC.Authenticate(w.ctx, authuc.LoginInput{
		Mail:     requestData.Mail,
		Password: password,
		RespID:   requestData.RespId,
	})
	if err != nil {
		logger.Error("'Login' Ошибка аутентификации: %v", requestData.Mail)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	// TOTP — не выдаём токен сразу, возвращаем промежуточный totp_token
	if result.TOTPEnabled {
		totpToken, err := newTOTPToken()
		if err != nil {
			logger.Error("'Login' ошибка генерации TOTP токена: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
			return
		}
		w.storeTOTPPending(totpToken, totpPendingSession{
			userId:    result.UserID,
			respId:    requestData.RespId,
			confirmed: result.Confirmed,
			disabled:  result.Disabled,
			auto:      requestData.Auto,
			password:  result.Password,
			expiresAt: time.Now().Add(totpPendingTTL),
		})
		c.JSON(http.StatusOK, gin.H{"status": "totp_required", "totp_token": totpToken})
		return
	}

	// Тихая миграция legacy-хэша (SHA3 → bcrypt) в фоне
	if result.IsLegacy {
		go func(uid uint32, pass, mail string) {
			if err := w.authUC.MigrateLegacyUser(uid, pass, mail); err != nil {
				logger.Error("'Login' lazy migration: %v", err, uid)
			}
		}(result.UserID, result.Password, requestData.Mail)
	}

	// Загружаем MasterKey в RAM если он существует
	masterKeyExists, _ := w.authUC.LoadMasterKeyIfExists(result.UserID, result.Password)

	// Генерируем токены (задача delivery-слоя)
	sta, lta, err := w.exam.GenerateAccessToken(result.UserID, requestData.RespId)
	if err != nil {
		logger.Error("'Login' Ошибка при генерации токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if requestData.Auto {
		//cookie := &http.Cookie{
		//	Name:     "MarusiaRefreshToken",
		//	Value:    lta,
		//	Path:     "/v1/auth/token/refresh",
		//	Secure:   true,
		//	HttpOnly: true,
		//	SameSite: http.SameSiteLaxMode,
		//	Expires:  time.Now().Add(config.RefreshTokenTTL),
		//}
		//if mode.RealHost != "localhost" {
		//	cookie.Domain = mode.RealHost
		//}

		devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEVELOPMENT")), "true")
		cookie := &http.Cookie{
			Name:     "MarusiaRefreshToken",
			Value:    lta,
			Path:     "/",
			Secure:   !devMode,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(config.RefreshTokenTTL),
		}
		if !devMode {
			cookie.Path = "/v1/auth/token/refresh"
			cookie.Domain = mode.GetRealHost()
		}

		http.SetCookie(c.Writer, cookie)
	}

	//mk, ok := w.exam.GetMasterKey(result.UserID)
	//if !ok {
	//	logger.Warn("'Login' Не удалось получить MasterKey для шифрования векторных документов")
	//}
	//progress := func(s string) {
	//	logger.Debug("progress: %s", s)
	//}
	//
	//if err := w.db.EncryptUserStorageConfigWSS(result.UserID, mk, progress); err != nil {
	//	logger.Warn("'Login' ошибка тестового шифрования: %v", err)
	//}

	c.JSON(http.StatusOK, gin.H{
		"token":     sta,
		"confirmed": result.Confirmed,
		"disabled":  result.Disabled,
		"master":    masterKeyExists,
	})
}

// Logout godoc
// @Summary Завершение сессии пользователя
// @Tags auth
// @Produce json
// @Success 200 {object} map[string]string
// @Router /auth/logout [post]
func (w *Web) Logout(c *gin.Context) {
	if refreshToken, err := c.Cookie("MarusiaRefreshToken"); err == nil && refreshToken != "" {
		// Добавляем в блэклист (на макс. время жизни LTA)
		ltaExpiration := int(config.RefreshTokenTTL / time.Second)
		_ = w.exam.BlacklistToken(c.Request.Context(), refreshToken, time.Duration(ltaExpiration)*time.Second)
	}

	// Удаляем HttpOnly куку (важно: Path должен совпадать с тем, что был при установке)
	devMode := strings.EqualFold(strings.TrimSpace(os.Getenv("DEVELOPMENT")), "true")
	cookiePath, cookieDomain := "/", ""
	if !devMode {
		cookiePath, cookieDomain = "/v1/auth/token/refresh", mode.GetRealHost()
	}
	c.SetCookie("MarusiaRefreshToken", "", -1, cookiePath, cookieDomain, !devMode, true)

	// MasterKey НЕ удаляем из памяти/Redis, так как он может быть нужен фоновым сервисам
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
