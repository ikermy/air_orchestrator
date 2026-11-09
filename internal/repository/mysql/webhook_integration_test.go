//go:build integration

package mysql

import (
	"context"
	"testing"
)

func TestRegisterWebhookEventLocal(t *testing.T) {
	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	const (
		source  = "test-source"
		eventID = "test-event-1"
	)
	cleanup := func() {
		_, _ = repo.Conn().ExecContext(ctx, "DELETE FROM webhook_dedup WHERE source=? AND event_id=?", source, eventID)
	}
	defer cleanup()
	cleanup()

	isNew, err := repo.RegisterWebhookEvent(ctx, source, eventID)
	if err != nil {
		t.Fatalf("RegisterWebhookEvent: %v", err)
	}
	if !isNew {
		t.Fatal("first registration must be new")
	}

	isNew, err = repo.RegisterWebhookEvent(ctx, source, eventID)
	if err != nil {
		t.Fatalf("RegisterWebhookEvent duplicate: %v", err)
	}
	if isNew {
		t.Fatal("second registration must be a duplicate")
	}
}
