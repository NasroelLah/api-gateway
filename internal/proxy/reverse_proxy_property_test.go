package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/nasroellah/api-gateway/internal/config"
)

const PropertyTestIterations = 100

// TestProperty4_1_RequestBodyPassthrough tests request body passthrough
// **Property 4.1: Request Body Passthrough**
// **Validates: Requirements 1.2.3**
//
// *For any* request dengan body, body yang diterima upstream SHALL identik
// dengan body yang dikirim client (byte-for-byte equality).
func TestProperty4_1_RequestBodyPassthrough(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Request body SHALL be forwarded byte-for-byte
	properties.Property("Request body is forwarded byte-for-byte", prop.ForAll(
		func(bodyBytes []byte) bool {
			var receivedBody []byte

			// Create upstream server that captures the body
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				receivedBody, err = io.ReadAll(r.Body)
				if err != nil {
					t.Logf("Failed to read body: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			// Create proxy manager
			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			// Get proxy
			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			// Create request with body
			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()

			// Execute proxy
			proxy.ServeHTTP(rec, req)

			// Verify body was forwarded byte-for-byte
			return bytes.Equal(bodyBytes, receivedBody)
		},
		gen.SliceOf(gen.UInt8()),
	))

	// Property: Empty body SHALL be forwarded as empty
	properties.Property("Empty body is forwarded as empty", prop.ForAll(
		func(_ int) bool {
			var receivedBody []byte

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				receivedBody, err = io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return len(receivedBody) == 0
		},
		gen.Int(),
	))

	// Property: Large body SHALL be forwarded completely
	properties.Property("Large body is forwarded completely", prop.ForAll(
		func(seed byte) bool {
			// Generate a large body (1KB - 10KB)
			size := int(seed)*40 + 1024
			bodyBytes := make([]byte, size)
			for i := range bodyBytes {
				bodyBytes[i] = byte(i % 256)
			}

			var receivedBody []byte

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				receivedBody, err = io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(bodyBytes))
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return bytes.Equal(bodyBytes, receivedBody)
		},
		gen.UInt8(),
	))

	properties.TestingRun(t)
}


// TestProperty4_2_HeaderPassthrough tests header passthrough
// **Property 4.2: Header Passthrough**
// **Validates: Requirements 1.2.1, 1.2.2, 1.2.4**
//
// *For any* request dengan headers (kecuali hop-by-hop headers), semua headers
// termasuk Authorization dan Content-Type SHALL diteruskan ke upstream tanpa modifikasi.
func TestProperty4_2_HeaderPassthrough(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Authorization header SHALL be forwarded without modification
	properties.Property("Authorization header is forwarded without modification", prop.ForAll(
		func(authValue string) bool {
			if authValue == "" {
				return true // Skip empty values
			}

			var receivedAuth string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set("Authorization", authValue)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedAuth == authValue
		},
		gen.AnyString().SuchThat(func(s string) bool { return len(s) > 0 && len(s) < 1000 }),
	))

	// Property: Content-Type header SHALL be forwarded without modification
	properties.Property("Content-Type header is forwarded without modification", prop.ForAll(
		func(contentType string) bool {
			if contentType == "" {
				return true
			}

			var receivedContentType string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedContentType = r.Header.Get("Content-Type")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader("test body"))
			req.Header.Set("Content-Type", contentType)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedContentType == contentType
		},
		gen.OneConstOf(
			"application/json",
			"application/xml",
			"text/plain",
			"text/html",
			"multipart/form-data",
			"application/x-www-form-urlencoded",
		),
	))

	// Property: Custom headers SHALL be forwarded
	properties.Property("Custom headers are forwarded", prop.ForAll(
		func(headerName, headerValue string) bool {
			// Skip invalid header names
			if headerName == "" || headerValue == "" {
				return true
			}
			// Skip hop-by-hop headers
			for _, h := range HopByHopHeaders {
				if strings.EqualFold(headerName, h) {
					return true
				}
			}

			var receivedValue string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedValue = r.Header.Get(headerName)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set(headerName, headerValue)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedValue == headerValue
		},
		gen.OneConstOf("X-Custom-Header", "X-Request-Id", "X-Correlation-Id", "Accept", "Accept-Language"),
		gen.AnyString().SuchThat(func(s string) bool { return len(s) > 0 && len(s) < 500 }),
	))

	// Property: Hop-by-hop headers SHALL NOT be forwarded
	properties.Property("Hop-by-hop headers are not forwarded", prop.ForAll(
		func(hopHeaderIdx int) bool {
			hopHeader := HopByHopHeaders[hopHeaderIdx%len(HopByHopHeaders)]
			var receivedValue string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedValue = r.Header.Get(hopHeader)
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.Header.Set(hopHeader, "test-value")
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			// Hop-by-hop headers should be removed
			return receivedValue == ""
		},
		gen.IntRange(0, len(HopByHopHeaders)-1),
	))

	properties.TestingRun(t)
}


