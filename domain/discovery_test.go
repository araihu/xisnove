package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestDiscoveryCandidateNormalizesStableIdentityAndCopiesLabels(t *testing.T) {
	labels := map[string]string{"app": "api"}
	observed := time.Date(2026, 7, 26, 8, 30, 0, 0, time.FixedZone("west", -3*60*60))
	candidate, err := domain.NewDiscoveryCandidate(domain.NewDiscoveryCandidateParams{
		ID: "candidate-1", AgentID: "agent-1", LocationID: "location-1",
		SourceKind: " Service ", SourceUID: " uid-1 ", Namespace: " Production ",
		Name: " API ", Labels: labels, Protocol: domain.MonitorKindHTTP,
		Target: " https://example.com/health ", NetworkPerspective: " cluster-a ",
		Present: true, ObservedAt: observed,
	})
	if err != nil {
		t.Fatal(err)
	}
	labels["app"] = "mutated"
	if candidate.SourceKind != "service" || candidate.SourceUID != "uid-1" ||
		candidate.Namespace != "production" || candidate.Name != "api" ||
		candidate.Target != "https://example.com/health" ||
		candidate.NetworkPerspective != "cluster-a" || candidate.Labels["app"] != "api" ||
		!candidate.LastObservedAt.Equal(observed.UTC()) {
		t.Fatalf("candidate = %#v", candidate)
	}
	identity := candidate.Identity()
	if identity.AgentID != "agent-1" || identity.LocationID != "location-1" ||
		identity.SourceKind != "service" || identity.SourceUID != "uid-1" ||
		identity.Protocol != domain.MonitorKindHTTP || identity.Target != "https://example.com/health" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestDiscoveryCandidateRejectsIncompleteIdentityAndInvalidProtocol(t *testing.T) {
	base := domain.NewDiscoveryCandidateParams{
		ID: "candidate-1", AgentID: "agent-1", LocationID: "location-1",
		SourceKind: "service", SourceUID: "uid-1", Namespace: "default", Name: "api",
		Protocol: domain.MonitorKindHTTP, Target: "https://example.com", NetworkPerspective: "cluster-a",
		Present: true, ObservedAt: time.Now(),
	}
	for name, mutate := range map[string]func(*domain.NewDiscoveryCandidateParams){
		"source UID":          func(p *domain.NewDiscoveryCandidateParams) { p.SourceUID = "" },
		"network perspective": func(p *domain.NewDiscoveryCandidateParams) { p.NetworkPerspective = "" },
		"protocol":            func(p *domain.NewDiscoveryCandidateParams) { p.Protocol = "icmp" },
	} {
		t.Run(name, func(t *testing.T) {
			params := base
			mutate(&params)
			_, err := domain.NewDiscoveryCandidate(params)
			if !errors.Is(err, domain.ErrInvalidDiscoveryCandidate) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
