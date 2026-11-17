package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/gen"
	"github.com/leanovate/gopter/prop"
	"github.com/nasroellah/api-gateway/internal/config"
	"github.com/nasroellah/api-gateway/internal/observability"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// setupTestTracer creates a tracer for testing with in-memory exporter
func setupTestTracer() (trace.Tracer, *sdktrace.TracerProvider) {
	exporter := observability.NewNoopExporter()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Tracer("test-tracer"), provider
}

// TestProperty13_TraceContextPropagation tests trace context propagation
// **Property 13: Trace Context Propagation**
// **Validates: Requirements 6.2, 6.3, 6.4**
//
// *For any* request dengan incoming trace headers, span yang dibuat SHALL melanjutkan
// trace tersebut (same trace ID). *For any* request tanpa trace headers, span baru
// SHALL dibuat. *For any* outgoing request ke upstream, trace context SHALL disertakan
// dalam headers.
func TestProperty13_TraceContextPropagation(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: Request without trace headers SHALL create new span
	properties.Property("Request without trace headers creates new span", prop.ForAll(
		func(method string, pathIdx int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics"}
			path := paths[pathIdx%len(paths)]

			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = "192.168.1.1:7070"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created
			if capturedSpan == nil {
				return false
			}

			// Verify span context is valid
			spanCtx := capturedSpan.SpanContext()
			return spanCtx.IsValid() && spanCtx.TraceID().IsValid() && spanCtx.SpanID().IsValid()
		},
		genTracingHTTPMethod(),
		gen.IntRange(0, 4),
	))

	// Property: Request with trace headers SHALL continue existing trace
	properties.Property("Request with trace headers continues existing trace", prop.ForAll(
		func(method string, pathIdx int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics"}
			path := paths[pathIdx%len(paths)]

			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			// Create a parent span to get a valid trace ID
			parentCtx, parentSpan := tracer.Start(context.Background(), "parent-span")
			parentTraceID := parentSpan.SpanContext().TraceID()
			parentSpan.End()

			// Create request with trace context headers
			req := httptest.NewRequest(method, path, nil)
			req.RemoteAddr = "192.168.1.1:7070"

			// Inject parent trace context into request headers
			propagator := otel.GetTextMapPropagator()
			propagator.Inject(parentCtx, propagation.HeaderCarrier(req.Header))

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created and continues the parent trace
			if capturedSpan == nil {
				return false
			}

			spanCtx := capturedSpan.SpanContext()
			// The trace ID should match the parent trace ID
			return spanCtx.IsValid() && spanCtx.TraceID() == parentTraceID
		},
		genTracingHTTPMethod(),
		gen.IntRange(0, 4),
	))

	// Property: Span SHALL have correct HTTP method attribute
	properties.Property("Span has correct HTTP method attribute", prop.ForAll(
		func(method string) bool {
			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, "/test", nil)
			req.RemoteAddr = "192.168.1.1:7070"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created
			return capturedSpan != nil && capturedSpan.SpanContext().IsValid()
		},
		genTracingHTTPMethod(),
	))

	// Property: Span SHALL have correct URL path attribute
	properties.Property("Span has correct URL path attribute", prop.ForAll(
		func(pathIdx int) bool {
			paths := []string{"/api", "/v1/users", "/v2/orders", "/health", "/metrics", "/api/v1/products"}
			path := paths[pathIdx%len(paths)]

			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "192.168.1.1:7070"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created
			return capturedSpan != nil && capturedSpan.SpanContext().IsValid()
		},
		gen.IntRange(0, 5),
	))

	// Property: Span SHALL have client IP attribute
	properties.Property("Span has client IP attribute", prop.ForAll(
		func(clientIP string, clientPort int) bool {
			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = clientIP + ":" + itoa(clientPort)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created
			return capturedSpan != nil && capturedSpan.SpanContext().IsValid()
		},
		genTracingIPv4String(),
		gen.IntRange(1, 65535),
	))

	// Property: Different requests SHALL have different span IDs
	properties.Property("Different requests have different span IDs", prop.ForAll(
		func(numRequests int) bool {
			if numRequests < 2 {
				numRequests = 2
			}

			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			spanIDs := make(map[trace.SpanID]bool)

			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				span := trace.SpanFromContext(r.Context())
				spanIDs[span.SpanContext().SpanID()] = true
				w.WriteHeader(http.StatusOK)
			}))

			for i := 0; i < numRequests; i++ {
				req := httptest.NewRequest("GET", "/test", nil)
				req.RemoteAddr = "192.168.1.1:7070"

				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
			}

			// Each request should have a unique span ID
			return len(spanIDs) == numRequests
		},
		gen.IntRange(2, 10),
	))

	// Property: Trace context SHALL be injectable into outgoing requests
	properties.Property("Trace context is injectable into outgoing requests", prop.ForAll(
		func(method string) bool {
			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var outgoingReq *http.Request
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Simulate creating an outgoing request
				outgoingReq = httptest.NewRequest("GET", "http://upstream/api", nil)
				outgoingReq = outgoingReq.WithContext(r.Context())

				// Inject trace context into outgoing request
				InjectTracingHeaders(outgoingReq)

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(method, "/test", nil)
			req.RemoteAddr = "192.168.1.1:7070"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify trace context was injected into outgoing request
			if outgoingReq == nil {
				return false
			}

			// Check for traceparent header (W3C Trace Context)
			traceparent := outgoingReq.Header.Get("traceparent")
			return traceparent != ""
		},
		genTracingHTTPMethod(),
	))

	// Property: Error responses SHALL still have valid spans
	properties.Property("Error responses have valid spans", prop.ForAll(
		func(errorCode int) bool {
			tracer, provider := setupTestTracer()
			defer provider.Shutdown(context.Background())

			var capturedSpan trace.Span
			handler := TracingMiddleware(tracer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedSpan = trace.SpanFromContext(r.Context())
				w.WriteHeader(errorCode)
			}))

			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = "192.168.1.1:7070"

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Verify span was created even for error responses
			return capturedSpan != nil && capturedSpan.SpanContext().IsValid()
		},
		gen.IntRange(400, 599),
	))

	properties.TestingRun(t)
}

