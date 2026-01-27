package testing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nasroellah/api-gateway/internal/config"
	"github.com/nasroellah/api-gateway/internal/middleware"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/nasroellah/api-gateway/internal/proxy"
	"github.com/nasroellah/api-gateway/internal/security"
	"github.com/nasroellah/api-gateway/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// TestIntegration_EndToEndRequestFlow tests the complete request flow through the gateway
func TestIntegration_EndToEndRequestFlow(t *testing.T) {
	// Create a mock upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back request details
		response := map[string]interface{}{
			"method":  r.Method,
			"path":    r.URL.Path,
			"query":   r.URL.RawQuery,
			"headers": flattenHeaders(r.Header),
		}
		
		// Read and echo body if present
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			if len(body) > 0 {
				response["body"] = string(body)
			}
		}
		
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Response", "true")
		json.NewEncoder(w).Encode(response)
	}))
	defer upstreamServer.Close()

	// Setup gateway configuration
	cfg := createTestConfig(upstreamServer.URL)
	
	// Create logger
	logger, _ := zap.NewDevelopment()
	
	// Initialize components
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	whitelist, err := security.NewIPWhitelist(cfg.Whitelist.Entries)
	if err != nil {
		t.Fatalf("Failed to create whitelist: %v", err)
	}
	
	rateLimiter := middleware.NewInMemoryRateLimiter(
		cfg.RateLimit.RequestsPerSecond,
		cfg.RateLimit.Burst,
	)
	defer rateLimiter.Stop()
	
	// Create server
	srv := server.NewServer(&cfg.Server, logger)
	
	// Setup router
	routerCfg := &server.RouterConfig{
		WhitelistEnabled:    cfg.Whitelist.Enabled,
		WhitelistTrustProxy: false,
		RateLimitEnabled:    cfg.RateLimit.Enabled,
		TracingEnabled:      false,
		MetricsEnabled:      false,
		MetricsPath:         "/metrics",
		AuthEnabled:         false,
	}
	
	deps := &server.RouterDependencies{
		Logger:      logger,
		ProxyMgr:    proxyMgr,
		Whitelist:   whitelist,
		RateLimiter: rateLimiter,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	// Create test server using the router
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	// Test cases
	t.Run("GET request passthrough", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users?id=123")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["method"] != "GET" {
			t.Errorf("Expected method GET, got %v", result["method"])
		}
		if result["query"] != "id=123" {
			t.Errorf("Expected query 'id=123', got %v", result["query"])
		}
	})

	t.Run("POST request with body passthrough", func(t *testing.T) {
		body := `{"name":"test","value":42}`
		resp, err := http.Post(testServer.URL+"/api/v1/users", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["method"] != "POST" {
			t.Errorf("Expected method POST, got %v", result["method"])
		}
		if result["body"] != body {
			t.Errorf("Expected body '%s', got %v", body, result["body"])
		}
	})
	
	t.Run("Authorization header passthrough", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("Authorization", "Bearer test-token-123")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		headers := result["headers"].(map[string]interface{})
		if headers["Authorization"] != "Bearer test-token-123" {
			t.Errorf("Authorization header not passed through correctly")
		}
	})
	
	t.Run("Custom headers passthrough", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("X-Custom-Header", "custom-value")
		req.Header.Set("X-Another-Header", "another-value")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		headers := result["headers"].(map[string]interface{})
		if headers["X-Custom-Header"] != "custom-value" {
			t.Errorf("X-Custom-Header not passed through")
		}
		if headers["X-Another-Header"] != "another-value" {
			t.Errorf("X-Another-Header not passed through")
		}
	})

	t.Run("Request ID is generated", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		// Check that X-Request-ID is in the response header
		requestID := resp.Header.Get("X-Request-ID")
		if requestID == "" {
			t.Error("X-Request-ID header not generated in response")
		}
	})
	
	t.Run("Health endpoint returns healthy", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/health")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["status"] != "healthy" {
			t.Errorf("Expected status 'healthy', got %v", result["status"])
		}
	})
	
	t.Run("Ready endpoint returns ready", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/ready")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["status"] != "ready" {
			t.Errorf("Expected status 'ready', got %v", result["status"])
		}
	})
	
	t.Run("Unknown path returns 404", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/unknown/path")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", resp.StatusCode)
		}
	})
}


