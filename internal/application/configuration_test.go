package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

func TestCreateHTTPMonitorPersistsAssignmentAndInitialHealth(t *testing.T) {
	store := newConfigurationStore()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := application.NewConfigurationService(
		store,
		func() time.Time { return now },
		func() string { return "m1" },
	)
	store.locations["l1"] = domain.Location{ID: "l1", Name: "public"}

	monitor, err := service.CreateMonitor(
		context.Background(),
		application.CreateMonitorCommand{
			Name:              "website",
			LocationID:        "l1",
			RequiredLocation:  true,
			Interval:          time.Minute,
			Timeout:           5 * time.Second,
			FailureThreshold:  3,
			RecoveryThreshold: 2,
			Probe: domain.ProbeDefinition{
				Kind: domain.MonitorKindHTTP,
				HTTP: domain.HTTPProbe{
					Method:         "GET",
					URL:            "https://example.com/health",
					ExpectedStatus: []domain.StatusRange{{Min: 200, Max: 299}},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.ID != "m1" {
		t.Fatalf("ID = %s", monitor.ID)
	}
	if monitor.LocationID != "l1" || !monitor.RequiredLocation {
		t.Fatalf("assignment = %#v", monitor)
	}
	if got := store.monitorHealth["m1"].State; got != domain.HealthPending {
		t.Fatalf("initial health = %s", got)
	}
	if got := store.locationHealth["m1/l1"].State; got != domain.HealthPending {
		t.Fatalf("initial location health = %s", got)
	}
	if !monitor.NextRunAt.Equal(now) {
		t.Fatalf("NextRunAt = %v", monitor.NextRunAt)
	}
}

func TestCreateTCPMonitorPersistsTypedProbe(t *testing.T) {
	store := newConfigurationStore()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := application.NewConfigurationService(
		store,
		func() time.Time { return now },
		func() string { return "m1" },
	)
	store.locations["l1"] = domain.Location{ID: "l1", Name: "private"}

	monitor, err := service.CreateMonitor(
		context.Background(),
		application.CreateMonitorCommand{
			Name: "postgres", LocationID: "l1", RequiredLocation: true,
			Interval: time.Minute, Timeout: 5 * time.Second,
			FailureThreshold: 3, RecoveryThreshold: 2,
			Probe: domain.ProbeDefinition{
				Kind: domain.MonitorKindTCP,
				TCP: domain.TCPProbe{
					Host: "postgres.internal", Port: 5432,
					Send: []byte("PING"), Expect: []byte("PONG"),
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindTCP ||
		monitor.TCP.Host != "postgres.internal" ||
		monitor.TCP.Port != 5432 {
		t.Fatalf("monitor = %#v", monitor)
	}
}

func TestCreateDNSMonitorPersistsTypedProbe(t *testing.T) {
	store := newConfigurationStore()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	service := application.NewConfigurationService(
		store,
		func() time.Time { return now },
		func() string { return "m1" },
	)
	store.locations["l1"] = domain.Location{ID: "l1", Name: "private"}

	monitor, err := service.CreateMonitor(
		context.Background(),
		application.CreateMonitorCommand{
			Name: "cluster dns", LocationID: "l1", RequiredLocation: true,
			Interval: time.Minute, Timeout: 5 * time.Second,
			FailureThreshold: 3, RecoveryThreshold: 2,
			Probe: domain.ProbeDefinition{
				Kind: domain.MonitorKindDNS,
				DNS: domain.DNSProbe{
					Resolver: "10.43.0.10:53", Name: "kubernetes.default.svc",
					RecordType: "A", ExpectedValues: []string{"10.43.0.1"},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if monitor.Kind != domain.MonitorKindDNS ||
		monitor.DNS.Resolver != "10.43.0.10:53" ||
		monitor.DNS.Name != "kubernetes.default.svc" {
		t.Fatalf("monitor = %#v", monitor)
	}
}

func TestCreateMonitorRejectsProbeVariantMismatch(t *testing.T) {
	store := newConfigurationStore()
	service := application.NewConfigurationService(store, time.Now, func() string { return "m1" })
	store.locations["l1"] = domain.Location{ID: "l1", Name: "private"}

	_, err := service.CreateMonitor(context.Background(), application.CreateMonitorCommand{
		Name: "bad", LocationID: "l1", Interval: time.Minute, Timeout: time.Second,
		FailureThreshold: 1, RecoveryThreshold: 1,
		Probe: domain.ProbeDefinition{
			Kind: domain.MonitorKindTCP,
			HTTP: domain.HTTPProbe{Method: "GET", URL: "https://example.com"},
		},
	})
	var validation *application.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v", err)
	}
	if len(store.monitors) != 0 {
		t.Fatalf("monitors = %#v", store.monitors)
	}
}

func TestCreateMonitorReturnsNotFoundForMissingLocation(t *testing.T) {
	store := newConfigurationStore()
	service := application.NewConfigurationService(
		store,
		time.Now,
		func() string { return "m1" },
	)

	_, err := service.CreateMonitor(
		context.Background(),
		application.CreateMonitorCommand{
			Name:              "website",
			LocationID:        "missing",
			Interval:          time.Minute,
			Timeout:           time.Second,
			FailureThreshold:  1,
			RecoveryThreshold: 1,
			Probe: domain.ProbeDefinition{
				Kind: domain.MonitorKindHTTP,
				HTTP: domain.HTTPProbe{URL: "https://example.com"},
			},
		},
	)
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateLocationNormalizesNameAndReportsDuplicates(t *testing.T) {
	store := newConfigurationStore()
	service := application.NewConfigurationService(
		store,
		func() time.Time {
			return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
		},
		func() string { return "l1" },
	)

	location, err := service.CreateLocation(
		context.Background(),
		application.CreateLocationCommand{Name: " public "},
	)
	if err != nil {
		t.Fatal(err)
	}
	if location.Name != "public" {
		t.Fatalf("Name = %q", location.Name)
	}
	_, err = service.CreateLocation(
		context.Background(),
		application.CreateLocationCommand{Name: "public"},
	)
	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("error = %v", err)
	}
}

type configurationStore struct {
	locations      map[domain.LocationID]domain.Location
	monitors       map[domain.MonitorID]domain.Monitor
	assignments    map[domain.MonitorID]application.MonitorLocation
	locationHealth map[string]domain.LocationHealth
	monitorHealth  map[domain.MonitorID]domain.MonitorHealth
}

func newConfigurationStore() *configurationStore {
	return &configurationStore{
		locations:      map[domain.LocationID]domain.Location{},
		monitors:       map[domain.MonitorID]domain.Monitor{},
		assignments:    map[domain.MonitorID]application.MonitorLocation{},
		locationHealth: map[string]domain.LocationHealth{},
		monitorHealth:  map[domain.MonitorID]domain.MonitorHealth{},
	}
}

func (s *configurationStore) Repositories() application.Repositories {
	return application.Repositories{
		Locations: &configurationLocationRepository{store: s},
		Monitors:  &configurationMonitorRepository{store: s},
		Health:    &configurationHealthRepository{store: s},
	}
}

func (s *configurationStore) WithinTx(
	_ context.Context,
	fn func(application.Repositories) error,
) error {
	return fn(s.Repositories())
}

type configurationLocationRepository struct {
	store *configurationStore
}

func (r *configurationLocationRepository) Create(
	_ context.Context,
	location domain.Location,
) error {
	for _, existing := range r.store.locations {
		if existing.Name == location.Name {
			return application.ErrConflict
		}
	}
	r.store.locations[location.ID] = location
	return nil
}

func (r *configurationLocationRepository) Get(
	_ context.Context,
	id domain.LocationID,
) (domain.Location, error) {
	location, ok := r.store.locations[id]
	if !ok {
		return domain.Location{}, application.ErrNotFound
	}
	return location, nil
}

type configurationMonitorRepository struct {
	store *configurationStore
}

func (r *configurationMonitorRepository) Create(
	_ context.Context,
	monitor domain.Monitor,
) error {
	for _, existing := range r.store.monitors {
		if existing.Name == monitor.Name {
			return application.ErrConflict
		}
	}
	r.store.monitors[monitor.ID] = monitor
	return nil
}

func (r *configurationMonitorRepository) Get(
	_ context.Context,
	id domain.MonitorID,
) (domain.Monitor, error) {
	monitor, ok := r.store.monitors[id]
	if !ok {
		return domain.Monitor{}, application.ErrNotFound
	}
	return monitor, nil
}

func (r *configurationMonitorRepository) AssignLocation(
	_ context.Context,
	assignment application.MonitorLocation,
) error {
	r.store.assignments[assignment.MonitorID] = assignment
	return nil
}

func (r *configurationMonitorRepository) GetAssignment(
	_ context.Context,
	monitorID domain.MonitorID,
) (application.MonitorLocation, error) {
	assignment, ok := r.store.assignments[monitorID]
	if !ok {
		return application.MonitorLocation{}, application.ErrNotFound
	}
	return assignment, nil
}

func (*configurationMonitorRepository) ListDue(
	context.Context,
	time.Time,
	int,
) ([]application.DueMonitor, error) {
	return []application.DueMonitor{}, nil
}

func (*configurationMonitorRepository) AdvanceNextRun(
	context.Context,
	domain.MonitorID,
	time.Time,
	time.Time,
) (bool, error) {
	return false, nil
}

type configurationHealthRepository struct {
	store *configurationStore
}

func (r *configurationHealthRepository) GetLocation(
	_ context.Context,
	monitorID domain.MonitorID,
	locationID domain.LocationID,
) (domain.LocationHealth, error) {
	health, ok := r.store.locationHealth[string(monitorID)+"/"+string(locationID)]
	if !ok {
		return domain.LocationHealth{}, application.ErrNotFound
	}
	return health, nil
}

func (r *configurationHealthRepository) UpsertLocation(
	_ context.Context,
	health domain.LocationHealth,
) error {
	r.store.locationHealth[string(health.MonitorID)+"/"+string(health.LocationID)] = health
	return nil
}

func (r *configurationHealthRepository) ListRequiredLocations(
	_ context.Context,
	monitorID domain.MonitorID,
) ([]domain.LocationHealth, error) {
	assignment, ok := r.store.assignments[monitorID]
	if !ok || !assignment.Required {
		return []domain.LocationHealth{}, nil
	}
	key := string(monitorID) + "/" + string(assignment.LocationID)
	health, ok := r.store.locationHealth[key]
	if !ok {
		return []domain.LocationHealth{}, nil
	}
	return []domain.LocationHealth{health}, nil
}

func (*configurationHealthRepository) ListStale(
	context.Context,
	time.Time,
	int,
) ([]domain.LocationHealth, error) {
	return nil, nil
}

func (*configurationHealthRepository) ClaimStale(
	context.Context,
	domain.MonitorID,
	domain.LocationID,
	time.Time,
	time.Time,
) (bool, error) {
	return false, nil
}

func (r *configurationHealthRepository) GetMonitor(
	_ context.Context,
	monitorID domain.MonitorID,
) (domain.MonitorHealth, error) {
	health, ok := r.store.monitorHealth[monitorID]
	if !ok {
		return domain.MonitorHealth{}, application.ErrNotFound
	}
	return health, nil
}

func (r *configurationHealthRepository) UpsertMonitor(
	_ context.Context,
	health domain.MonitorHealth,
) error {
	r.store.monitorHealth[health.MonitorID] = health
	return nil
}
