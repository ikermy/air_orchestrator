package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"air_orchestrator/internal/relay"
	"air_orchestrator/internal/repository/mysql"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

const (
	airDeviceKey  = "air_device"
	airCodeTTL    = 15 * time.Minute
	airSessionTTL = 10 * time.Minute
)

var airCodeAlphabet = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ23456789")

func airHash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// airNormalizeCode приводит код к виду без разделителей: "abcd-1234" == "ABCD1234".
func airNormalizeCode(v string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(v)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func airRandomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("air random: %v", err))
	}
	return hex.EncodeToString(buf)
}

func airRandomCode() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("air random code: %v", err))
	}
	var b strings.Builder
	for i, v := range buf {
		if i == 4 {
			b.WriteByte('-')
		}
		b.WriteByte(airCodeAlphabet[int(v)%len(airCodeAlphabet)])
	}
	return b.String()
}

func normalizeAirOS(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "linux":
		return "linux"
	case "macos", "darwin", "mac":
		return "macos"
	default:
		return "windows"
	}
}

func normalizeAirArch(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "arm64", "aarch64":
		return "arm64"
	default:
		return "amd64"
	}
}

func normalizeAirOutput(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "overlay":
		return "overlay"
	case "web":
		return "web"
	default:
		return "both"
	}
}

// airViewerBase выбирает базовый URL для QR. В dev (AIR_VIEWER_USE_LAN_IP=1)
// используется LAN IP, который агент сообщил в heartbeat: localhost с телефона
// недоступен. В prod — публичный адрес frontend.
func (w *Web) airViewerBase(ctx context.Context, deviceID string) string {
	if strings.TrimSpace(os.Getenv("AIR_VIEWER_USE_LAN_IP")) == "1" {
		if d, err := w.db.AirDeviceByID(ctx, deviceID); err == nil && d != nil && strings.TrimSpace(d.LanIP) != "" {
			base := "http://" + strings.TrimSpace(d.LanIP)
			if port := strings.TrimSpace(os.Getenv("AIR_VIEWER_LAN_PORT")); port != "" {
				base += ":" + port
			}
			return base
		}
	}
	return w.airPublicURL()
}

func (w *Web) airPublicURL() string {
	if v := strings.TrimSpace(os.Getenv("AIR_PUBLIC_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	// QR-ссылка должна вести на реальный frontend (/air/viewer), а не на заглушку.
	return publicWebHost()
}

func (w *Web) airAPIURL() string {
	if v := strings.TrimSpace(os.Getenv("AIR_API_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return publicWebHost()
}

func (w *Web) airGRPCEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("AIR_GRPC_ENDPOINT")); v != "" {
		return v
	}
	host := strings.TrimPrefix(strings.TrimPrefix(publicWebHost(), "https://"), "http://")
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return ""
	}
	return host + ":50443"
}

// airDeviceAuth — авторизация агента по device token.
func (w *Web) airDeviceAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := strings.TrimSpace(c.GetHeader("Authorization"))
		parts := strings.Fields(raw)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "device") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "device authorization required"})
			return
		}
		d, err := w.db.AirDeviceByTokenHash(c.Request.Context(), airHash(parts[1]))
		if err != nil {
			logger.Error("air device auth: %v", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		if d == nil || d.Status != "active" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid device token"})
			return
		}
		c.Set(airDeviceKey, d)
		c.Next()
	}
}

func currentAirDevice(c *gin.Context) *mysql.AirDevice {
	v, ok := c.Get(airDeviceKey)
	if !ok {
		return nil
	}
	d, _ := v.(*mysql.AirDevice)
	return d
}

// AirCreateEnrollment создаёт код привязки для текущего пользователя.
func (w *Web) AirCreateEnrollment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req struct {
		TTLSec int `json:"ttl_sec"`
	}
	_ = c.ShouldBindJSON(&req)
	ttl := airCodeTTL
	if req.TTLSec >= 60 && req.TTLSec <= 3600 {
		ttl = time.Duration(req.TTLSec) * time.Second
	}

	code := airRandomCode()
	expires := time.Now().Add(ttl)
	if err := w.db.AirCreateEnrollment(c.Request.Context(), userID, airHash(airNormalizeCode(code)), expires); err != nil {
		logger.Error("AirCreateEnrollment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":       code,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"ttl_sec":    int(ttl.Seconds()),
	})
}