// TestProperty4_3_HTTPMethodPassthrough tests HTTP method passthrough
// **Property 4.3: HTTP Method Passthrough**
// **Validates: Requirements 1.2.5**
//
// *For any* HTTP request dengan method apapun (GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD),
// method yang sama SHALL diteruskan ke upstream.
func TestProperty4_3_HTTPMethodPassthrough(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	httpMethods := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodOptions,
		http.MethodHead,
	}

	// Property: HTTP method SHALL be forwarded without modification
	properties.Property("HTTP method is forwarded without modification", prop.ForAll(
		func(methodIdx int) bool {
			method := httpMethods[methodIdx%len(httpMethods)]
			var receivedMethod string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedMethod = r.Method
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			var body io.Reader
			if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
				body = strings.NewReader("test body")
			}

			req := httptest.NewRequest(method, "/test", body)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedMethod == method
		},
		gen.IntRange(0, len(httpMethods)-1),
	))

	// Property: All standard HTTP methods SHALL be supported
	properties.Property("All standard HTTP methods are supported", prop.ForAll(
		func(_ int) bool {
			for _, method := range httpMethods {
				var receivedMethod string

				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					receivedMethod = r.Method
					w.WriteHeader(http.StatusOK)
				}))

				cfg := &config.ProxyConfig{
					MaxIdleConns:        100,
					MaxIdleConnsPerHost: 10,
					Upstreams: []config.UpstreamConfig{
						{PathPrefix: "/", Target: upstream.URL},
					},
				}
				pm := NewProxyManager(cfg)

				proxy, ok := pm.GetProxy("/test")
				if !ok {
					pm.Close()
					upstream.Close()
					return false
				}

				var body io.Reader
				if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
					body = strings.NewReader("test body")
				}

				req := httptest.NewRequest(method, "/test", body)
				rec := httptest.NewRecorder()

				proxy.ServeHTTP(rec, req)

				pm.Close()
				upstream.Close()

				if receivedMethod != method {
					return false
				}
			}
			return true
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}


