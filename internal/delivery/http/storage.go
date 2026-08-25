package web

import (
	"air_orchestrator/internal/infrastructure/storage"
	"air_orchestrator/internal/metrics"
	storageusecase "air_orchestrator/internal/usecase/storage"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// GetStorageConfig godoc
// @Summary Получить конфигурацию хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /storage/config [get]
// GetStorageConfig exposes only non-secret control-plane settings. Ciphertexts
// and access keys are intentionally never serialized to clients.
func (w *Web) GetStorageConfig(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	cfg, err := w.db.StorageConfig(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load storage config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"storage_type": cfg.Type, "endpoint": cfg.Endpoint, "bucket": cfg.Bucket, "region": cfg.Region})
}

// CreateStorageSession godoc
// @Summary Создать сессию доступа к хранилищу
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/session [post]
// CreateStorageSession returns temporary scoped credentials for internal MinIO
// (via STS) or a presigned descriptor for external S3 without STS.
// Permanent or root credentials are never returned.
func (w *Web) CreateStorageSession(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	cfg, err := w.db.StorageConfig(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load storage config"})
		return
	}

	if cfg.Type == storage.BackendInternal && w.storage.Sessions != nil {
		ttl := 1 * time.Hour
		session, sessErr := w.storage.Sessions.CreateSession(c.Request.Context(), userID, ttl)
		if sessErr != nil {
			metrics.StorageSessionTotal.WithLabelValues("sts_error").Inc()
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to create storage session"})
			return
		}
		metrics.StorageSessionTotal.WithLabelValues("sts").Inc()
		c.JSON(http.StatusOK, gin.H{
			"mode":          "sts",
			"storage_type":  cfg.Type,
			"endpoint":      session.Endpoint,
			"bucket":        session.Bucket,
			"prefix":        session.Prefix,
			"access_key":    session.AccessKey,
			"secret_key":    session.SecretKey,
			"session_token": session.SessionToken,
			"expires_in":    int(time.Until(session.ExpiresAt).Seconds()),
		})
		return
	}

	if cfg.Type == storage.BackendExternal && w.exam != nil {
		if cfg.ExternalSTSSupported {
			session, sessionErr := storage.CreateExternalSession(cfg.Endpoint, cfg.AccessKeyCiphertext, cfg.SecretKeyCiphertext, cfg.Bucket, cfg.Region, userID, 1*time.Hour)
			if sessionErr == nil {
				c.JSON(http.StatusOK, gin.H{"mode": "sts", "storage_type": cfg.Type, "endpoint": session.Endpoint, "bucket": session.Bucket, "prefix": session.Prefix, "region": cfg.Region, "access_key": session.AccessKey, "secret_key": session.SecretKey, "session_token": session.SessionToken, "expires_in": int(time.Until(session.ExpiresAt).Seconds())})
				return
			}
		}
	}

	metrics.StorageSessionTotal.WithLabelValues("presigned").Inc()

	c.JSON(http.StatusOK, gin.H{"mode": "presigned", "storage_type": cfg.Type, "bucket": cfg.Bucket, "prefix": fmt.Sprintf("users/%d/", userID), "expires_in": 900})
}

// GetStorageQuota godoc
// @Summary Получить квоту хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 501 {object} map[string]string
// @Router /storage/quota [get]
func (w *Web) GetStorageQuota(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	quotaReader, ok := any(w.db).(interface {
		StorageQuota(context.Context, uint32) (uint64, uint64, uint64, error)
	})
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "storage quota repository unavailable"})
		return
	}

	quota, used, reserved, err := quotaReader.StorageQuota(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "storage quota is not initialized"})
		return
	}

	logger.Debug("quota for user %d: quota=%d, used=%d, reserved=%d, available=%d", userID, quota, used, reserved, availableQuota(quota, used, reserved))

	c.JSON(http.StatusOK, gin.H{"quota_bytes": quota, "used_bytes": used, "reserved_bytes": reserved, "available_bytes": availableQuota(quota, used, reserved)})
}

func availableQuota(quota, used, reserved uint64) uint64 {
	if quota == 0 || used >= quota || reserved >= quota-used {
		return 0
	}

	return quota - used - reserved
}

