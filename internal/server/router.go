package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/nasroellah/api-gateway/internal/middleware"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/nasroellah/api-gateway/internal/proxy"
	"github.com/nasroellah/api-gateway/internal/security"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// RouterConfig holds configuration for the router
type RouterConfig struct {
	// WhitelistEnabled enables IP whitelist checking
	WhitelistEnabled bool
	// TrustedProxies are the only peers whose forwarded headers are accepted.
	TrustedProxies []string
	// RateLimitEnabled enables rate limiting
	RateLimitEnabled bool
	// TracingEnabled enables OpenTelemetry tracing
	TracingEnabled bool
	// MetricsEnabled enables Prometheus metrics
	MetricsEnabled bool
	// MetricsPath is the path for metrics endpoint
	MetricsPath string
	// AuthEnabled enables API key authentication
	AuthEnabled bool
	// BypassPaths are paths that bypass whitelist, rate limiting, and auth
	BypassPaths []string
}

// RouterDependencies holds all dependencies needed by the router
type RouterDependencies struct {
	Logger      *zap.Logger
	ProxyMgr    *proxy.ProxyManager
	Whitelist   *security.IPWhitelist
	RateLimiter middleware.RateLimiter
	Metrics     *observability.Metrics
	Tracer      trace.Tracer
	APIKeyAuth  *middleware.APIKeyAuth
}

// HealthStatus merepresentasikan status kesehatan Gateway
// Requirements: 10.1, 10.2
type HealthStatus struct {
	Status    string            `json:"status"`
	Timestamp time.Time         `json:"timestamp"`
	Checks    map[string]string `json:"checks,omitempty"`
}

// SetupRouter configures the Chi router with middleware chain and routes
// Requirements: 8.5, 10.1, 10.2
func SetupRouter(router chi.Router, cfg *RouterConfig, deps *RouterDependencies) {
	// Default bypass paths for health and metrics
	bypassPaths := []string{"/health", "/ready"}
	if cfg.MetricsPath != "" {
		bypassPaths = append(bypassPaths, cfg.MetricsPath)
	}
	if len(cfg.BypassPaths) > 0 {
		bypassPaths = append(bypassPaths, cfg.BypassPaths...)
	}

	// Setup middleware chain in correct order:
	// 1. Recovery (catch panics)
	// 2. Request ID (generate unique ID for each request)
	// 3. Header Sanitizer (sanitize X-Forwarded-For, X-Real-IP)
	// 4. Tracing (create/continue spans)
	// 5. Logging (log requests)
	// 6. Metrics (record metrics)
	// 7. IP Whitelist (security check)
	// 8. Rate Limiter (throttling)

	// 1. Recovery middleware - catch panics
	router.Use(chimiddleware.Recoverer)

	// 2. Request ID middleware - generate unique ID for each request
	router.Use(middleware.RequestIDMiddleware)

	// 3. Sanitize forwarding headers using explicitly trusted proxy hops.
	router.Use(middleware.HeaderSanitizerWithTrust(cfg.TrustedProxies))

	// 4. Tracing middleware (if enabled)
	if cfg.TracingEnabled && deps.Tracer != nil {
		router.Use(middleware.TracingMiddleware(deps.Tracer))
	}

	// 5. Logging middleware
	if deps.Logger != nil {
		router.Use(middleware.LoggingMiddlewareWithErrors(deps.Logger))
	}

	// 6. Metrics middleware (if enabled)
	if cfg.MetricsEnabled && deps.Metrics != nil {
		router.Use(middleware.MetricsMiddlewareWithUpstreamErrors(deps.Metrics))
	}

	// 7. IP Whitelist middleware (if enabled)
	if cfg.WhitelistEnabled && deps.Whitelist != nil {
		router.Use(middleware.WhitelistMiddlewareWithBypass(
			deps.Whitelist,
			false,
			bypassPaths,
		))
	}

	// 8. Rate Limiter middleware (if enabled)
	if cfg.RateLimitEnabled && deps.RateLimiter != nil {
		router.Use(middleware.RateLimitByIPWithBypassMiddleware(
			deps.RateLimiter,
			bypassPaths,
		))
	}

	// 9. API Key Auth middleware (if enabled)
	if cfg.AuthEnabled && deps.APIKeyAuth != nil {
		router.Use(middleware.APIKeyMiddlewareWithBypass(
			deps.APIKeyAuth,
			bypassPaths,
		))
	}

	// Setup health check endpoints
	// Requirements: 10.1, 10.2
	setupHealthEndpoints(router)

	// Setup metrics endpoint (if enabled)
	// Requirements: 5.1
	if cfg.MetricsEnabled {
		metricsPath := cfg.MetricsPath
		if metricsPath == "" {
			metricsPath = "/metrics"
		}
		router.Handle(metricsPath, promhttp.Handler())
	}

	// Setup proxy handler for all other routes
	if deps.ProxyMgr != nil {
		setupProxyHandler(router, deps.ProxyMgr, deps.Logger)
	}
}

// setupHealthEndpoints configures health check endpoints
// Requirements: 10.1, 10.2
func setupHealthEndpoints(router chi.Router) {
	// Liveness probe endpoint
	// Requirements: 10.1
	router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		status := HealthStatus{
			Status:    "healthy",
			Timestamp: time.Now().UTC(),
		}
		writeJSON(w, http.StatusOK, status)
	})

	// Readiness probe endpoint
	// Requirements: 10.2
	router.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		// In a full implementation, this would check:
		// - Database connections
		// - Upstream service availability
		// - Other dependencies
		status := HealthStatus{
			Status:    "ready",
			Timestamp: time.Now().UTC(),
			Checks: map[string]string{
				"server": "ok",
			},
		}
		writeJSON(w, http.StatusOK, status)
	})
}

// setupProxyHandler configures the reverse proxy handler
func setupProxyHandler(router chi.Router, proxyMgr *proxy.ProxyManager, logger *zap.Logger) {
	// Handle all requests that don't match specific routes
	router.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		// Check for dynamic upstream header first
		if proxyMgr.IsDynamicUpstreamEnabled() {
			headerName := proxyMgr.GetDynamicHeaderName()
			if targetURL := r.Header.Get(headerName); targetURL != "" {
				dynamicProxy, err := proxyMgr.GetDynamicProxy(targetURL)
				if err != nil {
					if logger != nil {
						logger.Warn("dynamic upstream error",
							zap.String("target", targetURL),
							zap.Error(err),
						)
					}
					writeErrorJSON(w, http.StatusBadRequest, "invalid upstream target")
					return
				}
				dynamicProxy.ServeHTTP(w, r)
				return
			}
		}

		// Try static routing
		staticProxy, found := proxyMgr.GetProxy(r.URL.Path)
		if !found {
			writeErrorJSON(w, http.StatusNotFound, "no upstream configured for path")
			return
		}

		staticProxy.ServeHTTP(w, r)
	})
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// writeErrorJSON writes a JSON error response
func writeErrorJSON(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// NewDefaultRouterConfig creates a RouterConfig with sensible defaults
func NewDefaultRouterConfig() *RouterConfig {
	return &RouterConfig{
		WhitelistEnabled:    true,
		RateLimitEnabled:    true,
		TracingEnabled:      false,
		MetricsEnabled:      true,
		MetricsPath:         "/metrics",
		AuthEnabled:         false,
		BypassPaths:         []string{},
	}
}
