package storageusecase

import (
	"context"
	"sync"
	"testing"
	"time"

	infra "air_orchestrator/internal/infrastructure/storage"
)

type mockCache struct {
	mu   sync.Mutex
	data map[string][]byte
	ttl  map[string]time.Time
	ping bool
}

func newMockCache() *mockCache {
	return &mockCache{data: make(map[string][]byte), ttl: make(map[string]time.Time), ping: true}
}

func (m *mockCache) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	m.ttl[key] = time.Now().Add(ttl)
	return nil
}

func (m *mockCache) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, infra.ErrReservationNotFound
	}
	return v, nil
}

func (m *mockCache) Del(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	delete(m.ttl, key)
	return nil
}

func (m *mockCache) Ping(_ context.Context) error {
	if m.ping {
		return nil
	}
	return infra.ErrCacheUnavailable
}

func (m *mockCache) Keys(_ context.Context, _ string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

type mockQuota struct {
	mu         sync.Mutex
	reserved   map[uint32]int64
	used       map[uint32]int64
	quotaLimit map[uint32]uint64
	err        error
}

func newMockQuota(limit uint64) *mockQuota {
	return &mockQuota{
		reserved:   make(map[uint32]int64),
		used:       make(map[uint32]int64),
		quotaLimit: map[uint32]uint64{1: limit},
	}
}

func (q *mockQuota) ReserveStorage(_ context.Context, userID uint32, size int64) error {
	if q.err != nil {
		return q.err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.reserved[userID]+size > int64(q.quotaLimit[userID]) {
		return infra.ErrQuotaExceeded
	}
	q.reserved[userID] += size
	return nil
}

func (q *mockQuota) CommitStorage(_ context.Context, userID uint32, size int64) error {
	if q.err != nil {
		return q.err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.reserved[userID] < size {
		return infra.ErrReservationNotFound
	}
	q.reserved[userID] -= size
	q.used[userID] += size
	return nil
}

func (q *mockQuota) ReleaseStorage(_ context.Context, userID uint32, size int64) error {
	if q.err != nil {
		return q.err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.reserved[userID] >= size {
		q.reserved[userID] -= size
	} else {
		q.reserved[userID] = 0
	}
	return nil
}

func (q *mockQuota) ListReservedUsers(_ context.Context) ([]uint32, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var users []uint32
	for uid, r := range q.reserved {
		if r > 0 {
			users = append(users, uid)
		}
	}
	return users, nil
}

func TestReservationReserveCommit(t *testing.T) {
	svc := NewReservationService(newMockCache(), nil, newMockQuota(1024*1024))
	if svc == nil {
		t.Fatal("NewReservationService returned nil")
	}
	svc.setHealthy(true)

	id, idempotency, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 15*time.Minute)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if id == "" || idempotency == "" {
		t.Fatal("reservation ID or idempotency key is empty")
	}

	if err := svc.Commit(context.Background(), id); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Commit twice should fail
	if err := svc.Commit(context.Background(), id); err == nil {
		t.Fatal("second Commit should fail")
	}
}

func TestReservationIdempotencyReusesReservation(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	firstID, _, err := svc.ReserveWithIdempotency(context.Background(), 1, "users/1/object", 1024, "upload-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("first reservation failed: %v", err)
	}
	secondID, _, err := svc.ReserveWithIdempotency(context.Background(), 1, "users/1/other", 1024, "upload-1", 15*time.Minute)
	if err != nil {
		t.Fatalf("retry reservation failed: %v", err)
	}
	if firstID != secondID {
		t.Fatalf("idempotent retry created reservation %q instead of reusing %q", secondID, firstID)
	}
	if quota.reserved[1] != 1024 {
		t.Fatalf("idempotent retry charged quota twice: reserved=%d", quota.reserved[1])
	}
}

func TestReservationQuotaExceeded(t *testing.T) {
	svc := NewReservationService(newMockCache(), nil, newMockQuota(512))
	svc.setHealthy(true)

	_, _, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 15*time.Minute)
	if err == nil {
		t.Fatal("expected quota exceeded error")
	}
}

func TestReservationExpired(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	id, _, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Commit on expired reservation releases quota and returns error
	if err := svc.Commit(context.Background(), id); err == nil {
		t.Fatal("expected expired reservation error")
	}

	if quota.reserved[1] != 0 {
		t.Fatalf("expected reserved=0 after expired commit, got %d", quota.reserved[1])
	}
}

func TestReservationRelease(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	id, _, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 15*time.Minute)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}

	if err := svc.Release(context.Background(), id); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	if quota.reserved[1] != 0 {
		t.Fatal("reserved bytes not released")
	}

	// Release again should be no-op
	if err := svc.Release(context.Background(), id); err != nil {
		t.Fatal("second Release should succeed (no-op)")
	}
}

func TestReservationDegraded(t *testing.T) {
	cache := newMockCache()
	cache.ping = false
	svc := NewReservationService(cache, nil, newMockQuota(1024*1024))

	if svc.Healthy() {
		t.Fatal("expected degraded state when Redis is down")
	}

	_, _, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 15*time.Minute)
	if err == nil {
		t.Fatal("expected degraded error")
	}
}

func TestSweepExpired(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	id, _, err := svc.Reserve(context.Background(), 1, "obj-key", 1024, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	_ = id

	time.Sleep(10 * time.Millisecond)

	swept, err := svc.SweepExpired(context.Background())
	if err != nil {
		t.Fatalf("SweepExpired failed: %v", err)
	}
	if swept != 1 {
		t.Fatalf("expected 1 swept, got %d", swept)
	}

	if quota.reserved[1] != 0 {
		t.Fatal("reserved bytes not released by sweep")
	}
}

func TestReconcileFromMySQL(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	// Reserve quota in MySQL but WITHOUT a Redis reservation
	quota.reserved[2] = 2048

	released, err := svc.ReconcileFromMySQL(context.Background())
	if err != nil {
		t.Fatalf("ReconcileFromMySQL failed: %v", err)
	}
	if released != 1 {
		t.Fatalf("expected 1 released, got %d", released)
	}

	if quota.reserved[2] > 2048-1 {
		t.Fatal("MySQL reserved not released by reconcile")
	}
}

func TestConcurrentReserveCommit(t *testing.T) {
	cache := newMockCache()
	quota := newMockQuota(1024 * 1024)
	svc := NewReservationService(cache, nil, quota)
	svc.setHealthy(true)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, _, err := svc.Reserve(context.Background(), 1, "concurrent-key", 100, 15*time.Minute)
			if err != nil {
				errs <- err
				return
			}
			if err := svc.Commit(context.Background(), id); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for e := range errs {
		t.Logf("concurrent error: %v", e)
	}

	totalUsed := quota.used[1]
	if totalUsed > 500 || totalUsed == 0 {
		t.Fatalf("expected used between 1 and 500, got %d", totalUsed)
	}
}
