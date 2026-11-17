package middleware

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/nasroellah/api-gateway/internal/observability"
)

func routeLabel(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return "unmatched"
}

func methodLabel(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return method
	default:
		return "other"
	}
}

// MetricsMiddleware creates a middleware that records request metrics
// Records request count and latency for each request
// Requirements: 5.2, 5.3
func MetricsMiddleware(metrics *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			wrapped := newResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			metrics.RecordRequest(methodLabel(r.Method), routeLabel(r), responseStatus(wrapped), time.Since(start))
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

			wrapped := newResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			statusCode := responseStatus(wrapped)
			metrics.RecordRequest(methodLabel(r.Method), routeLabel(r), statusCode, time.Since(start))
			if statusCode == http.StatusBadGateway || statusCode == http.StatusGatewayTimeout {
				metrics.RecordUpstreamError("unknown", getErrorType(statusCode))
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
