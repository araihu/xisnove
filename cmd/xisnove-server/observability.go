package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"time"

	"github.com/araihu/xisnove/internal/adapters/observability"
)

type observabilityFlagValues struct {
	otlpEndpoint string
	otlpInsecure bool
	sampleRatio  float64
	traceTimeout time.Duration
}

func addObservabilityFlags(flags *flag.FlagSet) *observabilityFlagValues {
	values := &observabilityFlagValues{}
	flags.StringVar(&values.otlpEndpoint, "tracing-otlp-http-endpoint", "", "OTLP/HTTP traces endpoint; tracing is disabled when empty")
	flags.BoolVar(&values.otlpInsecure, "tracing-otlp-insecure", false, "allow plaintext OTLP/HTTP trace export")
	flags.Float64Var(&values.sampleRatio, "tracing-sample-ratio", 1, "parent-based trace sampling ratio from 0 through 1")
	flags.DurationVar(&values.traceTimeout, "tracing-export-timeout", 10*time.Second, "OTLP trace export timeout")
	return values
}

func (v *observabilityFlagValues) tracing(ctx context.Context) (*observability.Tracing, error) {
	if v.sampleRatio < 0 || v.sampleRatio > 1 || v.traceTimeout <= 0 {
		return nil, errors.New("invalid tracing operational bounds")
	}
	endpoint := strings.TrimSpace(v.otlpEndpoint)
	return observability.NewTracing(ctx, observability.TraceConfig{
		Enabled: endpoint != "", ServiceName: "xisnove-server", Endpoint: endpoint,
		Insecure: v.otlpInsecure, SampleRatio: v.sampleRatio, Timeout: v.traceTimeout,
	})
}