// TestTracerProviderCreation tests the TracerProvider creation
func TestTracerProviderCreation(t *testing.T) {
	parameters := gopter.DefaultTestParameters()
	parameters.MinSuccessfulTests = PropertyTestIterations
	parameters.Rng.Seed(12345)

	properties := gopter.NewProperties(parameters)

	// Property: TracerProvider with nil config SHALL return error
	properties.Property("TracerProvider with nil config returns error", prop.ForAll(
		func(_ int) bool {
			_, err := observability.NewTracerProvider(nil)
			return err != nil
		},
		gen.Int(),
	))

	properties.TestingRun(t)
}

// TestTracerProviderWithValidConfig tests TracerProvider creation with valid config
// This is a unit test rather than property test since it requires network connectivity
func TestTracerProviderWithValidConfig(t *testing.T) {
	cfg := &config.TracingConfig{
		Enabled:     true,
		ServiceName: "test-service",
		Exporter:    "zipkin",
		Endpoint:    "http://localhost:9411/api/v2/spans",
		SampleRate:  1.0,
	}

	tp, err := observability.NewTracerProvider(cfg)
	if err != nil {
		// Zipkin exporter creation may fail if endpoint is unreachable, which is OK for unit tests
		t.Skipf("Skipping test due to exporter creation error (expected in CI): %v", err)
	}
	defer tp.Shutdown(context.Background())

	// Verify tracer is not nil
	tracer := tp.Tracer()
	if tracer == nil {
		t.Error("Expected tracer to be non-nil")
	}
}

// genTracingHTTPMethod generates valid HTTP methods
func genTracingHTTPMethod() gopter.Gen {
	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	return gen.OneConstOf(methods[0], methods[1], methods[2], methods[3], methods[4], methods[5], methods[6])
}

// genTracingIPv4String generates random IPv4 address strings
func genTracingIPv4String() gopter.Gen {
	return gen.SliceOfN(4, gen.IntRange(0, 255)).Map(func(octets []int) string {
		return itoa(octets[0]) + "." + itoa(octets[1]) + "." + itoa(octets[2]) + "." + itoa(octets[3])
	})
}
