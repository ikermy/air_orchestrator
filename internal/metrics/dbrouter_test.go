package metrics

import (
	"testing"

	"github.com/ikermy/air-common/v2/pkg/dbrouter"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDBRouterCollector(t *testing.T) {
	cfg := dbrouter.Config{
		Generation: 7,
		Slots:      []dbrouter.SlotRange{{From: 0, To: 1023, Shard: "shard-0"}},
		Shards:     map[string]dbrouter.ShardConfig{"shard-0": {DSNEnv: "DB_SHARD0_DSN"}},
	}
	router, err := dbrouter.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	router.ShardForUser(1)
	router.ShardForUser(2)

	collector := &dbRouterCollector{router: router}
	if n := testutil.CollectAndCount(collector, "air_common_db_router_requests_total"); n == 0 {
		t.Fatal("requests metric was not collected")
	}
	if n := testutil.CollectAndCount(collector, "air_common_db_router_generation"); n == 0 {
		t.Fatal("generation metric was not collected")
	}
}
