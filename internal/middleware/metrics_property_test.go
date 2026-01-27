package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestProperty11_MetricsRecording tests that metrics are recorded correctly
// **Property 11: Metrics Recording**
// **Validates: Requirements 5.2, 5.3**
//
// *For any* request yang diproses, metrics counter SHALL bertambah dengan labels
// yang benar (method, path, status_code) dan histogram latency SHALL mencatat
// durasi dengan labels (method, path).
func TestProperty11_MetricsRecording(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Request counter SHALL increment for each request
	properties.Property("Request counter increments for each request", prop.ForAll(
		func(method string, pathIdx int, statusCode int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics"}
			path := paths[pathIdx%len(paths)]

			// Create a new registry for isolation
			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			// Create handler that returns the specified status code
			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			// Create and execute request
			req := httptest.NewRequest(method, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Get the counter value
			counterValue := getCounterValue(metrics.RequestsTotal, method, path, statusCode)

			// Counter should be exactly 1
			return counterValue == 1
		},
		genMetricsHTTPMethod(),
		gen.IntRange(0, 4),
		gen.IntRange(100, 599),
	))

	// Property: Multiple requests SHALL increment counter correctly
	properties.Property("Multiple requests increment counter correctly", prop.ForAll(
		func(numRequests int) bool {
			if numRequests < 1 || numRequests > 20 {
				return true
			}

			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Make multiple requests
			for i := 0; i < numRequests; i++ {
				req := httptest.NewRequest("GET", "/test", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}

			// Counter should equal number of requests
			counterValue := getCounterValue(metrics.RequestsTotal, "GET", "/test", http.StatusOK)
			return counterValue == float64(numRequests)
		},
		gen.IntRange(1, 20),
	))

	// Property: Different methods SHALL have separate counters
	properties.Property("Different methods have separate counters", prop.ForAll(
		func(numRequests int) bool {
			if numRequests < 1 || numRequests > 10 {
				return true
			}

			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			methods := []string{"GET", "POST", "PUT", "DELETE"}

			// Make requests with different methods
			for _, method := range methods {
				for i := 0; i < numRequests; i++ {
					req := httptest.NewRequest(method, "/test", nil)
					rr := httptest.NewRecorder()
					handler.ServeHTTP(rr, req)
				}
			}

			// Each method should have its own counter
			for _, method := range methods {
				counterValue := getCounterValue(metrics.RequestsTotal, method, "/test", http.StatusOK)
				if counterValue != float64(numRequests) {
					return false
				}
			}
			return true
		},
		gen.IntRange(1, 10),
	))

	// Property: Different paths SHALL have separate counters
	properties.Property("Different paths have separate counters", prop.ForAll(
		func(numRequests int) bool {
			if numRequests < 1 || numRequests > 10 {
				return true
			}

			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			paths := []string{"/api", "/users", "/orders"}

			// Make requests with different paths
			for _, path := range paths {
				for i := 0; i < numRequests; i++ {
					req := httptest.NewRequest("GET", path, nil)
					rr := httptest.NewRecorder()
					handler.ServeHTTP(rr, req)
				}
			}

			// Each path should have its own counter
			for _, path := range paths {
				counterValue := getCounterValue(metrics.RequestsTotal, "GET", path, http.StatusOK)
				if counterValue != float64(numRequests) {
					return false
				}
			}
			return true
		},
		gen.IntRange(1, 10),
	))

	// Property: Different status codes SHALL have separate counters
	properties.Property("Different status codes have separate counters", prop.ForAll(
		func(numRequests int) bool {
			if numRequests < 1 || numRequests > 10 {
				return true
			}

			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			statusCodes := []int{200, 400, 404, 500}

			// Make requests with different status codes
			for _, statusCode := range statusCodes {
				sc := statusCode // capture for closure
				handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(sc)
				}))

				for i := 0; i < numRequests; i++ {
					req := httptest.NewRequest("GET", "/test", nil)
					rr := httptest.NewRecorder()
					handler.ServeHTTP(rr, req)
				}
			}

			// Each status code should have its own counter
			for _, statusCode := range statusCodes {
				counterValue := getCounterValue(metrics.RequestsTotal, "GET", "/test", statusCode)
				if counterValue != float64(numRequests) {
					return false
				}
			}
			return true
		},
		gen.IntRange(1, 10),
	))

	// Property: Histogram SHALL record latency for each request
	properties.Property("Histogram records latency for each request", prop.ForAll(
		func(method string, pathIdx int, numRequests int) bool {
			if numRequests < 1 || numRequests > 20 {
				return true
			}

			paths := []string{"/api", "/v1/users", "/v2/orders"}
			path := paths[pathIdx%len(paths)]

			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Make requests
			for i := 0; i < numRequests; i++ {
				req := httptest.NewRequest(method, path, nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}

			// Histogram count should equal number of requests
			histogramCount := getHistogramCount(metrics.RequestDuration, method, path)
			return histogramCount == uint64(numRequests)
		},
		genMetricsHTTPMethod(),
		gen.IntRange(0, 2),
		gen.IntRange(1, 20),
	))

	// Property: Histogram latency SHALL be non-negative
	properties.Property("Histogram latency is non-negative", prop.ForAll(
		func(method string) bool {
			registry := prometheus.NewRegistry()
			metrics := observability.NewMetrics(registry)

			handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, "/test", nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Get histogram sum (total latency)
			histogramSum := getHistogramSum(metrics.RequestDuration, method, "/test")
			return histogramSum >= 0
		},
		genMetricsHTTPMethod(),
	))

	properties.TestingRun(t)
}

