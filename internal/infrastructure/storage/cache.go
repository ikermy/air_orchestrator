package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	configCacheTTL      = 5 * time.Minute
	connCheckCacheTTL   = 10 * time.Minute
	quotaReservedKeyFmt = "quota:%d:reserved"
	configCacheKeyFmt   = "storage:config:%d"
	connCheckKeyFmt     = "storage:conncheck:%s"
	sessionMetaKeyFmt   = "storage:session:%s"
)

// ConfigCacheEntry is the non-secret part of user storage config cached in Redis.
type ConfigCacheEntry struct {
	Type                 BackendType `json:"type"`
	Endpoint             string      `json:"endpoint"`
	Bucket               string      `json:"bucket"`
	Region               string      `json:"region"`
	ExternalSTSSupported bool        `json:"external_sts_supported"`
}

// CachedBackendConfig stores the resolved config in Redis without plaintext credentials.
func CachedBackendConfig(ctx context.Context, cache ReservationCache, userID uint32, cfg BackendConfig) error {
	if cache == nil {
		return fmt.Errorf("cache is nil")
	}
	entry := ConfigCacheEntry{
		Type:                 cfg.Type,
		Endpoint:             cfg.Endpoint,
		Bucket:               cfg.Bucket,
		Region:               cfg.Region,
		ExternalSTSSupported: cfg.ExternalSTSSupported,
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return cache.Set(ctx, fmt.Sprintf(configCacheKeyFmt, userID), b, configCacheTTL)
}

// LoadCachedConfig returns the cached non-secret config, or false if not found.
func LoadCachedConfig(ctx context.Context, cache ReservationCache, userID uint32) (ConfigCacheEntry, bool) {
	if cache == nil {
		return ConfigCacheEntry{}, false
	}
	b, err := cache.Get(ctx, fmt.Sprintf(configCacheKeyFmt, userID))
	if err != nil {
		return ConfigCacheEntry{}, false
	}
	var entry ConfigCacheEntry
	if err := json.Unmarshal(b, &entry); err != nil {
		return ConfigCacheEntry{}, false
	}
	return entry, true
}

// CacheConnectionCheck stores the result of an external endpoint health check.
func CacheConnectionCheck(ctx context.Context, cache ReservationCache, endpoint string, ok bool) error {
	if cache == nil || endpoint == "" {
		return fmt.Errorf("invalid connection-check cache arguments")
	}
	b, _ := json.Marshal(map[string]bool{"ok": ok})
	return cache.Set(ctx, fmt.Sprintf(connCheckKeyFmt, endpoint), b, connCheckCacheTTL)
}

// LoadConnectionCheck returns the cached endpoint check result, or false if not cached.
func LoadConnectionCheck(ctx context.Context, cache ReservationCache, endpoint string) (bool, bool) {
	if cache == nil || endpoint == "" {
		return false, false
	}
	b, err := cache.Get(ctx, fmt.Sprintf(connCheckKeyFmt, endpoint))
	if err != nil {
		return false, false
	}
	var entry map[string]bool
	if err := json.Unmarshal(b, &entry); err != nil {
		return false, false
	}
	return entry["ok"], true
}

// SessionMeta is metadata about an active STS session stored in Redis.
type SessionMeta struct {
	UserID    uint32    `json:"user_id"`
	Prefix    string    `json:"prefix"`
	Bucket    string    `json:"bucket"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SaveSessionMeta stores session metadata in Redis with TTL matching the session.
func SaveSessionMeta(ctx context.Context, cache ReservationCache, sessionID string, meta SessionMeta, ttl time.Duration) error {
	if cache == nil || sessionID == "" {
		return fmt.Errorf("invalid session meta arguments")
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return cache.Set(ctx, fmt.Sprintf(sessionMetaKeyFmt, sessionID), b, ttl)
}

// LoadSessionMeta reads session metadata from Redis.
func LoadSessionMeta(ctx context.Context, cache ReservationCache, sessionID string) (SessionMeta, error) {
	if cache == nil || sessionID == "" {
		return SessionMeta{}, fmt.Errorf("invalid session meta arguments")
	}
	b, err := cache.Get(ctx, fmt.Sprintf(sessionMetaKeyFmt, sessionID))
	if err != nil {
		return SessionMeta{}, err
	}
	var meta SessionMeta
	if err := json.Unmarshal(b, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

// SetQuotaReserved sets a fast-check key in Redis for the user's reserved bytes.
func SetQuotaReserved(ctx context.Context, cache ReservationCache, userID uint32, size int64) error {
	if cache == nil {
		return fmt.Errorf("cache is nil")
	}
	return cache.Set(ctx, fmt.Sprintf(quotaReservedKeyFmt, userID), []byte("1"), time.Duration(size/1024)*time.Second+time.Minute)
}

// DelQuotaReserved removes the fast-check key.
func DelQuotaReserved(ctx context.Context, cache ReservationCache, userID uint32) error {
	if cache == nil {
		return fmt.Errorf("cache is nil")
	}
	return cache.Del(ctx, fmt.Sprintf(quotaReservedKeyFmt, userID))
}
