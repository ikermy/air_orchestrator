package storage

import (
	"context"
	"io"
	"testing"
	"time"
)

type stubStorage struct{}

func (stubStorage) PutObject(context.Context, string, io.Reader, int64, PutOptions) (ObjectInfo, error) {
	return ObjectInfo{}, nil
}
func (stubStorage) GetObject(context.Context, string) (io.ReadCloser, ObjectInfo, error) {
	return nil, ObjectInfo{}, nil
}
func (stubStorage) DeleteObject(context.Context, string) error { return nil }
func (stubStorage) ListObjects(context.Context, string, ListOptions) (ListResult, error) {
	return ListResult{}, nil
}
func (stubStorage) StatObject(context.Context, string) (ObjectInfo, error) {
	return ObjectInfo{}, nil
}
func (stubStorage) PresignedGetURL(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func TestStorageFactoryResolve(t *testing.T) {
	backend := stubStorage{}
	factory := NewStorageFactory(backend)

	resolved, err := factory.Resolve(context.Background(), 42)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved != backend {
		t.Fatalf("Resolve() returned %T, want configured backend", resolved)
	}
}

func TestStorageFactoryRejectsMissingBackend(t *testing.T) {
	_, err := NewStorageFactory(nil).Resolve(context.Background(), 42)
	if err == nil {
		t.Fatal("Resolve() error = nil, want missing backend error")
	}
}

func TestNilStorageFactory(t *testing.T) {
	var factory *StorageFactory
	if _, err := factory.Resolve(context.Background(), 42); err == nil {
		t.Fatal("Resolve() error = nil, want nil factory error")
	}
}
