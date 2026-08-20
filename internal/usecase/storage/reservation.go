package storageusecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	infra "air_orchestrator/internal/infrastructure/storage"
	"air_orchestrator/internal/metrics"
)

type ReservationService struct {
	cache   infra.ReservationCache
	backend infra.Storage
	quota   interface {
		ReserveStorage(context.Context, uint32, int64) error
		CommitStorage(context.Context, uint32, int64) error
		ReleaseStorage(context.Context, uint32, int64) error
		ListReservedUsers(context.Context) ([]uint32, error)
	}
	healthy bool
}

func NewReservationService(cache infra.ReservationCache, backend infra.Storage, quota interface {
	ReserveStorage(context.Context, uint32, int64) error
	CommitStorage(context.Context, uint32, int64) error
	ReleaseStorage(context.Context, uint32, int64) error
	ListReservedUsers(context.Context) ([]uint32, error)
}) *ReservationService {
	svc := &ReservationService{cache: cache, backend: backend, quota: quota}
	svc.checkHealth(context.Background())
	return svc
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *ReservationService) checkHealth(ctx context.Context) {
	if s == nil || s.cache == nil {
		s.setHealthy(false)
		return
	}

	if err := s.cache.Ping(ctx); err != nil {
		s.setHealthy(false)
		return
	}

	s.setHealthy(true)
}

func (s *ReservationService) setHealthy(v bool) {
	if s == nil {
		return
	}

	s.healthy = v
}

func (s *ReservationService) Healthy() bool {
	if s == nil || s.cache == nil {
		return false
	}

	return s.healthy
}

// Cache returns the underlying Redis cache for direct access by storage helpers.
func (s *ReservationService) Cache() infra.ReservationCache { return s.cache }

func (s *ReservationService) HealthCheck(ctx context.Context) bool {
	s.checkHealth(ctx)
	return s.Healthy()
}

func (s *ReservationService) Reserve(ctx context.Context, userID uint32, objectKey string, size int64, ttl time.Duration) (reservationID string, idempotencyKey string, err error) {
	return s.ReserveWithIdempotency(ctx, userID, objectKey, size, "", ttl)
}

func (s *ReservationService) ReserveWithIdempotency(ctx context.Context, userID uint32, objectKey string, size int64, requestedKey string, ttl time.Duration) (reservationID string, idempotencyKey string, err error) {
	if s == nil || s.cache == nil || s.quota == nil {
		return "", "", fmt.Errorf("reservation service is not configured")
	}

	if size <= 0 || userID == 0 || ttl <= 0 {
		return "", "", fmt.Errorf("invalid reservation parameters")
	}

	if !s.Healthy() {
		return "", "", fmt.Errorf("storage is degraded — reservation unavailable")
	}
	if requestedKey != "" {
		if existing, getErr := s.cache.Get(ctx, infra.IdempotencyKey(userID, requestedKey)); getErr == nil {
			if existingReservation, loadErr := infra.LoadUploadReservation(ctx, s.cache, string(existing)); loadErr == nil && existingReservation.Status == "reserved" && time.Now().Before(existingReservation.ExpiresAt) {
				return existingReservation.ID, existingReservation.IdempotencyKey, nil
			}
		}
	}

	if err := s.quota.ReserveStorage(ctx, userID, size); err != nil {
		return "", "", err
	}

	_ = infra.SetQuotaReserved(ctx, s.cache, userID, size)
	reservationID = newID()
	idempotencyKey = requestedKey
	if idempotencyKey == "" {
		idempotencyKey = newID()
	}

	reservation := infra.UploadReservation{
		ID:             reservationID,
		IdempotencyKey: idempotencyKey,
		UserID:         userID,
		ObjectKey:      objectKey,
		Size:           size,
		Status:         "reserved",
	}

	if err = infra.SaveUploadReservation(ctx, s.cache, reservation, ttl); err != nil {
		_ = s.quota.ReleaseStorage(context.Background(), userID, size)
		return "", "", err
	}
	if requestedKey != "" {
		claimed := true
		if setNX, ok := s.cache.(infra.ReservationCacheSetNX); ok {
			claimed, err = setNX.SetNX(ctx, infra.IdempotencyKey(userID, requestedKey), []byte(reservationID), ttl)
		} else {
			err = s.cache.Set(ctx, infra.IdempotencyKey(userID, requestedKey), []byte(reservationID), ttl)
		}
		if err != nil {
			_ = s.quota.ReleaseStorage(context.Background(), userID, size)
			_ = infra.DeleteUploadReservation(context.Background(), s.cache, reservationID)
			return "", "", err
		}
		if !claimed {
			_ = s.quota.ReleaseStorage(context.Background(), userID, size)
			_ = infra.DeleteUploadReservation(context.Background(), s.cache, reservationID)
			if existing, getErr := s.cache.Get(ctx, infra.IdempotencyKey(userID, requestedKey)); getErr == nil {
				if existingReservation, loadErr := infra.LoadUploadReservation(ctx, s.cache, string(existing)); loadErr == nil {
					return existingReservation.ID, existingReservation.IdempotencyKey, nil
				}
			}
			return "", "", fmt.Errorf("idempotency reservation is being created")
		}
	}

	return reservationID, idempotencyKey, nil
}

