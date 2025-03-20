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

func TracerProvider(version string, url string, csiRole CsiPluginMode) (*sdktrace.TracerProvider, error) {
	// Ensure default SDK resources and the required service name are set.
	hostname, _ := os.Hostname()
	attributes := []attribute.KeyValue{
		semconv.ServiceNameKey.String("Weka CSI Plugin"),
		semconv.ServiceVersionKey.String(version),
		semconv.HostNameKey.String(hostname),
		attribute.String("weka.csi.mode", string(csiRole)),
		attribute.String("weka.csi.version", version),
	}
	deploymentIdentifier := os.Getenv("OTEL_DEPLOYMENT_IDENTIFIER")
	if deploymentIdentifier != "" {
		attributes = append(attributes, attribute.String("deployment_identifier", deploymentIdentifier))
	}
	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			attributes...,
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
