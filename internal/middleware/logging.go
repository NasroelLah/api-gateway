package middleware

import (
	"net/http"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
)

func newResponseWriter(w http.ResponseWriter, protoMajor int) chimiddleware.WrapResponseWriter {
	return chimiddleware.NewWrapResponseWriter(w, protoMajor)
}

func responseStatus(w chimiddleware.WrapResponseWriter) int {
	if status := w.Status(); status != 0 {
		return status
	}
	return http.StatusOK
}


// LoggingMiddleware creates a middleware that logs each request with structured fields
// Fields logged: timestamp, request_id, method, path, status_code, latency, client_ip
// Requirements: 4.2, 4.4
func LoggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			wrapped := newResponseWriter(w, r.ProtoMajor)

			// Get request ID from context (set by RequestIDMiddleware)
			requestID := GetRequestID(r.Context())

			// Get client IP
			clientIP := getClientIPFromRequest(r)

			// Process request
			next.ServeHTTP(wrapped, r)

			// Calculate latency
			latency := time.Since(start)

			// Log the request with all required fields
			// timestamp is automatically added by zap
			logger.Info("request completed",
				zap.String("request_id", requestID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status_code", responseStatus(wrapped)),
				zap.Duration("latency", latency),
				zap.String("client_ip", clientIP),
			)
		})
	}
}

// LoggingMiddlewareWithErrors creates a middleware that logs requests and errors
// Errors are logged with ERROR level and include stack trace when available
// Requirements: 4.2, 4.4
func LoggingMiddlewareWithErrors(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := newResponseWriter(w, r.ProtoMajor)
			requestID := GetRequestID(r.Context())
			clientIP := getClientIPFromRequest(r)

			// Process request
			next.ServeHTTP(wrapped, r)

			latency := time.Since(start)
			statusCode := responseStatus(wrapped)

			// Common fields for all log entries
			fields := []zap.Field{
				zap.String("request_id", requestID),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status_code", statusCode),
				zap.Duration("latency", latency),
				zap.String("client_ip", clientIP),
			}

			// Log with appropriate level based on status code
			if statusCode >= 500 {
				// Server errors - log with ERROR level
				logger.Error("request failed", fields...)
			} else if statusCode >= 400 {
				// Client errors - log with WARN level
				logger.Warn("request error", fields...)
			} else {
				// Success - log with INFO level
				logger.Info("request completed", fields...)
			}
		})
	}
}

// getClientIPFromRequest extracts the client IP from the request
func getClientIPFromRequest(r *http.Request) string {
	if ip := GetSanitizedClientIP(r); ip != "" {
		return ip
	}
	return "unknown"
}