func (s *ReservationService) Commit(ctx context.Context, reservationID string) error {
	if s == nil || s.cache == nil || s.quota == nil || reservationID == "" {
		return fmt.Errorf("invalid commit")
	}

	r, err := infra.LoadUploadReservation(ctx, s.cache, reservationID)
	if err != nil {
		return fmt.Errorf("reservation not found or expired")
	}

	if r.Status != "reserved" {
		return fmt.Errorf("reservation already committed or released")
	}

	if time.Now().After(r.ExpiresAt) {
		_ = s.quota.ReleaseStorage(context.Background(), r.UserID, r.Size)
		_ = infra.DeleteUploadReservation(context.Background(), s.cache, reservationID)
		return fmt.Errorf("reservation expired")
	}

	if err = s.quota.CommitStorage(ctx, r.UserID, r.Size); err != nil {
		return err
	}

	_ = infra.DelQuotaReserved(context.Background(), s.cache, r.UserID)
	_ = infra.DeleteUploadReservation(context.Background(), s.cache, reservationID)

	return nil
}

func (s *ReservationService) Release(ctx context.Context, reservationID string) error {
	if s == nil || s.cache == nil || s.quota == nil || reservationID == "" {
		return fmt.Errorf("invalid release")
	}

	r, err := infra.LoadUploadReservation(ctx, s.cache, reservationID)
	if err != nil {
		return nil
	}

	if r.Status != "reserved" {
		return nil
	}

	if err = s.quota.ReleaseStorage(ctx, r.UserID, r.Size); err != nil {
		return err
	}

	_ = infra.DelQuotaReserved(context.Background(), s.cache, r.UserID)
	_ = infra.DeleteUploadReservation(context.Background(), s.cache, reservationID)

	return nil
}

func (s *ReservationService) RecoverReservation(ctx context.Context, userID uint32, size int64) error {
	if s == nil || s.quota == nil {
		return fmt.Errorf("reservation service is not configured")
	}

	return s.quota.ReleaseStorage(ctx, userID, size)
}

// SweepExpired scans Redis for expired reservations and releases their MySQL quota.
// Returns the number of reservations swept.
func (s *ReservationService) SweepExpired(ctx context.Context) (int, error) {
	if s == nil || s.cache == nil || s.quota == nil {
		return 0, fmt.Errorf("reservation service is not configured")
	}

	keys, err := s.cache.Keys(ctx, "reservation:*")
	if err != nil {
		return 0, fmt.Errorf("sweep: list keys: %w", err)
	}

	swept := 0
	for _, key := range keys {
		raw, getErr := s.cache.Get(ctx, key)
		if getErr != nil {
			continue
		}

		var r infra.UploadReservation
		if unmarshalErr := json.Unmarshal(raw, &r); unmarshalErr != nil {
			continue
		}

		if r.Status != "reserved" || time.Now().Before(r.ExpiresAt) {
			continue
		}

		if releaseErr := s.quota.ReleaseStorage(ctx, r.UserID, r.Size); releaseErr != nil {
			continue
		}

		_ = s.cache.Del(ctx, key)
		swept++
	}
	return swept, nil
}