// PutExternalStorageConfig godoc
// @Summary Сохранить конфигурацию внешнего хранилища
// @Tags storage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "Параметры внешнего S3-хранилища"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Failure 501 {object} map[string]string
// @Router /storage/config/external [put]
func (w *Web) PutExternalStorageConfig(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req struct{ Endpoint, Bucket, Region, AccessKey, SecretKey string }
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Endpoint) == "" || strings.TrimSpace(req.Bucket) == "" || req.AccessKey == "" || req.SecretKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint, bucket, access_key and secret_key are required"})
		return
	}

	if err := storage.ValidateExternalEndpoint(c.Request.Context(), req.Endpoint, true); err != nil {
		logger.Error("external storage endpoint validation failed: %v", err, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := storage.ValidateBucketName(req.Bucket); err != nil {
		logger.Error("external storage bucket validation failed: %v", err, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	stsSupported, _ := storage.ProbeSTSCapability(c.Request.Context(), req.Endpoint, req.AccessKey, req.SecretKey, req.Bucket, req.Region)

	if err := w.db.SaveStorageConfig(c.Request.Context(), storage.BackendConfig{UserID: userID, Type: storage.BackendExternal, Endpoint: req.Endpoint, Bucket: req.Bucket, Region: req.Region, AccessKeyCiphertext: req.AccessKey, SecretKeyCiphertext: req.SecretKey, ExternalSTSSupported: stsSupported}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save storage config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"storage_type": storage.BackendExternal, "sts_supported": stsSupported})
}

// TestExternalStorageConfig godoc
// @Summary Проверить доступность внешнего хранилища
// @Tags storage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "Endpoint внешнего хранилища"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /storage/config/test [post]
func (w *Web) TestExternalStorageConfig(c *gin.Context) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.Endpoint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "endpoint is required"})
		return
	}

	if err := storage.ValidateExternalEndpoint(c.Request.Context(), req.Endpoint, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if w.storage.Reservations != nil && w.storage.Reservations.Cache() != nil {
		if ok, cached := storage.LoadConnectionCheck(c.Request.Context(), w.storage.Reservations.Cache(), req.Endpoint); cached {
			if ok {
				c.JSON(http.StatusOK, gin.H{"ok": true, "cached": true})
			} else {
				c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "endpoint is unreachable", "cached": true})
			}
			return
		}
	}

	u, _ := url.Parse(req.Endpoint)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext}}
	resp, err := client.Head(u.String())
	if err != nil {
		if w.storage.Reservations != nil && w.storage.Reservations.Cache() != nil {
			_ = storage.CacheConnectionCheck(context.Background(), w.storage.Reservations.Cache(), req.Endpoint, false)
		}
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "endpoint is unreachable"})
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 500 {
		if w.storage.Reservations != nil && w.storage.Reservations.Cache() != nil {
			_ = storage.CacheConnectionCheck(context.Background(), w.storage.Reservations.Cache(), req.Endpoint, false)
		}
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "status": resp.StatusCode})
		return
	}

	if w.storage.Reservations != nil && w.storage.Reservations.Cache() != nil {
		_ = storage.CacheConnectionCheck(context.Background(), w.storage.Reservations.Cache(), req.Endpoint, true)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "status": resp.StatusCode})
}

// SwitchToInternalStorage godoc
// @Summary Переключить на внутреннее хранилище
// @Tags storage
// @Security BearerAuth
// @Success 204
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Failure 501 {object} map[string]string
// @Router /storage/config/switch [post]
func (w *Web) SwitchToInternalStorage(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if err := w.db.SaveStorageConfig(c.Request.Context(), storage.BackendConfig{UserID: userID, Type: storage.BackendInternal}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to switch storage"})
		return
	}

	c.Status(http.StatusNoContent)
}

type presignedPutter interface {
	PresignedPutURL(context.Context, string, time.Duration) (string, error)
}

func validStorageKey(userID uint32, key string) bool {
	prefix := fmt.Sprintf("users/%d/", userID)
	if !strings.HasPrefix(key, prefix) || strings.ContainsAny(key, "\\\x00\r\n") {
		return false
	}

	for _, part := range strings.Split(strings.TrimPrefix(key, prefix), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}

	return true
}

