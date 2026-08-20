package storage

import (
	"context"
	"testing"
	"time"
)

type cacheMock struct {
	data map[string][]byte
}

func (m *cacheMock) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.data[key] = value
	return nil
}
func (m *cacheMock) Get(_ context.Context, key string) ([]byte, error) {
	v, ok := m.data[key]
	if !ok {
		return nil, ErrReservationNotFound
	}
	return v, nil
}
func (m *cacheMock) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}
func (m *cacheMock) Ping(_ context.Context) error { return nil }
func (m *cacheMock) Keys(_ context.Context, _ string) ([]string, error) {
	var keys []string
	for k := range m.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func TestCachedBackendConfig(t *testing.T) {
	cache := &cacheMock{data: make(map[string][]byte)}
	cfg := BackendConfig{UserID: 1, Type: BackendExternal, Endpoint: "https://s3.example.com", Bucket: "test", Region: "us-east-1"}

	if err := CachedBackendConfig(context.Background(), cache, 1, cfg); err != nil {
		t.Fatal(err)
	}

	entry, ok := LoadCachedConfig(context.Background(), cache, 1)
	if !ok {
		t.Fatal("config not found in cache")
	}
	if entry.Type != BackendExternal || entry.Endpoint != "https://s3.example.com" {
		t.Fatalf("wrong cached config: %+v", entry)
	}
}

func TestConnectionCheckCache(t *testing.T) {
	cache := &cacheMock{data: make(map[string][]byte)}

	if err := CacheConnectionCheck(context.Background(), cache, "https://s3.example.com", true); err != nil {
		t.Fatal(err)
	}

	ok, cached := LoadConnectionCheck(context.Background(), cache, "https://s3.example.com")
	if !cached || !ok {
		t.Fatal("connection check not cached correctly")
	}

	_, notCached := LoadConnectionCheck(context.Background(), cache, "https://other.example.com")
	if notCached {
		t.Fatal("unexpected cache hit")
	}
}

func TestSessionMetaCache(t *testing.T) {
	cache := &cacheMock{data: make(map[string][]byte)}
	meta := SessionMeta{UserID: 1, Prefix: "users/1/", Bucket: "test", ExpiresAt: time.Now().Add(time.Hour)}

	if err := SaveSessionMeta(context.Background(), cache, "session-1", meta, time.Hour); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSessionMeta(context.Background(), cache, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserID != 1 || loaded.Prefix != "users/1/" {
		t.Fatalf("wrong session meta: %+v", loaded)
	}
}

func TestQuotaReservedCache(t *testing.T) {
	cache := &cacheMock{data: make(map[string][]byte)}

	if err := SetQuotaReserved(context.Background(), cache, 1, 1024); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.data["quota:1:reserved"]; !ok {
		t.Fatal("quota reserved key not set")
	}

	if err := DelQuotaReserved(context.Background(), cache, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.data["quota:1:reserved"]; ok {
		t.Fatal("quota reserved key not deleted")
	}
}
