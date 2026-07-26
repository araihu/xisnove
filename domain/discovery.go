package domain

import (
	"errors"
	"strings"
	"time"
)

var ErrInvalidDiscoveryCandidate = errors.New("invalid discovery candidate")

type DiscoveryCandidateID string

type DiscoveryIdentity struct {
	AgentID    AgentID
	LocationID LocationID
	SourceKind string
	SourceUID  string
	Protocol   MonitorKind
	Target     string
}

type DiscoveryCandidate struct {
	ID                 DiscoveryCandidateID
	AgentID            AgentID
	LocationID         LocationID
	SourceKind         string
	SourceUID          string
	Namespace          string
	Name               string
	Labels             map[string]string
	Protocol           MonitorKind
	Target             string
	NetworkPerspective string
	Present            bool
	LastObservedAt     time.Time
	PromotedMonitorID  *MonitorID
	DriftHint          string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NewDiscoveryCandidateParams struct {
	ID                 DiscoveryCandidateID
	AgentID            AgentID
	LocationID         LocationID
	SourceKind         string
	SourceUID          string
	Namespace          string
	Name               string
	Labels             map[string]string
	Protocol           MonitorKind
	Target             string
	NetworkPerspective string
	Present            bool
	ObservedAt         time.Time
}

func NewDiscoveryCandidate(params NewDiscoveryCandidateParams) (DiscoveryCandidate, error) {
	params.SourceKind = strings.ToLower(strings.TrimSpace(params.SourceKind))
	params.SourceUID = strings.TrimSpace(params.SourceUID)
	params.Namespace = strings.ToLower(strings.TrimSpace(params.Namespace))
	params.Name = strings.ToLower(strings.TrimSpace(params.Name))
	params.Target = strings.TrimSpace(params.Target)
	params.NetworkPerspective = strings.TrimSpace(params.NetworkPerspective)
	if params.ID == "" || params.AgentID == "" || params.LocationID == "" ||
		params.SourceKind == "" || params.SourceUID == "" || params.Name == "" ||
		params.Target == "" || params.NetworkPerspective == "" || params.ObservedAt.IsZero() ||
		(params.Protocol != MonitorKindHTTP && params.Protocol != MonitorKindTCP && params.Protocol != MonitorKindDNS) ||
		!validMonitorLabels(params.Labels) {
		return DiscoveryCandidate{}, ErrInvalidDiscoveryCandidate
	}
	observedAt := params.ObservedAt.UTC()
	return DiscoveryCandidate{
		ID: params.ID, AgentID: params.AgentID, LocationID: params.LocationID,
		SourceKind: params.SourceKind, SourceUID: params.SourceUID, Namespace: params.Namespace,
		Name: params.Name, Labels: cloneStringMap(params.Labels), Protocol: params.Protocol,
		Target: params.Target, NetworkPerspective: params.NetworkPerspective,
		Present: params.Present, LastObservedAt: observedAt,
		CreatedAt: observedAt, UpdatedAt: observedAt,
	}, nil
}

func (candidate DiscoveryCandidate) Identity() DiscoveryIdentity {
	return DiscoveryIdentity{
		AgentID: candidate.AgentID, LocationID: candidate.LocationID,
		SourceKind: candidate.SourceKind, SourceUID: candidate.SourceUID,
		Protocol: candidate.Protocol, Target: candidate.Target,
	}
}

func (candidate DiscoveryCandidate) Clone() DiscoveryCandidate {
	candidate.Labels = cloneStringMap(candidate.Labels)
	if candidate.PromotedMonitorID != nil {
		id := *candidate.PromotedMonitorID
		candidate.PromotedMonitorID = &id
	}
	return candidate
}