// CreatePresignedUpload godoc
// @Summary Создать presigned URL для загрузки объекта
// @Tags storage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "Параметры файла"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 413 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/presigned-upload [post]
func (w *Web) CreatePresignedUpload(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req struct {
		FileName       string `json:"file_name"`
		ContentType    string `json:"content_type"`
		Size           int64  `json:"size"`
		IdempotencyKey string `json:"idempotency_key"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.FileName) == "" || req.FileName != filepath.Base(req.FileName) || strings.ContainsAny(req.FileName, "\\/\x00\r\n") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_name"})
		return
	}

	if len(req.FileName) > 255 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_name is too long"})
		return
	}

	if req.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "size must be positive"})
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	if w.storage.Reservations != nil && !w.storage.Reservations.Healthy() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is degraded — reservation unavailable"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	putter, ok := backend.(presignedPutter)
	if !ok {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "backend does not support presigned uploads"})
		return
	}

	objectID := uuid.NewString()
	if req.IdempotencyKey != "" {
		// A retry must address the same object before reservation lookup; this
		// prevents a second URL from being issued for an existing reservation.
		objectID = uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%d:%s:%s", userID, req.IdempotencyKey, req.FileName))).String()
	}
	key := fmt.Sprintf("users/%d/%s-%s", userID, objectID, filepath.Base(req.FileName))

	reservationID, idempotencyKey, reserveErr := reserveStorage(c.Request.Context(), w.storage.Reservations, userID, key, req.Size, req.IdempotencyKey)
	if reserveErr != nil {
		if strings.Contains(reserveErr.Error(), "degraded") {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is degraded — try again later"})
		} else {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "storage quota exceeded"})
		}
		return
	}

	putURL, err := putter.PresignedPutURL(c.Request.Context(), key, 15*time.Minute)
	if err != nil {
		logger.Error("storage presigned upload URL failed: %v", err, userID)
		releaseReservation(c.Request.Context(), w.storage.Reservations, reservationID)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create upload URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "url": putURL, "expires_in": 900, "reservation_id": reservationID, "idempotency_key": idempotencyKey, "reserved_size": req.Size})
}

// CommitStorageUpload godoc
// @Summary Подтвердить загрузку объекта
// @Tags storage
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body object true "Ключ объекта и ID резервации"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/commit [post]
func (w *Web) CommitStorageUpload(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	var req struct {
		Key           string `json:"key"`
		ReservationID string `json:"reservation_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || !strings.HasPrefix(req.Key, fmt.Sprintf("users/%d/", userID)) || strings.ContainsAny(req.Key, "\\\x00\r\n") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	info, err := backend.StatObject(c.Request.Context(), req.Key)
	if err != nil {
		metrics.StorageCommitTotal.WithLabelValues("not_found").Inc()
		c.JSON(http.StatusNotFound, gin.H{"error": "uploaded object not found"})
		return
	}

	if req.ReservationID != "" && w.storage.Reservations != nil {
		if commitErr := w.storage.Reservations.Commit(c.Request.Context(), req.ReservationID); commitErr != nil {
			if strings.Contains(commitErr.Error(), "expired") {
				metrics.StorageCommitTotal.WithLabelValues("expired").Inc()
			} else {
				metrics.StorageCommitTotal.WithLabelValues("error").Inc()
			}
			c.JSON(http.StatusConflict, gin.H{"error": "upload reservation is invalid"})
			return
		}
	} else {
		if quota, ok := any(w.db).(interface {
			CommitStorage(context.Context, uint32, int64) error
		}); ok {
			if err := quota.CommitStorage(c.Request.Context(), userID, info.Size); err != nil {
				metrics.StorageCommitTotal.WithLabelValues("error").Inc()
				c.JSON(http.StatusConflict, gin.H{"error": "upload reservation is invalid"})
				return
			}
		}
	}

	metrics.StorageCommitTotal.WithLabelValues("ok").Inc()

	c.JSON(http.StatusOK, gin.H{"key": info.Key, "size": info.Size, "etag": info.ETag, "modified_at": info.LastModified})
}

// ListStorageObjects godoc
// @Summary Получить список объектов хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Param limit query integer false "Максимальное количество объектов (1-1000)"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/objects [get]
func (w *Web) ListStorageObjects(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if _, scanErr := fmt.Sscanf(raw, "%d", &limit); scanErr != nil || limit < 1 || limit > 1000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be between 1 and 1000"})
			return
		}
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	prefix := fmt.Sprintf("users/%d/", userID)
	result, err := backend.ListObjects(c.Request.Context(), prefix, storage.ListOptions{Limit: limit})
	if err != nil {
		logger.Error("storage object list failed, prefix %q: %v", prefix, err, userID)
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list objects"})
		return
	}

	objects := make([]gin.H, 0, len(result.Objects))
	for _, object := range result.Objects {
		getURL, urlErr := backend.PresignedGetURL(c.Request.Context(), object.Key, 15*time.Minute)
		if urlErr != nil {
			// Listing must not hide an existing object when only URL signing fails.
			// The client can still display the object and retry the download URL.
			logger.Error("storage object presign failed, key %q: %v", object.Key, urlErr, userID)
		}
		objects = append(objects, gin.H{"key": object.Key, "size": object.Size, "etag": object.ETag, "modified_at": object.LastModified, "url": getURL})
	}

	logger.Debug("object list, prefix %q: found=%d returned=%d", prefix, len(result.Objects), len(objects), userID)

	c.JSON(http.StatusOK, gin.H{"objects": objects})
}

