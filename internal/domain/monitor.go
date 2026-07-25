package domain

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrInvalidMonitor = errors.New("invalid monitor")

type MonitorKind string

const MonitorKindHTTP MonitorKind = "http"

type StatusRange struct {
	Min int
	Max int
}

type HTTPProbe struct {
	Method          string
	URL             string
	ExpectedStatus  []StatusRange
	BodyContains    []string
	FollowRedirects bool
}

type NewHTTPMonitorParams struct {
	ID                MonitorID
	Name              string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	HTTP              HTTPProbe
	CreatedAt         time.Time
}

type Monitor struct {
	ID                MonitorID
	Name              string
	Kind              MonitorKind
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	HTTP              HTTPProbe
	Enabled           bool
	NextRunAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func NewHTTPMonitor(p NewHTTPMonitorParams) (Monitor, error) {
	parsed, err := url.ParseRequestURI(p.HTTP.URL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		p.ID == "" ||
		strings.TrimSpace(p.Name) == "" ||
		p.Interval <= 0 ||
		p.Timeout <= 0 ||
		p.Timeout >= p.Interval ||
		p.FailureThreshold == 0 ||
		p.RecoveryThreshold == 0 {
		return Monitor{}, ErrInvalidMonitor
	}

	if p.HTTP.Method == "" {
		p.HTTP.Method = http.MethodGet
	}
	for _, status := range p.HTTP.ExpectedStatus {
		if status.Min < 100 || status.Max > 599 || status.Min > status.Max {
			return Monitor{}, ErrInvalidMonitor
		}
	}

	createdAt := p.CreatedAt.UTC()
	return Monitor{
		ID:                p.ID,
		Name:              strings.TrimSpace(p.Name),
		Kind:              MonitorKindHTTP,
		Interval:          p.Interval,
		Timeout:           p.Timeout,
		FailureThreshold:  p.FailureThreshold,
		RecoveryThreshold: p.RecoveryThreshold,
		HTTP:              p.HTTP,
		Enabled:           true,
		NextRunAt:         createdAt,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}, nil
}
