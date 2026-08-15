package domain

import (
	"errors"
	"net"
	"strings"
	"time"
)

var ErrInvalidLocation = errors.New("invalid location")

// LocationProtocol is the default probe family for monitors assigned to a
// location. It is deliberately separate from agent transport (pull, push,
// webhook).
type LocationProtocol string

const (
	LocationProtocolHTTP LocationProtocol = "http"
	LocationProtocolTCP  LocationProtocol = "tcp"
	LocationProtocolDNS  LocationProtocol = "dns"
)

type LocationPolicy struct {
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
}

func DefaultLocationPolicy() LocationPolicy {
	return LocationPolicy{
		Interval:          time.Minute,
		Timeout:           5 * time.Second,
		FailureThreshold:  3,
		RecoveryThreshold: 2,
	}
}

func (p LocationPolicy) normalized() LocationPolicy {
	defaults := DefaultLocationPolicy()
	if p.Interval <= 0 {
		p.Interval = defaults.Interval
	}
	if p.Timeout <= 0 {
		p.Timeout = defaults.Timeout
	}
	if p.FailureThreshold == 0 {
		p.FailureThreshold = defaults.FailureThreshold
	}
	if p.RecoveryThreshold == 0 {
		p.RecoveryThreshold = defaults.RecoveryThreshold
	}
	return p
}

type Location struct {
	ID        LocationID
	Name      string
	Address   string
	Protocol  LocationProtocol
	Policy    LocationPolicy
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NewLocation(id LocationID, name string, createdAt time.Time) (Location, error) {
	return NewLocationWithDefaults(id, name, "", "", LocationPolicy{}, createdAt)
}

func NewLocationWithDefaults(
	id LocationID,
	name string,
	address string,
	protocol LocationProtocol,
	policy LocationPolicy,
	createdAt time.Time,
) (Location, error) {
	name = strings.TrimSpace(name)
	address = strings.TrimSpace(address)
	if id == "" || name == "" || !validLocationAddress(address) {
		return Location{}, ErrInvalidLocation
	}
	if protocol == "" {
		protocol = LocationProtocolHTTP
	}
	switch protocol {
	case LocationProtocolHTTP, LocationProtocolTCP, LocationProtocolDNS:
	default:
		return Location{}, ErrInvalidLocation
	}
	policy = policy.normalized()
	if policy.Timeout >= policy.Interval || policy.FailureThreshold == 0 || policy.RecoveryThreshold == 0 {
		return Location{}, ErrInvalidLocation
	}

	return Location{
		ID:        id,
		Name:      name,
		Address:   address,
		Protocol:  protocol,
		Policy:    policy,
		Enabled:   true,
		CreatedAt: createdAt.UTC(),
		UpdatedAt: createdAt.UTC(),
	}, nil
}

func validLocationAddress(address string) bool {
	if address == "" {
		return true
	}
	if net.ParseIP(address) != nil {
		return true
	}
	if len(address) > 253 || strings.ContainsAny(address, " /\t\r\n") {
		return false
	}
	for _, label := range strings.Split(address, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}
