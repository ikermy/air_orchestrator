package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// getGoogleOAuthConfig читает параметры Google OAuth из app_config БД.
// Ключи: google_oauth.client_id, google_oauth.client_secret, google_oauth.redirect_uri.
func getGoogleOAuthConfig(ctx context.Context, adminUC AdminUC) *oauth2.Config {
	keys := []string{
		"google_oauth.client_id",
		"google_oauth.client_secret",
		"google_oauth.redirect_uri",
	}
	configs, _ := adminUC.GetConfigs(ctx, keys)

	return &oauth2.Config{
		ClientID:     configs["google_oauth.client_id"],
		ClientSecret: configs["google_oauth.client_secret"],
		RedirectURL:  configs["google_oauth.redirect_uri"],
		Scopes: []string{
			"https://www.dbapis.com/auth/userinfo.email",
			"https://www.dbapis.com/auth/calendar.events.readonly",
			"https://www.dbapis.com/auth/calendar.readonly",
		},
		Endpoint: google.Endpoint,
	}
}

// GoogleOAuthURL godoc
// @Summary Получить URL для авторизации Google OAuth
// @Tags google
// @Produce json
// @Security BearerAuth
// @Param model_id query integer true "ID модели"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /google/oauth/url [get]
func (w *Web) GoogleOAuthURL(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	// Формируем URL для авторизации Google OAuth
	state := fmt.Sprintf("%d", userId)

	oauthConf := getGoogleOAuthConfig(w.ctx, w.adminUC)
	authURL := oauthConf.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	c.JSON(http.StatusOK, gin.H{
		"url": authURL,
	})
}

// GoogleOAuthCallback godoc
// @Summary Callback для Google OAuth авторизации
// @Tags google
// @Produce json
// @Param code query string true "Authorization code"
// @Param state query string true "State parameter"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /open/google/oauth/callback [get]
func (w *Web) GoogleOAuthCallback(c *gin.Context) {
	host := publicWebHost()
	code := c.Query("code")
	state := c.Query("state")
	errorParam := c.Query("error")

	if errorParam != "" {
		logger.Error("'GoogleOAuthCallback' Ошибка OAuth от Google: %s", errorParam)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=%s", host, url.QueryEscape(errorParam)))
		return
	}

	if code == "" || state == "" {
		logger.Error("'GoogleOAuthCallback' Отсутствует code или state")
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=missing-params", host))
		return
	}

	var userID uint32
	_, err := fmt.Sscanf(state, "%d", &userID)
	if err != nil {
		logger.Error("'GoogleOAuthCallback' Ошибка парсинга state: %v", err)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=invalid-state", host))
		return
	}

	config := getGoogleOAuthConfig(w.ctx, w.adminUC)

	ctx := context.Background()
	token, err := config.Exchange(ctx, code)
	if err != nil {
		logger.Error("'GoogleOAuthCallback' Ошибка обмена code на token: %v", err, userID)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=exchange-failed", host))
		return
	}

	client := config.Client(ctx, token)
	resp, err := client.Get("https://www.dbapis.com/oauth2/v2/userinfo")
	if err != nil {
		logger.Error("'GoogleOAuthCallback' Ошибка получения userinfo: %v", err, userID)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=userinfo-failed", host))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var userInfo struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		logger.Error("'GoogleOAuthCallback' Ошибка декодирования userinfo: %v", err, userID)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=decode-failed", host))
		return
	}

	googleEmail := userInfo.Email
	if googleEmail == "" {
		logger.Error("'GoogleOAuthCallback' Получен пустой email от Google", userID)
		c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/error?reason=empty-email", host))
		return
	}

	if err := w.db.SaveGoogleToken(userID, googleEmail, token); err != nil {
		logger.Error("'GoogleOAuthCallback' Ошибка сохранения токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save token"})
		return
	}

	c.Redirect(http.StatusFound, fmt.Sprintf("%s/auth/google/success?email=%s", host, url.QueryEscape(googleEmail)))
}

// GetGoogleTokenStatus godoc
// @Summary Получить статус Google OAuth токена
// @Tags google
// @Produce json
// @Security BearerAuth
// @Param model_id query integer true "ID модели"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /google/token/status [get]
func (w *Web) GetGoogleTokenStatus(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	token, email, err := w.db.GetGoogleToken(userId)
	if err != nil {
		logger.Error("'GetGoogleTokenStatus' Ошибка получения токена: %v", err)
		c.JSON(http.StatusNotFound, gin.H{"status": "not_found"})
		return
	}

	if token == nil {
		c.JSON(http.StatusOK, gin.H{
			"connected": false,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"connected":    true,
		"google_email": email,
		"expiry":       token.Expiry,
		"valid":        token.Valid(),
	})
}

// RevokeGoogleToken godoc
// @Summary Отозвать Google OAuth токен
// @Tags google
// @Produce json
// @Security BearerAuth
// @Param model_id query integer true "ID модели"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Router /google/token/revoke [delete]
func (w *Web) RevokeGoogleToken(c *gin.Context) {
	userId, ok := getUserID(c)
	if !ok {
		return
	}

	if err := w.db.DeleteGoogleToken(userId); err != nil {
		logger.Error("'RevokeGoogleToken' Ошибка удаления токена: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete token"})
		return
	}

	logger.Info("'RevokeGoogleToken' Токен отозван", userId)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Google OAuth token revoked successfully",
	})
}
