package tursocloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProvisionAndDeleteExactDisposableDatabase(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	createdName := ""
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer platform-secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations":
			writeJSON(t, w, []any{map[string]any{"slug": "test-org"}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/test-org/groups":
			writeJSON(t, w, map[string]any{"groups": []any{map[string]any{"name": "xisnove-ci", "delete_protection": false}}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/test-org/databases":
			var body struct {
				Name  string `json:"name"`
				Group string `json:"group"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Group != "xisnove-ci" || !strings.HasPrefix(body.Name, "xisnove-ci-") {
				t.Fatalf("create body = %+v", body)
			}
			mu.Lock()
			createdName = body.Name
			mu.Unlock()
			writeJSON(t, w, map[string]any{"database": map[string]any{
				"Name": body.Name, "DbId": "db-id", "Hostname": body.Name + ".example.turso.io",
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/auth/tokens"):
			if r.URL.Query().Get("expiration") != "10m" || r.URL.Query().Get("authorization") != "full-access" {
				t.Fatalf("token query = %q", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{"jwt": "database-secret"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/organizations/test-org/databases/"):
			mu.Lock()
			want := createdName
			deleted = true
			mu.Unlock()
			if !strings.HasSuffix(r.URL.Path, "/"+want) {
				t.Fatalf("deleted path = %q, want exact database %q", r.URL.Path, want)
			}
			writeJSON(t, w, map[string]any{"database": want})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/organizations/test-org/databases/"):
			mu.Lock()
			isDeleted := deleted
			mu.Unlock()
			if isDeleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(t, w, map[string]any{"database": map[string]any{"Name": createdName}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	database, err := Provision(context.Background(), Config{
		BaseURL:      server.URL,
		Token:        "platform-secret",
		Group:        "xisnove-ci",
		HTTPClient:   server.Client(),
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if database.Organization != "test-org" || database.ID != "db-id" || database.AuthToken != "database-secret" {
		t.Fatalf("database = %+v", database)
	}
	if !strings.HasPrefix(database.URL, "libsql://") || strings.Contains(database.URL, database.AuthToken) {
		t.Fatalf("database URL = %q", database.URL)
	}
	if err := database.Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProvisionRefusesDeleteProtectedGroupBeforeCreation(t *testing.T) {
	t.Parallel()

	created := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/test-org/groups":
			writeJSON(t, w, map[string]any{"groups": []any{map[string]any{"name": "protected", "delete_protection": true}}})
		case r.Method == http.MethodPost:
			created = true
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	_, err := Provision(context.Background(), Config{
		BaseURL:      server.URL,
		Token:        "platform-secret",
		Organization: "test-org",
		Group:        "protected",
		HTTPClient:   server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "delete protection") {
		t.Fatalf("Provision() error = %v", err)
	}
	if created {
		t.Fatal("database was created in a protected group")
	}
}

func TestProvisionRequiresExplicitOrganizationWhenAmbiguous(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []any{
			map[string]any{"slug": "first"}, map[string]any{"slug": "second"},
		})
	}))
	t.Cleanup(server.Close)

	_, err := Provision(context.Background(), Config{
		BaseURL: server.URL, Token: "platform-secret", Group: "xisnove-ci", HTTPClient: server.Client(),
	})
	if err == nil || !strings.Contains(err.Error(), "explicit organization") {
		t.Fatalf("Provision() error = %v", err)
	}
}

func TestProvisionCleansUpWhenTokenMintFailsAndRedactsSecrets(t *testing.T) {
	t.Parallel()

	deleted := false
	createdName := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/groups"):
			writeJSON(t, w, map[string]any{"groups": []any{map[string]any{"name": "xisnove-ci", "delete_protection": false}}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/databases"):
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			createdName = body.Name
			writeJSON(t, w, map[string]any{"database": map[string]any{
				"Name": body.Name, "DbId": "db-id", "Hostname": body.Name + ".example.turso.io",
			}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/auth/tokens"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"platform-secret leaked-database-secret"}`))
		case r.Method == http.MethodDelete:
			if !strings.HasSuffix(r.URL.Path, "/"+createdName) {
				t.Fatalf("deleted path = %q, want %q", r.URL.Path, createdName)
			}
			deleted = true
			writeJSON(t, w, map[string]any{"database": createdName})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/databases/"):
			if deleted {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	_, err := Provision(context.Background(), Config{
		BaseURL: server.URL, Token: "platform-secret", Organization: "test-org", Group: "xisnove-ci",
		HTTPClient: server.Client(), PollInterval: time.Millisecond,
	})
	if err == nil {
		t.Fatal("Provision() error = nil")
	}
	if !deleted {
		t.Fatal("database was not deleted after token mint failure")
	}
	if strings.Contains(err.Error(), "platform-secret") || strings.Contains(err.Error(), "database-secret") {
		t.Fatalf("Provision() leaked a secret: %v", err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
