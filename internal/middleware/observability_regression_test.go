package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type optionalWriter struct {
	header http.Header
	flushed bool
	hijacked bool
}

func (w *optionalWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (*optionalWriter) Write([]byte) (int, error) { return 0, nil }
func (*optionalWriter) WriteHeader(int)           {}
func (w *optionalWriter) Flush()                  { w.flushed = true }
func (w *optionalWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return nil, nil, nil
}

func TestObservabilityMiddlewarePreservesOptionalWriterInterfaces(t *testing.T) {
	cases := []struct {
		name string
		mw   func(http.Handler) http.Handler
		run  func(http.ResponseWriter) error
	}{
		{"logging", LoggingMiddleware(zap.NewNop()), func(w http.ResponseWriter) error {
			f, ok := w.(http.Flusher)
			if !ok { return errors.New("Flusher missing") }
			f.Flush()
			_, ok = w.(http.Hijacker)
			if !ok { return errors.New("Hijacker missing") }
			return nil
		}},
		{"metrics", MetricsMiddleware(observability.NewMetrics(nil)), func(w http.ResponseWriter) error {
			f, ok := w.(http.Flusher)
			if !ok { return errors.New("Flusher missing") }
			f.Flush()
			_, ok = w.(http.Hijacker)
			if !ok { return errors.New("Hijacker missing") }
			return nil
		}},
		{"tracing", TracingMiddleware(trace.NewNoopTracerProvider().Tracer("test")), func(w http.ResponseWriter) error {
			f, ok := w.(http.Flusher)
			if !ok { return errors.New("Flusher missing") }
			f.Flush()
			_, ok = w.(http.Hijacker)
			if !ok { return errors.New("Hijacker missing") }
			return nil
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			underlying := &optionalWriter{}
			h := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := tc.run(w); err != nil { t.Error(err) }
			}))
			h.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/events", nil))
			if !underlying.flushed { t.Fatal("Flush did not reach the underlying writer") }
		})
	}
}

func TestObservabilityMiddlewareSupportsControllerUnwrapAndHijack(t *testing.T) {
	middlewares := []struct {
		name string
		mw   func(http.Handler) http.Handler
	}{
		{"logging", LoggingMiddleware(zap.NewNop())},
		{"metrics", MetricsMiddleware(observability.NewMetrics(nil))},
		{"tracing", TracingMiddleware(trace.NewNoopTracerProvider().Tracer("test"))},
	}
	for _, tc := range middlewares {
		t.Run(tc.name, func(t *testing.T) {
			underlying := &optionalWriter{}
			var gotHijack bool
			h := tc.mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := http.NewResponseController(w).Flush(); err != nil { t.Errorf("ResponseController.Flush: %v", err) }
				_, _, err := http.NewResponseController(w).Hijack()
				if err != nil { t.Errorf("ResponseController.Hijack: %v", err) }
				gotHijack = underlying.hijacked
			}))
			h.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/upgrade", nil))
			if !underlying.flushed || !gotHijack { t.Fatal("optional writer methods did not reach underlying writer") }
		})
	}
}

func TestMetricsMiddlewareUsesBoundedRouteAndMethodLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := observability.NewMetrics(registry)
	router := chi.NewRouter()
	router.Use(MetricsMiddleware(metrics))
	router.Get("/users/{userID}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for i := range 20 {
		r := httptest.NewRequest(http.MethodGet, "/users/user-"+itoa(i), nil)
		router.ServeHTTP(httptest.NewRecorder(), r)
	}
	families, err := registry.Gather()
	if err != nil { t.Fatal(err) }
	var requests int
	for _, family := range families {
		if family.GetName() != "gateway_requests_total" { continue }
		requests = len(family.GetMetric())
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() { labels[label.GetName()] = label.GetValue() }
			if labels["path"] != "/users/{userID}" || labels["method"] != "GET" {
				t.Fatalf("unexpected cardinality labels: %#v", labels)
			}
		}
	}
	if requests != 1 { t.Fatalf("got %d distinct request metric series, want 1", requests) }
}

func TestUpstreamErrorMetricDoesNotUseArbitraryTargetHeader(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := observability.NewMetrics(registry)
	h := MetricsMiddlewareWithUpstreamErrors(metrics)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	for _, host := range []string{"secret.internal", "attacker.example"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Upstream-Target", host)
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	families, err := registry.Gather()
	if err != nil { t.Fatal(err) }
	for _, family := range families {
		if family.GetName() != "gateway_upstream_errors_total" { continue }
		if len(family.GetMetric()) != 1 { t.Fatalf("got %d upstream series, want one", len(family.GetMetric())) }
		for _, label := range family.GetMetric()[0].GetLabel() {
			if label.GetName() == "upstream_host" && label.GetValue() != "unknown" { t.Fatalf("upstream label leaked target: %q", label.GetValue()) }
		}
	}
}

func TestTracingMiddlewareOmitsQueryAndUsesRoutePattern(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	router := chi.NewRouter()
	router.Use(TracingMiddleware(provider.Tracer("test")))
	router.Get("/users/{userID}", func(http.ResponseWriter, *http.Request) {})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users/secret-id?token=private", nil))
	spans := recorder.Ended()
	if len(spans) != 1 { t.Fatalf("got %d ended spans, want 1", len(spans)) }
	span := spans[0]
	if got, want := span.Name(), "GET /users/{userID}"; got != want { t.Fatalf("span name = %q, want %q", got, want) }
	for _, attr := range span.Attributes() {
		if attr.Value.Type() == attribute.STRING && strings.Contains(attr.Value.AsString(), "private") {
			t.Fatalf("sensitive query value found in span attribute %s", attr.Key)
		}
	}

}
