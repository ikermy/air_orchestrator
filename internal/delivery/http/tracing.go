package web

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const httpTracerName = "air_orchestrator/http"

// TracingMiddleware creates a server span for every HTTP request, extracts
// W3C trace context from incoming headers and stores the span in the request
// context so nested operations (MCP, DB, logs) become children.
func TracingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/metrics", "/healthz", "/readyz":
			c.Next()
			return
		}

		parent := otel.GetTextMapPropagator().Extract(
			c.Request.Context(),
			propagation.HeaderCarrier(c.Request.Header),
		)

		ctx, span := otel.Tracer(httpTracerName).Start(parent,
			c.Request.Method,
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		c.Request = c.Request.WithContext(ctx)
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		span.SetName(c.Request.Method + " " + route)
		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", route),
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if status := c.Writer.Status(); status >= 500 {
			span.SetStatus(codes.Error, "server error")
		}
	}
}
