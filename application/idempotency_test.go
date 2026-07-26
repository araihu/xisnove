package application_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func TestIdempotencyExecutesOnceAndReplaysResource(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	var mutations atomic.Int32

	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createMonitor", Key: "retry-1", ResourceKind: "monitor",
		Request: struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}{Name: "gateway", Labels: map[string]string{"zone": "edge", "env": "home"}},
	}
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		mutations.Add(1)
		store.resources["monitor-1"] = "gateway"
		return "monitor-1", "gateway", nil
	}
	loader := func(_ context.Context, _ application.Repositories, id string) (string, error) {
		resource, ok := store.resources[id]
		if !ok {
			return "", application.ErrNotFound
		}
		return resource, nil
	}

	first, err := service.Execute(context.Background(), request, mutation, loader)
	if err != nil || first != "gateway" {
		t.Fatalf("first Execute() = %q, %v", first, err)
	}
	request.Request = struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	}{Name: "gateway", Labels: map[string]string{"env": "home", "zone": "edge"}}
	second, err := service.Execute(context.Background(), request, mutation, loader)
	if err != nil || second != "gateway" {
		t.Fatalf("replay Execute() = %q, %v", second, err)
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutations = %d, want 1", mutations.Load())
	}
	record := store.records[idempotencyIdentity("session-1", "createMonitor", "retry-1")]
	if len(record.RequestHash) != 64 || record.ResourceKind != "monitor" || record.ResourceID != "monitor-1" {
		t.Fatalf("stored record = %#v", record)
	}
	if record.CreatedAt != store.now || record.ExpiresAt != store.now.Add(24*time.Hour) {
		t.Fatalf("record times = %s..%s, want database time plus 24h", record.CreatedAt, record.ExpiresAt)
	}
}

func TestIdempotencyRejectsChangedFingerprint(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createMonitor", Key: "retry-1", ResourceKind: "monitor",
		Request: struct {
			Name string `json:"name"`
		}{Name: "first"},
	}
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		store.resources["monitor-1"] = "first"
		return "monitor-1", "first", nil
	}
	loader := func(_ context.Context, _ application.Repositories, id string) (string, error) {
		return store.resources[id], nil
	}
	if _, err := service.Execute(context.Background(), request, mutation, loader); err != nil {
		t.Fatal(err)
	}
	request.Request = struct {
		Name string `json:"name"`
	}{Name: "changed"}
	if _, err := service.Execute(context.Background(), request, mutation, loader); !errors.Is(err, application.ErrIdempotencyKeyReused) {
		t.Fatalf("changed request error = %v, want ErrIdempotencyKeyReused", err)
	}
}

func TestIdempotencyAllowsKeyReuseAfterRecordExpiry(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createMonitor", Key: "expires", ResourceKind: "monitor",
		Request: struct {
			Name string `json:"name"`
		}{Name: "first"},
	}
	var mutations atomic.Int32
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		ordinal := mutations.Add(1)
		id := "monitor-1"
		if ordinal == 2 {
			id = "monitor-2"
		}
		store.resources[id] = id
		return id, id, nil
	}
	loader := func(_ context.Context, _ application.Repositories, id string) (string, error) {
		return store.resources[id], nil
	}
	if _, err := service.Execute(context.Background(), request, mutation, loader); err != nil {
		t.Fatal(err)
	}
	store.now = store.now.Add(25 * time.Hour)
	request.Request = struct {
		Name string `json:"name"`
	}{Name: "second"}
	got, err := service.Execute(context.Background(), request, mutation, loader)
	if err != nil || got != "monitor-2" {
		t.Fatalf("Execute() after expiry = %q, %v", got, err)
	}
	if mutations.Load() != 2 {
		t.Fatalf("mutations = %d, want key reuse after expiry", mutations.Load())
	}
}

func TestConcurrentIdempotencyHasOneWinner(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	var mutations atomic.Int32
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "api-token-1"},
		OperationID: "createLocation", Key: "parallel", ResourceKind: "location",
		Request: struct {
			Name string `json:"name"`
		}{Name: "home"},
	}
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		mutations.Add(1)
		store.resources["location-1"] = "home"
		return "location-1", "home", nil
	}
	loader := func(_ context.Context, _ application.Repositories, id string) (string, error) {
		return store.resources[id], nil
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := service.Execute(context.Background(), request, mutation, loader)
			errs <- err
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutations = %d, want one database-decided winner", mutations.Load())
	}
}

