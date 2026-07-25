package application

import (
	"context"
	"errors"
	"time"

	"github.com/araihu/xisnove/internal/domain"
)

var ErrNotFound = errors.New("not found")

type Store interface {
	Repositories() Repositories
	WithinTx(context.Context, func(Repositories) error) error
}

type Repositories struct {
	Admins    AdminRepository
	Sessions  SessionRepository
	Locations LocationRepository
	Monitors  MonitorRepository
	Health    HealthRepository
}

type AdminRecord struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type SessionRecord struct {
	ID        string
	AdminID   string
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type MonitorLocation struct {
	MonitorID  domain.MonitorID
	LocationID domain.LocationID
	Required   bool
}

type AdminRepository interface {
	Count(context.Context) (int64, error)
	Create(context.Context, AdminRecord) error
	FindByEmail(context.Context, string) (AdminRecord, error)
}

type SessionRepository interface {
	Create(context.Context, SessionRecord) error
	FindActiveByTokenHash(context.Context, []byte, time.Time) (SessionRecord, error)
}

type LocationRepository interface {
	Create(context.Context, domain.Location) error
	Get(context.Context, domain.LocationID) (domain.Location, error)
}

type MonitorRepository interface {
	Create(context.Context, domain.Monitor) error
	Get(context.Context, domain.MonitorID) (domain.Monitor, error)
	AssignLocation(context.Context, MonitorLocation) error
	GetAssignment(context.Context, domain.MonitorID) (MonitorLocation, error)
}

type HealthRepository interface {
	GetLocation(
		context.Context,
		domain.MonitorID,
		domain.LocationID,
	) (domain.LocationHealth, error)
	UpsertLocation(context.Context, domain.LocationHealth) error
	ListRequiredLocations(context.Context, domain.MonitorID) ([]domain.LocationHealth, error)
	GetMonitor(context.Context, domain.MonitorID) (domain.MonitorHealth, error)
	UpsertMonitor(context.Context, domain.MonitorHealth) error
}
