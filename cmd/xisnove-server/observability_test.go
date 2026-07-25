package main

import (
	"context"
	"flag"
	"testing"
	"time"
)

func TestObservabilityFlagsKeepTracingOptIn(t *testing.T) {
	flags := flag.NewFlagSet("observability", flag.ContinueOnError)
	values := addObservabilityFlags(flags)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	tracing, err := values.tracing(context.Background())
	if err != nil || tracing.Enabled {
		t.Fatalf("default tracing = %#v, %v", tracing, err)
	}
	values.otlpEndpoint = "http://collector.example.test/v1/traces"
	values.sampleRatio = -0.1
	if _, err := values.tracing(context.Background()); err == nil {
		t.Fatal("negative trace sample ratio was accepted")
	}
	values.sampleRatio = 0.5
	values.traceTimeout = 0
	if _, err := values.tracing(context.Background()); err == nil {
		t.Fatal("zero trace export timeout was accepted")
	}
	values.traceTimeout = time.Second
}