func TestIdempotencyConflictRollsBackAndReloadsWinnerFromFreshView(t *testing.T) {
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createMonitor", Key: "contended", ResourceKind: "monitor",
		Request: struct {
			Name string `json:"name"`
		}{Name: "gateway"},
	}
	requestHash, err := application.CanonicalRequestFingerprint(request.Request)
	if err != nil {
		t.Fatal(err)
	}
	store := newForcedIdempotencyConflictStore(application.IdempotencyRecord{
		PrincipalID: "session-1", OperationID: "createMonitor", Key: "contended",
		RequestHash: requestHash, ResourceKind: "monitor", ResourceID: "winner-id",
		CreatedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	})
	service := application.NewIdempotencyService[string](store)
	var mutations, loads atomic.Int32
	result, err := service.Execute(context.Background(), request,
		func(_ context.Context, _ application.Repositories) (string, string, error) {
			mutations.Add(1)
			store.resources["loser-id"] = "loser"
			return "loser-id", "loser", nil
		},
		func(_ context.Context, _ application.Repositories, id string) (string, error) {
			loads.Add(1)
			resource, ok := store.resources[id]
			if !ok {
				return "", application.ErrNotFound
			}
			return resource, nil
		},
	)
	if err != nil || result != "winner" {
		t.Fatalf("Execute() = %q, %v, want durable winner", result, err)
	}
	if mutations.Load() != 1 || loads.Load() != 1 {
		t.Fatalf("callbacks = %d mutations, %d loads; want 1 each", mutations.Load(), loads.Load())
	}
	if store.transactCalls != 1 || store.viewCalls != 1 || store.transactionGets != 1 || store.creates != 1 || store.viewGets != 1 {
		t.Fatalf("transaction path = transact:%d view:%d tx-get:%d create:%d view-get:%d",
			store.transactCalls, store.viewCalls, store.transactionGets, store.creates, store.viewGets)
	}
	if _, retained := store.resources["loser-id"]; retained {
		t.Fatal("losing transaction resource was not rolled back")
	}
	if store.resources["winner-id"] != "winner" {
		t.Fatal("durable winner was changed")
	}
}

func TestIdempotencyMutationConflictRollsBackAndReloadsWinnerFromFreshView(t *testing.T) {
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createLocation", Key: "same-location", ResourceKind: "location",
		Request: struct {
			Name string `json:"name"`
		}{Name: "homelab"},
	}
	requestHash, err := application.CanonicalRequestFingerprint(request.Request)
	if err != nil {
		t.Fatal(err)
	}
	store := newForcedIdempotencyConflictStore(application.IdempotencyRecord{
		PrincipalID: "session-1", OperationID: "createLocation", Key: "same-location",
		RequestHash: requestHash, ResourceKind: "location", ResourceID: "winner-id",
		CreatedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC),
	})
	service := application.NewIdempotencyService[string](store)
	result, err := service.Execute(context.Background(), request,
		func(_ context.Context, _ application.Repositories) (string, string, error) {
			store.resources["loser-id"] = "loser"
			return "", "", fmt.Errorf("create duplicate location: %w", application.ErrConflict)
		},
		func(_ context.Context, _ application.Repositories, id string) (string, error) {
			return store.resources[id], nil
		},
	)
	if err != nil || result != "winner" {
		t.Fatalf("Execute() = %q, %v, want durable winner", result, err)
	}
	if store.transactCalls != 1 || store.viewCalls != 1 || store.transactionGets != 1 || store.creates != 0 || store.viewGets != 1 {
		t.Fatalf("mutation-conflict path = transact:%d view:%d tx-get:%d create:%d view-get:%d",
			store.transactCalls, store.viewCalls, store.transactionGets, store.creates, store.viewGets)
	}
	if _, retained := store.resources["loser-id"]; retained {
		t.Fatal("mutation-conflict loser resource was not rolled back")
	}
}

