package storageusecase

import (
	infra "air_orchestrator/internal/infrastructure/storage"
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

type migrationBackend struct {
	objects map[string][]byte
	failPut bool
}

func (m *migrationBackend) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ infra.PutOptions) (infra.ObjectInfo, error) {
	if m.failPut {
		return infra.ObjectInfo{}, io.ErrClosedPipe
	}
	b, _ := io.ReadAll(r)
	m.objects[key] = b
	return infra.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}
func (m *migrationBackend) GetObject(_ context.Context, key string) (io.ReadCloser, infra.ObjectInfo, error) {
	b, ok := m.objects[key]
	if !ok {
		return nil, infra.ObjectInfo{}, io.EOF
	}
	return io.NopCloser(bytes.NewReader(b)), infra.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}
func (m *migrationBackend) DeleteObject(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}
func (m *migrationBackend) ListObjects(_ context.Context, prefix string, _ infra.ListOptions) (infra.ListResult, error) {
	r := infra.ListResult{}
	for k, b := range m.objects {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			r.Objects = append(r.Objects, infra.ObjectInfo{Key: k, Size: int64(len(b))})
		}
	}
	return r, nil
}
func (m *migrationBackend) StatObject(_ context.Context, key string) (infra.ObjectInfo, error) {
	b, ok := m.objects[key]
	if !ok {
		return infra.ObjectInfo{}, io.EOF
	}
	return infra.ObjectInfo{Key: key, Size: int64(len(b))}, nil
}
func (m *migrationBackend) PresignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

type migrationStore struct{ record MigrationRecord }

func (s *migrationStore) CreateMigration(context.Context, uint32, infra.BackendType, infra.BackendType) (MigrationRecord, error) {
	return s.record, nil
}
func (s *migrationStore) UpdateMigration(_ context.Context, r MigrationRecord) error {
	s.record = r
	return nil
}
func (s *migrationStore) GetMigration(context.Context, uint64) (MigrationRecord, error) {
	return s.record, nil
}
func (s *migrationStore) ListPendingMigrations(context.Context, int) ([]MigrationRecord, error) {
	return nil, nil
}

func TestMigrationCopiesVerifiesAndCleansSource(t *testing.T) {
	src := &migrationBackend{objects: map[string][]byte{"users/7/a": []byte("one"), "users/7/b": []byte("two")}}
	dst := &migrationBackend{objects: map[string][]byte{}}
	store := &migrationStore{record: MigrationRecord{ID: 1, UserID: 7}}
	if err := NewMigrationService(store, nil).Run(context.Background(), 1, 7, src, dst); err != nil {
		t.Fatal(err)
	}
	if len(src.objects) != 0 || len(dst.objects) != 2 || store.record.State != MigrationCompleted {
		t.Fatalf("source=%v target=%v record=%+v", src.objects, dst.objects, store.record)
	}
}

func TestMigrationDoesNotCleanSourceOnCopyFailure(t *testing.T) {
	src := &migrationBackend{objects: map[string][]byte{"users/7/a": []byte("one")}}
	dst := &migrationBackend{objects: map[string][]byte{}, failPut: true}
	store := &migrationStore{record: MigrationRecord{ID: 2, UserID: 7}}
	if err := NewMigrationService(store, nil).Run(context.Background(), 2, 7, src, dst); err == nil {
		t.Fatal("expected copy failure")
	}
	if len(src.objects) != 1 || store.record.State != MigrationFailed {
		t.Fatalf("source=%v record=%+v", src.objects, store.record)
	}
}

func TestMigrationResumesUsingVerifiedKeys(t *testing.T) {
	src := &migrationBackend{objects: map[string][]byte{"users/7/a": []byte("one"), "users/7/b": []byte("two")}}
	dst := &migrationBackend{objects: map[string][]byte{"users/7/a": []byte("one")}}
	store := &migrationStore{record: MigrationRecord{ID: 3, UserID: 7, State: MigrationFailed, Verified: 1, VerifiedKeys: []string{"users/7/a"}}}
	if err := NewMigrationService(store, nil).Run(context.Background(), 3, 7, src, dst); err != nil {
		t.Fatal(err)
	}
	if store.record.Verified != 2 || len(dst.objects) != 2 {
		t.Fatalf("record=%+v target=%v", store.record, dst.objects)
	}
}
