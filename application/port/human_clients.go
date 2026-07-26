package port

import (
	"context"
	"time"
)

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
)

type PageRequest struct {
	Limit  int
	Cursor string
}

type Page[T any] struct {
	Items      []T
	NextCursor string
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