func TestIdempotencyMutationConflictWithoutWinnerReturnsOriginalError(t *testing.T) {
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createLocation", Key: "domain-conflict", ResourceKind: "location",
		Request: struct {
			Name string `json:"name"`
		}{Name: "existing"},
	}
	store := newForcedIdempotencyConflictStore(application.IdempotencyRecord{
		CreatedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	})
	store.winnerAvailable = false
	store.resources = make(map[string]string)
	service := application.NewIdempotencyService[string](store)
	wantErr := fmt.Errorf("location name already exists: %w", application.ErrConflict)
	_, err := service.Execute(context.Background(), request,
		func(_ context.Context, _ application.Repositories) (string, string, error) {
			store.resources["loser-id"] = "loser"
			return "", "", wantErr
		},
		nil,
	)
	if err != wantErr {
		t.Fatalf("Execute() error = %v, want original %v", err, wantErr)
	}
	if store.transactCalls != 1 || store.viewCalls != 1 || store.viewGets != 1 {
		t.Fatalf("mutation-conflict miss path = transact:%d view:%d view-get:%d", store.transactCalls, store.viewCalls, store.viewGets)
	}
	if _, retained := store.resources["loser-id"]; retained {
		t.Fatal("domain-conflict loser resource was not rolled back")
	}
}

func TestIdempotencyRetriesTransactionThenReplaysWinner(t *testing.T) {
	request := retryIdempotencyRequest(false)
	requestHash, err := application.CanonicalRequestFingerprint(request.Request)
	if err != nil {
		t.Fatal(err)
	}
	repository := &retryIdempotencyRepository{}
	store := newRetryScriptStore(repository)
	winner := application.IdempotencyRecord{
		PrincipalID: request.Principal.CredentialID, OperationID: request.OperationID, Key: request.Key,
		RequestHash: requestHash, ResourceKind: request.ResourceKind, ResourceID: "winner-id",
		CreatedAt: store.now, ExpiresAt: store.now.Add(time.Hour),
	}
	store.transact = func(call int, ctx context.Context, fn func(context.Context, application.Repositories) error) error {
		if err := fn(ctx, store.repositories()); err != nil {
			return err
		}
		if call == 1 {
			repository.record = &winner
			return application.ErrRetryableTransaction
		}
		return nil
	}
	service := application.NewIdempotencyService[string](store)
	mutations := 0
	result, err := service.Execute(context.Background(), request,
		func(context.Context, application.Repositories) (string, string, error) {
			mutations++
			return "loser-id", "loser", nil
		},
		func(_ context.Context, _ application.Repositories, id string) (string, error) {
			if id != "winner-id" {
				t.Fatalf("loader ID = %q, want winner-id", id)
			}
			return "winner", nil
		},
	)
	if err != nil || result != "winner" {
		t.Fatalf("Execute() = %q, %v", result, err)
	}
	if store.transactCalls != 2 || mutations != 1 || repository.gets != 2 {
		t.Fatalf("retry path = %d transactions, %d mutations, %d gets", store.transactCalls, mutations, repository.gets)
	}
}

func TestIdempotencyRetryExhaustionReturnsLastError(t *testing.T) {
	repository := &retryIdempotencyRepository{}
	store := newRetryScriptStore(repository)
	wantErr := fmt.Errorf("serialization exhausted: %w", application.ErrRetryableTransaction)
	store.transact = func(int, context.Context, func(context.Context, application.Repositories) error) error {
		return wantErr
	}
	service := application.NewIdempotencyService[string](store)
	_, err := service.Execute(context.Background(), retryIdempotencyRequest(false),
		func(context.Context, application.Repositories) (string, string, error) {
			t.Fatal("mutation ran while transaction admission failed")
			return "", "", nil
		}, nil,
	)
	if err != wantErr || store.transactCalls != 5 {
		t.Fatalf("Execute() = %v after %d attempts, want original final error after 5", err, store.transactCalls)
	}
}

func TestIdempotencyRetryWaitHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newRetryScriptStore(&retryIdempotencyRepository{})
	store.transact = func(int, context.Context, func(context.Context, application.Repositories) error) error {
		cancel()
		return application.ErrRetryableTransaction
	}
	service := application.NewIdempotencyService[string](store)
	_, err := service.Execute(ctx, retryIdempotencyRequest(false),
		func(context.Context, application.Repositories) (string, string, error) {
			return "resource-id", "resource", nil
		}, nil,
	)
	if err != context.Canceled || store.transactCalls != 1 {
		t.Fatalf("Execute() = %v after %d attempts, want context cancellation during wait", err, store.transactCalls)
	}
}

