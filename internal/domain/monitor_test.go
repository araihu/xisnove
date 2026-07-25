package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

func TestNewHTTPMonitorAppliesDefaults(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.FixedZone("test", -3*60*60))
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                domain.MonitorID("m1"),
		Name:              " router ",
		Interval:          60 * time.Second,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP: domain.HTTPProbe{
			URL:            "https://router.example/health",
			ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
			BodyContains:   []string{"ok"},
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindHTTP {
		t.Fatalf("Kind = %q", monitor.Kind)
	}
	if monitor.Name != "router" {
		t.Fatalf("Name = %q", monitor.Name)
	}
	if monitor.HTTP.Method != "GET" {
		t.Fatalf("HTTP.Method = %q", monitor.HTTP.Method)
	}
	if !monitor.Enabled {
		t.Fatal("Enabled = false")
	}
	if !monitor.NextRunAt.Equal(now.UTC()) || monitor.NextRunAt.Location() != time.UTC {
		t.Fatalf("NextRunAt = %v", monitor.NextRunAt)
	}
}

func TestNewHTTPMonitorRejectsTimeoutAtOrAboveInterval(t *testing.T) {
	_, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "m1",
		Name:              "bad",
		Interval:          time.Second,
		Timeout:           time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		HTTP:              domain.HTTPProbe{Method: "GET", URL: "https://example.com"},
		CreatedAt:         time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidMonitor) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewHTTPMonitorRejectsEmptyIdentity(t *testing.T) {
	_, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		Name:              "missing ID",
		Interval:          time.Minute,
		Timeout:           time.Second,
		FailureThreshold:  1,
		RecoveryThreshold: 1,
		HTTP:              domain.HTTPProbe{URL: "https://example.com"},
		CreatedAt:         time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidMonitor) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewHTTPMonitorRejectsInvalidStatusRange(t *testing.T) {
	_, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID:                "m1",
		Name:              "bad status",
		Interval:          time.Minute,
		Timeout:           time.Second,
		FailureThreshold:  1,
		RecoveryThreshold: 1,
		HTTP: domain.HTTPProbe{
			Method:         "GET",
			URL:            "https://example.com",
			ExpectedStatus: []domain.StatusRange{{Min: 299, Max: 200}},
		},
		CreatedAt: time.Now(),
	})
	if !errors.Is(err, domain.ErrInvalidMonitor) {
		t.Fatalf("error = %v", err)
	}
}

func TestNewLocationNormalizesNameAndTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.FixedZone("test", 2*60*60))
	location, err := domain.NewLocation("location-1", " home ", now)
	if err != nil {
		t.Fatal(err)
	}
	if location.Name != "home" {
		t.Fatalf("Name = %q", location.Name)
	}
	if !location.CreatedAt.Equal(now.UTC()) || location.CreatedAt.Location() != time.UTC {
		t.Fatalf("CreatedAt = %v", location.CreatedAt)
	}
}

func TestNewLocationRejectsEmptyIdentity(t *testing.T) {
	if _, err := domain.NewLocation("", "home", time.Now()); !errors.Is(err, domain.ErrInvalidLocation) {
		t.Fatalf("error = %v", err)
	}
}
