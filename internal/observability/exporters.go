package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/zipkin"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// createOTLPExporter creates an OTLP gRPC exporter
// Requirements: 6.5
func createOTLPExporter(endpoint string) (sdktrace.SpanExporter, error) {
	ctx := context.Background()
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(), // For development; use TLS in production
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}
	return exporter, nil
}

// createJaegerExporter creates a Jaeger exporter using OTLP
// Note: Jaeger now supports OTLP natively, so we use OTLP exporter
// Requirements: 6.5
func createJaegerExporter(endpoint string) (sdktrace.SpanExporter, error) {
	// Jaeger supports OTLP protocol, so we use OTLP exporter
	return createOTLPExporter(endpoint)
}

// createZipkinExporter creates a Zipkin exporter
// Requirements: 6.5
func createZipkinExporter(endpoint string) (sdktrace.SpanExporter, error) {
	exporter, err := zipkin.New(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create Zipkin exporter: %w", err)
	}
	return exporter, nil
}

// NoopExporter is a no-op exporter for testing
type NoopExporter struct{}

func (n *NoopExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

func (n *NoopExporter) Shutdown(ctx context.Context) error {
	return nil
}

// NewNoopExporter creates a no-op exporter for testing
func NewNoopExporter() sdktrace.SpanExporter {
	return &NoopExporter{}
}
