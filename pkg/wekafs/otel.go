package wekafs

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TracerProvider(version string, url string, csiRole CsiPluginMode, deploymentIdentifier string) (*sdktrace.TracerProvider, error) {
	// Ensure default SDK resources and the required service name are set.
	hostname, _ := os.Hostname()
	attributes := []attribute.KeyValue{
		attribute.String("service.name", "Weka CSI Plugin"),
		attribute.String("service.version", version),
		attribute.String("host.name", hostname),
		attribute.String("weka.csi.mode", string(csiRole)),
		attribute.String("weka.csi.version", version),
	}
	if deploymentIdentifier != "" {
		attributes = append(attributes, attribute.String("deployment_identifier", deploymentIdentifier))
	}
	r, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(attributes...),
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
