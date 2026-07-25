package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

func TestEnqueueDueIsIdempotent(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	store := newWorkStore(now)
	store.due["monitor-1"] = application.DueMonitor{
		Monitor: domain.Monitor{
			ID:        "monitor-1",
			Interval:  time.Minute,
			Timeout:   5 * time.Second,
			HTTP:      domain.HTTPProbe{Method: "GET", URL: "https://example.com/health"},
			NextRunAt: now,
		},
		LocationID: "location-1",
		Required:   true,
		NextRunAt:  now,
	}
	nextID := 0
	service := application.NewScheduler(
		store,
		func() string {
			nextID++
			return "run-1"
		},
	)

	first, err := service.EnqueueDue(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.EnqueueDue(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 0 || len(store.runs) != 1 {
		t.Fatalf("first=%d second=%d runs=%d", first, second, len(store.runs))
	}
	if !store.due["monitor-1"].NextRunAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("NextRunAt = %v", store.due["monitor-1"].NextRunAt)
	}
}

func TestEnqueueDueAdvancesByExactIntervalsPastDatabaseTime(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 5, 30, 0, time.UTC)
	store := newWorkStore(now)
	scheduledFor := now.Add(-5*time.Minute - 30*time.Second)
	store.due["monitor-1"] = application.DueMonitor{
		Monitor: domain.Monitor{
			ID:        "monitor-1",
			Interval:  time.Minute,
			Timeout:   5 * time.Second,
			HTTP:      domain.HTTPProbe{Method: "GET", URL: "https://example.com/health"},
			NextRunAt: scheduledFor,
		},
		LocationID: "location-1",
		NextRunAt:  scheduledFor,
	}
	service := application.NewScheduler(store, func() string { return "run-1" })

	if _, err := service.EnqueueDue(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 25, 12, 6, 0, 0, time.UTC)
	if got := store.due["monitor-1"].NextRunAt; !got.Equal(want) {
		t.Fatalf("NextRunAt = %v, want %v", got, want)
	}
}
