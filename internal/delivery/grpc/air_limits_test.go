package grpc

import "testing"

func TestDeviceSemaphore(t *testing.T) {
	s := newDeviceSemaphore(2)
	if !s.acquire("d1") || !s.acquire("d1") {
		t.Fatal("expected two acquisitions")
	}
	if s.acquire("d1") {
		t.Fatal("limit must be enforced")
	}
	if !s.acquire("d2") {
		t.Fatal("other device must not be limited")
	}
	s.release("d1")
	if !s.acquire("d1") {
		t.Fatal("release must free a slot")
	}
	s.release("d1")
	s.release("d1")
	if s.counts["d1"] != 0 {
		t.Fatalf("counts[d1] = %d, want 0", s.counts["d1"])
	}
}

func TestEnvLimitFallback(t *testing.T) {
	t.Setenv("AIR_TEST_LIMIT", "")
	if got := envLimit("AIR_TEST_LIMIT", 3); got != 3 {
		t.Fatalf("envLimit empty = %d, want 3", got)
	}
	t.Setenv("AIR_TEST_LIMIT", "5")
	if got := envLimit("AIR_TEST_LIMIT", 3); got != 5 {
		t.Fatalf("envLimit = %d, want 5", got)
	}
	t.Setenv("AIR_TEST_LIMIT", "-1")
	if got := envLimit("AIR_TEST_LIMIT", 3); got != 3 {
		t.Fatalf("envLimit negative = %d, want 3", got)
	}
}
