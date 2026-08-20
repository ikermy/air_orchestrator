package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/minio/minio-go/v7/pkg/credentials"
)

type SessionService struct {
	internalEndpoint string
	publicEndpoint   string
	rootAccessKey    string
	rootSecretKey    string
	bucket           string
	location         string
	cache            ReservationCache
}

func NewSessionService(internalEndpoint, publicEndpoint, rootAccessKey, rootSecretKey, bucket, location string) *SessionService {
	return &SessionService{
		internalEndpoint: internalEndpoint,
		publicEndpoint:   publicEndpoint,
		rootAccessKey:    rootAccessKey,
		rootSecretKey:    rootSecretKey,
		bucket:           bucket,
		location:         location,
	}
}

func (s *SessionService) SetCache(cache ReservationCache) { s.cache = cache }

const defaultSessionTTL = 1 * time.Hour

func (s *SessionService) CreateSession(ctx context.Context, userID uint32, ttl time.Duration) (StorageSession, error) {
	if s == nil || s.rootAccessKey == "" || s.rootSecretKey == "" {
		return StorageSession{}, fmt.Errorf("STS is not configured")
	}
	if userID == 0 {
		return StorageSession{}, fmt.Errorf("invalid user ID")
	}
	if ttl <= 0 || ttl > 24*time.Hour {
		ttl = defaultSessionTTL
	}

	prefix := fmt.Sprintf("users/%d/", userID)
	policy := fmt.Sprintf(
		`{"Version":"2012-10-17","Statement":[`+
			`{"Effect":"Allow","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::%s"],"Condition":{"StringLike":{"s3:prefix":["%s*"]}}},`+
			`{"Effect":"Allow","Action":["s3:GetObject","s3:PutObject","s3:DeleteObject"],"Resource":["arn:aws:s3:::%s/%s*"]}`+
			`]}`,
		s.bucket, prefix, s.bucket, prefix)

	sessionName := "session-" + hex.EncodeToString(randomBytes(16))
	durationSeconds := int(ttl.Seconds())
	if durationSeconds < 900 {
		durationSeconds = 900
	}

	creds, err := credentials.NewSTSAssumeRole(s.internalEndpoint, credentials.STSAssumeRoleOptions{
		AccessKey:       s.rootAccessKey,
		SecretKey:       s.rootSecretKey,
		Policy:          policy,
		DurationSeconds: durationSeconds,
		RoleARN:         "assumeRole",
		RoleSessionName: sessionName,
		Location:        s.location,
	})
	if err != nil {
		return StorageSession{}, fmt.Errorf("STS assume role: %w", err)
	}

	val, err := creds.GetWithContext(&credentials.CredContext{
		Client: &http.Client{
			Transport: contextTransport{
				ctx:  ctx,
				base: http.DefaultTransport,
			},
		},
	})
	if err != nil {
		return StorageSession{}, fmt.Errorf("STS get credentials: %w", err)
	}

	expiresAt := time.Now().Add(ttl)

	session := StorageSession{
		Endpoint:     s.publicEndpoint,
		AccessKey:    val.AccessKeyID,
		SecretKey:    val.SecretAccessKey,
		SessionToken: val.SessionToken,
		Bucket:       s.bucket,
		Prefix:       prefix,
		ExpiresAt:    expiresAt,
	}

	if s.cache != nil {
		_ = SaveSessionMeta(ctx, s.cache, sessionName, SessionMeta{
			UserID:    userID,
			Prefix:    prefix,
			Bucket:    s.bucket,
			ExpiresAt: expiresAt,
		}, ttl)
	}

	return session, nil
}

// CreateExternalSession creates the same scoped STS session using decrypted
// external credentials held only in memory by the caller.
func CreateExternalSession(endpoint, accessKey, secretKey, bucket, region string, userID uint32, ttl time.Duration) (StorageSession, error) {
	service := NewSessionService(endpoint, endpoint, accessKey, secretKey, bucket, region)
	return service.CreateSession(context.Background(), userID, ttl)
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
