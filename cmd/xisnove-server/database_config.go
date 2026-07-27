package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/secrets"
)

type databaseFlagValues struct {
	profile       string
	url           string
	urlFile       string
	legacySQLite  string
	authTokenFile string
}

func addDatabaseFlags(flags *flag.FlagSet) *databaseFlagValues {
	values := &databaseFlagValues{}
	flags.StringVar(&values.profile, "database-profile", string(database.ProfileSQLite), "database profile: sqlite, turso-local, turso-cloud, or postgres")
	flags.StringVar(&values.url, "database-url", "", "database path or connection URL")
	flags.StringVar(&values.urlFile, "database-url-file", "", "database path or connection URL secret file")
	flags.StringVar(&values.legacySQLite, "database", "", "deprecated SQLite database path alias")
	flags.StringVar(&values.authTokenFile, "database-auth-token-file", "", "database authentication token secret file")
	return values
}

func (v *databaseFlagValues) config() (database.Config, error) {
	return v.configContext(context.Background())
}

func (v *databaseFlagValues) configContext(ctx context.Context) (database.Config, error) {
	profile := database.Profile(strings.TrimSpace(v.profile))
	databaseURL := strings.TrimSpace(v.url)
	databaseURLFile := strings.TrimSpace(v.urlFile)
	legacy := strings.TrimSpace(v.legacySQLite)
	if databaseURL != "" && databaseURLFile != "" {
		return database.Config{}, fmt.Errorf("--database-url cannot be combined with --database-url-file")
	}
	if legacy != "" {
		if databaseURL != "" || databaseURLFile != "" {
			return database.Config{}, fmt.Errorf("--database cannot be combined with --database-url or --database-url-file")
		}
		if profile != database.ProfileSQLite {
			return database.Config{}, fmt.Errorf("--database is only valid with --database-profile sqlite")
		}
		databaseURL = legacy
	}
	if databaseURLFile != "" {
		content, err := resolveDatabaseSecretFile(ctx, databaseURLFile)
		if err != nil {
			return database.Config{}, fmt.Errorf("read database URL file: %w", err)
		}
		databaseURL = strings.TrimSpace(string(content))
	}
	if databaseURL == "" {
		return database.Config{}, fmt.Errorf("--database-url or --database-url-file is required (or deprecated --database for SQLite)")
	}

	authToken := ""
	if v.authTokenFile != "" {
		if profile != database.ProfileTursoCloud {
			return database.Config{}, fmt.Errorf("--database-auth-token-file is only valid with --database-profile turso-cloud")
		}
		content, err := resolveDatabaseSecretFile(ctx, v.authTokenFile)
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

func resolveDatabaseSecretFile(ctx context.Context, path string) ([]byte, error) {
	content, err := (secrets.FileResolver{}).Resolve(ctx, port.SecretReference{
		Kind: port.SecretReferenceFile, Locator: path,
	})
	if err != nil {
		return nil, fmt.Errorf("secret file is unavailable or unsafe")
	}
	return content, nil
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
