package postgres

import (
	"sort"
	"testing"
	"time"
)

func TestStateTickUnixNanosRoundTripPreservesSubMicrosecondInstant(t *testing.T) {
	t.Parallel()

	want := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.FixedZone("offset", -3*60*60))
	nanos, err := stateTickUnixNanos(want)
	if err != nil {
		t.Fatal(err)
	}
	got := stateTickTimeFromUnixNanos(nanos)
	if !got.Equal(want.UTC()) {
		t.Fatalf("round-trip time = %s, want %s", got, want.UTC())
	}
}

func TestStateTickUnixNanosRejectsUnrepresentableInstant(t *testing.T) {
	t.Parallel()

	if _, err := stateTickUnixNanos(time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Fatal("far-future timestamp was accepted")
	}
}

func TestStateTickUnixNanosProofRetainsNewestHalfOpenWindow(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 15, 12, 0, 0, 123456789, time.UTC)
	start := mustStateTickUnixNanos(t, base)
	end := mustStateTickUnixNanos(t, base.Add(3*time.Nanosecond))
	type sample struct {
		id    string
		nanos int64
	}
	samples := []sample{
		{id: "before", nanos: mustStateTickUnixNanos(t, base.Add(-time.Nanosecond))},
		{id: "start", nanos: start},
		{id: "middle", nanos: mustStateTickUnixNanos(t, base.Add(time.Nanosecond))},
		{id: "latest", nanos: mustStateTickUnixNanos(t, base.Add(2*time.Nanosecond))},
		{id: "end", nanos: end},
	}
	bounded := samples[:0]
	for _, sample := range samples {
		if sample.nanos >= start && sample.nanos < end {
			bounded = append(bounded, sample)
		}
	}
	sort.Slice(bounded, func(left, right int) bool {
		if bounded[left].nanos != bounded[right].nanos {
			return bounded[left].nanos > bounded[right].nanos
		}
		return bounded[left].id > bounded[right].id
	})
	bounded = bounded[:2]
	sort.Slice(bounded, func(left, right int) bool {
		if bounded[left].nanos != bounded[right].nanos {
			return bounded[left].nanos < bounded[right].nanos
		}
		return bounded[left].id < bounded[right].id
	})
	if len(bounded) != 2 || bounded[0].id != "middle" || bounded[1].id != "latest" {
		t.Fatalf("bounded samples = %#v, want middle/latest", bounded)
	}
}

func mustStateTickUnixNanos(t *testing.T, value time.Time) int64 {
	t.Helper()
	nanos, err := stateTickUnixNanos(value)
	if err != nil {
		t.Fatal(err)
	}
	return nanos
}
