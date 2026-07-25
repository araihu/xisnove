package domain

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
)

var ErrInvalidNotification = errors.New("invalid notification")

const maxNotificationTemplateBytes = 16 << 10

type NotificationChannelKind string

const (
	NotificationChannelShoutrrr     NotificationChannelKind = "shoutrrr"
	NotificationChannelAlertmanager NotificationChannelKind = "alertmanager"
)

type NotificationAction string

const (
	NotificationOpen             NotificationAction = "open"
	NotificationChange           NotificationAction = "change"
	NotificationRecover          NotificationAction = "recover"
	NotificationMaintenanceEnded NotificationAction = "maintenance-ended"
)

type DeliveryState string

const (
	DeliveryPending    DeliveryState = "pending"
	DeliveryClaimed    DeliveryState = "claimed"
	DeliveryRetrying   DeliveryState = "retrying"
	DeliveryDelivered  DeliveryState = "delivered"
	DeliveryPermanent  DeliveryState = "permanent-failure"
	DeliverySuppressed DeliveryState = "suppressed"
)

type NotificationChannel struct {
	ID        NotificationChannelID
	Name      string
	Kind      NotificationChannelKind
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type NotificationRoute struct {
	ID            NotificationRouteID
	Name          string
	ChannelID     NotificationChannelID
	MonitorID     *MonitorID
	LabelMatchers map[string]string
	Actions       []NotificationAction
	Severities    []IncidentSeverity
	Template      string
	Enabled       bool
	Precedence    int32
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type NotificationEvent struct {
	Action    NotificationAction
	Event     IncidentEvent
	MonitorID MonitorID
	Labels    map[string]string
}

type RenderSnapshot struct {
	EventID            string
	Action             NotificationAction
	IncidentID         IncidentID
	MonitorID          MonitorID
	MonitorName        string
	MonitorDescription string
	MonitorLabels      map[string]string
	PreviousState      HealthState
	State              HealthState
	Severity           IncidentSeverity
	OccurredAt         time.Time
	RouteID            NotificationRouteID
	ChannelID          NotificationChannelID
	ChannelKind        NotificationChannelKind
	Template           string
	RouteUpdatedAt     time.Time
}

func NewNotificationChannel(
	id NotificationChannelID,
	name string,
	kind NotificationChannelKind,
	enabled bool,
	at time.Time,
) (NotificationChannel, error) {
	name = strings.TrimSpace(name)
	if id == "" || name == "" || at.IsZero() || !validNotificationChannelKind(kind) {
		return NotificationChannel{}, ErrInvalidNotification
	}
	at = at.UTC()
	return NotificationChannel{
		ID: id, Name: name, Kind: kind, Enabled: enabled,
		CreatedAt: at, UpdatedAt: at,
	}, nil
}

func NewNotificationRoute(route NotificationRoute) (NotificationRoute, error) {
	route.Name = strings.TrimSpace(route.Name)
	route.Template = strings.TrimSpace(route.Template)
	if route.ID == "" || route.Name == "" || route.ChannelID == "" ||
		route.Precedence < 0 || route.CreatedAt.IsZero() || route.UpdatedAt.IsZero() ||
		len(route.Template) > maxNotificationTemplateBytes ||
		!validMonitorLabels(route.LabelMatchers) ||
		!validNotificationActions(route.Actions) || !validIncidentSeverities(route.Severities) {
		return NotificationRoute{}, ErrInvalidNotification
	}
	if route.MonitorID != nil && *route.MonitorID == "" {
		return NotificationRoute{}, ErrInvalidNotification
	}
	route.CreatedAt = route.CreatedAt.UTC()
	route.UpdatedAt = route.UpdatedAt.UTC()
	return route.Clone(), nil
}

func (r NotificationRoute) Clone() NotificationRoute {
	r.LabelMatchers = cloneStringMap(r.LabelMatchers)
	r.Actions = slices.Clone(r.Actions)
	r.Severities = slices.Clone(r.Severities)
	if r.MonitorID != nil {
		monitorID := *r.MonitorID
		r.MonitorID = &monitorID
	}
	return r
}

func (r NotificationRoute) Matches(event NotificationEvent) bool {
	if !r.Enabled {
		return false
	}
	if r.MonitorID != nil && *r.MonitorID != event.MonitorID {
		return false
	}
	for key, value := range r.LabelMatchers {
		if event.Labels[key] != value {
			return false
		}
	}
	if len(r.Actions) > 0 && !slices.Contains(r.Actions, event.Action) {
		return false
	}
	return len(r.Severities) == 0 || slices.Contains(r.Severities, event.Event.Severity)
}

func SelectNotificationRoutes(
	routes []NotificationRoute,
	channels map[NotificationChannelID]NotificationChannel,
	event NotificationEvent,
) []NotificationRoute {
	selected := make([]NotificationRoute, 0, len(routes))
	for _, route := range routes {
		channel, found := channels[route.ChannelID]
		if found && channel.Enabled && route.Matches(event) {
			selected = append(selected, route.Clone())
		}
	}
	slices.SortFunc(selected, func(left, right NotificationRoute) int {
		if byPrecedence := cmp.Compare(left.Precedence, right.Precedence); byPrecedence != 0 {
			return byPrecedence
		}
		return cmp.Compare(left.ID, right.ID)
	})
	return selected
}

func (e NotificationEvent) Clone() NotificationEvent {
	e.Labels = cloneStringMap(e.Labels)
	return e
}

func (s RenderSnapshot) Clone() RenderSnapshot {
	s.MonitorLabels = cloneStringMap(s.MonitorLabels)
	return s
}

func NewNotificationIdentity(
	eventID string,
	routeID NotificationRouteID,
	channelID NotificationChannelID,
) (string, error) {
	if eventID == "" || routeID == "" || channelID == "" {
		return "", ErrInvalidNotification
	}
	digest := sha256.Sum256([]byte(eventID + "\x00" + string(routeID) + "\x00" + string(channelID)))
	return hex.EncodeToString(digest[:]), nil
}

func NextNotificationRetry(
	now time.Time,
	attempt uint32,
	base time.Duration,
	capDelay time.Duration,
	jitter float64,
) (time.Time, error) {
	if attempt == 0 || base <= 0 || capDelay <= 0 || capDelay < base || jitter < 0 || jitter > 1 {
		return time.Time{}, ErrInvalidNotification
	}
	exponent := attempt - 1
	delay := capDelay
	if exponent < 63 {
		multiplier := uint64(1) << exponent
		if multiplier <= uint64(math.MaxInt64)/uint64(base) {
			delay = time.Duration(multiplier) * base
			if delay > capDelay {
				delay = capDelay
			}
		}
	}
	if delay < capDelay {
		remaining := capDelay - delay
		jitterWindow := base
		if jitterWindow > remaining {
			jitterWindow = remaining
		}
		delay += time.Duration(float64(jitterWindow) * jitter)
	}
	return now.UTC().Add(delay), nil
}

func validNotificationChannelKind(kind NotificationChannelKind) bool {
	return kind == NotificationChannelShoutrrr || kind == NotificationChannelAlertmanager
}

func validNotificationActions(actions []NotificationAction) bool {
	for _, action := range actions {
		switch action {
		case NotificationOpen, NotificationChange, NotificationRecover, NotificationMaintenanceEnded:
		default:
			return false
		}
	}
	return true
}

func validIncidentSeverities(severities []IncidentSeverity) bool {
	for _, severity := range severities {
		if severity != IncidentWarning && severity != IncidentCritical {
			return false
		}
	}
	return true
}
