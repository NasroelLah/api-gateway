package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
)

// TestAPIKeyGeneration tests that generated API keys are valid
func TestAPIKeyGeneration(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Generated API keys SHALL be 32 characters (hex encoded 16 bytes)
	properties.Property("Generated API keys are 32 characters", prop.ForAll(
		func(_ int) bool {
			key := GenerateAPIKey()
			return len(key) == 32
		},
		gen.Int(),
	))

	// Property: Generated API keys SHALL be unique
	properties.Property("Generated API keys are unique", prop.ForAll(
		func(_ int) bool {
			key1 := GenerateAPIKey()
			key2 := GenerateAPIKey()
			return key1 != key2
		},
		gen.Int(),
	))

	// Property: Generated API keys SHALL only contain hex characters
	properties.Property("Generated API keys contain only hex characters", prop.ForAll(
		func(_ int) bool {
			key := GenerateAPIKey()
			for _, c := range key {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					return false
				}
			}
			return true
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}

// TestAPIKeyValidation tests API key validation behavior
func TestAPIKeyValidation(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Correct API key SHALL be validated successfully
	properties.Property("Correct API key is validated successfully", prop.ForAll(
		func(_ int) bool {
			auth := NewAPIKeyAuth("", "X-API-Key", true)
			key := auth.GetAPIKey()
			return auth.Validate(key)
		},
		gen.Int(),
	))

	// Property: Incorrect API key SHALL be rejected
	properties.Property("Incorrect API key is rejected", prop.ForAll(
		func(wrongKey string) bool {
			auth := NewAPIKeyAuth("correct-key-12345678", "X-API-Key", true)
			if wrongKey == "correct-key-12345678" {
				return true // Skip if accidentally matches
			}
			return !auth.Validate(wrongKey)
		},
		gen.AnyString().SuchThat(func(s string) bool { return len(s) > 0 }),
	))

	// Property: Empty API key SHALL be rejected when auth is enabled
	properties.Property("Empty API key is rejected when auth enabled", prop.ForAll(
		func(_ int) bool {
			auth := NewAPIKeyAuth("valid-key-123456789", "X-API-Key", true)
			return !auth.Validate("")
		},
		gen.Int(),
	))

	// Property: Any key SHALL be accepted when auth is disabled
	properties.Property("Any key is accepted when auth disabled", prop.ForAll(
		func(anyKey string) bool {
			auth := NewAPIKeyAuth("some-key", "X-API-Key", false)
			return auth.Validate(anyKey)
		},
		gen.AnyString(),
	))

	// Property: Auto-generated key SHALL work for validation
	properties.Property("Auto-generated key works for validation", prop.ForAll(
		func(_ int) bool {
			// Create auth with empty key (should auto-generate)
			auth := NewAPIKeyAuth("", "X-API-Key", true)
			key := auth.GetAPIKey()
			
			// Key should be non-empty and valid
			return key != "" && auth.Validate(key)
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}

// TestAPIKeyMiddleware tests the API key middleware behavior
func TestAPIKeyMiddleware(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Request with valid API key SHALL pass through
	properties.Property("Request with valid API key passes through", prop.ForAll(
		func(_ int) bool {
			auth := NewAPIKeyAuth("test-api-key-12345", "X-API-Key", true)
			
			handler := APIKeyMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-API-Key", "test-api-key-12345")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			return rec.Code == http.StatusOK
		},
		gen.Int(),
	))

	// Property: Request without API key SHALL return 401
	properties.Property("Request without API key returns 401", prop.ForAll(
		func(_ int) bool {
			auth := NewAPIKeyAuth("test-api-key-12345", "X-API-Key", true)
			
			handler := APIKeyMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			// No API key header
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			return rec.Code == http.StatusUnauthorized
		},
		gen.Int(),
	))

	// Property: Request with invalid API key SHALL return 401
	properties.Property("Request with invalid API key returns 401", prop.ForAll(
		func(invalidKey string) bool {
			if invalidKey == "correct-key" {
				return true // Skip
			}
			
			auth := NewAPIKeyAuth("correct-key", "X-API-Key", true)
			
			handler := APIKeyMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("X-API-Key", invalidKey)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			return rec.Code == http.StatusUnauthorized
		},
		gen.AnyString().SuchThat(func(s string) bool { return len(s) > 0 && s != "correct-key" }),
	))

	// Property: Bypass paths SHALL not require API key
	properties.Property("Bypass paths do not require API key", prop.ForAll(
		func(pathIdx int) bool {
			bypassPaths := []string{"/health", "/ready", "/metrics"}
			path := bypassPaths[pathIdx%len(bypassPaths)]
			
			auth := NewAPIKeyAuth("test-api-key", "X-API-Key", true)
			
			handler := APIKeyMiddlewareWithBypass(auth, bypassPaths)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			// No API key header
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			return rec.Code == http.StatusOK
		},
		gen.IntRange(0, 2),
	))

	// Property: Custom header name SHALL be respected
	properties.Property("Custom header name is respected", prop.ForAll(
		func(headerIdx int) bool {
			headers := []string{"X-Custom-Key", "Authorization", "Api-Token"}
			headerName := headers[headerIdx%len(headers)]
			
			auth := NewAPIKeyAuth("my-secret-key", headerName, true)
			
			handler := APIKeyMiddleware(auth)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set(headerName, "my-secret-key")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			return rec.Code == http.StatusOK
		},
		gen.IntRange(0, 2),
	))

	properties.TestingRun(t)
}
