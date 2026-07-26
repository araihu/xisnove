package integration_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/database"
)

func TestHumanClientStorageMatrix(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		runHumanClientStorageJourney(t, newFileStorageHarness(t, database.ProfileSQLite))
	})
	t.Run("TursoLocal", func(t *testing.T) {
		runHumanClientStorageJourney(t, newFileStorageHarness(t, database.ProfileTursoLocal))
	})
	t.Run("Postgres", func(t *testing.T) {
		runHumanClientStorageJourney(t, newPostgresStorageHarness(t))
	})
	t.Run("TursoCloud", func(t *testing.T) {
		runHumanClientStorageJourney(t, newTursoCloudStorageHarness(t))
	})
}

func runHumanClientStorageJourney(t *testing.T, harness *storageHarness) {
	t.Helper()
	ctx := context.Background()
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "matrix-credential"},
		OperationID: "createLocation", Key: "same-key", ResourceKind: "location",
		Request: struct {
			Name string `json:"name"`
		}{Name: "hybrid edge"},
	}

	type outcome struct {
		location domain.Location
		err      error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	ids := []domain.LocationID{
		"10000000-0000-4000-8000-000000000001",
		"10000000-0000-4000-8000-000000000002",
	}
	stores := []application.UnitOfWork{harness.primary.Store, harness.secondary.Store}
	for index := range stores {
		index := index
		go func() {
			<-start
			service := application.NewIdempotencyService[domain.Location](stores[index])
			location, err := service.Execute(ctx, request,
				func(ctx context.Context, repositories application.Repositories) (string, domain.Location, error) {
					now, err := repositories.Runs.DatabaseNow(ctx)
					if err != nil {
						return "", domain.Location{}, err
					}
					location, err := domain.NewLocation(ids[index], "hybrid edge", now)
					if err != nil {
						return "", domain.Location{}, err
					}
					if err := repositories.Locations.Create(ctx, location); err != nil {
						return "", domain.Location{}, err
					}
					return string(location.ID), location, nil
				},
				func(ctx context.Context, repositories application.Repositories, id string) (domain.Location, error) {
					return repositories.Locations.Get(ctx, domain.LocationID(id))
				},
			)
			outcomes <- outcome{location: location, err: err}
		}()
	}
	close(start)
	first, second := <-outcomes, <-outcomes
	if first.err != nil || second.err != nil {
		t.Fatalf("same-key results = (%#v, %v), (%#v, %v)", first.location, first.err, second.location, second.err)
	}
	if first.location.ID == "" || first.location.ID != second.location.ID {
		t.Fatalf("same-key resources differ: %#v and %#v", first.location, second.location)
	}
	loserID := ids[0]
	if first.location.ID == loserID {
		loserID = ids[1]
	}
	if _, err := harness.primary.Store.Repositories().Locations.Get(ctx, loserID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("losing mutation %s persisted: %v", loserID, err)
	}

	changed := request
	changed.Request = struct {
		Name string `json:"name"`
	}{Name: "changed"}
	if _, err := application.NewIdempotencyService[domain.Location](harness.secondary.Store).Execute(
		ctx, changed,
		func(context.Context, application.Repositories) (string, domain.Location, error) {
			t.Fatal("mismatched replay executed its mutation")
			return "", domain.Location{}, nil
		},
		nil,
	); !errors.Is(err, application.ErrIdempotencyKeyReused) {
		t.Fatalf("mismatched replay error = %v, want ErrIdempotencyKeyReused", err)
	}

	rollbackID := domain.LocationID("10000000-0000-4000-8000-000000000003")
	rollbackRequest := request
	rollbackRequest.Key = "rollback-key"
	wantRollback := errors.New("force rollback")
	if _, err := application.NewIdempotencyService[domain.Location](harness.primary.Store).Execute(
		ctx, rollbackRequest,
		func(ctx context.Context, repositories application.Repositories) (string, domain.Location, error) {
			now, err := repositories.Runs.DatabaseNow(ctx)
			if err != nil {
				return "", domain.Location{}, err
			}
			location, err := domain.NewLocation(rollbackID, "rolled back", now)
			if err != nil {
				return "", domain.Location{}, err
			}
			if err := repositories.Locations.Create(ctx, location); err != nil {
				return "", domain.Location{}, err
			}
			return "", domain.Location{}, wantRollback
		},
		nil,
	); !errors.Is(err, wantRollback) {
		t.Fatalf("rollback error = %v, want sentinel", err)
	}
	if _, err := harness.secondary.Store.Repositories().Locations.Get(ctx, rollbackID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("rolled-back location persisted: %v", err)
	}
	databaseNow, err := harness.secondary.Store.Repositories().Runs.DatabaseNow(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.secondary.Store.Repositories().Idempotency.Get(
		ctx,
		rollbackRequest.Principal.CredentialID,
		rollbackRequest.OperationID,
		rollbackRequest.Key,
		databaseNow,
	); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("rolled-back idempotency record persisted: %v", err)
	}

	assertCredentialReservationAcrossHandles(t, ctx, harness)
}

func assertCredentialReservationAcrossHandles(t *testing.T, ctx context.Context, harness *storageHarness) {
	t.Helper()
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "admin-session"},
		OperationID: "rotateAgentCredential", Key: "credential-key",
		ResourceKind: "agent-credential", ResourceID: "credential-generation-2",
		CredentialIssuance: true,
		Request: struct {
			AgentID string `json:"agentId"`
		}{AgentID: "20000000-0000-4000-8000-000000000001"},
	}
	var mutations atomic.Int32
	start := make(chan struct{})
	errorsOut := make(chan error, 2)
	for _, store := range []application.UnitOfWork{harness.primary.Store, harness.secondary.Store} {
		store := store
		go func() {
			<-start
			_, err := application.NewIdempotencyService[string](store).Execute(
				ctx, request,
				func(context.Context, application.Repositories) (string, string, error) {
					mutations.Add(1)
					return request.ResourceID, "raw-secret-once", nil
				},
				nil,
			)
			errorsOut <- err
		}()
	}
	close(start)
	var successes, conflicts int
	for range 2 {
		switch err := <-errorsOut; {
		case err == nil:
			successes++
		case errors.Is(err, application.ErrCredentialAlreadyIssued):
			conflicts++
		default:
			t.Fatalf("credential reservation error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 || mutations.Load() != 1 {
		t.Fatalf("credential reservation = %d successes, %d conflicts, %d mutations", successes, conflicts, mutations.Load())
	}
}

func TestHumanClientCursorRejectsTampering(t *testing.T) {
	codec, err := application.NewHMACCursorCodec([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := codec.Encode(application.CursorKey{
		Sort: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		ID:   "30000000-0000-4000-8000-000000000001",
	}, application.CursorSortTime)
	if err != nil {
		t.Fatal(err)
	}
	replacement := byte('A')
	if cursor[len(cursor)-1] == replacement {
		replacement = 'B'
	}
	tampered := cursor[:len(cursor)-1] + string(replacement)
	if _, err := codec.Decode(tampered, application.CursorSortTime); err == nil {
		t.Fatal("tampered cursor decoded successfully")
	}
}