// AirEnroll обменивает код привязки на device token.
func (w *Web) AirEnroll(c *gin.Context) {
	var req struct {
		Code            string `json:"code"`
		DeviceName      string `json:"device_name"`
		OS              string `json:"os"`
		Arch            string `json:"arch"`
		OSVersion       string `json:"os_version"`
		HardwareID      string `json:"hardware_id"`
		AgentVersion    string `json:"agent_version"`
		ProtocolVersion int    `json:"protocol_version"`
		Capabilities    any    `json:"capabilities"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	code := airNormalizeCode(req.Code)
	if len(code) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid code"})
		return
	}

	capabilities := ""
	if req.Capabilities != nil {
		raw, err := json.Marshal(req.Capabilities)
		if err == nil {
			capabilities = string(raw)
		}
	}

	token := "ap_" + airRandomHex(32)
	in := mysql.AirEnrollInput{
		Name:            req.DeviceName,
		OS:              normalizeAirOS(req.OS),
		Arch:            normalizeAirArch(req.Arch),
		OSVersion:       req.OSVersion,
		HardwareHash:    airHash(req.HardwareID),
		AgentVersion:    req.AgentVersion,
		ProtocolVersion: req.ProtocolVersion,
		Capabilities:    capabilities,
		OutputMode:      "both",
	}
	dev, err := w.db.AirEnrollDevice(c.Request.Context(), airHash(code), in, airHash(token), token[:11])
	if err != nil {
		logger.Warn("AirEnroll: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	configRaw, configVersion, err := w.db.AirGetDeviceConfig(c.Request.Context(), dev.DeviceID)
	if err != nil {
		logger.Warn("AirEnroll config: %v", err)
		configRaw = "{}"
	}

	c.JSON(http.StatusOK, gin.H{
		"device_id":        dev.DeviceID,
		"token":            token,
		"token_expires_at": nil,
		"config_version":   configVersion,
		"outputs": gin.H{
			"overlay": dev.OutputMode == "overlay" || dev.OutputMode == "both",
			"web":     dev.OutputMode == "web" || dev.OutputMode == "both",
		},
		"endpoints": gin.H{
			"api":          w.airAPIURL(),
			"grpc":         w.airGRPCEndpoint(),
			"data_service": "air.v1.AirData",
		},
		"config": json.RawMessage(configRaw),
	})
}

// AirDeviceConfig возвращает конфигурацию устройства.
func (w *Web) AirDeviceConfig(c *gin.Context) {
	dev := currentAirDevice(c)
	if dev == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "device authorization required"})
		return
	}
	raw, version, err := w.db.AirGetDeviceConfig(c.Request.Context(), dev.DeviceID)
	if err != nil {
		logger.Error("AirDeviceConfig: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	c.JSON(http.StatusOK, gin.H{
		"device_id":      dev.DeviceID,
		"config_version": version,
		"output_mode":    dev.OutputMode,
		"config":         json.RawMessage(raw),
	})
}

// AirDeviceHeartbeat обрабатывает heartbeat агента.
func (w *Web) AirDeviceHeartbeat(c *gin.Context) {
	dev := currentAirDevice(c)
	if dev == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "device authorization required"})
		return
	}
	var req struct {
		AgentVersion    string `json:"agent_version"`
		ProtocolVersion int    `json:"protocol_version"`
		Capabilities    any    `json:"capabilities"`
		LanIP           string `json:"lan_ip"`
		Outputs         struct {
			Overlay bool `json:"overlay"`
			Web     bool `json:"web"`
		} `json:"outputs"`
		LastError string `json:"last_error"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	mode := "both"
	switch {
	case req.Outputs.Overlay && !req.Outputs.Web:
		mode = "overlay"
	case req.Outputs.Web && !req.Outputs.Overlay:
		mode = "web"
	}
	caps := ""
	if req.Capabilities != nil {
		if raw, err := json.Marshal(req.Capabilities); err == nil {
			caps = string(raw)
		}
	}
	if err := w.db.AirHeartbeat(c.Request.Context(), dev.DeviceID, mysql.AirHeartbeatInput{
		AgentVersion:    req.AgentVersion,
		ProtocolVersion: req.ProtocolVersion,
		Capabilities:    caps,
		OutputMode:      mode,
		LanIP:           strings.TrimSpace(req.LanIP),
		LastError:       req.LastError,
	}); err != nil {
		logger.Warn("AirDeviceHeartbeat: %v", err)
		var busy mysql.ErrDeviceBusy
		if errors.As(err, &busy) {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "another device is online",
				"code":    "device_busy",
				"blocker": busy.Blocker,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Один онлайн: вытесняем стримы других устройств пользователя.
	relay.Default().DisconnectUserExcept(dev.UserID, dev.DeviceID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AirDeviceMe возвращает данные устройства.
func (w *Web) AirDeviceMe(c *gin.Context) {
	dev := currentAirDevice(c)
	if dev == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "device authorization required"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"device": gin.H{
			"id":            dev.DeviceID,
			"name":          dev.Name,
			"os":            dev.OS,
			"arch":          dev.Arch,
			"status":        dev.Status,
			"output_mode":   dev.OutputMode,
			"agent_version": dev.AgentVersion,
			"last_seen":     dev.LastSeenAt,
		},
	})
}

// AirDeviceForget отзывает текущее устройство (локальная команда «Удалить данные»).
func (w *Web) AirDeviceForget(c *gin.Context) {
	dev := currentAirDevice(c)
	if dev == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "device authorization required"})
		return
	}
	if err := w.db.AirForgetDevice(c.Request.Context(), dev.DeviceID); err != nil {
		logger.Warn("AirDeviceForget: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	relay.Default().DisconnectDevice(dev.DeviceID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AirRevokeDevice отзывает устройство пользователя.
func (w *Web) AirRevokeDevice(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device_id required"})
		return
	}
	if err := w.db.AirRevokeDevice(c.Request.Context(), userID, req.DeviceID); err != nil {
		logger.Warn("AirRevokeDevice: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	relay.Default().DisconnectDevice(req.DeviceID)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AirUpdateDeviceConfig сохраняет желаемый режим вывода и базовые настройки
// устройства. Агент применит их при следующем запуске.
func (w *Web) AirUpdateDeviceConfig(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		DeviceID   string `json:"device_id"`
		OutputMode string `json:"output_mode"`
		Config     struct {
			Hotkeys  map[string]string `json:"hotkeys"`
			Language string            `json:"language"`
		} `json:"config"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device_id is required"})
		return
	}
	switch req.OutputMode {
	case "overlay", "web", "both":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid output_mode"})
		return
	}

	ctx := c.Request.Context()
	device, err := w.db.AirDeviceByID(ctx, req.DeviceID)
	if err != nil {
		logger.Error("AirUpdateDeviceConfig device: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if device == nil || device.Status != "active" {
		c.JSON(http.StatusNotFound, gin.H{"error": "device not found"})
		return
	}
	if device.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}

	hotkeys := req.Config.Hotkeys
	if hotkeys == nil {
		hotkeys = map[string]string{}
	}
	payload, err := json.Marshal(gin.H{
		"output_mode": req.OutputMode,
		"hotkeys":     hotkeys,
		"language":    strings.TrimSpace(req.Config.Language),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if err := w.db.AirUpsertDeviceConfig(ctx, req.DeviceID, string(payload)); err != nil {
		logger.Error("AirUpdateDeviceConfig upsert: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	_, version, _ := w.db.AirGetDeviceConfig(ctx, req.DeviceID)
	c.JSON(http.StatusOK, gin.H{"ok": true, "config_version": version})
}

// AirCreateSession создаёт сессию web-viewer и pairing code.
func (w *Web) AirCreateSession(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		DeviceID string `json:"device_id"`
		TTLSec   int    `json:"ttl_sec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.DeviceID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "device_id required"})
		return
	}
	ttl := airSessionTTL
	if req.TTLSec >= 60 && req.TTLSec <= 3600 {
		ttl = time.Duration(req.TTLSec) * time.Second
	}
	code := airRandomCode()
	expires := time.Now().Add(ttl)
	sessionID, err := w.db.AirCreateSession(c.Request.Context(), userID, req.DeviceID,
		airHash(airNormalizeCode(code)), expires)
	if err != nil {
		logger.Warn("AirCreateSession: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id":   sessionID,
		"pairing_code": code,
		"expires_at":   expires.UTC().Format(time.RFC3339),
		"qr_url": fmt.Sprintf("%s/air/viewer?session=%s&code=%s",
			w.airViewerBase(c.Request.Context(), req.DeviceID), sessionID, code),
	})
}

// AirClaimSession активирует сессию viewer.
func (w *Web) AirClaimSession(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		SessionID   string `json:"session_id"`
		PairingCode string `json:"pairing_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionID == "" || req.PairingCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id and pairing_code required"})
		return
	}
	s, err := w.db.AirClaimSession(c.Request.Context(), userID, req.SessionID,
		airHash(airNormalizeCode(req.PairingCode)))
	if err != nil {
		logger.Warn("AirClaimSession: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id": s.SessionID,
		"device_id":  s.DeviceID,
		"status":     s.Status,
		"expires_at": s.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// AirCloseSession закрывает сессию viewer.
func (w *Web) AirCloseSession(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.SessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id required"})
		return
	}
	if err := w.db.AirCloseSession(c.Request.Context(), userID, req.SessionID); err != nil {
		logger.Warn("AirCloseSession: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// AirSessionStatus возвращает статус сессии.
func (w *Web) AirSessionStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	s, err := w.db.AirSession(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		logger.Warn("AirSessionStatus: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"session_id": s.SessionID,
		"device_id":  s.DeviceID,
		"status":     s.Status,
		"expires_at": s.ExpiresAt.UTC().Format(time.RFC3339),
		"created_at": s.CreatedAt.UTC().Format(time.RFC3339),
	})
}

// AirState возвращает состояние сервиса и readiness для мастера подключения.
func (w *Web) AirState(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	devices, err := w.db.AirListDevices(ctx, userID)
	if err != nil {
		logger.Error("AirState devices: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	sessions, err := w.db.AirListSessions(ctx, userID, 20)
	if err != nil {
		logger.Warn("AirState sessions: %v", err)
	}

	var active *mysql.AirDevice
	online := false
	for i := range devices {
		d := &devices[i]
		if d.Status == "active" && active == nil {
			active = d
		}
		if d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < 45*time.Second {
			online = true
		}
	}

	enrolled := active != nil
	modeConfigured := active != nil && active.OutputMode != ""
	viewerConnected := false
	for _, s := range sessions {
		if s.Status == "active" {
			viewerConnected = true
			break
		}
	}

	next := "download"
	switch {
	case !enrolled:
		next = "download"
	case !online:
		next = "start_agent"
	case !modeConfigured:
		next = "choose_mode"
	case (active.OutputMode == "web" || active.OutputMode == "both") && !viewerConnected:
		next = "open_viewer"
	default:
		next = "ready"
	}

	readiness := gin.H{
		"agent_started":    enrolled,
		"enrolled":         enrolled,
		"online":           online,
		"gateway_ok":       online,
		"capabilities_ok":  online,
		"mode_configured":  modeConfigured,
		"viewer_connected": viewerConnected,
		"e2e_ok":           viewerConnected,
	}

	deviceJSON := any(nil)
	if active != nil {
		configRaw, configVersion, err := w.db.AirGetDeviceConfig(ctx, active.DeviceID)
		if err != nil || configRaw == "" {
			configRaw = "{}"
		}
		var configObj any
		if err := json.Unmarshal([]byte(configRaw), &configObj); err != nil {
			configObj = map[string]any{}
		}
		desiredMode := ""
		if m, ok := configObj.(map[string]any); ok {
			if v, ok := m["output_mode"].(string); ok {
				desiredMode = v
			}
		}
		deviceJSON = gin.H{
			"id":                  active.DeviceID,
			"name":                active.Name,
			"lan_ip":              active.LanIP,
			"status":              active.Status,
			"online":              online,
			"output_mode":         active.OutputMode,
			"desired_output_mode": desiredMode,
			"config":              configObj,
			"config_version":      configVersion,
			"last_seen":           active.LastSeenAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"service":   "phantom",
		"device":    deviceJSON,
		"devices":   devices,
		"readiness": readiness,
		"next_step": next,
		"hints":     []string{},
	})
}

// defaultAirArtifactURL — публичная ссылка на последний portable exe.
// Используется, если AIR_ARTIFACT_URL не задан в окружении.
const defaultAirArtifactURL = "https://github.com/ikermy/air_phantom/releases/latest/download/air_phantom-windows-amd64.exe"

// AirDownload возвращает информацию о последнем Windows-артефакте.
func (w *Web) AirDownload(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	version := strings.TrimSpace(os.Getenv("AIR_ARTIFACT_VERSION"))
	url := strings.TrimSpace(os.Getenv("AIR_ARTIFACT_URL"))
	sha := strings.TrimSpace(os.Getenv("AIR_ARTIFACT_SHA256"))
	signature := strings.TrimSpace(os.Getenv("AIR_ARTIFACT_SIGNATURE"))
	if url == "" {
		url = defaultAirArtifactURL
		if version == "" {
			version = "latest"
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"platform":  "windows",
		"arch":      "amd64",
		"version":   version,
		"url":       url,
		"sha256":    sha,
		"signature": signature,
	})
}
