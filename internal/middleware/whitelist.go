package middleware

import (
	"encoding/json"
	"net"
	"net/http"

	"github.com/nasroellah/api-gateway/internal/security"
)

// ErrorResponse represents a JSON error response
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

// WhitelistMiddleware creates middleware that checks IP against whitelist.
// Returns 403 Forbidden if IP is not in whitelist (default deny).
// Requirements: 2.1, 2.2, 2.3
func WhitelistMiddleware(whitelist *security.IPWhitelist, trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract client IP from request
			clientIP, err := security.ParseIP(r, trustProxy)
			if err != nil {
				// Cannot determine client IP - deny access
				writeErrorResponse(w, r, http.StatusForbidden, "access denied")
				return
			}

			// Check if IP is allowed
			if !whitelist.IsAllowed(clientIP) {
				writeErrorResponse(w, r, http.StatusForbidden, "access denied")
				return
			}

			// IP is allowed - continue to next handler
			next.ServeHTTP(w, r)
		})
	}
}

// WhitelistMiddlewareWithBypass creates middleware that can bypass whitelist check
// for certain paths (e.g., health checks, metrics endpoints)
func WhitelistMiddlewareWithBypass(whitelist *security.IPWhitelist, trustProxy bool, bypassPaths []string) func(http.Handler) http.Handler {
	// Create a map for O(1) lookup
	bypassMap := make(map[string]bool, len(bypassPaths))
	for _, path := range bypassPaths {
		bypassMap[path] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if path should bypass whitelist
			if bypassMap[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Extract client IP from request
			clientIP, err := security.ParseIP(r, trustProxy)
			if err != nil {
				writeErrorResponse(w, r, http.StatusForbidden, "access denied")
				return
			}

			// Check if IP is allowed
			if !whitelist.IsAllowed(clientIP) {
				writeErrorResponse(w, r, http.StatusForbidden, "access denied")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeErrorResponse writes a JSON error response
func writeErrorResponse(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	resp := ErrorResponse{
		Error:     message,
		RequestID: GetRequestID(r.Context()),
	}

	json.NewEncoder(w).Encode(resp)
}

// IsIPAllowed is a helper function to check if an IP string is allowed
// Useful for testing and external validation
func IsIPAllowed(whitelist *security.IPWhitelist, ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return whitelist.IsAllowed(ip)
}
