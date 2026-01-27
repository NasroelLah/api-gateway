package middleware

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// newResponseWriter creates a new responseWriter
func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		written:        false,
	}
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

// Write captures that a write occurred
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// StatusCode returns the captured status code
func (rw *responseWriter) StatusCode() int {
	return rw.statusCode
}

// LoggingMiddleware creates a middleware that logs each request with structured fields
// Fields logged: timestamp, request_id, method, path, status_code, latency, client_ip
// Requirements: 4.2, 4.4
func LoggingMiddleware(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := newResponseWriter(w)

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
				zap.Int("status_code", wrapped.StatusCode()),
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
			wrapped := newResponseWriter(w)
			requestID := GetRequestID(r.Context())
			clientIP := getClientIPFromRequest(r)

			// Process request
			next.ServeHTTP(wrapped, r)

			latency := time.Since(start)
			statusCode := wrapped.StatusCode()

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
	// Check X-Real-IP first (set by sanitizer middleware)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Check X-Forwarded-For
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}
