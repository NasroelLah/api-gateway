package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics menyimpan semua Prometheus metrics
// Requirements: 5.1, 5.2, 5.3, 5.4, 5.5
type Metrics struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	UpstreamErrors  *prometheus.CounterVec
	ActiveConns     prometheus.Gauge
	IdleConns       prometheus.Gauge
}

// NewMetrics membuat dan mendaftarkan metrics
func NewMetrics(registry prometheus.Registerer) *Metrics {
	m := &Metrics{
		// RequestsTotal counter dengan labels: method, path, status_code
		// Requirements: 5.2
		RequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_requests_total",
				Help: "Total number of HTTP requests processed",
			},
			[]string{"method", "path", "status_code"},
		),

		// RequestDuration histogram dengan labels: method, path
		// Requirements: 5.3
		RequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "gateway_request_duration_seconds",
				Help:    "HTTP request latency in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),

		// UpstreamErrors counter dengan labels: upstream_host, error_type
		// Requirements: 5.4
		UpstreamErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gateway_upstream_errors_total",
				Help: "Total number of upstream errors",
			},
			[]string{"upstream_host", "error_type"},
		),

		// Connection pool gauges
		// Requirements: 5.5
		ActiveConns: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "gateway_active_connections",
				Help: "Number of active connections in the pool",
			},
		),

		IdleConns: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "gateway_idle_connections",
				Help: "Number of idle connections in the pool",
			},
		),
	}

	// Register metrics with the provided registry
	if registry != nil {
		registry.MustRegister(m.RequestsTotal)
		registry.MustRegister(m.RequestDuration)
		registry.MustRegister(m.UpstreamErrors)
		registry.MustRegister(m.ActiveConns)
		registry.MustRegister(m.IdleConns)
	}

	return m
}

// NewMetricsWithDefaults creates metrics registered with the default registry
func NewMetricsWithDefaults() *Metrics {
	return NewMetrics(prometheus.DefaultRegisterer)
}

// RecordRequest mencatat metrics untuk request
// Requirements: 5.2, 5.3
func (m *Metrics) RecordRequest(method, path string, statusCode int, duration time.Duration) {
	statusCodeStr := statusCodeToString(statusCode)
	m.RequestsTotal.WithLabelValues(method, path, statusCodeStr).Inc()
	m.RequestDuration.WithLabelValues(method, path).Observe(duration.Seconds())
}

// RecordUpstreamError mencatat error dari upstream
// Requirements: 5.4
func (m *Metrics) RecordUpstreamError(upstream, errorType string) {
	m.UpstreamErrors.WithLabelValues(upstream, errorType).Inc()
}

// SetActiveConnections sets the number of active connections
// Requirements: 5.5
func (m *Metrics) SetActiveConnections(count float64) {
	m.ActiveConns.Set(count)
}

// SetIdleConnections sets the number of idle connections
// Requirements: 5.5
func (m *Metrics) SetIdleConnections(count float64) {
	m.IdleConns.Set(count)
}

// statusCodeToString converts status code to string for labels
func statusCodeToString(code int) string {
	switch {
	case code < 10:
		return "00" + itoa(code)
	case code < 100:
		return "0" + itoa(code)
	default:
		return itoa(code)
	}
}

// itoa converts int to string without importing strconv
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
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
