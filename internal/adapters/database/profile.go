package database

import (
	"fmt"
	"strings"
)

type Profile string

const (
	ProfileSQLite     Profile = "sqlite"
	ProfileTursoLocal Profile = "turso-local"
	ProfileTursoCloud Profile = "turso-cloud"
	ProfilePostgres   Profile = "postgres"
)

type Config struct {
	Profile   Profile
	URL       string
	AuthToken string
}

func (c Config) Validate() error {
	switch c.Profile {
	case ProfileSQLite, ProfileTursoLocal, ProfileTursoCloud, ProfilePostgres:
	default:
		return fmt.Errorf("unsupported database profile %q", c.Profile)
	}
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("database URL is required")
	}
	if c.Profile == ProfileTursoCloud && strings.TrimSpace(c.AuthToken) == "" {
		return fmt.Errorf("database auth token is required for %s", c.Profile)
	}
	return nil
}

func (c Config) String() string {
	return fmt.Sprintf("database profile=%q url=<redacted>", c.Profile)
}

func (p Profile) ReplicaSafe() bool {
	return p == ProfileTursoCloud || p == ProfilePostgres
}
