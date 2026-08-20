package storageusecase

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	infra "air_orchestrator/internal/infrastructure/storage"
)

func TestValidateNameRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", `a\\b`, "a\x00b", "a\n"} {
		if err := validateName(name); err == nil {
			t.Errorf("validateName(%q) accepted unsafe name", name)
		}
	}
}

func TestValidateUserKey(t *testing.T) {
	for _, key := range []string{"users/42/file.txt", "users/420/file.txt", "users/42/../file.txt"} {
		if err := validateUserKey(42, key); (key == "users/42/file.txt" && err != nil) || (key != "users/42/file.txt" && err == nil) {
			t.Errorf("validateUserKey(42, %q) error = %v", key, err)
		}
	}
}

type memoryStorage struct{ objects map[string][]byte }

func (m *memoryStorage) PutObject(_ context.Context, key string, r io.Reader, _ int64, _ infra.PutOptions) (infra.ObjectInfo, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return infra.ObjectInfo{}, err
	}
	m.objects[key] = b
	return infra.ObjectInfo{Key: key, Size: int64(len(b)), LastModified: time.Now()}, nil
}
func (m *memoryStorage) GetObject(context.Context, string) (io.ReadCloser, infra.ObjectInfo, error) {
	return nil, infra.ObjectInfo{}, nil
}
func (m *memoryStorage) DeleteObject(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}
func (m *memoryStorage) ListObjects(_ context.Context, prefix string, _ infra.ListOptions) (infra.ListResult, error) {
	result := infra.ListResult{}
	for key, data := range m.objects {
		if strings.HasPrefix(key, prefix) {
			result.Objects = append(result.Objects, infra.ObjectInfo{Key: key, Size: int64(len(data)), LastModified: time.Now()})
		}
	}
	return result, nil
}
func (m *memoryStorage) StatObject(context.Context, string) (infra.ObjectInfo, error) {
	return infra.ObjectInfo{}, nil
}
func (m *memoryStorage) PresignedGetURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.test/" + key, nil
}

type memoryResolver struct{ storage infra.Storage }

func (r memoryResolver) Resolve(context.Context, uint32) (infra.Storage, error) {
	return r.storage, nil
}

func TestServiceFileLifecycle(t *testing.T) {
	backend := &memoryStorage{objects: map[string][]byte{}}
	service := NewService(memoryResolver{storage: backend})
	ctx := context.Background()
	file, err := service.CreateTextFile(ctx, 42, "note.txt", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(file.Key, "users/42/") || file.URL == "" {
		t.Fatalf("unexpected file: %+v", file)
	}
	files, err := service.ListFiles(ctx, 42)
	if err != nil || len(files) != 1 || files[0].FileName != "note.txt" {
		t.Fatalf("list = %+v, err=%v", files, err)
	}
	if err := service.DeleteFile(ctx, 42, file.Key); err != nil {
		t.Fatal(err)
	}
	if len(backend.objects) != 0 {
		t.Fatalf("objects remain: %v", backend.objects)
	}
}

func TestServiceRejectsForeignKey(t *testing.T) {
	service := NewService(memoryResolver{storage: &memoryStorage{objects: map[string][]byte{}}})
	if err := service.DeleteFile(context.Background(), 42, "users/43/file.txt"); err == nil {
		t.Fatal("foreign key accepted")
	}
}
