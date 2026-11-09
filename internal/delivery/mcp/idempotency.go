package mcp

import (
	"encoding/json"
	"strings"
	"time"

	"air_orchestrator/internal/config"
)

type idempotencyEntry struct {
	result  toolResult
	expires time.Time
}

// extractIdempotencyKey returns the optional "idempotency_key" argument.
func extractIdempotencyKey(args json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	raw, ok := m["idempotency_key"]
	if !ok {
		return ""
	}
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		return ""
	}
	key = strings.TrimSpace(key)
	if len(key) > 128 {
		return ""
	}
	return key
}

func (h *Handler) getIdempotent(key string) (toolResult, bool) {
	now := time.Now()

	h.idemMu.Lock()
	defer h.idemMu.Unlock()

	entry, ok := h.idem[key]
	if !ok {
		return toolResult{}, false
	}
	if now.After(entry.expires) {
		delete(h.idem, key)
		return toolResult{}, false
	}
	return entry.result, true
}

func (h *Handler) storeIdempotent(key string, result toolResult) {
	now := time.Now()

	h.idemMu.Lock()
	defer h.idemMu.Unlock()

	if h.idem == nil {
		h.idem = make(map[string]idempotencyEntry)
	}
	h.idem[key] = idempotencyEntry{result: result, expires: now.Add(config.MCPIdempotencyTTL)}

	if len(h.idem) > 1024 {
		for k, v := range h.idem {
			if now.After(v.expires) {
				delete(h.idem, k)
			}
		}
	}
}
