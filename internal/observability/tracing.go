package observability

import (
	"context"
	"fmt"
	"net/http"

	"github.com/nasroellah/api-gateway/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracerProvider mengelola OpenTelemetry tracer
// Requirements: 6.1, 6.5
type TracerProvider struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	cfg      *config.TracingConfig
}

// NewTracerProvider membuat tracer provider dengan configurable exporter
// Requirements: 6.1, 6.5
func NewTracerProvider(cfg *config.TracingConfig) (*TracerProvider, error) {
	if cfg == nil {
		return nil, fmt.Errorf("tracing config is nil")
	}

	// Create resource with service name
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create span exporter based on config
	exporter, err := createExporter(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create exporter: %w", err)
	}

	// Create sampler based on sample rate
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// Create TracerProvider
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global TracerProvider and propagator
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Create tracer
	tracer := provider.Tracer(cfg.ServiceName)

	return &TracerProvider{
		provider: provider,
		tracer:   tracer,
		cfg:      cfg,
	}, nil
}

// createExporter creates the appropriate span exporter based on config
func createExporter(cfg *config.TracingConfig) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "otlp":
		return createOTLPExporter(cfg.Endpoint)
	case "jaeger":
		return createJaegerExporter(cfg.Endpoint)
	case "zipkin":
		return createZipkinExporter(cfg.Endpoint)
	default:
		return nil, fmt.Errorf("unsupported exporter type: %s", cfg.Exporter)
	}
}

// Tracer mengembalikan tracer instance
func (tp *TracerProvider) Tracer() trace.Tracer {
	return tp.tracer
}

// Provider returns the underlying TracerProvider
func (tp *TracerProvider) Provider() *sdktrace.TracerProvider {
	return tp.provider
}

// Shutdown melakukan cleanup tracer
func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	if tp.provider != nil {
		return tp.provider.Shutdown(ctx)
	}
	return nil
}

// ExtractTraceContext mengekstrak trace context dari incoming headers
// Requirements: 6.2
func ExtractTraceContext(ctx context.Context, r *http.Request) context.Context {
	propagator := otel.GetTextMapPropagator()
	return propagator.Extract(ctx, propagation.HeaderCarrier(r.Header))
}

// InjectTraceContext menyisipkan trace context ke outgoing headers
// Requirements: 6.3
func InjectTraceContext(ctx context.Context, r *http.Request) {
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, propagation.HeaderCarrier(r.Header))
}

// headerCarrier adapts http.Header to propagation.TextMapCarrier
type headerCarrier http.Header

func (hc headerCarrier) Get(key string) string {
	return http.Header(hc).Get(key)
}

func (hc headerCarrier) Set(key, value string) {
	http.Header(hc).Set(key, value)
}

func (hc headerCarrier) Keys() []string {
	keys := make([]string, 0, len(hc))
	for k := range hc {
		keys = append(keys, k)
	}
	return keys
}
