package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	ErrReservationNotFound = errors.New("reservation not found")
	ErrQuotaExceeded       = errors.New("storage quota exceeded")
	ErrCacheUnavailable    = errors.New("cache unavailable")
)

// Storage is the application-facing object storage contract.
type Storage interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64, opts PutOptions) (ObjectInfo, error)
	GetObject(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error)
	DeleteObject(ctx context.Context, key string) error
	ListObjects(ctx context.Context, prefix string, opts ListOptions) (ListResult, error)
	StatObject(ctx context.Context, key string) (ObjectInfo, error)
	PresignedGetURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

type PutOptions struct {
	ContentType string
}

type ListOptions struct {
	Limit int
}

type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
}

type ListResult struct {
	Objects               []ObjectInfo
	NextContinuationToken string
}

// StorageResolver decides which Storage to use for a given user.
type StorageResolver interface {
	Resolve(ctx context.Context, userID uint32) (Storage, error)
}

// StorageSessionService issues temporary credentials / sessions for frontend.
//type StorageSessionService interface {
//	CreateSession(ctx context.Context, userID uint32, ttl time.Duration) (StorageSession, error)
//}

type StorageSession struct {
	Endpoint     string
	AccessKey    string
	SecretKey    string
	SessionToken string
	Bucket       string
	Prefix       string
	ExpiresAt    time.Time
}

type BackendType string

const (
	BackendInternal BackendType = "internal_minio"
	BackendExternal BackendType = "external_s3"
)

type BackendConfig struct {
	UserID                                   uint32
	Type                                     BackendType
	Endpoint, Bucket, Region                 string
	AccessKeyCiphertext, SecretKeyCiphertext string
	ExternalSTSSupported                     bool
}
