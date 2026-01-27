package middleware

import (
	"net/http"
	"time"

	"github.com/nasroellah/api-gateway/internal/observability"
)

// MetricsMiddleware creates a middleware that records request metrics
// Records request count and latency for each request
// Requirements: 5.2, 5.3
func MetricsMiddleware(metrics *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := newResponseWriter(w)

			// Process request
			next.ServeHTTP(wrapped, r)

			// Record metrics
			duration := time.Since(start)
			metrics.RecordRequest(r.Method, r.URL.Path, wrapped.StatusCode(), duration)
		})
	}
}

// MetricsMiddlewareWithUpstreamErrors creates a middleware that records request metrics
// and provides a way to record upstream errors
// Requirements: 5.2, 5.3, 5.4
func MetricsMiddlewareWithUpstreamErrors(metrics *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := newResponseWriter(w)

			// Process request
			next.ServeHTTP(wrapped, r)

			// Record metrics
			duration := time.Since(start)
			statusCode := wrapped.StatusCode()
			metrics.RecordRequest(r.Method, r.URL.Path, statusCode, duration)

			// Record upstream errors based on status code
			if statusCode == http.StatusBadGateway || statusCode == http.StatusGatewayTimeout {
				upstreamHost := r.Header.Get("X-Upstream-Target")
				if upstreamHost == "" {
					upstreamHost = "unknown"
				}
				errorType := getErrorType(statusCode)
				metrics.RecordUpstreamError(upstreamHost, errorType)
			}
		})
	}
}

// getErrorType returns the error type based on status code
func getErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusBadGateway:
		return "unreachable"
	case http.StatusGatewayTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}
