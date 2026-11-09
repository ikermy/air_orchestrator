package mcp

import (
	"fmt"
	"time"

	"air_orchestrator/internal/config"

	"golang.org/x/time/rate"
)

type toolLimiterEntry struct {
	limiter *rate.Limiter
	seen    time.Time
}

// allowTool applies a per-user, per-tool token bucket.
func (h *Handler) allowTool(userID uint64, tool string) bool {
	key := fmt.Sprintf("%d:%s", userID, tool)
	now := time.Now()

	h.limiterMu.Lock()
	defer h.limiterMu.Unlock()

	if h.limiters == nil {
		h.limiters = make(map[string]*toolLimiterEntry)
	}
	entry, ok := h.limiters[key]
	if !ok {
		entry = &toolLimiterEntry{
			limiter: rate.NewLimiter(rate.Limit(config.MCPToolRateLimit), config.MCPToolRateBurst),
		}
		h.limiters[key] = entry
	}
	entry.seen = now

	// Ленивая очистка, чтобы карта не росла бесконечно.
	if len(h.limiters) > 1024 {
		for k, v := range h.limiters {
			if now.Sub(v.seen) > config.MCPToolLimiterTTL {
				delete(h.limiters, k)
			}
		}
	}

	return entry.limiter.Allow()
}
