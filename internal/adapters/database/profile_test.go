package database_test

import (
	"strings"
	"testing"

	xisdatabase "github.com/araihu/xisnove/internal/adapters/database"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      xisdatabase.Config
		replicaSafe bool
		errorText   string
	}{
		{
			name: "sqlite",
			config: xisdatabase.Config{
				Profile: xisdatabase.ProfileSQLite,
				URL:     "./xisnove.db",
			},
		},
		{
			name: "local Turso",
			config: xisdatabase.Config{
				Profile: xisdatabase.ProfileTursoLocal,
				URL:     "./xisnove.turso",
			},
		},
		{
			name: "managed Turso",
			config: xisdatabase.Config{
				Profile:   xisdatabase.ProfileTursoCloud,
				URL:       "libsql://example.turso.io",
				AuthToken: "secret",
			},
			replicaSafe: true,
		},
		{
			name: "managed Turso needs token",
			config: xisdatabase.Config{
				Profile: xisdatabase.ProfileTursoCloud,
				URL:     "libsql://example.turso.io",
			},
			errorText: "auth token",
		},
		{
			name: "PostgreSQL",
			config: xisdatabase.Config{
				Profile: xisdatabase.ProfilePostgres,
				URL:     "postgres://localhost/xisnove",
			},
			replicaSafe: true,
		},
		{
			name: "unknown profile",
			config: xisdatabase.Config{
				Profile: "etcd",
				URL:     "https://127.0.0.1:2379",
			},
			errorText: "database profile",
		},
		{
			name: "missing URL",
			config: xisdatabase.Config{
				Profile: xisdatabase.ProfileSQLite,
			},
			errorText: "database URL",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if test.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errorText) {
					t.Fatalf("Validate() error = %v, want containing %q", err, test.errorText)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if got := test.config.Profile.ReplicaSafe(); got != test.replicaSafe {
				t.Fatalf("ReplicaSafe() = %v, want %v", got, test.replicaSafe)
			}
		})
	}
}

func TestConfigStringRedactsCredentials(t *testing.T) {
	t.Parallel()

	config := xisdatabase.Config{
		Profile:   xisdatabase.ProfileTursoCloud,
		URL:       "libsql://user:password@example.turso.io/database?authToken=query-secret",
		AuthToken: "field-secret",
	}
	got := config.String()

	for _, secret := range []string{"user", "password", "query-secret", "field-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("String() = %q, contains secret %q", got, secret)
		}
	}
	if !strings.Contains(got, string(xisdatabase.ProfileTursoCloud)) {
		t.Fatalf("String() = %q, missing profile", got)
	}
}
