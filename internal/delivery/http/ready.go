package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// AddReadinessCheck registers an additional readiness probe (for example a
// shard ping). Checks run sequentially with a shared timeout.
func (w *Web) AddReadinessCheck(fn func(context.Context) error) {
	if fn == nil {
		return
	}
	w.readyMu.Lock()
	w.readyChecks = append(w.readyChecks, fn)
	w.readyMu.Unlock()
}

// Healthz reports process liveness.
func (w *Web) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz reports dependency readiness: primary DB and registered checks.
func (w *Web) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	var errs []error
	if w.db != nil {
		if err := w.db.Conn().PingContext(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	w.readyMu.RLock()
	checks := append([]func(context.Context) error(nil), w.readyChecks...)
	w.readyMu.RUnlock()

	for _, check := range checks {
		if err := check(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		logger.WarnCtx(ctx, "readyz: %v", errors.Join(errs...))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"error":  errors.Join(errs...).Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
