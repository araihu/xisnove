package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

func TestNewNotificationChannelAndRouteValidateAndClone(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.FixedZone("test", -3*60*60))
	channel, err := domain.NewNotificationChannel("channel-1", " primary alerts ", domain.NotificationChannelShoutrrr, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Name != "primary alerts" || channel.CreatedAt.Location() != time.UTC || channel.UpdatedAt.Location() != time.UTC {
		t.Fatalf("channel = %#v", channel)
	}

	target := domain.MonitorID("monitor-1")
	matchers := map[string]string{"environment": "homelab"}
	actions := []domain.NotificationAction{domain.NotificationOpen}
	severities := []domain.IncidentSeverity{domain.IncidentCritical}
	route, err := domain.NewNotificationRoute(domain.NotificationRoute{
		ID: "route-1", Name: " critical ", ChannelID: channel.ID, MonitorID: &target,
		LabelMatchers: matchers, Actions: actions, Severities: severities,
		Template: " {{ .Monitor.Name }} is down ", Enabled: true, Precedence: 10,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	matchers["environment"] = "mutated"
	actions[0] = domain.NotificationRecover
	severities[0] = domain.IncidentWarning
	target = "mutated"
	if route.Name != "critical" || route.Template != "{{ .Monitor.Name }} is down" ||
		route.LabelMatchers["environment"] != "homelab" ||
		route.Actions[0] != domain.NotificationOpen ||
		route.Severities[0] != domain.IncidentCritical ||
		*route.MonitorID != "monitor-1" {
		t.Fatalf("route = %#v", route)
	}
	cloned := route.Clone()
	cloned.LabelMatchers["environment"] = "other"
	*cloned.MonitorID = "other"
	if route.LabelMatchers["environment"] != "homelab" || *route.MonitorID != "monitor-1" {
		t.Fatal("route clone exposed mutable state")
	}

	if _, err := domain.NewNotificationChannel("", "name", domain.NotificationChannelShoutrrr, true, now); !errors.Is(err, domain.ErrInvalidNotification) {
		t.Fatalf("empty channel ID error = %v", err)
	}
	if _, err := domain.NewNotificationChannel("channel", "name", "unknown", true, now); !errors.Is(err, domain.ErrInvalidNotification) {
		t.Fatalf("unknown channel kind error = %v", err)
	}
	if _, err := domain.NewNotificationRoute(domain.NotificationRoute{ID: "route", Name: "name", ChannelID: "channel", Actions: []domain.NotificationAction{"unknown"}, CreatedAt: now, UpdatedAt: now}); !errors.Is(err, domain.ErrInvalidNotification) {
		t.Fatalf("unknown action error = %v", err)
	}
}

func TestSelectNotificationRoutesIsDeterministicAndSkipsDisabledChannels(t *testing.T) {
	event := domain.NotificationEvent{Action: domain.NotificationOpen, Event: domain.IncidentEvent{Severity: domain.IncidentCritical}}
	routes := []domain.NotificationRoute{
		{ID: "route-c", ChannelID: "channel-on", Enabled: true, Precedence: 20},
		{ID: "route-b", ChannelID: "channel-off", Enabled: true, Precedence: 10},
		{ID: "route-a", ChannelID: "channel-on", Enabled: true, Precedence: 10},
		{ID: "route-disabled", ChannelID: "channel-on", Enabled: false, Precedence: 1},
	}
	channels := map[domain.NotificationChannelID]domain.NotificationChannel{
		"channel-on":  {ID: "channel-on", Enabled: true},
		"channel-off": {ID: "channel-off", Enabled: false},
	}
	got := domain.SelectNotificationRoutes(routes, channels, event)
	if len(got) != 2 || got[0].ID != "route-a" || got[1].ID != "route-c" {
		t.Fatalf("routes = %#v", got)
	}
}

func TestNotificationRouteMatchesEventAndMonitor(t *testing.T) {
	route := domain.NotificationRoute{
		ID: "route-1", ChannelID: "channel-1", Enabled: true,
		LabelMatchers: map[string]string{"environment": "homelab", "exposure": "public"},
		Actions:       []domain.NotificationAction{domain.NotificationOpen, domain.NotificationChange},
		Severities:    []domain.IncidentSeverity{domain.IncidentCritical},
	}
	event := domain.NotificationEvent{
		Action:    domain.NotificationOpen,
		Event:     domain.IncidentEvent{ID: "event-1", Severity: domain.IncidentCritical},
		MonitorID: "monitor-1",
		Labels:    map[string]string{"environment": "homelab", "exposure": "public", "owner": "infra"},
	}
	if !route.Matches(event) {
		t.Fatal("matching event was rejected")
	}

	tests := map[string]func(domain.NotificationEvent) domain.NotificationEvent{
		"label mismatch": func(e domain.NotificationEvent) domain.NotificationEvent {
			e.Labels["exposure"] = "private"
			return e
		},
		"action mismatch": func(e domain.NotificationEvent) domain.NotificationEvent {
			e.Action = domain.NotificationRecover
			return e
		},
		"severity mismatch": func(e domain.NotificationEvent) domain.NotificationEvent {
			e.Event.Severity = domain.IncidentWarning
			return e
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := event.Clone()
			if route.Matches(mutate(candidate)) {
				t.Fatal("non-matching event was accepted")
			}
		})
	}

	route.Enabled = false
	if route.Matches(event) {
		t.Fatal("disabled route matched")
	}
}

func TestNotificationRouteCanTargetOneMonitor(t *testing.T) {
	target := domain.MonitorID("monitor-1")
	route := domain.NotificationRoute{ID: "route-1", ChannelID: "channel-1", MonitorID: &target, Enabled: true}
	if !route.Matches(domain.NotificationEvent{MonitorID: target}) {
		t.Fatal("target monitor did not match")
	}
	if route.Matches(domain.NotificationEvent{MonitorID: "monitor-2"}) {
		t.Fatal("unrelated monitor matched")
	}
}

func TestNotificationIdentityIsStableAndScoped(t *testing.T) {
	first, err := domain.NewNotificationIdentity("event-1", "route-1", "channel-1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := domain.NewNotificationIdentity("event-1", "route-1", "channel-1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := domain.NewNotificationIdentity("event-1", "route-2", "channel-1")
	if err != nil {
		t.Fatal(err)
	}
	if first != again || first == other || len(first) != 64 {
		t.Fatalf("identities = %q %q %q", first, again, other)
	}
	if _, err := domain.NewNotificationIdentity("", "route-1", "channel-1"); err == nil {
		t.Fatal("empty event ID accepted")
	}
}

func TestNextNotificationRetryUsesCappedExponentialBackoffAndInjectedJitter(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		attempt uint32
		jitter  float64
		want    time.Duration
	}{{
		attempt: 1, jitter: 0, want: time.Second,
	}, {
		attempt: 2, jitter: .5, want: 2500 * time.Millisecond,
	}, {
		attempt: 8, jitter: 1, want: 10 * time.Second,
	}}
	for _, tc := range tests {
		got, err := domain.NextNotificationRetry(now, tc.attempt, time.Second, 10*time.Second, tc.jitter)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Equal(now.Add(tc.want)) {
			t.Fatalf("attempt %d: got %v, want %v", tc.attempt, got, now.Add(tc.want))
		}
	}
	for _, tc := range []struct {
		attempt uint32
		base    time.Duration
		cap     time.Duration
		jitter  float64
	}{{0, time.Second, time.Minute, 0}, {1, 0, time.Minute, 0}, {1, time.Second, 0, 0}, {1, time.Second, time.Minute, -0.1}, {1, time.Second, time.Minute, 1.1}} {
		if _, err := domain.NextNotificationRetry(now, tc.attempt, tc.base, tc.cap, tc.jitter); err == nil {
			t.Fatalf("invalid retry accepted: %#v", tc)
		}
	}
}

func TestNotificationEventCloneDoesNotExposeLabels(t *testing.T) {
	event := domain.NotificationEvent{Labels: map[string]string{"owner": "infra"}}
	clone := event.Clone()
	clone.Labels["owner"] = "mutated"
	if event.Labels["owner"] != "infra" {
		t.Fatal("Clone exposed event labels")
	}
}
