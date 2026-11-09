package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newReadyContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	return c, rec
}

func TestHealthz(t *testing.T) {
	w := &Web{}
	c, rec := newReadyContext(t)
	w.Healthz(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

func TestReadyzWithoutChecks(t *testing.T) {
	w := &Web{}
	c, rec := newReadyContext(t)
	w.Readyz(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestReadyzWithPassingCheck(t *testing.T) {
	w := &Web{}
	w.AddReadinessCheck(func(context.Context) error { return nil })
	c, rec := newReadyContext(t)
	w.Readyz(c)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
}

func TestReadyzWithFailingCheck(t *testing.T) {
	w := &Web{}
	w.AddReadinessCheck(func(context.Context) error { return errors.New("shard down") })
	c, rec := newReadyContext(t)
	w.Readyz(c)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503, body=%s", rec.Code, rec.Body.String())
	}
}
