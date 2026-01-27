package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
)

// APIKeyAuth handles static API key authentication
type APIKeyAuth struct {
	mu         sync.RWMutex
	apiKey     string
	headerName string
	enabled    bool
}

// NewAPIKeyAuth creates a new API key authenticator
// If apiKey is empty and enabled is true, a new key will be auto-generated
func NewAPIKeyAuth(apiKey string, headerName string, enabled bool) *APIKeyAuth {
	if headerName == "" {
		headerName = "X-API-Key"
	}

	auth := &APIKeyAuth{
		headerName: headerName,
		enabled:    enabled,
	}

	if enabled {
		if apiKey == "" {
			// Auto-generate API key if not provided
			auth.apiKey = GenerateAPIKey()
		} else {
			auth.apiKey = apiKey
		}
	}

	return auth
}

// GenerateAPIKey generates a cryptographically secure random API key
// Returns a 32-character hex string (128 bits of entropy)
func GenerateAPIKey() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// Fallback to a less secure but still random key
		// This should never happen in practice
		return "fallback-key-" + hex.EncodeToString(bytes[:8])
	}
	return hex.EncodeToString(bytes)
}

// GetAPIKey returns the current API key
func (a *APIKeyAuth) GetAPIKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.apiKey
}

// GetHeaderName returns the header name used for API key
func (a *APIKeyAuth) GetHeaderName() string {
	return a.headerName
}

// IsEnabled returns whether API key auth is enabled
func (a *APIKeyAuth) IsEnabled() bool {
	return a.enabled
}

// Validate checks if the provided API key is valid
// Uses constant-time comparison to prevent timing attacks
func (a *APIKeyAuth) Validate(providedKey string) bool {
	if !a.enabled {
		return true // Auth disabled, allow all
	}

	a.mu.RLock()
	expectedKey := a.apiKey
	a.mu.RUnlock()

	if expectedKey == "" {
		return false // No key configured but auth enabled
	}

	// Constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(providedKey), []byte(expectedKey)) == 1
}

// SetAPIKey updates the API key (useful for key rotation)
func (a *APIKeyAuth) SetAPIKey(newKey string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiKey = newKey
}

// APIKeyMiddleware creates middleware that validates API key from request header
// Returns 401 Unauthorized if API key is missing or invalid
func APIKeyMiddleware(auth *APIKeyAuth) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.IsEnabled() {
				next.ServeHTTP(w, r)
				return
			}

			// Get API key from header
			providedKey := r.Header.Get(auth.GetHeaderName())

			if providedKey == "" {
				writeErrorResponse(w, r, http.StatusUnauthorized, "API key required")
				return
			}

			if !auth.Validate(providedKey) {
				writeErrorResponse(w, r, http.StatusUnauthorized, "invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyMiddlewareWithBypass creates middleware that can bypass API key check
// for certain paths (e.g., health checks, metrics endpoints)
func APIKeyMiddlewareWithBypass(auth *APIKeyAuth, bypassPaths []string) func(http.Handler) http.Handler {
	// Create a map for O(1) lookup
	bypassMap := make(map[string]bool, len(bypassPaths))
	for _, path := range bypassPaths {
		bypassMap[path] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !auth.IsEnabled() {
				next.ServeHTTP(w, r)
				return
			}

			// Check if path should bypass API key check
			if bypassMap[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			// Get API key from header
			providedKey := r.Header.Get(auth.GetHeaderName())

			if providedKey == "" {
				writeErrorResponse(w, r, http.StatusUnauthorized, "API key required")
				return
			}

			if !auth.Validate(providedKey) {
				writeErrorResponse(w, r, http.StatusUnauthorized, "invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
