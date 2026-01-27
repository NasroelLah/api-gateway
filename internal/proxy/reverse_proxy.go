package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nasroellah/api-gateway/internal/config"
)

// HopByHopHeaders adalah headers yang tidak boleh diteruskan ke upstream
var HopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailers",
	"Transfer-Encoding",
	"Upgrade",
}

// ErrorResponse represents an error response from the gateway
type ErrorResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

// ProxyManager mengelola reverse proxy ke upstream services
type ProxyManager struct {
	mu           sync.RWMutex
	proxies      map[string]*httputil.ReverseProxy
	upstreams    []config.UpstreamConfig
	transport    *http.Transport
	timeout      time.Duration
	allowedHosts []string
	dynamicCfg   config.DynamicUpstreamConfig
}

// NewProxyManager membuat proxy manager dengan custom transport
func NewProxyManager(cfg *config.ProxyConfig) *ProxyManager {
	pm := &ProxyManager{
		proxies:      make(map[string]*httputil.ReverseProxy),
		upstreams:    cfg.Upstreams,
		transport:    createTransport(cfg),
		timeout:      cfg.Timeout,
		allowedHosts: cfg.DynamicUpstream.AllowedHosts,
		dynamicCfg:   cfg.DynamicUpstream,
	}

	// Sort upstreams by path prefix length (longest first) for longest prefix match
	sort.Slice(pm.upstreams, func(i, j int) bool {
		return len(pm.upstreams[i].PathPrefix) > len(pm.upstreams[j].PathPrefix)
	})

	// Initialize proxies for static upstreams
	for _, upstream := range pm.upstreams {
		targetURL, err := url.Parse(upstream.Target)
		if err != nil {
			continue
		}
		pm.proxies[upstream.PathPrefix] = pm.createProxy(targetURL)
	}

	return pm
}


// createTransport membuat HTTP transport dengan connection pooling
func createTransport(cfg *config.ProxyConfig) *http.Transport {
	maxIdleConns := cfg.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = 1000
	}

	maxIdleConnsPerHost := cfg.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = 100
	}

	return &http.Transport{
		MaxIdleConns:        maxIdleConns,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		ForceAttemptHTTP2:   true,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// createProxy creates a reverse proxy for a target URL
func (pm *ProxyManager) createProxy(target *url.URL) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Director:       pm.PassthroughDirector(target),
		Transport:      pm.transport,
		ModifyResponse: pm.ModifyResponse,
		ErrorHandler:   pm.ErrorHandler,
	}
	return proxy
}

// GetProxy mengembalikan proxy untuk path tertentu (static routing)
// Uses longest prefix match
func (pm *ProxyManager) GetProxy(path string) (*httputil.ReverseProxy, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Find longest matching prefix
	for _, upstream := range pm.upstreams {
		if strings.HasPrefix(path, upstream.PathPrefix) {
			if proxy, ok := pm.proxies[upstream.PathPrefix]; ok {
				return proxy, true
			}
		}
	}

	return nil, false
}

// GetUpstreamTarget returns the target URL for a given path
func (pm *ProxyManager) GetUpstreamTarget(path string) (string, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, upstream := range pm.upstreams {
		if strings.HasPrefix(path, upstream.PathPrefix) {
			return upstream.Target, true
		}
	}

	return "", false
}


// PassthroughDirector meneruskan request secara 1:1
// - Semua headers diteruskan (kecuali hop-by-hop)
// - Authorization header diteruskan tanpa modifikasi
// - Body diteruskan byte-for-byte (handled by ReverseProxy)
// - Query parameters diteruskan tanpa modifikasi
// - HTTP method diteruskan tanpa modifikasi
func (pm *ProxyManager) PassthroughDirector(target *url.URL) func(*http.Request) {
	return func(req *http.Request) {
		// Preserve original host for X-Forwarded-Host
		originalHost := req.Host

		// Set target URL - preserving the original path and query
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		// Query parameters are already in req.URL.RawQuery (no modification needed)
		// HTTP method is already set (no modification needed)
		// Body is handled by ReverseProxy (no modification needed)

		// Remove hop-by-hop headers
		for _, h := range HopByHopHeaders {
			req.Header.Del(h)
		}

		// All other headers (including Authorization, Content-Type) remain intact

		// Add gateway headers
		req.Header.Set("X-Forwarded-Host", originalHost)
		if target.Scheme != "" {
			req.Header.Set("X-Forwarded-Proto", target.Scheme)
		}
	}
}

// ModifyResponse memodifikasi response dari upstream (currently no-op for passthrough)
func (pm *ProxyManager) ModifyResponse(resp *http.Response) error {
	// For passthrough mode, we don't modify the response body
	// Response is forwarded as-is
	return nil
}


// ErrDynamicUpstreamDisabled is returned when dynamic upstream is not enabled
var ErrDynamicUpstreamDisabled = errors.New("dynamic upstream routing is disabled")

