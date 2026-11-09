package config

import (
	"testing"
	"time"
)

func TestUserMigrationDeadline(t *testing.T) {
	t.Setenv("USER_MIGRATION_DEADLINE", "2026-12-31T23:59:59Z")
	got := UserMigrationDeadline()
	want := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("deadline = %v, want %v", got, want)
	}

	t.Setenv("USER_MIGRATION_DEADLINE", "not-a-date")
	got = UserMigrationDeadline()
	def, _ := time.Parse(time.RFC3339, DefaultUserMigrationDeadline)
	if !got.Equal(def) {
		t.Fatalf("invalid env should fall back to default %v, got %v", def, got)
	}

	t.Setenv("USER_MIGRATION_DEADLINE", "")
	if got = UserMigrationDeadline(); !got.Equal(def) {
		t.Fatalf("empty env should use default %v, got %v", def, got)
	}
}

func TestUserMigrationFinalize(t *testing.T) {
	t.Setenv("USER_MIGRATION_FINALIZE", "")
	if !UserMigrationFinalize() {
		t.Fatal("finalize must be enabled by default")
	}
	t.Setenv("USER_MIGRATION_FINALIZE", "false")
	if UserMigrationFinalize() {
		t.Fatal("finalize must be disabled by false")
	}
	t.Setenv("USER_MIGRATION_FINALIZE", "true")
	if !UserMigrationFinalize() {
		t.Fatal("finalize must be enabled by true")
	}
}

func TestUserMigrationCheckInterval(t *testing.T) {
	t.Setenv("USER_MIGRATION_CHECK_INTERVAL", "5m")
	if got := UserMigrationCheckInterval(); got != 5*time.Minute {
		t.Fatalf("interval = %v, want 5m", got)
	}
	t.Setenv("USER_MIGRATION_CHECK_INTERVAL", "-1m")
	if got := UserMigrationCheckInterval(); got != time.Hour {
		t.Fatalf("invalid interval = %v, want 1h", got)
	}
}
