package httpapi_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/application"
)

func TestCreateLocationReturnsGeneratedCreatedResponse(t *testing.T) {
	server := newConfigurationServer(t)

	response, err := server.CreateLocation(
		context.Background(),
		httpapi.CreateLocationRequestObject{
			Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateLocation201JSONResponse)
	if !ok || created.Name != "public" {
		t.Fatalf("response = %#v", response)
	}
	if created.Id.String() != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("Id = %s", created.Id)
	}
}

func TestCreateLocationMapsDuplicateNameToConflictProblem(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	request := httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
	}
	if _, err := server.CreateLocation(ctx, request); err != nil {
		t.Fatal(err)
	}

	response, err := server.CreateLocation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateLocationdefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 409 || problem.Body.Code != "conflict" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateAndGetMonitorMapsAPIDurationsAndAssignment(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(
		ctx,
		httpapi.CreateLocationRequestObject{
			Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)

	response, err := server.CreateMonitor(
		ctx,
		httpapi.CreateMonitorRequestObject{
			Body: &httpapi.CreateMonitorJSONRequestBody{
				Name:              "website",
				LocationId:        location.Id,
				RequiredLocation:  true,
				IntervalSeconds:   60,
				TimeoutMillis:     5000,
				FailureThreshold:  3,
				RecoveryThreshold: 2,
				Http: httpapi.HTTPProbe{
					Method:          httpapi.GET,
					Url:             "https://example.com/health",
					ExpectedStatus:  200,
					BodyContains:    "ok",
					FollowRedirects: false,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateMonitor201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	if created.IntervalSeconds != 60 || created.TimeoutMillis != 5000 {
		t.Fatalf("created monitor = %#v", created)
	}

	gotResponse, err := server.GetMonitor(
		ctx,
		httpapi.GetMonitorRequestObject{MonitorId: created.Id},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := gotResponse.(httpapi.GetMonitor200JSONResponse)
	if !ok || got.LocationId != location.Id || !got.RequiredLocation {
		t.Fatalf("response = %#v", gotResponse)
	}
}

func TestCreateMonitorMapsMissingLocationToNotFoundProblem(t *testing.T) {
	server := newConfigurationServer(t)
	response, err := server.CreateMonitor(
		context.Background(),
		httpapi.CreateMonitorRequestObject{
			Body: &httpapi.CreateMonitorJSONRequestBody{
				Name:              "website",
				LocationId:        uuid.MustParse("99999999-9999-4999-8999-999999999999"),
				RequiredLocation:  true,
				IntervalSeconds:   60,
				TimeoutMillis:     5000,
				FailureThreshold:  3,
				RecoveryThreshold: 2,
				Http: httpapi.HTTPProbe{
					Method:         httpapi.GET,
					Url:            "https://example.com/health",
					ExpectedStatus: 200,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateMonitordefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 404 || problem.Body.Code != "not_found" {
		t.Fatalf("response = %#v", response)
	}
}

func newConfigurationServer(t *testing.T) *httpapi.Server {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
	}
	nextID := 0
	service := application.NewConfigurationService(
		sqlitestore.NewStore(db),
		func() time.Time {
			return time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
		},
		func() string {
			id := ids[nextID]
			nextID++
			return id
		},
	)
	return httpapi.NewServer(httpapi.ServerConfig{Configuration: service})
}
