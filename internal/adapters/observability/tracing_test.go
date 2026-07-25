package observability

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTracingDisabledByDefaultAndPropagatesW3CContext(t *testing.T) {
	tracing, err := NewTracing(context.Background(), TraceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if tracing.Enabled {
		t.Fatal("tracing must be opt-in")
	}
	header := http.Header{"Traceparent": []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}
	ctx := tracing.ExtractHTTP(context.Background(), header)
	span := trace.SpanContextFromContext(ctx)
	if !span.IsValid() || !span.IsRemote() {
		t.Fatalf("W3C trace context was not extracted: %+v", span)
	}
	outgoing := make(http.Header)
	tracing.InjectHTTP(ctx, outgoing)
	if outgoing.Get("traceparent") == "" {
		t.Fatal("W3C trace context was not injected")
	}
	if err := tracing.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTracingEnabledRequiresExplicitConfiguration(t *testing.T) {
	if _, err := NewTracing(context.Background(), TraceConfig{Enabled: true}); err == nil {
		t.Fatal("expected missing configuration error")
	}
}

func TestTracingRejectsSampleRatioOutsideClosedUnitInterval(t *testing.T) {
	for _, ratio := range []float64{-0.01, 1.01} {
		_, err := NewTracing(context.Background(), TraceConfig{
			Enabled: true, ServiceName: "test", Endpoint: "http://collector.example.test/v1/traces",
			SampleRatio: ratio,
		})
		if err == nil {
			t.Fatalf("sample ratio %v was accepted", ratio)
		}
	}
}
