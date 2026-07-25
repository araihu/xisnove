package extension_test

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/contracttest"
	"github.com/araihu/xisnove/domain"
)

type locationRepository struct {
	locations map[domain.LocationID]domain.Location
}

func (r *locationRepository) Create(_ context.Context, location domain.Location) error {
	r.locations[location.ID] = location
	return nil
}

func (r *locationRepository) Get(
	_ context.Context,
	id domain.LocationID,
) (domain.Location, error) {
	location, ok := r.locations[id]
	if !ok {
		return domain.Location{}, port.ErrNotFound
	}
	return location, nil
}

type unitOfWork struct {
	repositories port.Repositories
}

func (u unitOfWork) View(
	ctx context.Context,
	fn func(context.Context, port.Repositories) error,
) error {
	return fn(ctx, u.repositories)
}

func (u unitOfWork) Transact(
	ctx context.Context,
	fn func(context.Context, port.Repositories) error,
) error {
	return fn(ctx, u.repositories)
}

func TestExternalModuleCanComposeCore(t *testing.T) {
	repository := &locationRepository{locations: make(map[domain.LocationID]domain.Location)}
	uow := unitOfWork{repositories: port.Repositories{Locations: repository}}
	service := application.NewConfigurationService(
		uow,
		func() time.Time { return time.Unix(1, 0) },
		func() string { return "external-location" },
	)

	location, err := service.CreateLocation(
		context.Background(),
		application.CreateLocationCommand{Name: "External"},
	)
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	if location.ID != "external-location" {
		t.Fatalf("location ID = %q", location.ID)
	}

	// Importing the public contract package is part of the supported extension
	// surface; adapters can opt into its complete suites as they are implemented.
	_ = contracttest.Run
}
