package tracing

import (
	"context"
	"fmt"

	otclient "github.com/Azure/operatortrace/operatortrace-go/pkg/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const tracerName = "kubemox-operator"

// Setup initializes OpenTelemetry with an OTLP HTTP exporter and returns
// an operatortrace TracingClient that wraps the given base client.
// The returned shutdown function flushes any pending spans and should be
// deferred by the caller.
func Setup(ctx context.Context, otlpEndpoint string, baseClient client.Client,
	apiReader client.Reader, scheme *runtime.Scheme) (otclient.TracingClient, func(), error) {
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(otlpEndpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("kubemox"),
		)),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
	))

	tracer := otel.Tracer(tracerName)
	logger := ctrl.Log.WithName("tracing")

	tracingClient := otclient.NewTracingClientWithOptions(baseClient, apiReader, tracer, logger, scheme,
		otclient.WithIncomingTraceRelationship(otclient.TraceParentRelationshipParent),
	)

	shutdown := func() {
		if err := provider.Shutdown(ctx); err != nil {
			logger.Error(err, "failed to shutdown trace provider")
		}
	}

	return tracingClient, shutdown, nil
}
