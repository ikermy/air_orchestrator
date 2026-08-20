package storage

import (
	"context"
	"fmt"
)

// StorageFactory resolves the storage backend for a user. User-specific routing
// can be added without changing callers or the Storage contract.
type StorageFactory struct {
	internal Storage
	configs  interface {
		StorageConfig(context.Context, uint32) (BackendConfig, error)
	}
	masterKey func(uint32) ([32]byte, bool)
	cache     ReservationCache
	onLocked  func(userID uint32)
}

func NewStorageFactory(internal Storage) *StorageFactory {
	return &StorageFactory{internal: internal}
}

func NewConfiguredStorageFactory(internal Storage, configs interface {
	StorageConfig(context.Context, uint32) (BackendConfig, error)
}, masterKey func(uint32) ([32]byte, bool)) *StorageFactory {
	return &StorageFactory{internal: internal, configs: configs, masterKey: masterKey}
}

// SetCache enables Redis caching for resolved storage config (without ciphertext).
func (f *StorageFactory) SetCache(cache ReservationCache) { f.cache = cache }

// OnStorageLocked sets a callback invoked when the user MasterKey is unavailable.
func (f *StorageFactory) OnStorageLocked(fn func(uint32)) { f.onLocked = fn }

func (f *StorageFactory) Resolve(ctx context.Context, userID uint32) (Storage, error) {
	if f == nil {
		return nil, fmt.Errorf("storage factory is nil")
	}
	if f.internal == nil {
		return nil, fmt.Errorf("no internal storage configured")
	}
	if f.configs != nil {
		cfg, err := f.loadCachedConfig(ctx, userID)
		if err != nil {
			return nil, err
		}
		if cfg.Type == BackendExternal {
			return f.externalClient(ctx, userID, cfg)
		}
	}
	return f.internal, nil
}

func (f *StorageFactory) loadCachedConfig(ctx context.Context, userID uint32) (BackendConfig, error) {
	if cached, ok := LoadCachedConfig(ctx, f.cache, userID); ok {
		return BackendConfig{
			UserID:               userID,
			Type:                 cached.Type,
			Endpoint:             cached.Endpoint,
			Bucket:               cached.Bucket,
			Region:               cached.Region,
			ExternalSTSSupported: cached.ExternalSTSSupported,
		}, nil
	}
	cfg, err := f.configs.StorageConfig(ctx, userID)
	if err != nil {
		return BackendConfig{}, err
	}
	if f.cache != nil {
		if cacheErr := CachedBackendConfig(ctx, f.cache, userID, cfg); cacheErr != nil {
			return BackendConfig{}, fmt.Errorf("cache storage config: %w", cacheErr)
		}
	}
	return cfg, nil
}

// ResolveByType returns the backend for the specified type regardless of the
// user's current active config. Used by migrations to get the target backend.
func (f *StorageFactory) ResolveByType(ctx context.Context, userID uint32, typ BackendType) (Storage, error) {
	if f == nil || f.internal == nil {
		return nil, fmt.Errorf("storage factory is nil")
	}
	if typ == BackendInternal {
		return f.internal, nil
	}
	if f.configs == nil {
		return nil, fmt.Errorf("storage config provider is nil")
	}
	cfg, err := f.loadCachedConfig(ctx, userID)
	if err != nil {
		return nil, err
	}
	if cfg.Type != BackendExternal && (cfg.AccessKeyCiphertext == "" || cfg.SecretKeyCiphertext == "") {
		return nil, fmt.Errorf("external storage is not configured")
	}
	return f.externalClient(ctx, userID, cfg)
}

func (f *StorageFactory) externalClient(ctx context.Context, userID uint32, cfg BackendConfig) (Storage, error) {
	if err := ValidateExternalEndpoint(ctx, cfg.Endpoint, true); err != nil {
		return nil, fmt.Errorf("external storage endpoint rejected: %w", err)
	}

	if f.masterKey == nil {
		if f.onLocked != nil {
			f.onLocked(userID)
		}
		return nil, fmt.Errorf("storage credentials are locked")
	}

	return NewExternalS3Storage(cfg.Endpoint, cfg.AccessKeyCiphertext, cfg.SecretKeyCiphertext, cfg.Bucket)
}
