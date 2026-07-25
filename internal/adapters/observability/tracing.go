package observability

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

type TraceConfig struct {
	Enabled     bool
	ServiceName string
	Endpoint    string
	Headers     map[string]string
	Insecure    bool
	SampleRatio float64
	Timeout     time.Duration
}

type Tracing struct {
	Enabled    bool
	Provider   trace.TracerProvider
	Propagator propagation.TextMapPropagator
	shutdown   func(context.Context) error
}

func NewTracing(ctx context.Context, config TraceConfig) (*Tracing, error) {
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	if !config.Enabled {
		return &Tracing{Provider: trace.NewNoopTracerProvider(), Propagator: propagator, shutdown: func(context.Context) error { return nil }}, nil
	}
	if config.ServiceName == "" {
		return nil, errors.New("tracing service name is required")
	}
	if config.Endpoint == "" {
		return nil, errors.New("tracing OTLP HTTP endpoint is required")
	}
	if config.SampleRatio < 0 || config.SampleRatio > 1 {
		return nil, errors.New("tracing sample ratio must be between zero and one")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	exporterOptions := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(config.Endpoint), otlptracehttp.WithTimeout(timeout)}
	if len(config.Headers) > 0 {
		exporterOptions = append(exporterOptions, otlptracehttp.WithHeaders(cloneStrings(config.Headers)))
	}
	if config.Insecure {
		exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(config.ServiceName)))
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, err
	}
	ratio := config.SampleRatio
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))))
	return &Tracing{Enabled: true, Provider: provider, Propagator: propagator, shutdown: provider.Shutdown}, nil
}

func (t *Tracing) Shutdown(ctx context.Context) error { return t.shutdown(ctx) }
func (t *Tracing) Tracer(name string) trace.Tracer    { return t.Provider.Tracer(name) }
func (t *Tracing) ExtractHTTP(ctx context.Context, header http.Header) context.Context {
	return t.Propagator.Extract(ctx, propagation.HeaderCarrier(header))
}
func (t *Tracing) InjectHTTP(ctx context.Context, header http.Header) {
	t.Propagator.Inject(ctx, propagation.HeaderCarrier(header))
}
func (t *Tracing) StartWorker(ctx context.Context, tracerName, operation string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.Tracer(tracerName).Start(ctx, operation, options...)
}

// HTTPMiddleware extracts W3C context and creates a fixed-name server span.
// Keeping the span name fixed prevents arbitrary request paths from becoming
// an unbounded telemetry dimension.
func (t *Tracing) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx := t.ExtractHTTP(request.Context(), request.Header)
		ctx, span := t.Tracer("github.com/araihu/xisnove/http").Start(ctx, "http.request", trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (t *Tracing) InjectRequest(request *http.Request) {
	t.InjectHTTP(request.Context(), request.Header)
}

// InstallGlobal is explicit so constructing disabled tracing never mutates
// process-wide state. The composition root may opt in after configuration.
func (t *Tracing) InstallGlobal() {
	otel.SetTracerProvider(t.Provider)
	otel.SetTextMapPropagator(t.Propagator)
}

func cloneStrings(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
