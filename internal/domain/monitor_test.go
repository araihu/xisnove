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

func TestNewHTTPMonitorNormalizesAndClonesMetadata(t *testing.T) {
	labels := map[string]string{
		"environment":         "homelab",
		"xisnove.io/exposure": "public",
	}
	monitor, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
		ID: "monitor-1", Name: " edge router ", Description: "  WAN gateway  ",
		Labels: labels, DisplayOrder: 7, Public: true,
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		HTTP:      domain.HTTPProbe{URL: "https://router.example/health"},
		CreatedAt: time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	labels["environment"] = "mutated"
	if monitor.Description != "WAN gateway" ||
		monitor.Labels["environment"] != "homelab" ||
		monitor.DisplayOrder != 7 || !monitor.Public {
		t.Fatalf("metadata = %#v", monitor)
	}
	cloned := monitor.MetadataLabels()
	cloned["environment"] = "also-mutated"
	if monitor.Labels["environment"] != "homelab" {
		t.Fatal("MetadataLabels exposed the monitor label map")
	}
}

func TestNewHTTPMonitorRejectsInvalidMetadata(t *testing.T) {
	tests := map[string]struct {
		description  string
		labels       map[string]string
		displayOrder int32
	}{
		"negative display order": {displayOrder: -1},
		"invalid label key":      {labels: map[string]string{"bad key": "value"}},
		"invalid label prefix":   {labels: map[string]string{"_bad/key": "value"}},
		"invalid label value":    {labels: map[string]string{"key": "contains space"}},
		"description too long":   {description: string(make([]byte, 2049))},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewHTTPMonitor(domain.NewHTTPMonitorParams{
				ID: "monitor-1", Name: "router", Description: tc.description,
				Labels: tc.labels, DisplayOrder: tc.displayOrder,
				Interval: time.Minute, Timeout: 5 * time.Second,
				FailureThreshold: 3, RecoveryThreshold: 2,
				HTTP:      domain.HTTPProbe{URL: "https://router.example/health"},
				CreatedAt: time.Now(),
			})
			if !errors.Is(err, domain.ErrInvalidMonitor) {
				t.Fatalf("error = %v", err)
			}
		})
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

func TestNewTCPMonitorNormalizesAndValidatesEndpoint(t *testing.T) {
	createdAt := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	monitor, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
		ID:                "monitor-1",
		Name:              "postgres",
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
		TCP: domain.TCPProbe{
			Host:   " db.internal. ",
			Port:   5432,
			Send:   []byte("PING\r\n"),
			Expect: []byte("PONG"),
		},
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindTCP ||
		monitor.TCP.Host != "db.internal" ||
		monitor.TCP.Port != 5432 {
		t.Fatalf("monitor = %#v", monitor)
	}
	probe := monitor.Probe()
	if probe.Kind != domain.MonitorKindTCP ||
		probe.TCP.Host != "db.internal" ||
		string(probe.TCP.Send) != "PING\r\n" {
		t.Fatalf("probe = %#v", probe)
	}
}

func TestNewTCPMonitorRejectsZeroPortAndOversizedPayload(t *testing.T) {
	tests := map[string]domain.TCPProbe{
		"zero port": {Host: "db.internal"},
		"oversized send": {
			Host: "db.internal",
			Port: 5432,
			Send: make([]byte, 4<<10+1),
		},
	}
	for name, probe := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewTCPMonitor(domain.NewTCPMonitorParams{
				ID: "monitor-1", Name: "postgres",
				Interval: time.Minute, Timeout: 5 * time.Second,
				FailureThreshold: 3, RecoveryThreshold: 2,
				TCP: probe, CreatedAt: time.Now(),
			})
			if !errors.Is(err, domain.ErrInvalidMonitor) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestNewDNSMonitorNormalizesExpectedValues(t *testing.T) {
	monitor, err := domain.NewDNSMonitor(domain.NewDNSMonitorParams{
		ID: "monitor-1", Name: "private dns",
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		DNS: domain.DNSProbe{
			Resolver:       "10.0.0.53:53",
			Name:           " service.internal. ",
			RecordType:     "A",
			ExpectedValues: []string{"10.0.0.2", "10.0.0.1"},
		},
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindDNS ||
		monitor.DNS.Name != "service.internal" ||
		monitor.DNS.RecordType != "A" {
		t.Fatalf("monitor = %#v", monitor)
	}
	if got := monitor.Probe(); got.Kind != domain.MonitorKindDNS ||
		len(got.DNS.ExpectedValues) != 2 {
		t.Fatalf("probe = %#v", got)
	}
}

func TestNewDNSMonitorRejectsUnsupportedRecordType(t *testing.T) {
	_, err := domain.NewDNSMonitor(domain.NewDNSMonitorParams{
		ID: "monitor-1", Name: "dns",
		Interval: time.Minute, Timeout: 5 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2,
		DNS:       domain.DNSProbe{Name: "example.com", RecordType: "CAA"},
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
