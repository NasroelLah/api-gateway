package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/nasroellah/api-gateway/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingMiddleware menambahkan OpenTelemetry tracing ke setiap request
// Creates/continues span untuk setiap request dan adds span attributes
// Requirements: 6.2, 6.3, 6.4
func TracingMiddleware(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract trace context from incoming headers (continue existing trace)
			// Requirements: 6.2
			ctx := observability.ExtractTraceContext(r.Context(), r)

			spanName := methodLabel(r.Method)
			// Start a new span or continue from extracted context
			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
			)
			defer span.End()

			// Add span attributes
			// Requirements: 6.4
			clientIP := getClientIPForTracing(r)
			span.SetAttributes(
				semconv.HTTPRequestMethodKey.String(r.Method),
				semconv.URLScheme(r.URL.Scheme),
				semconv.ClientAddress(clientIP),
				semconv.UserAgentOriginal(r.UserAgent()),
			)

			// Add request ID if available
			if requestID := GetRequestID(r.Context()); requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}

			// Wrap response writer to capture status code
			wrapped := newResponseWriter(w, r.ProtoMajor)

			// Update request with new context containing span
			r = r.WithContext(ctx)

			// Call next handler
			next.ServeHTTP(wrapped, r)

			if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
				if pattern := routeContext.RoutePattern(); pattern != "" {
					span.SetName(methodLabel(r.Method) + " " + pattern)
				}
			}
			statusCode := responseStatus(wrapped)
			span.SetAttributes(semconv.HTTPResponseStatusCode(statusCode))
			if statusCode >= 400 {
				span.SetStatus(codes.Error, http.StatusText(statusCode))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		})
	}
}


// getClientIPForTracing extracts the client IP from the request for tracing
func getClientIPForTracing(r *http.Request) string {
	if ip := GetSanitizedClientIP(r); ip != "" {
		return ip
	}
	return "unknown"
}

// InjectTracingHeaders injects trace context into outgoing request headers
// Used by reverse proxy to propagate trace context to upstream
// Requirements: 6.3
func InjectTracingHeaders(r *http.Request) {
	observability.InjectTraceContext(r.Context(), r)
}