func TestCredentialIdempotencyRetriesBeforeReservationAndMutation(t *testing.T) {
	repository := &retryIdempotencyRepository{}
	store := newRetryScriptStore(repository)
	store.transact = func(call int, ctx context.Context, fn func(context.Context, application.Repositories) error) error {
		if call == 1 {
			return application.ErrRetryableTransaction
		}
		return fn(ctx, store.repositories())
	}
	service := application.NewIdempotencyService[string](store)
	mutations := 0
	result, err := service.Execute(context.Background(), retryIdempotencyRequest(true),
		func(context.Context, application.Repositories) (string, string, error) {
			mutations++
			return "credential-id", "secret-once", nil
		}, nil,
	)
	if err != nil || result != "secret-once" || store.transactCalls != 2 || mutations != 1 || repository.creates != 1 {
		t.Fatalf("Execute() = %q, %v; transactions=%d mutations=%d reservations=%d",
			result, err, store.transactCalls, mutations, repository.creates)
	}
}

func TestCredentialIdempotencyDoesNotRetryAfterMutationStarts(t *testing.T) {
	repository := &retryIdempotencyRepository{}
	store := newRetryScriptStore(repository)
	store.transact = func(_ int, ctx context.Context, fn func(context.Context, application.Repositories) error) error {
		if err := fn(ctx, store.repositories()); err != nil {
			return err
		}
		repository.record = nil
		return application.ErrRetryableTransaction
	}
	service := application.NewIdempotencyService[string](store)
	mutations := 0
	_, err := service.Execute(context.Background(), retryIdempotencyRequest(true),
		func(context.Context, application.Repositories) (string, string, error) {
			mutations++
			return "credential-id", "secret-once", nil
		}, nil,
	)
	if err != application.ErrRetryableTransaction || store.transactCalls != 1 || mutations != 1 {
		t.Fatalf("Execute() = %v; transactions=%d mutations=%d, want no second mint", err, store.transactCalls, mutations)
	}
}

func TestIdempotencyRollbackLeavesNoRecord(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	wantErr := errors.New("mutation failed")
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createMonitor", Key: "rollback", ResourceKind: "monitor",
		Request: struct {
			Name string `json:"name"`
		}{Name: "broken"},
	}
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		store.resources["monitor-1"] = "must roll back"
		return "", "", wantErr
	}
	if _, err := service.Execute(context.Background(), request, mutation, nil); !errors.Is(err, wantErr) {
		t.Fatalf("Execute() error = %v, want mutation error", err)
	}
	if len(store.records) != 0 || len(store.resources) != 0 {
		t.Fatalf("rollback retained state: records=%v resources=%v", store.records, store.resources)
	}
}

func TestIdempotencyCredentialIssuanceDoesNotReplaySecret(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	var mutations atomic.Int32
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createAPIToken", Key: "credential", ResourceKind: "api-token",
		ResourceID: "token-1", Request: struct {
			Label string `json:"label"`
		}{Label: "automation"}, CredentialIssuance: true,
	}
	mutation := func(_ context.Context, _ application.Repositories) (string, string, error) {
		mutations.Add(1)
		if _, reserved := store.records[idempotencyIdentity("session-1", "createAPIToken", "credential")]; !reserved {
			return "", "", errors.New("credential was minted before idempotency reservation")
		}
		store.resources["token-1"] = "metadata only"
		return "token-1", "raw-secret-once", nil
	}
	if secret, err := service.Execute(context.Background(), request, mutation, nil); err != nil || secret != "raw-secret-once" {
		t.Fatalf("first credential Execute() = %q, %v", secret, err)
	}
	if _, err := service.Execute(context.Background(), request, mutation, nil); !errors.Is(err, application.ErrCredentialAlreadyIssued) {
		t.Fatalf("credential replay error = %v, want ErrCredentialAlreadyIssued", err)
	}
	if mutations.Load() != 1 {
		t.Fatalf("credential mutations = %d, want one reserved issuance", mutations.Load())
	}
}

