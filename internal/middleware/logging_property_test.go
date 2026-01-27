package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LogEntry represents a parsed JSON log entry
type LogEntry struct {
	Timestamp  string  `json:"timestamp"`
	Level      string  `json:"level"`
	Message    string  `json:"message"`
	RequestID  string  `json:"request_id"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	StatusCode int     `json:"status_code"`
	Latency    float64 `json:"latency"`
	ClientIP   string  `json:"client_ip"`
}

// createTestLogger creates a zap logger that writes to a buffer
func createTestLogger(buf *bytes.Buffer) *zap.Logger {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.MillisDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	encoder := zapcore.NewJSONEncoder(encoderConfig)
	writer := zapcore.AddSync(buf)
	core := zapcore.NewCore(encoder, writer, zapcore.DebugLevel)

	return zap.New(core)
}

// TestProperty9_LogFieldsCompleteness tests that all required fields are present in log entries
// **Property 9: Log Fields Completeness**
// **Validates: Requirements 4.2**
//
// *For any* request yang diproses, log entry yang dihasilkan SHALL mengandung
// semua required fields: timestamp, request_id, method, path, status_code, latency, client_ip.
func TestProperty9_LogFieldsCompleteness(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: All required fields SHALL be present in log entry
	properties.Property("All required fields are present in log entry", prop.ForAll(
		func(method string, pathIdx int, requestID string, clientIP string, clientPort int, statusCode int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics"}
			path := paths[pathIdx%len(paths)]

			// Create buffer to capture log output
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			// Create handler that returns the specified status code
			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			// Create request with specified parameters
			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			// Set request ID in context
			ctx := SetRequestID(req.Context(), requestID)
			req = req.WithContext(ctx)

			// Execute request
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Parse log entry
			logOutput := buf.String()
			if logOutput == "" {
				return false
			}

			var entry LogEntry
			if err := json.Unmarshal([]byte(logOutput), &entry); err != nil {
				return false
			}

			// Verify all required fields are present and non-empty
			hasTimestamp := entry.Timestamp != ""
			hasRequestID := entry.RequestID == requestID
			hasMethod := entry.Method == method
			hasPath := entry.Path == path
			hasStatusCode := entry.StatusCode == statusCode
			hasLatency := entry.Latency >= 0 // Latency should be non-negative
			hasClientIP := entry.ClientIP != ""

			return hasTimestamp && hasRequestID && hasMethod && hasPath && hasStatusCode && hasLatency && hasClientIP
		},
		genLoggingHTTPMethod(),
		gen.IntRange(0, 4),
		genLoggingRequestID(),
		genLoggingIPv4String(),
		gen.IntRange(1, 65535),
		gen.IntRange(100, 599),
	))

	// Property: Request ID SHALL match the one from context
	properties.Property("Request ID in log matches context request ID", prop.ForAll(
		func(requestID string) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"
			ctx := SetRequestID(req.Context(), requestID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var entry LogEntry
			if err := json.Unmarshal([]byte(buf.String()), &entry); err != nil {
				return false
			}

			return entry.RequestID == requestID
		},
		genLoggingRequestID(),
	))

	// Property: Method SHALL match the request method
	properties.Property("Method in log matches request method", prop.ForAll(
		func(method string) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var entry LogEntry
			if err := json.Unmarshal([]byte(buf.String()), &entry); err != nil {
				return false
			}

			return entry.Method == method
		},
		genLoggingHTTPMethod(),
	))

	// Property: Path SHALL match the request path
	properties.Property("Path in log matches request path", prop.ForAll(
		func(pathIdx int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics", "/api/v1/products"}
			path := paths[pathIdx%len(paths)]

			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "192.168.1.1:8080"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var entry LogEntry
			if err := json.Unmarshal([]byte(buf.String()), &entry); err != nil {
				return false
			}

			return entry.Path == path
		},
		gen.IntRange(0, 5),
	))

	// Property: Status code SHALL match the response status code
	properties.Property("Status code in log matches response status code", prop.ForAll(
		func(statusCode int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var entry LogEntry
			if err := json.Unmarshal([]byte(buf.String()), &entry); err != nil {
				return false
			}

			return entry.StatusCode == statusCode
		},
		gen.IntRange(100, 599),
	))

	// Property: Client IP SHALL be extracted correctly
	properties.Property("Client IP in log is extracted correctly", prop.ForAll(
		func(clientIP string, clientPort int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var entry LogEntry
			if err := json.Unmarshal([]byte(buf.String()), &entry); err != nil {
				return false
			}

			// Client IP should contain the IP address
			return strings.Contains(entry.ClientIP, clientIP)
		},
		genLoggingIPv4String(),
		gen.IntRange(1, 65535),
	))

	properties.TestingRun(t)
}

// genLoggingHTTPMethod generates valid HTTP methods
func genLoggingHTTPMethod() gopter.Gen {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	return gen.OneConstOf(methods[0], methods[1], methods[2], methods[3], methods[4], methods[5], methods[6])
}

// genLoggingIPv4String generates random IPv4 address strings
func genLoggingIPv4String() gopter.Gen {
	return gen.SliceOfN(4, gen.IntRange(0, 255)).Map(func(octets []int) string {
		return itoa(octets[0]) + "." + itoa(octets[1]) + "." + itoa(octets[2]) + "." + itoa(octets[3])
	})
}

// genLoggingRequestID generates UUID-like request IDs
func genLoggingRequestID() gopter.Gen {
	return gen.Identifier().Map(func(s string) string {
		if len(s) < 8 {
			return "req-" + s + "-12345678"
		}
		return "req-" + s[:8]
	})
}


// TestProperty10_JSONLogFormatValidity tests that all log entries are valid JSON
// **Property 10: JSON Log Format Validity**
// **Validates: Requirements 4.3**
//
// *For any* log entry yang dihasilkan, output SHALL merupakan valid JSON
// yang dapat di-parse tanpa error.
func TestProperty10_JSONLogFormatValidity(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Log output SHALL be valid JSON
	properties.Property("Log output is valid JSON", prop.ForAll(
		func(method string, pathIdx int, requestID string, clientIP string, clientPort int, statusCode int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics"}
			path := paths[pathIdx%len(paths)]

			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)
			ctx := SetRequestID(req.Context(), requestID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			logOutput := buf.String()
			if logOutput == "" {
				return false
			}

			// Verify output is valid JSON
			var jsonData map[string]interface{}
			err := json.Unmarshal([]byte(logOutput), &jsonData)
			return err == nil
		},
		genLoggingHTTPMethod(),
		gen.IntRange(0, 4),
		genLoggingRequestID(),
		genLoggingIPv4String(),
		gen.IntRange(1, 65535),
		gen.IntRange(100, 599),
	))

	// Property: JSON log SHALL contain expected keys
	properties.Property("JSON log contains expected keys", prop.ForAll(
		func(method string, statusCode int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			req := httptest.NewRequest(method, "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"
			ctx := SetRequestID(req.Context(), "test-request-id")
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var jsonData map[string]interface{}
			if err := json.Unmarshal([]byte(buf.String()), &jsonData); err != nil {
				return false
			}

			// Check for required keys
			requiredKeys := []string{"timestamp", "level", "message", "request_id", "method", "path", "status_code", "latency", "client_ip"}
			for _, key := range requiredKeys {
				if _, exists := jsonData[key]; !exists {
					return false
				}
			}
			return true
		},
		genLoggingHTTPMethod(),
		gen.IntRange(100, 599),
	))

	// Property: JSON values SHALL have correct types
	properties.Property("JSON values have correct types", prop.ForAll(
		func(statusCode int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(statusCode)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"
			ctx := SetRequestID(req.Context(), "test-request-id")
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var jsonData map[string]interface{}
			if err := json.Unmarshal([]byte(buf.String()), &jsonData); err != nil {
				return false
			}

			// Verify types
			_, timestampIsString := jsonData["timestamp"].(string)
			_, levelIsString := jsonData["level"].(string)
			_, messageIsString := jsonData["message"].(string)
			_, requestIDIsString := jsonData["request_id"].(string)
			_, methodIsString := jsonData["method"].(string)
			_, pathIsString := jsonData["path"].(string)
			_, statusCodeIsNumber := jsonData["status_code"].(float64) // JSON numbers are float64
			_, latencyIsNumber := jsonData["latency"].(float64)
			_, clientIPIsString := jsonData["client_ip"].(string)

			return timestampIsString && levelIsString && messageIsString &&
				requestIDIsString && methodIsString && pathIsString &&
				statusCodeIsNumber && latencyIsNumber && clientIPIsString
		},
		gen.IntRange(100, 599),
	))

	// Property: Multiple log entries SHALL each be valid JSON
	properties.Property("Multiple log entries are each valid JSON", prop.ForAll(
		func(numRequests int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddleware(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))

			// Make multiple requests
			for i := 0; i < numRequests; i++ {
				req := httptest.NewRequest("GET", "/test", nil)
				req.RemoteAddr = "192.168.1.1:8080"
				ctx := SetRequestID(req.Context(), "req-"+itoa(i))
				req = req.WithContext(ctx)

				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}

			// Each line should be valid JSON
			logOutput := buf.String()
			lines := strings.Split(strings.TrimSpace(logOutput), "\n")

			if len(lines) != numRequests {
				return false
			}

			for _, line := range lines {
				var jsonData map[string]interface{}
				if err := json.Unmarshal([]byte(line), &jsonData); err != nil {
					return false
				}
			}
			return true
		},
		gen.IntRange(1, 10),
	))

	// Property: Error responses SHALL produce valid JSON logs
	properties.Property("Error responses produce valid JSON logs", prop.ForAll(
		func(errorCode int) bool {
			var buf bytes.Buffer
			logger := createTestLogger(&buf)

			handler := LoggingMiddlewareWithErrors(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(errorCode)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "192.168.1.1:8080"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			var jsonData map[string]interface{}
			err := json.Unmarshal([]byte(buf.String()), &jsonData)
			return err == nil
		},
		gen.IntRange(400, 599), // Error status codes
	))

	properties.TestingRun(t)
}