// genMetricsHTTPMethod generates valid HTTP methods
func genMetricsHTTPMethod() gopter.Gen {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	return gen.OneConstOf(methods[0], methods[1], methods[2], methods[3], methods[4], methods[5], methods[6])
}

// getCounterValue retrieves the counter value for given labels
func getCounterValue(counter *prometheus.CounterVec, method, path string, statusCode int) float64 {
	statusCodeStr := statusCodeToString(statusCode)
	metric, err := counter.GetMetricWithLabelValues(method, path, statusCodeStr)
	if err != nil {
		return 0
	}

	var m dto.Metric
	if err := metric.Write(&m); err != nil {
		return 0
	}

	if m.Counter != nil {
		return m.Counter.GetValue()
	}
	return 0
}

// getHistogramCount retrieves the histogram sample count for given labels
func getHistogramCount(histogram *prometheus.HistogramVec, method, path string) uint64 {
	observer, err := histogram.GetMetricWithLabelValues(method, path)
	if err != nil {
		return 0
	}

	// Type assert to get the histogram
	h, ok := observer.(prometheus.Histogram)
	if !ok {
		return 0
	}

	var m dto.Metric
	if err := h.Write(&m); err != nil {
		return 0
	}

	if m.Histogram != nil {
		return m.Histogram.GetSampleCount()
	}
	return 0
}

// getHistogramSum retrieves the histogram sum for given labels
func getHistogramSum(histogram *prometheus.HistogramVec, method, path string) float64 {
	observer, err := histogram.GetMetricWithLabelValues(method, path)
	if err != nil {
		return 0
	}

	h, ok := observer.(prometheus.Histogram)
	if !ok {
		return 0
	}

	var m dto.Metric
	if err := h.Write(&m); err != nil {
		return 0
	}

	if m.Histogram != nil {
		return m.Histogram.GetSampleSum()
	}
	return 0
}

// statusCodeToString converts status code to string for labels
func statusCodeToString(code int) string {
	switch {
	case code < 10:
		return "00" + metricsItoa(code)
	case code < 100:
		return "0" + metricsItoa(code)
	default:
		return metricsItoa(code)
	}
}

// metricsItoa converts int to string without importing strconv
func metricsItoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + metricsItoa(-i)
	}
	var b [20]byte
	bp := len(b) - 1
	for i > 0 {
		b[bp] = byte('0' + i%10)
		bp--
		i /= 10
	}
	return string(b[bp+1:])
}