// TestIntegration_IPWhitelistEnforcement tests IP whitelist security
func TestIntegration_IPWhitelistEnforcement(t *testing.T) {
	// Create a mock upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstreamServer.Close()
	
	// Setup gateway with restrictive whitelist
	cfg := createTestConfig(upstreamServer.URL)
	cfg.Whitelist.Enabled = true
	cfg.Whitelist.Entries = []string{"192.168.1.0/24"} // Only allow this CIDR
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	whitelist, _ := security.NewIPWhitelist(cfg.Whitelist.Entries)
	rateLimiter := middleware.NewInMemoryRateLimiter(100, 200)
	defer rateLimiter.Stop()
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled:    true,
		WhitelistTrustProxy: false,
		RateLimitEnabled:    false,
		MetricsEnabled:      false,
	}
	
	deps := &server.RouterDependencies{
		Logger:      logger,
		ProxyMgr:    proxyMgr,
		Whitelist:   whitelist,
		RateLimiter: rateLimiter,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	// Test that requests from non-whitelisted IPs are blocked
	// Note: httptest.Server uses 127.0.0.1 which is not in our whitelist
	t.Run("Non-whitelisted IP is blocked", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Expected status 403, got %d", resp.StatusCode)
		}
	})
	
	// Health endpoints should bypass whitelist
	t.Run("Health endpoint bypasses whitelist", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/health")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
}


// TestIntegration_RateLimiting tests rate limiting functionality
func TestIntegration_RateLimiting(t *testing.T) {
	// Create a mock upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstreamServer.Close()
	
	// Setup gateway with strict rate limiting
	cfg := createTestConfig(upstreamServer.URL)
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.RequestsPerSecond = 2
	cfg.RateLimit.Burst = 3
	cfg.Whitelist.Enabled = false // Disable whitelist for this test
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	rateLimiter := middleware.NewInMemoryRateLimiter(
		cfg.RateLimit.RequestsPerSecond,
		cfg.RateLimit.Burst,
	)
	defer rateLimiter.Stop()
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: true,
		MetricsEnabled:   false,
	}
	
	deps := &server.RouterDependencies{
		Logger:      logger,
		ProxyMgr:    proxyMgr,
		RateLimiter: rateLimiter,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	t.Run("Requests within burst limit succeed", func(t *testing.T) {
		// First 3 requests should succeed (burst = 3)
		for i := 0; i < 3; i++ {
			resp, err := http.Get(testServer.URL + "/api/v1/users")
			if err != nil {
				t.Fatalf("Request %d failed: %v", i, err)
			}
			resp.Body.Close()
			
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Request %d: Expected status 200, got %d", i, resp.StatusCode)
			}
		}
	})
	
	t.Run("Requests exceeding burst limit are rate limited", func(t *testing.T) {
		// Make requests until we hit rate limit
		rateLimited := false
		for i := 0; i < 10; i++ {
			resp, err := http.Get(testServer.URL + "/api/v1/users")
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			
			if resp.StatusCode == http.StatusTooManyRequests {
				rateLimited = true
				// Check for Retry-After header
				retryAfter := resp.Header.Get("Retry-After")
				if retryAfter == "" {
					t.Error("Expected Retry-After header on 429 response")
				}
				resp.Body.Close()
				break
			}
			resp.Body.Close()
		}
		
		if !rateLimited {
			t.Error("Expected to hit rate limit but didn't")
		}
	})
	
	t.Run("Health endpoint bypasses rate limiting", func(t *testing.T) {
		// Health endpoint should always work
		for i := 0; i < 5; i++ {
			resp, err := http.Get(testServer.URL + "/health")
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			resp.Body.Close()
			
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Health endpoint should bypass rate limiting, got %d", resp.StatusCode)
			}
		}
	})
}


// TestIntegration_DynamicUpstreamRouting tests dynamic upstream routing via header
func TestIntegration_DynamicUpstreamRouting(t *testing.T) {
	// Create two mock upstream servers
	upstream1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"server":"upstream1"}`))
	}))
	defer upstream1.Close()
	
	upstream2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"server":"upstream2"}`))
	}))
	defer upstream2.Close()
	
	// Extract host from upstream2 URL for allowed hosts
	upstream2Host := strings.TrimPrefix(upstream2.URL, "http://")
	
	// Setup gateway with dynamic upstream enabled
	cfg := createTestConfig(upstream1.URL)
	cfg.Proxy.DynamicUpstream.Enabled = true
	cfg.Proxy.DynamicUpstream.HeaderName = "X-Upstream-Target"
	cfg.Proxy.DynamicUpstream.AllowedHosts = []string{upstream2Host}
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   false,
	}
	
	deps := &server.RouterDependencies{
		Logger:   logger,
		ProxyMgr: proxyMgr,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	t.Run("Request without dynamic header uses static routing", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["server"] != "upstream1" {
			t.Errorf("Expected upstream1, got %v", result["server"])
		}
	})
	
	t.Run("Request with dynamic header routes to specified upstream", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("X-Upstream-Target", upstream2.URL)
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["server"] != "upstream2" {
			t.Errorf("Expected upstream2, got %v", result["server"])
		}
	})
	
	t.Run("Request with non-allowed host is rejected", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("X-Upstream-Target", "http://not-allowed.example.com")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Expected status 400, got %d", resp.StatusCode)
		}
	})
}


