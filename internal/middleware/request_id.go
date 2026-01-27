package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// requestIDKey is the context key for storing request ID
type requestIDKey struct{}

// RequestIDHeader is the header name for request ID
const RequestIDHeader = "X-Request-ID"

// RequestIDMiddleware generates a UUID for each request, sets X-Request-ID header,
// and stores it in context for propagation.
// Requirements: 1.3
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if request already has a request ID (from upstream)
		requestID := r.Header.Get(RequestIDHeader)
		if requestID == "" {
			// Generate new UUID
			requestID = uuid.New().String()
		}

		// Set request ID in response header
		w.Header().Set(RequestIDHeader, requestID)

		// Store request ID in context for propagation
		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)

		// Continue with updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetRequestID retrieves the request ID from context
// Returns empty string if not found
func GetRequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// SetRequestID sets a request ID in the context (useful for testing)
func SetRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}
