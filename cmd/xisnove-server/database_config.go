package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/araihu/xisnove/internal/adapters/database"
)

type databaseFlagValues struct {
	profile       string
	url           string
	legacySQLite  string
	authTokenFile string
}

func addDatabaseFlags(flags *flag.FlagSet) *databaseFlagValues {
	values := &databaseFlagValues{}
	flags.StringVar(&values.profile, "database-profile", string(database.ProfileSQLite), "database profile: sqlite, turso-local, turso-cloud, or postgres")
	flags.StringVar(&values.url, "database-url", "", "database path or connection URL")
	flags.StringVar(&values.legacySQLite, "database", "", "deprecated SQLite database path alias")
	flags.StringVar(&values.authTokenFile, "database-auth-token-file", "", "database authentication token secret file")
	return values
}

func (v *databaseFlagValues) config() (database.Config, error) {
	profile := database.Profile(strings.TrimSpace(v.profile))
	databaseURL := strings.TrimSpace(v.url)
	legacy := strings.TrimSpace(v.legacySQLite)
	if legacy != "" {
		if databaseURL != "" {
			return database.Config{}, fmt.Errorf("--database cannot be combined with --database-url")
		}
		if profile != database.ProfileSQLite {
			return database.Config{}, fmt.Errorf("--database is only valid with --database-profile sqlite")
		}
		databaseURL = legacy
	}
	if databaseURL == "" {
		return database.Config{}, fmt.Errorf("--database-url is required (or deprecated --database for SQLite)")
	}

	authToken := ""
	if v.authTokenFile != "" {
		if profile != database.ProfileTursoCloud {
			return database.Config{}, fmt.Errorf("--database-auth-token-file is only valid with --database-profile turso-cloud")
		}
		content, err := os.ReadFile(v.authTokenFile)
		if err != nil {
			return database.Config{}, fmt.Errorf("read database auth token file: %w", err)
		}
		authToken = strings.TrimSpace(string(content))
		if authToken == "" {
			return database.Config{}, fmt.Errorf("database auth token file is empty")
		}
	}

	config := database.Config{Profile: profile, URL: databaseURL, AuthToken: authToken}
	if err := config.Validate(); err != nil {
		return database.Config{}, fmt.Errorf("database configuration: %w", err)
	}
	return config, nil
}

func validateReplicaCount(profile database.Profile, replicas int) error {
	if replicas <= 0 {
		return fmt.Errorf("--replicas must be positive")
	}
	if replicas > 1 && !profile.ReplicaSafe() {
		return fmt.Errorf("database profile %q does not support multiple server replicas", profile)
	}
	return nil
}