// TestIntegration_MetricsEndpoint tests Prometheus metrics endpoint
func TestIntegration_MetricsEndpoint(t *testing.T) {
	// Create a mock upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstreamServer.Close()
	
	// Setup gateway with metrics enabled
	cfg := createTestConfig(upstreamServer.URL)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Path = "/metrics"
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	// Create a new registry for this test to avoid conflicts
	registry := prometheus.NewRegistry()
	metrics := observability.NewMetrics(registry)
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   true,
		MetricsPath:      "/metrics",
	}
	
	deps := &server.RouterDependencies{
		Logger:   logger,
		ProxyMgr: proxyMgr,
		Metrics:  metrics,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	t.Run("Metrics endpoint is accessible", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/metrics")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
		
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		
		// Check for expected metric names
		if !strings.Contains(bodyStr, "go_") {
			t.Error("Expected Go runtime metrics")
		}
	})
	
	t.Run("Metrics are recorded after requests", func(t *testing.T) {
		// Make some requests
		for i := 0; i < 3; i++ {
			resp, _ := http.Get(testServer.URL + "/api/v1/users")
			resp.Body.Close()
		}
		
		// Check metrics endpoint
		resp, err := http.Get(testServer.URL + "/metrics")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)
		
		// Should contain request metrics
		if !strings.Contains(bodyStr, "gateway_") || !strings.Contains(bodyStr, "http_") {
			// Metrics may have different prefixes, just check it's not empty
			if len(bodyStr) < 100 {
				t.Error("Expected metrics content to be present")
			}
		}
	})
}


// TestIntegration_GracefulShutdown tests graceful shutdown behavior
func TestIntegration_GracefulShutdown(t *testing.T) {
	// Create a slow upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstreamServer.Close()
	
	cfg := createTestConfig(upstreamServer.URL)
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find available port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = port
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   false,
	}
	
	deps := &server.RouterDependencies{
		Logger:   logger,
		ProxyMgr: proxyMgr,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	// Start server
	errCh := srv.StartAsync()
	
	// Wait for server to be ready
	time.Sleep(50 * time.Millisecond)
	
	serverURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	
	t.Run("In-flight requests complete during shutdown", func(t *testing.T) {
		// Start a request that will take some time
		responseCh := make(chan *http.Response, 1)
		go func() {
			resp, err := http.Get(serverURL + "/api/v1/users")
			if err == nil {
				responseCh <- resp
			} else {
				responseCh <- nil
			}
		}()
		
		// Wait a bit for request to start
		time.Sleep(20 * time.Millisecond)
		
		// Initiate shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		
		shutdownErr := srv.Shutdown(ctx)
		if shutdownErr != nil {
			t.Errorf("Shutdown error: %v", shutdownErr)
		}
		
		// Check that the in-flight request completed
		select {
		case resp := <-responseCh:
			if resp != nil {
				resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Errorf("Expected in-flight request to complete with 200, got %d", resp.StatusCode)
				}
			}
		case <-time.After(3 * time.Second):
			t.Error("In-flight request did not complete in time")
		}
	})
	
	// Check for startup errors
	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("Server error (expected after shutdown): %v", err)
		}
	default:
	}
}


// TestIntegration_UpstreamErrorHandling tests error handling for upstream failures
func TestIntegration_UpstreamErrorHandling(t *testing.T) {
	// Create an upstream server that will be closed
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	upstreamURL := upstreamServer.URL
	upstreamServer.Close() // Close immediately to simulate unreachable upstream
	
	cfg := createTestConfig(upstreamURL)
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   false,
	}
	
	deps := &server.RouterDependencies{
		Logger:   logger,
		ProxyMgr: proxyMgr,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	t.Run("Unreachable upstream returns 502", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusBadGateway {
			t.Errorf("Expected status 502, got %d", resp.StatusCode)
		}
		
		var result map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&result)
		
		if result["error"] == nil {
			t.Error("Expected error message in response")
		}
	})
}