// CollectOrphaned returns object keys on the backend that have no active
// reservation. The caller decides whether to delete them.
func (s *ReservationService) CollectOrphaned(ctx context.Context, backend infra.Storage, prefix string) ([]string, error) {
	if s == nil || backend == nil {
		return nil, fmt.Errorf("invalid collect-orphaned call")
	}

	result, err := backend.ListObjects(ctx, prefix, infra.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list orphaned candidates: %w", err)
	}

	var orphaned []string
	for _, obj := range result.Objects {
		reservationKey := objToReservationKey(obj.Key)
		_, loadErr := infra.LoadUploadReservation(ctx, s.cache, reservationKey)
		if loadErr != nil {
			orphaned = append(orphaned, obj.Key)
		}
	}

	return orphaned, nil
}

// ReconcileFromMySQL finds users with reserved_bytes > 0 but no active Redis
// reservation, and releases the stuck MySQL quota.
func (s *ReservationService) ReconcileFromMySQL(ctx context.Context) (int, error) {
	if s == nil || s.cache == nil || s.quota == nil {
		return 0, fmt.Errorf("reservation service is not configured")
	}

	users, err := s.quota.ListReservedUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("list reserved users: %w", err)
	}

	allKeys, keysErr := s.cache.Keys(ctx, "reservation:*")
	if keysErr != nil {
		return 0, fmt.Errorf("list reservation keys: %w", keysErr)
	}

	reservedUsers := make(map[uint32]bool)
	for _, k := range allKeys {
		raw, getErr := s.cache.Get(ctx, k)
		if getErr != nil {
			continue
		}
		var r infra.UploadReservation
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		if r.Status == "reserved" {
			reservedUsers[r.UserID] = true
		}
	}

	released := 0
	for _, userID := range users {
		if reservedUsers[userID] {
			continue
		}
		if releaser, ok := s.quota.(interface {
			ReleaseAllReservedStorage(context.Context, uint32) error
		}); ok {
			if err := releaser.ReleaseAllReservedStorage(ctx, userID); err != nil {
				continue
			}
		} else if err := s.quota.ReleaseStorage(ctx, userID, 1); err != nil {
			continue
		}
		released++
	}

	return released, nil
}

func objToReservationKey(objectKey string) string {
	return fmt.Sprintf("reservation:%s", objectKey)
}

func (s *ReservationService) Tick(ctx context.Context) {
	s.healthTick(ctx)
	s.sweepTick(ctx)
}

func (s *ReservationService) healthTick(ctx context.Context) {
	healthy := s.HealthCheck(ctx)
	metrics.StorageHealthy.Set(boolToFloat(healthy))
}

func (s *ReservationService) sweepTick(ctx context.Context) {
	swept, err := s.SweepExpired(ctx)
	if err != nil {
		metrics.StorageSweepTotal.Add(0)
	} else {
		metrics.StorageSweepTotal.Add(float64(swept))
	}

	if s.backend != nil {
		keys, keysErr := s.cache.Keys(ctx, "reservation:*")
		if keysErr == nil {
			type userPrefix struct {
				userID uint32
				prefix string
			}
			seen := make(map[uint32]bool)
			var users []userPrefix
			for _, k := range keys {
				raw, getErr := s.cache.Get(ctx, k)
				if getErr != nil {
					continue
				}
				var r infra.UploadReservation
				if json.Unmarshal(raw, &r) != nil {
					continue
				}
				if seen[r.UserID] {
					continue
				}
				seen[r.UserID] = true
				users = append(users, userPrefix{r.UserID, fmt.Sprintf("users/%d/", r.UserID)})
			}

			for _, u := range users {
				orphaned, collectErr := s.CollectOrphaned(ctx, s.backend, u.prefix)
				if collectErr == nil {
					for _, key := range orphaned {
						_ = s.backend.DeleteObject(ctx, key)
					}
				}
			}
		}
	}

	reconciled, recErr := s.ReconcileFromMySQL(ctx)
	if recErr == nil && reconciled > 0 {
		metrics.StorageReleaseTotal.Add(float64(reconciled))
	}
}

func boolToFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
