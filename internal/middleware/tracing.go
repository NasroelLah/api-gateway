package middleware

import (
	"net/http"

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

			// Create span name from HTTP method and path
			spanName := r.Method + " " + r.URL.Path

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
				semconv.URLFull(r.URL.String()),
				semconv.URLPath(r.URL.Path),
				semconv.URLScheme(r.URL.Scheme),
				semconv.ClientAddress(clientIP),
				semconv.UserAgentOriginal(r.UserAgent()),
			)

			// Add request ID if available
			if requestID := GetRequestID(r.Context()); requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}

			// Wrap response writer to capture status code
			wrapped := &statusResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			// Update request with new context containing span
			r = r.WithContext(ctx)

			// Call next handler
			next.ServeHTTP(wrapped, r)

			// Add response attributes after handler completes
			span.SetAttributes(
				semconv.HTTPResponseStatusCode(wrapped.statusCode),
			)

			// Set span status based on HTTP status code
			if wrapped.statusCode >= 400 {
				span.SetStatus(codes.Error, http.StatusText(wrapped.statusCode))
			} else {
				span.SetStatus(codes.Ok, "")
			}
		})
	}
}

// statusResponseWriter wraps http.ResponseWriter to capture status code
type statusResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (w *statusResponseWriter) WriteHeader(code int) {
	if !w.written {
		w.statusCode = code
		w.written = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusResponseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// getClientIPForTracing extracts the client IP from the request for tracing
func getClientIPForTracing(r *http.Request) string {
	// Check X-Forwarded-For header first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr (use extractClientIP from sanitizer.go)
	return extractClientIP(r.RemoteAddr)
}

// InjectTracingHeaders injects trace context into outgoing request headers
// Used by reverse proxy to propagate trace context to upstream
// Requirements: 6.3
func InjectTracingHeaders(r *http.Request) {
	observability.InjectTraceContext(r.Context(), r)
}
