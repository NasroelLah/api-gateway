package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nasroellah/api-gateway/internal/observability"
	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsUseFinalStatusAfterEarlyHints(t *testing.T) {
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	handler := MetricsMiddleware(metrics)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items", nil))
	if got := getCounterValue(metrics.RequestsTotal, "GET", "unmatched", http.StatusCreated); got != 1 {
		t.Fatalf("final status counter = %v, want 1", got)
	}
}