// ErrInvalidUpstreamTarget is returned when the target URL is invalid
var ErrInvalidUpstreamTarget = errors.New("invalid upstream target URL")

// ErrUpstreamNotAllowed is returned when the target host is not in allowed hosts
var ErrUpstreamNotAllowed = errors.New("upstream host not in allowed hosts")

// GetDynamicProxy membuat proxy untuk dynamic upstream dari header
func (pm *ProxyManager) GetDynamicProxy(targetURL string) (*httputil.ReverseProxy, error) {
	if !pm.dynamicCfg.Enabled {
		return nil, ErrDynamicUpstreamDisabled
	}

	// Validate the target URL
	if err := pm.ValidateDynamicTarget(targetURL); err != nil {
		return nil, err
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidUpstreamTarget, err)
	}

	// Create a new proxy for this dynamic target
	return pm.createProxy(target), nil
}

// ValidateDynamicTarget memvalidasi target URL terhadap allowed hosts
func (pm *ProxyManager) ValidateDynamicTarget(targetURL string) error {
	if targetURL == "" {
		return ErrInvalidUpstreamTarget
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidUpstreamTarget, err)
	}

	// Validate scheme
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("%w: invalid scheme %s", ErrInvalidUpstreamTarget, target.Scheme)
	}

	// Validate host is not empty
	if target.Host == "" {
		return fmt.Errorf("%w: empty host", ErrInvalidUpstreamTarget)
	}

	// Check against allowed hosts
	if !pm.isHostAllowed(target.Host) {
		return ErrUpstreamNotAllowed
	}

	return nil
}

// isHostAllowed checks if a host matches any of the allowed host patterns
func (pm *ProxyManager) isHostAllowed(host string) bool {
	// If no allowed hosts configured, deny all
	if len(pm.allowedHosts) == 0 {
		return false
	}

	// Extract hostname without port for pattern matching
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}

	for _, pattern := range pm.allowedHosts {
		// Allow all hosts with "*" pattern
		if pattern == "*" {
			return true
		}

		// Extract pattern hostname without port
		patternHost := pattern
		if h, _, err := net.SplitHostPort(pattern); err == nil {
			patternHost = h
		}

		// Check exact match with full host (including port)
		if pattern == host {
			return true
		}

		// Check pattern match with hostname only
		if matchHostPattern(patternHost, hostname) {
			return true
		}
	}

	return false
}

// matchHostPattern matches a hostname against a pattern
// Supports wildcard patterns like "*.example.com"
func matchHostPattern(pattern, hostname string) bool {
	// Exact match
	if pattern == hostname {
		return true
	}

	// Wildcard match (e.g., "*.example.com")
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(hostname, suffix) {
			// Ensure there's at least one character before the suffix
			prefix := hostname[:len(hostname)-len(suffix)]
			if len(prefix) > 0 && !strings.Contains(prefix, ".") {
				return true
			}
		}
	}

	return false
}

// GetDynamicHeaderName returns the header name used for dynamic upstream routing
func (pm *ProxyManager) GetDynamicHeaderName() string {
	if pm.dynamicCfg.HeaderName == "" {
		return "X-Upstream-Target"
	}
	return pm.dynamicCfg.HeaderName
}

// IsDynamicUpstreamEnabled returns whether dynamic upstream routing is enabled
func (pm *ProxyManager) IsDynamicUpstreamEnabled() bool {
	return pm.dynamicCfg.Enabled
}


// ErrorHandler menangani error dari upstream
// Returns 502 for unreachable, 504 for timeout
func (pm *ProxyManager) ErrorHandler(w http.ResponseWriter, r *http.Request, err error) {
	var statusCode int
	var message string

	// Determine error type
	if isTimeoutError(err) {
		statusCode = http.StatusGatewayTimeout // 504
		message = "upstream timeout"
	} else {
		statusCode = http.StatusBadGateway // 502
		message = "upstream unavailable"
	}

	// Get request ID from context if available
	requestID := ""
	if rid := r.Context().Value(RequestIDKey); rid != nil {
		if s, ok := rid.(string); ok {
			requestID = s
		}
	}

	// Write error response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	resp := ErrorResponse{
		Error:     message,
		RequestID: requestID,
	}

	json.NewEncoder(w).Encode(resp)
}

// RequestIDKey is the context key for request ID
type contextKey string

const RequestIDKey contextKey = "request_id"

// isTimeoutError checks if an error is a timeout error
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}

	// Check for context deadline exceeded
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check for net.Error timeout
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Check error message for timeout indicators
	errStr := err.Error()
	return strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "context canceled")
}

// Close closes the transport and releases resources
func (pm *ProxyManager) Close() {
	if pm.transport != nil {
		pm.transport.CloseIdleConnections()
	}
}
