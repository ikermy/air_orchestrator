//go:build integration

package mysql

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/ikermy/air-common/v2/pkg/dbrouter"
)

// TestShardRoutingLocal proves that user-scoped comdb methods use the shard
// connection when a resolver is configured. Run with RUN_SHARD_ROUTING=true.
func TestShardRoutingLocal(t *testing.T) {
	if os.Getenv("RUN_SHARD_ROUTING") != "true" {
		t.Skip("set RUN_SHARD_ROUTING=true to exercise shard routing")
	}

	ctx := context.Background()
	repo, err := New(ctx)
	if err != nil {
		t.Skipf("database is not configured: %v", err)
	}
	defer repo.Conn().Close()

	dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true",
		os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD"), os.Getenv("DB_HOST"), os.Getenv("DB_NAME"))
	t.Setenv("DB_SHARD0_DSN", dsn)

	cfg := dbrouter.Config{
		Generation: 1,
		Slots:      []dbrouter.SlotRange{{From: 0, To: 1023, Shard: "shard-0"}},
		Shards:     map[string]dbrouter.ShardConfig{"shard-0": {DSNEnv: "DB_SHARD0_DSN"}},
		Reference:  "shard-0",
	}
	router, err := dbrouter.New(cfg)
	if err != nil {
		t.Fatalf("dbrouter.New: %v", err)
	}
	defer router.Close()

	if err := router.Ping(ctx); err != nil {
		t.Fatalf("router.Ping: %v", err)
	}
	repo.SetShardResolver(router)

	if _, err := repo.GetNotificationChannel(23); err != nil {
		t.Fatalf("GetNotificationChannel via shard: %v", err)
	}
	if _, err := repo.GetUserSubscriptionLimites(23); err != nil {
		t.Logf("GetUserSubscriptionLimites (no subscription): %v", err)
	}

	metrics := router.Metrics()
	if metrics.Requests == 0 {
		t.Fatal("shard router was not used")
	}
	if metrics.Errors != 0 {
		t.Fatalf("shard router errors: %d", metrics.Errors)
	}
}