// TestProperty4_4_QueryParametersPassthrough tests query parameters passthrough
// **Property 4.4: Query Parameters Passthrough**
// **Validates: Requirements 1.2.6**
//
// *For any* request dengan query parameters, query string yang diterima upstream
// SHALL identik dengan query string dari client.
func TestProperty4_4_QueryParametersPassthrough(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Query parameters SHALL be forwarded without modification
	properties.Property("Query parameters are forwarded without modification", prop.ForAll(
		func(key, value string) bool {
			if key == "" {
				return true // Skip empty keys
			}

			var receivedQuery string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			// Build query string
			queryString := url.Values{}
			queryString.Set(key, value)
			expectedQuery := queryString.Encode()

			req := httptest.NewRequest(http.MethodGet, "/test?"+expectedQuery, nil)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedQuery == expectedQuery
		},
		gen.AlphaString().SuchThat(func(s string) bool { return len(s) > 0 && len(s) < 50 }),
		gen.AlphaString().SuchThat(func(s string) bool { return len(s) < 100 }),
	))

	// Property: Multiple query parameters SHALL be forwarded
	properties.Property("Multiple query parameters are forwarded", prop.ForAll(
		func(numParams int) bool {
			if numParams < 1 || numParams > 10 {
				return true
			}

			var receivedQuery string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			// Build query string with multiple params
			queryString := url.Values{}
			for i := 0; i < numParams; i++ {
				queryString.Set("param"+string(rune('a'+i)), "value"+string(rune('0'+i)))
			}
			expectedQuery := queryString.Encode()

			req := httptest.NewRequest(http.MethodGet, "/test?"+expectedQuery, nil)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedQuery == expectedQuery
		},
		gen.IntRange(1, 10),
	))

	// Property: Empty query string SHALL remain empty
	properties.Property("Empty query string remains empty", prop.ForAll(
		func(_ int) bool {
			var receivedQuery string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedQuery == ""
		},
		gen.Int(),
	))

	// Property: Special characters in query parameters SHALL be preserved
	properties.Property("Special characters in query parameters are preserved", prop.ForAll(
		func(idx int) bool {
			specialValues := []string{
				"hello world",
				"foo=bar",
				"test&value",
				"special+chars",
				"percent%20encoded",
			}
			value := specialValues[idx%len(specialValues)]

			var receivedValue string

			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedValue = r.URL.Query().Get("key")
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				Upstreams: []config.UpstreamConfig{
					{PathPrefix: "/", Target: upstream.URL},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			proxy, ok := pm.GetProxy("/test")
			if !ok {
				return false
			}

			queryString := url.Values{}
			queryString.Set("key", value)

			req := httptest.NewRequest(http.MethodGet, "/test?"+queryString.Encode(), nil)
			rec := httptest.NewRecorder()

			proxy.ServeHTTP(rec, req)

			return receivedValue == value
		},
		gen.IntRange(0, 4),
	))

	properties.TestingRun(t)
}


// TestProperty4_5_DynamicUpstreamRouting tests dynamic upstream routing
// **Property 4.5: Dynamic Upstream Routing**
// **Validates: Requirements 1.1.1, 1.1.2**
//
// *For any* request dengan header X-Upstream-Target yang berisi URL valid dan host
// yang ada dalam allowed_hosts, request SHALL diteruskan ke target tersebut.
func TestProperty4_5_DynamicUpstreamRouting(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Valid dynamic upstream target SHALL be routed correctly
	properties.Property("Valid dynamic upstream target is routed correctly", prop.ForAll(
		func(pathIdx int) bool {
			paths := []string{"test", "api", "users", "data", "health", "status", "v1", "v2"}
			pathSuffix := paths[pathIdx%len(paths)]

			var receivedPath string

			// Create upstream server
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer upstream.Close()

			// Parse upstream URL to get host
			upstreamURL, _ := url.Parse(upstream.URL)

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{upstreamURL.Host},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			// Get dynamic proxy
			proxy, err := pm.GetDynamicProxy(upstream.URL)
			if err != nil {
				t.Logf("GetDynamicProxy error: %v", err)
				return false
			}

			// Create request - use the full URL path
			targetPath := "/" + pathSuffix
			req := httptest.NewRequest(http.MethodGet, targetPath, nil)
			rec := httptest.NewRecorder()

			// Execute proxy
			proxy.ServeHTTP(rec, req)

			// Check if the path was forwarded correctly
			if receivedPath != targetPath {
				t.Logf("Expected path %s, got %s", targetPath, receivedPath)
				return false
			}
			return true
		},
		gen.IntRange(0, 7),
	))

	// Property: Wildcard host pattern SHALL match subdomains
	properties.Property("Wildcard host pattern matches subdomains", prop.ForAll(
		func(subdomainIdx int) bool {
			subdomains := []string{"api", "web", "app", "service", "backend", "frontend", "admin", "test"}
			subdomain := subdomains[subdomainIdx%len(subdomains)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{"*.example.com"},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			// Test that subdomain.example.com is allowed
			targetURL := "http://" + subdomain + ".example.com/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == nil
		},
		gen.IntRange(0, 7),
	))

	// Property: Exact host match SHALL be allowed
	properties.Property("Exact host match is allowed", prop.ForAll(
		func(idx int) bool {
			hosts := []string{
				"api.example.com",
				"service.internal.local",
				"backend.test.io",
			}
			host := hosts[idx%len(hosts)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{host},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			targetURL := "http://" + host + "/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == nil
		},
		gen.IntRange(0, 2),
	))

	// Property: Dynamic upstream disabled SHALL return error
	properties.Property("Dynamic upstream disabled returns error", prop.ForAll(
		func(_ int) bool {
			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled: false,
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			_, err := pm.GetDynamicProxy("http://example.com/test")

			return err == ErrDynamicUpstreamDisabled
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}


// TestProperty4_6_DynamicUpstreamValidation tests dynamic upstream validation
// **Property 4.6: Dynamic Upstream Validation**
// **Validates: Requirements 1.1.3**
//
// *For any* request dengan header X-Upstream-Target yang berisi host yang tidak ada
// dalam allowed_hosts, request SHALL ditolak dengan HTTP 400.
func TestProperty4_6_DynamicUpstreamValidation(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Host not in allowed list SHALL be rejected
	properties.Property("Host not in allowed list is rejected", prop.ForAll(
		func(idx int) bool {
			disallowedHosts := []string{
				"evil.com",
				"attacker.io",
				"malicious.net",
				"unauthorized.org",
			}
			host := disallowedHosts[idx%len(disallowedHosts)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{"allowed.example.com", "*.trusted.com"},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			targetURL := "http://" + host + "/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == ErrUpstreamNotAllowed
		},
		gen.IntRange(0, 3),
	))

	// Property: Empty allowed hosts SHALL reject all
	properties.Property("Empty allowed hosts rejects all", prop.ForAll(
		func(hostIdx int) bool {
			hosts := []string{"example.com", "test.io", "api.service.com", "backend.local", "app.internal"}
			host := hosts[hostIdx%len(hosts)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{}, // Empty
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			targetURL := "http://" + host + "/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == ErrUpstreamNotAllowed
		},
		gen.IntRange(0, 4),
	))

	// Property: Invalid URL SHALL be rejected
	properties.Property("Invalid URL is rejected", prop.ForAll(
		func(idx int) bool {
			invalidURLs := []string{
				"",
				"not-a-url",
				"ftp://example.com",
				"://missing-scheme.com",
				"http://",
			}
			invalidURL := invalidURLs[idx%len(invalidURLs)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{"*"},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			err := pm.ValidateDynamicTarget(invalidURL)

			// Should return an error (either invalid target or not allowed)
			return err != nil
		},
		gen.IntRange(0, 4),
	))

	// Property: Wildcard SHALL NOT match different domain
	properties.Property("Wildcard does not match different domain", prop.ForAll(
		func(subdomainIdx int) bool {
			subdomains := []string{"api", "web", "app", "service", "backend", "frontend", "admin", "test"}
			subdomain := subdomains[subdomainIdx%len(subdomains)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{"*.example.com"},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			// Test that subdomain.different.com is NOT allowed
			targetURL := "http://" + subdomain + ".different.com/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == ErrUpstreamNotAllowed
		},
		gen.IntRange(0, 7),
	))

	// Property: Nested subdomain SHALL NOT match single wildcard
	properties.Property("Nested subdomain does not match single wildcard", prop.ForAll(
		func(sub1Idx, sub2Idx int) bool {
			subs := []string{"api", "web", "app", "svc", "be", "fe", "adm", "tst"}
			sub1 := subs[sub1Idx%len(subs)]
			sub2 := subs[sub2Idx%len(subs)]

			cfg := &config.ProxyConfig{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				DynamicUpstream: config.DynamicUpstreamConfig{
					Enabled:      true,
					HeaderName:   "X-Upstream-Target",
					AllowedHosts: []string{"*.example.com"},
				},
			}
			pm := NewProxyManager(cfg)
			defer pm.Close()

			// Test that sub1.sub2.example.com is NOT allowed (nested subdomain)
			targetURL := "http://" + sub1 + "." + sub2 + ".example.com/test"
			err := pm.ValidateDynamicTarget(targetURL)

			return err == ErrUpstreamNotAllowed
		},
		gen.IntRange(0, 7),
		gen.IntRange(0, 7),
	))

	properties.TestingRun(t)
}