// DeleteStorageObject godoc
// @Summary Удалить объект хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Param key query string true "Ключ объекта"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/object [delete]
func (w *Web) DeleteStorageObject(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	key := c.Query("key")
	if !validStorageKey(userID, key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	if err := backend.DeleteObject(c.Request.Context(), key); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
		return
	}

	c.Status(http.StatusNoContent)
}

// DeleteAllStorageObjects godoc
// @Summary Удалить все объекты хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/delete-all [post]
func (w *Web) DeleteAllStorageObjects(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	result, err := backend.ListObjects(c.Request.Context(), fmt.Sprintf("users/%d/", userID), storage.ListOptions{})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list objects"})
		return
	}

	deleted := 0
	for _, object := range result.Objects {
		if !validStorageKey(userID, object.Key) {
			continue
		}
		if err := backend.DeleteObject(c.Request.Context(), object.Key); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "failed to delete all objects", "deleted": deleted})
			return
		}
		deleted++
	}

	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

// StorageHealth godoc
// @Summary Проверить состояние хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 401 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/health [get]
func (w *Web) StorageHealth(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"ok": false, "error": "storage is not configured"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"ok": false, "error": err.Error()})
		return
	}

	_, err = backend.ListObjects(c.Request.Context(), fmt.Sprintf("users/%d/", userID), storage.ListOptions{Limit: 1})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "storage backend unavailable"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// StartStorageMigration godoc
// @Summary Запустить миграцию хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/migration [post]
func (w *Web) StartStorageMigration(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	if w.storage.Migrations == nil || w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "migration is not configured"})
		return
	}

	cfg, err := w.db.StorageConfig(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load storage config"})
		return
	}

	var targetType storage.BackendType
	if cfg.Type == storage.BackendInternal {
		targetType = storage.BackendExternal
	} else {
		targetType = storage.BackendInternal
	}

	source, target, resolveErr := resolveMigrationPair(c.Request.Context(), w.storage.Factory, w.db, userID, targetType)
	if resolveErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": resolveErr.Error()})
		return
	}

	record, startErr := w.storage.Migrations.StartWithTypes(c.Request.Context(), userID, source, target, cfg.Type, targetType)
	if startErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "migration failed: " + startErr.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":          record.ID,
		"state":       record.State,
		"source_type": cfg.Type,
		"target_type": targetType,
	})
}

type userConfigProvider interface {
	StorageConfig(context.Context, uint32) (storage.BackendConfig, error)
}

