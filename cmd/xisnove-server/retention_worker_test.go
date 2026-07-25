package main

import (
	"flag"
	"testing"
	"time"
)

func TestRetentionWorkerFlags(t *testing.T) {
	flags := flag.NewFlagSet("retention", flag.ContinueOnError)
	values := addRetentionWorkerFlags(flags)
	if err := flags.Parse([]string{
		"--retention-batch-size=250", "--retention-lease=30s", "--retention-poll=2m",
		"--retention-probe-results=1440h", "--retention-daily-months=24",
	}); err != nil {
		t.Fatal(err)
	}
	if values.batchSize != 250 || values.leaseDuration != 30*time.Second ||
		values.pollInterval != 2*time.Minute || values.rawRetention != 60*24*time.Hour ||
		values.dailyRetentionMonths != 24 {
		t.Fatalf("retention flags = %#v", values)
	}
	if err := values.validate(); err != nil {
		t.Fatal(err)
	}
	values.batchSize = 0
	if err := values.validate(); err == nil {
		t.Fatal("zero retention batch size was accepted")
	}
}
