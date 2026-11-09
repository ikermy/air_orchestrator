package app

import (
	"air_orchestrator/internal/config"
	appmetrics "air_orchestrator/internal/metrics"
	"context"
	"time"

	"github.com/ikermy/air-common/v2/pkg/dbrouter"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// initShardRouter loads DB_SHARDS_CONFIG when present. Without a config the
// service keeps working in single-DB mode.
func initShardRouter(ctx context.Context) *dbrouter.Router {
	cfg, err := dbrouter.LoadConfigFromEnv()
	if err != nil {
		logger.Debug("DB router: конфиг шардов не задан — single-DB режим")
		return nil
	}

	router, err := dbrouter.New(cfg)
	if err != nil {
		if config.ShardStrict() {
			logger.Fatal("DB router: неверный конфиг шардов: %v", err)
		}
		logger.Error("DB router: неверный конфиг шардов: %v", err)
		return nil
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := router.Ping(pingCtx); err != nil {
		if config.ShardStrict() {
			logger.Fatal("DB router: ping шардов не удался: %v", err)
		}
		logger.Warn("DB router: ping шардов не удался: %v", err)
	} else {
		logger.Info("DB router: generation=%d, шарды=%v", router.Generation(), router.ShardIDs())
	}

	appmetrics.RegisterDBRouter(router)
	return router
}