func TestCanonicalRequestFingerprintDistinguishesOptionalPresence(t *testing.T) {
	type request struct {
		Description *string `json:"description,omitempty"`
	}
	empty := ""
	absent, err := application.CanonicalRequestFingerprint(request{})
	if err != nil {
		t.Fatal(err)
	}
	present, err := application.CanonicalRequestFingerprint(request{Description: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if absent == present {
		t.Fatal("fingerprint collapsed absent and explicitly supplied optional values")
	}
}

func TestIdempotencyValidatesIdentityAndKeyLength(t *testing.T) {
	store := newIdempotencyStore()
	service := application.NewIdempotencyService[string](store)
	base := application.IdempotencyRequest{
		Principal: application.Principal{CredentialID: "session-1"}, OperationID: "createMonitor",
		Key: "key", ResourceKind: "monitor", Request: struct{}{},
	}
	mutate := func(context.Context, application.Repositories) (string, string, error) {
		return "monitor-1", "monitor", nil
	}
	for name, change := range map[string]func(*application.IdempotencyRequest){
		"credential": func(request *application.IdempotencyRequest) { request.Principal.CredentialID = "" },
		"operation":  func(request *application.IdempotencyRequest) { request.OperationID = "" },
		"key":        func(request *application.IdempotencyRequest) { request.Key = "" },
		"long key":   func(request *application.IdempotencyRequest) { request.Key = string(make([]byte, 256)) },
		"credential resource": func(request *application.IdempotencyRequest) {
			request.CredentialIssuance = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := base
			change(&request)
			if _, err := service.Execute(context.Background(), request, mutate, nil); err == nil {
				t.Fatal("Execute() accepted invalid identity")
			}
		})
	}
}

type idempotencyStore struct {
	mu        sync.Mutex
	now       time.Time
	records   map[string]application.IdempotencyRecord
	resources map[string]string
}

func newIdempotencyStore() *idempotencyStore {
	return &idempotencyStore{
		now:       time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		records:   make(map[string]application.IdempotencyRecord),
		resources: make(map[string]string),
	}
}

func (s *idempotencyStore) View(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(ctx, s.repositories())
}

func (s *idempotencyStore) Transact(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	records := cloneIdempotencyRecords(s.records)
	resources := cloneStrings(s.resources)
	if err := fn(ctx, s.repositories()); err != nil {
		s.records = records
		s.resources = resources
		return err
	}
	return nil
}

func (s *idempotencyStore) repositories() application.Repositories {
	return application.Repositories{Runs: idempotencyRunRepository{s.now}, Idempotency: idempotencyRepository{s}}
}

type idempotencyRepository struct{ store *idempotencyStore }

func (r idempotencyRepository) Get(_ context.Context, principalID, operationID, key string, now time.Time) (application.IdempotencyRecord, error) {
	record, ok := r.store.records[idempotencyIdentity(principalID, operationID, key)]
	if !ok || !record.ExpiresAt.After(now) {
		return application.IdempotencyRecord{}, application.ErrNotFound
	}
	return record, nil
}

func (r idempotencyRepository) Create(_ context.Context, record application.IdempotencyRecord) error {
	identity := idempotencyIdentity(record.PrincipalID, record.OperationID, record.Key)
	if existing, exists := r.store.records[identity]; exists && existing.ExpiresAt.After(record.CreatedAt) {
		return application.ErrConflict
	}
	r.store.records[identity] = record
	return nil
}

func (r idempotencyRepository) DeleteExpired(_ context.Context, now time.Time, limit int) (int64, error) {
	var deleted int64
	for identity, record := range r.store.records {
		if deleted == int64(limit) {
			break
		}
		if !record.ExpiresAt.After(now) {
			delete(r.store.records, identity)
			deleted++
		}
	}
	return deleted, nil
}

type idempotencyRunRepository struct{ now time.Time }

func (r idempotencyRunRepository) DatabaseNow(context.Context) (time.Time, error) { return r.now, nil }
func (idempotencyRunRepository) Insert(context.Context, application.NewRunRecord) (bool, error) {
	return false, nil
}
func (idempotencyRunRepository) ClaimProbe(context.Context, application.ClaimRunParams) (application.RunRecord, error) {
	return application.RunRecord{}, nil
}
func (idempotencyRunRepository) Get(context.Context, domain.CheckRunID) (application.RunRecord, error) {
	return application.RunRecord{}, nil
}
func (idempotencyRunRepository) Resolve(context.Context, domain.CheckRunID, domain.AgentID, []byte, time.Time) (bool, error) {
	return false, nil
}

func idempotencyIdentity(principalID, operationID, key string) string {
	return principalID + "\x00" + operationID + "\x00" + key
}

func cloneIdempotencyRecords(source map[string]application.IdempotencyRecord) map[string]application.IdempotencyRecord {
	clone := make(map[string]application.IdempotencyRecord, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func cloneStrings(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func retryIdempotencyRequest(credential bool) application.IdempotencyRequest {
	request := application.IdempotencyRequest{
		Principal:   application.Principal{CredentialID: "session-1"},
		OperationID: "createLocation", Key: "retryable", ResourceKind: "location",
		Request: struct {
			Name string `json:"name"`
		}{Name: "homelab"},
	}
	if credential {
		request.OperationID = "createAPIToken"
		request.ResourceKind = "api-token"
		request.ResourceID = "credential-id"
		request.CredentialIssuance = true
	}
	return request
}

type retryScriptStore struct {
	now           time.Time
	repository    *retryIdempotencyRepository
	transactCalls int
	viewCalls     int
	transact      func(int, context.Context, func(context.Context, application.Repositories) error) error
}

func newRetryScriptStore(repository *retryIdempotencyRepository) *retryScriptStore {
	return &retryScriptStore{
		now: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC), repository: repository,
	}
}

func (s *retryScriptStore) Transact(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	s.transactCalls++
	return s.transact(s.transactCalls, ctx, fn)
}

func (s *retryScriptStore) View(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	s.viewCalls++
	return fn(ctx, s.repositories())
}

func (s *retryScriptStore) repositories() application.Repositories {
	return application.Repositories{
		Runs: idempotencyRunRepository{s.now}, Idempotency: s.repository,
	}
}

type retryIdempotencyRepository struct {
	record  *application.IdempotencyRecord
	gets    int
	creates int
}

func (r *retryIdempotencyRepository) Get(context.Context, string, string, string, time.Time) (application.IdempotencyRecord, error) {
	r.gets++
	if r.record == nil {
		return application.IdempotencyRecord{}, application.ErrNotFound
	}
	return *r.record, nil
}

func (r *retryIdempotencyRepository) Create(_ context.Context, record application.IdempotencyRecord) error {
	r.creates++
	clone := record
	r.record = &clone
	return nil
}

func (*retryIdempotencyRepository) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

type forcedIdempotencyConflictStore struct {
	now             time.Time
	winner          application.IdempotencyRecord
	resources       map[string]string
	inTransaction   bool
	transactCalls   int
	viewCalls       int
	transactionGets int
	viewGets        int
	creates         int
	winnerAvailable bool
}

func newForcedIdempotencyConflictStore(winner application.IdempotencyRecord) *forcedIdempotencyConflictStore {
	return &forcedIdempotencyConflictStore{
		now: winner.CreatedAt.Add(time.Minute), winner: winner,
		resources: map[string]string{"winner-id": "winner"}, winnerAvailable: true,
	}
}

func (s *forcedIdempotencyConflictStore) Transact(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	s.transactCalls++
	before := cloneStrings(s.resources)
	s.inTransaction = true
	err := fn(ctx, s.repositories())
	s.inTransaction = false
	if err != nil {
		s.resources = before
	}
	return err
}

func (s *forcedIdempotencyConflictStore) View(ctx context.Context, fn func(context.Context, application.Repositories) error) error {
	if s.inTransaction {
		return errors.New("View called before losing transaction returned")
	}
	s.viewCalls++
	return fn(ctx, s.repositories())
}

func (s *forcedIdempotencyConflictStore) repositories() application.Repositories {
	return application.Repositories{
		Runs:        idempotencyRunRepository{s.now},
		Idempotency: forcedIdempotencyConflictRepository{store: s},
	}
}

type forcedIdempotencyConflictRepository struct {
	store *forcedIdempotencyConflictStore
}

func (r forcedIdempotencyConflictRepository) Get(context.Context, string, string, string, time.Time) (application.IdempotencyRecord, error) {
	if r.store.inTransaction {
		r.store.transactionGets++
		return application.IdempotencyRecord{}, application.ErrNotFound
	}
	r.store.viewGets++
	if !r.store.winnerAvailable {
		return application.IdempotencyRecord{}, application.ErrNotFound
	}
	return r.store.winner, nil
}

func (r forcedIdempotencyConflictRepository) Create(context.Context, application.IdempotencyRecord) error {
	r.store.creates++
	return application.ErrConflict
}

func (forcedIdempotencyConflictRepository) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}
