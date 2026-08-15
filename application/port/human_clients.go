package port

import (
	"context"
	"errors"
	"slices"
	"sort"
	"time"
)

var ErrInvalidScopes = errors.New("invalid API token scopes")

type Scope string

const (
	ScopeTokensRead         Scope = "tokens:read"
	ScopeTokensWrite        Scope = "tokens:write"
	ScopeLocationsRead      Scope = "locations:read"
	ScopeLocationsWrite     Scope = "locations:write"
	ScopeMonitorsRead       Scope = "monitors:read"
	ScopeMonitorsWrite      Scope = "monitors:write"
	ScopeAgentsRead         Scope = "agents:read"
	ScopeAgentsWrite        Scope = "agents:write"
	ScopeIncidentsRead      Scope = "incidents:read"
	ScopeNotificationsRead  Scope = "notifications:read"
	ScopeNotificationsWrite Scope = "notifications:write"
	ScopeMaintenanceRead    Scope = "maintenance:read"
	ScopeMaintenanceWrite   Scope = "maintenance:write"
	ScopeDiscoveryRead      Scope = "discovery:read"
	ScopeDiscoveryWrite     Scope = "discovery:write"
	ScopeStatusRead         Scope = "status:read"
	ScopeOperatorProvision  Scope = "operator:provision"
)

var recognizedScopes = map[Scope]struct{}{
	ScopeTokensRead: {}, ScopeTokensWrite: {},
	ScopeLocationsRead: {}, ScopeLocationsWrite: {},
	ScopeMonitorsRead: {}, ScopeMonitorsWrite: {},
	ScopeAgentsRead: {}, ScopeAgentsWrite: {},
	ScopeIncidentsRead:     {},
	ScopeNotificationsRead: {}, ScopeNotificationsWrite: {},
	ScopeMaintenanceRead: {}, ScopeMaintenanceWrite: {},
	ScopeDiscoveryRead: {}, ScopeDiscoveryWrite: {},
	ScopeStatusRead:        {},
	ScopeOperatorProvision: {},
}

func NormalizeScopes(scopes []Scope) ([]Scope, error) {
	if len(scopes) == 0 {
		return nil, ErrInvalidScopes
	}
	normalized := slices.Clone(scopes)
	seen := make(map[Scope]struct{}, len(normalized))
	for _, scope := range normalized {
		if _, ok := recognizedScopes[scope]; !ok {
			return nil, ErrInvalidScopes
		}
		if _, duplicate := seen[scope]; duplicate {
			return nil, ErrInvalidScopes
		}
		seen[scope] = struct{}{}
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

type PageRequest struct {
	Limit  int
	Cursor string
}

type Page[T any] struct {
	Items      []T
	NextCursor string
}

const (
	DefaultPageLimit = 50
	MaxPageLimit     = 200
)

func NormalizePageLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}

// NormalizeMonitorHistoryQueryLimit keeps one extra row available for
// truncation checks while preserving the public history limit.
func NormalizeMonitorHistoryQueryLimit(limit int) int {
	const (
		defaultLimit = 4096
		maxLimit     = 10001
	)
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

type IdempotencyRecord struct {
	PrincipalID  string
	OperationID  string
	Key          string
	RequestHash  string
	ResourceKind string
	ResourceID   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type IdempotencyRepository interface {
	Get(context.Context, string, string, string, time.Time) (IdempotencyRecord, error)
	// Create atomically inserts a record, replaces the same identity only when
	// its stored expiry is at or before record.CreatedAt, and otherwise returns
	// ErrConflict. CreatedAt is authoritative database time supplied by the
	// caller's transaction.
	Create(context.Context, IdempotencyRecord) error
	DeleteExpired(context.Context, time.Time, int) (int64, error)
}

type APITokenRecord struct {
	ID         string
	AdminID    string
	Label      string
	TokenHash  []byte
	Scopes     []Scope
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type APITokenRepository interface {
	Create(context.Context, APITokenRecord) error
	FindActiveByTokenHash(context.Context, []byte, time.Time) (APITokenRecord, error)
	List(context.Context, PageRequest) (Page[APITokenRecord], error)
	Revoke(context.Context, string, time.Time) (bool, error)
	TouchLastUsed(context.Context, string, time.Time) error
}
