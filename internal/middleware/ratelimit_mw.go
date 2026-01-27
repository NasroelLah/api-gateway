package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// RateLimitErrorResponse represents a rate limit error response
type RateLimitErrorResponse struct {
	Error      string `json:"error"`
	RetryAfter int    `json:"retry_after,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

// RateLimitByIPMiddleware creates middleware that applies rate limiting per client IP.
// Returns 429 Too Many Requests with Retry-After header if rate limit exceeded.
// Requirements: 3.1, 3.2, 3.3, 3.6
func RateLimitByIPMiddleware(limiter RateLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract client IP for rate limiting key
			clientIP := extractRateLimitKey(r)

			// Check rate limit
			allowed, retryAfter := limiter.Allow(clientIP)

			if !allowed {
				// Set Retry-After header (RFC 7231)
				retryAfterSecs := formatRetryAfter(retryAfter)
				w.Header().Set("Retry-After", retryAfterSecs)
				w.Header().Set("X-RateLimit-Retry-After", retryAfterSecs)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)

				// Parse retry after as int for JSON response
				var retryAfterInt int
				if retryAfter.Seconds() < 1 {
					retryAfterInt = 1
				} else {
					retryAfterInt = int(retryAfter.Seconds())
				}

				resp := RateLimitErrorResponse{
					Error:      "rate limit exceeded",
					RetryAfter: retryAfterInt,
					RequestID:  GetRequestID(r.Context()),
				}
				json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitByIPWithBypassMiddleware creates rate limiting middleware with path bypass
func RateLimitByIPWithBypassMiddleware(limiter RateLimiter, bypassPaths []string) func(http.Handler) http.Handler {
	// Create a map for O(1) lookup
	bypassMap := make(map[string]bool, len(bypassPaths))
	for _, path := range bypassPaths {
		bypassMap[path] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if path should bypass rate limiting
			if bypassMap[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Extract client IP for rate limiting key
			clientIP := extractRateLimitKey(r)

			// Check rate limit
			allowed, retryAfter := limiter.Allow(clientIP)

			if !allowed {
				retryAfterSecs := formatRetryAfter(retryAfter)
				w.Header().Set("Retry-After", retryAfterSecs)
				w.Header().Set("X-RateLimit-Retry-After", retryAfterSecs)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)

				var retryAfterInt int
				if retryAfter.Seconds() < 1 {
					retryAfterInt = 1
				} else {
					retryAfterInt = int(retryAfter.Seconds())
				}

				resp := RateLimitErrorResponse{
					Error:      "rate limit exceeded",
					RetryAfter: retryAfterInt,
					RequestID:  GetRequestID(r.Context()),
				}
				json.NewEncoder(w).Encode(resp)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractRateLimitKey extracts the client IP to use as rate limit key
// Uses X-Real-IP (set by sanitizer) or falls back to RemoteAddr
func extractRateLimitKey(r *http.Request) string {
	// Prefer X-Real-IP (should be set by HeaderSanitizer)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return normalizeIP(ip)
	}

	// Fallback to RemoteAddr
	return normalizeIP(r.RemoteAddr)
}

// normalizeIP normalizes an IP address string by removing port if present
func normalizeIP(addr string) string {
	if addr == "" {
		return "unknown"
	}

	// Try to split host:port
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// No port, return as-is
		return strings.TrimSpace(addr)
	}

	return host
}
