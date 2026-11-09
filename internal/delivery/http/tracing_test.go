package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTracingMiddlewareCreatesServerSpan(t *testing.T) {
	prev := otel.GetTracerProvider()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	prevProp := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTracerProvider(prev)
	defer otel.SetTextMapPropagator(prevProp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(TracingMiddleware())
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans=%d, want 1", len(spans))
	}
	if spans[0].Name != "GET /ping" {
		t.Fatalf("span name=%q, want GET /ping", spans[0].Name)
	}
	if got := spans[0].Parent.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("parent trace id=%s", got)
	}
}
