package web

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMTLSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(t *testing.T, tlsState *tls.ConnectionState) int {
		t.Helper()
		r := gin.New()
		r.Use((&Web{}).mtlsMiddleware())
		r.GET("/int/mcp", func(c *gin.Context) { c.Status(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/int/mcp", nil)
		req.TLS = tlsState
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	t.Run("disabled", func(t *testing.T) {
		t.Setenv("MTLS_REQUIRED", "false")
		if got := run(t, nil); got != http.StatusOK {
			t.Fatalf("status = %d, want 200", got)
		}
	})

	t.Run("required without tls", func(t *testing.T) {
		t.Setenv("MTLS_REQUIRED", "true")
		if got := run(t, nil); got != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", got)
		}
	})

	t.Run("required without verified chain", func(t *testing.T) {
		t.Setenv("MTLS_REQUIRED", "true")
		if got := run(t, &tls.ConnectionState{}); got != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", got)
		}
	})

	t.Run("required with verified chain", func(t *testing.T) {
		t.Setenv("MTLS_REQUIRED", "true")
		state := &tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{{}}},
		}
		if got := run(t, state); got != http.StatusOK {
			t.Fatalf("status = %d, want 200", got)
		}
	})
}