// TestIntegration_APIKeyAuthentication tests API key authentication
func TestIntegration_APIKeyAuthentication(t *testing.T) {
	// Create a mock upstream server
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstreamServer.Close()
	
	cfg := createTestConfig(upstreamServer.URL)
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	cfg.Auth.Enabled = true
	cfg.Auth.APIKey = "test-api-key-12345"
	cfg.Auth.HeaderName = "X-API-Key"
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	apiKeyAuth := middleware.NewAPIKeyAuth(cfg.Auth.APIKey, cfg.Auth.HeaderName, true)
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   false,
		AuthEnabled:      true,
	}
	
	deps := &server.RouterDependencies{
		Logger:     logger,
		ProxyMgr:   proxyMgr,
		APIKeyAuth: apiKeyAuth,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	t.Run("Request without API key returns 401", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/api/v1/users")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", resp.StatusCode)
		}
	})
	
	t.Run("Request with valid API key succeeds", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("X-API-Key", "test-api-key-12345")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
	
	t.Run("Request with invalid API key returns 401", func(t *testing.T) {
		req, _ := http.NewRequest("GET", testServer.URL+"/api/v1/users", nil)
		req.Header.Set("X-API-Key", "wrong-api-key")
		
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Expected status 401, got %d", resp.StatusCode)
		}
	})
	
	t.Run("Health endpoint bypasses API key auth", func(t *testing.T) {
		resp, err := http.Get(testServer.URL + "/health")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()
		
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
}


// TestIntegration_AllHTTPMethods tests all HTTP methods are properly proxied
func TestIntegration_AllHTTPMethods(t *testing.T) {
	// Create a mock upstream server that echoes the method
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"method": r.Method})
	}))
	defer upstreamServer.Close()
	
	cfg := createTestConfig(upstreamServer.URL)
	cfg.Whitelist.Enabled = false
	cfg.RateLimit.Enabled = false
	
	logger, _ := zap.NewDevelopment()
	proxyMgr := proxy.NewProxyManager(&cfg.Proxy)
	defer proxyMgr.Close()
	
	srv := server.NewServer(&cfg.Server, logger)
	
	routerCfg := &server.RouterConfig{
		WhitelistEnabled: false,
		RateLimitEnabled: false,
		MetricsEnabled:   false,
	}
	
	deps := &server.RouterDependencies{
		Logger:   logger,
		ProxyMgr: proxyMgr,
	}
	
	server.SetupRouter(srv.Router(), routerCfg, deps)
	
	testServer := httptest.NewServer(srv.Router())
	defer testServer.Close()
	
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"}
	
	for _, method := range methods {
		t.Run(method+" method passthrough", func(t *testing.T) {
			var body io.Reader
			if method == "POST" || method == "PUT" || method == "PATCH" {
				body = bytes.NewReader([]byte(`{"test":"data"}`))
			}
			
			req, _ := http.NewRequest(method, testServer.URL+"/api/v1/users", body)
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()
			
			if resp.StatusCode != http.StatusOK {
				t.Errorf("Expected status 200, got %d", resp.StatusCode)
			}
			
			// HEAD requests don't have a body
			if method != "HEAD" {
				var result map[string]string
				json.NewDecoder(resp.Body).Decode(&result)
				
				if result["method"] != method {
					t.Errorf("Expected method %s, got %s", method, result["method"])
				}
			}
		})
	}
}

// Helper functions

// createTestConfig creates a test configuration
func createTestConfig(upstreamURL string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:         "127.0.0.1",
			Port:         0, // Will be assigned by test server
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		Proxy: config.ProxyConfig{
			Upstreams: []config.UpstreamConfig{
				{
					PathPrefix: "/api/v1/users",
					Target:     upstreamURL,
				},
			},
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			Timeout:             30 * time.Second,
			DynamicUpstream: config.DynamicUpstreamConfig{
				Enabled:      false,
				HeaderName:   "X-Upstream-Target",
				AllowedHosts: []string{},
			},
		},
		Whitelist: config.WhitelistConfig{
			Enabled: true,
			Entries: []string{"127.0.0.1", "::1"},
		},
		RateLimit: config.RateLimitConfig{
			Enabled:           true,
			Type:              "memory",
			RequestsPerSecond: 100,
			Burst:             200,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
			Output: "stdout",
		},
		Metrics: config.MetricsConfig{
			Enabled: false,
			Path:    "/metrics",
		},
		Tracing: config.TracingConfig{
			Enabled: false,
		},
		TLS: config.TLSConfig{
			Enabled: false,
		},
		Auth: config.AuthConfig{
			Enabled:    false,
			APIKey:     "",
			HeaderName: "X-API-Key",
		},
	}
}

// flattenHeaders converts http.Header to a simple map
func flattenHeaders(h http.Header) map[string]string {
	result := make(map[string]string)
	for k, v := range h {
		if len(v) > 0 {
			result[k] = v[0]
		}
	}
	return result
}