func resolveMigrationPair(ctx context.Context, factory *storage.StorageFactory, cfgProvider userConfigProvider, userID uint32, targetType storage.BackendType) (storage.Storage, storage.Storage, error) {
	if factory == nil {
		return nil, nil, fmt.Errorf("storage factory is nil")
	}

	cfg, err := cfgProvider.StorageConfig(ctx, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load storage config: %w", err)
	}
	if cfg.Type == targetType {
		return nil, nil, fmt.Errorf("already using target storage type")
	}

	// Source = current active backend
	source, srcErr := factory.Resolve(ctx, userID)
	if srcErr != nil {
		return nil, nil, fmt.Errorf("failed to resolve source: %w", srcErr)
	}

	// Target = the other backend
	target, tgtErr := factory.ResolveByType(ctx, userID, targetType)
	if tgtErr != nil {
		return nil, nil, fmt.Errorf("failed to resolve target: %w", tgtErr)
	}

	return source, target, nil
}

// GetStorageMigration godoc
// @Summary Получить статус миграции хранилища
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Param id path integer true "ID миграции"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /storage/migration/{id} [get]
func (w *Web) GetStorageMigration(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid migration id"})
		return
	}

	r, err := w.db.GetMigration(c.Request.Context(), id)
	if err != nil || r.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "migration not found"})
		return
	}

	c.JSON(http.StatusOK, r)
}

// CancelStorageMigration godoc
// @Summary Отменить миграцию хранилища
// @Tags storage
// @Security BearerAuth
// @Param id path integer true "ID миграции"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 409 {object} map[string]string
// @Router /storage/migration/{id}/cancel [post]
func (w *Web) CancelStorageMigration(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid migration id"})
		return
	}

	r, err := w.db.GetMigration(c.Request.Context(), id)
	if err != nil || r.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": "migration not found"})
		return
	}

	if err := w.db.CancelMigration(c.Request.Context(), id); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// PresignStorageDownload godoc
// @Summary Создать presigned URL для скачивания объекта
// @Tags storage
// @Produce json
// @Security BearerAuth
// @Param key query string true "Ключ объекта"
// @Param ttl query integer false "Время действия URL в секундах (60-3600)"
// @Success 200 {object} map[string]any
// @Failure 400 {object} map[string]string
// @Failure 401 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 423 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /storage/presigned-download [get]
func (w *Web) PresignStorageDownload(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}

	key := c.Query("key")
	if !validStorageKey(userID, key) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid key"})
		return
	}

	if w.storage.Factory == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage is not configured"})
		return
	}

	backend, err := w.storage.Factory.Resolve(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusLocked, gin.H{"error": err.Error()})
		return
	}

	ttl := 15 * time.Minute
	if raw := c.Query("ttl"); raw != "" {
		seconds, scanErr := strconv.Atoi(raw)
		if scanErr != nil || seconds < 60 || seconds > 3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ttl"})
			return
		}
		ttl = time.Duration(seconds) * time.Second
	}

	getURL, err := backend.PresignedGetURL(c.Request.Context(), key, ttl)
	if err != nil {
		logger.Error("presigned download failed: key=%q err=%v", key, err, userID)
		c.JSON(http.StatusNotFound, gin.H{"error": "object not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"key": key, "url": getURL, "expires_in": int(ttl.Seconds())})
}

func reserveStorage(ctx context.Context, svc *storageusecase.ReservationService, userID uint32, objectKey string, size int64, requested ...string) (reservationID, idempotencyKey string, err error) {
	if svc == nil {
		metrics.StorageReservationTotal.WithLabelValues("unavailable").Inc()
		return "", "", fmt.Errorf("reservation service is unavailable")
	}

	idempotency := ""
	if len(requested) > 0 {
		idempotency = requested[0]
	}
	id, key, e := svc.ReserveWithIdempotency(ctx, userID, objectKey, size, idempotency, 15*time.Minute)
	if e != nil {
		if strings.Contains(e.Error(), "degraded") {
			metrics.StorageReservationTotal.WithLabelValues("degraded").Inc()
		} else if strings.Contains(e.Error(), "exceeded") {
			metrics.StorageReservationTotal.WithLabelValues("quota_exceeded").Inc()
		} else {
			metrics.StorageReservationTotal.WithLabelValues("error").Inc()
		}
		return "", "", e
	}

	metrics.StorageReservationTotal.WithLabelValues("ok").Inc()

	return id, key, nil
}

func releaseReservation(ctx context.Context, svc *storageusecase.ReservationService, reservationID string) {
	if svc == nil {
		return
	}

	if err := svc.Release(ctx, reservationID); err == nil {
		metrics.StorageReleaseTotal.Inc()
	}
}
