package wekafs

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"os"
)

func TracerProvider(version string, url string) (*sdktrace.TracerProvider, error) {
	// Ensure default SDK resources and the required service name are set.
	hostname, _ := os.Hostname()
	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("Weka CSI Plugin"),
			semconv.ServiceVersionKey.String(version),
			attribute.String("hostname", hostname),
		),
	)
	if err != nil {
		return nil, err
	}

	// Create the OpenTelemetry exporter if tracing is enabled
	if url != "" {
		ctx := context.Background()
		exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(url))
		if err != nil {
			return nil, err
		}
		return sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(r),
		), nil
	} else {
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(r),
		), nil
	}
}
