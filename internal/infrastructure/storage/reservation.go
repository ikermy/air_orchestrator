package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ReservationCache is the minimal Redis contract used for temporary upload
// reservations. MySQL remains the source of truth for committed usage.
type ReservationCache interface {
	Set(context.Context, string, []byte, time.Duration) error
	Get(context.Context, string) ([]byte, error)
	Del(context.Context, string) error
	Ping(context.Context) error
	Keys(ctx context.Context, pattern string) ([]string, error)
}

type ReservationPinger interface {
	Ping(context.Context) error
}

type ReservationCacheSetNX interface {
	SetNX(context.Context, string, []byte, time.Duration) (bool, error)
}

type UploadReservation struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	UserID         uint32    `json:"user_id"`
	ObjectKey      string    `json:"object_key"`
	Size           int64     `json:"size"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// IdempotencyKey returns a user-scoped Redis key for an upload request.
func IdempotencyKey(userID uint32, key string) string {
	return fmt.Sprintf("reservation-idempotency:%d:%s", userID, key)
}

func SaveUploadReservation(ctx context.Context, cache ReservationCache, reservation UploadReservation, ttl time.Duration) error {
	if cache == nil || reservation.ID == "" || reservation.UserID == 0 || reservation.Size <= 0 || ttl <= 0 {
		return fmt.Errorf("invalid upload reservation")
	}

	reservation.ExpiresAt = time.Now().Add(ttl)
	if reservation.Status == "" {
		reservation.Status = "reserved"
	}

	b, err := json.Marshal(reservation)

	if err != nil {
		return err
	}

	return cache.Set(ctx, "reservation:"+reservation.ID, b, ttl)
}

func LoadUploadReservation(ctx context.Context, cache ReservationCache, id string) (UploadReservation, error) {
	if cache == nil || id == "" {
		return UploadReservation{}, fmt.Errorf("invalid reservation")
	}

	b, err := cache.Get(ctx, "reservation:"+id)
	if err != nil {
		return UploadReservation{}, err
	}

	var r UploadReservation
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}

	return r, nil
}

func DeleteUploadReservation(ctx context.Context, cache ReservationCache, id string) error {
	if cache == nil || id == "" {
		return fmt.Errorf("invalid reservation")
	}

	return cache.Del(ctx, "reservation:"+id)
}
