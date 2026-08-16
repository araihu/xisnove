// Package controlplane adapts the generated Xisnove SDK to the UI BFF.
package controlplane

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/araihu/xisnove/sdk"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrUnauthorized       = errors.New("control-plane session is unauthorized")
)

const (
	// StateHistoryLookback is the maximum window exposed by the UI BFF.
	StateHistoryLookback = 3 * time.Hour
	// StateHistoryMaxRecords is the maximum number of state ticks retained by
	// the BFF adapter when an upstream response is truncated or over-sized.
	StateHistoryMaxRecords int32 = 10000
)

// StateHistoryWindow returns the bounded half-open UTC window used by the UI.
func StateHistoryWindow(now time.Time) (startsAt, endsAt time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	endsAt = now.UTC()
	return endsAt.Add(-StateHistoryLookback), endsAt
}

// BoundStateHistory defensively adapts an SDK response to the UI's bounded,
// monitor-scoped history contract. The control plane remains authoritative;
// this only filters malformed/out-of-window records and never reconstructs
// provenance fields.
func BoundStateHistory(history sdk.MonitorStateHistory, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) sdk.MonitorStateHistory {
	if startsAt.IsZero() || endsAt.IsZero() || !endsAt.After(startsAt) {
		startsAt, endsAt = StateHistoryWindow(endsAt)
	}
	if limit <= 0 || limit > StateHistoryMaxRecords {
		limit = StateHistoryMaxRecords
	}
	bounded := history
	bounded.MonitorId = monitorID
	bounded.StartsAt = startsAt.UTC()
	bounded.EndsAt = endsAt.UTC()

	ticks := make([]sdk.MonitorStateTick, 0, len(history.Ticks))
	for _, tick := range history.Ticks {
		if tick.MonitorId != monitorID {
			continue
		}
		occurredAt := tick.OccurredAt.UTC()
		if occurredAt.Before(bounded.StartsAt) || !occurredAt.Before(bounded.EndsAt) {
			continue
		}
		tick.OccurredAt = occurredAt
		ticks = append(ticks, tick)
	}
	sort.SliceStable(ticks, func(i, j int) bool {
		if ticks[i].OccurredAt.Equal(ticks[j].OccurredAt) {
			return ticks[i].Id.String() < ticks[j].Id.String()
		}
		return ticks[i].OccurredAt.Before(ticks[j].OccurredAt)
	})
	if int32(len(ticks)) > limit {
		bounded.Truncated = true
		ticks = ticks[len(ticks)-int(limit):]
	}
	bounded.Ticks = ticks
	return bounded
}

// Client is the UI-facing subset of the public generated SDK.
type Client interface {
	ExchangeAdministratorCredentials(ctx context.Context, email, password string) (opaqueCredential string, err error)
	RevokeSession(ctx context.Context, opaqueCredential string) error
	GetPublicStatusPage(ctx context.Context) (sdk.PublicStatusPage, error)
	SearchResources(ctx context.Context, opaqueCredential, query string, limit int32) ([]sdk.SearchResult, error)
	ListMonitors(ctx context.Context, opaqueCredential, cursor string, limit int32) (sdk.Page[sdk.Monitor], error)
	GetMonitorHealth(ctx context.Context, opaqueCredential string, monitorID openapi_types.UUID) (sdk.MonitorHealth, error)
	GetMonitorAvailabilityHistory(ctx context.Context, opaqueCredential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorAvailabilityHistory, error)
	GetMonitorStateHistory(ctx context.Context, opaqueCredential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorStateHistory, error)
}

// LocationClient is the optional UI-facing management surface for locations.
// Keeping it separate from Client lets monitor-only test doubles and read-only
// deployments continue to satisfy the control-plane contract.
type LocationClient interface {
	ListLocations(ctx context.Context, opaqueCredential, cursor string, limit int32) (sdk.Page[sdk.Location], error)
	CreateLocation(ctx context.Context, opaqueCredential string, input LocationInput) (sdk.Location, error)
	UpdateLocation(ctx context.Context, opaqueCredential string, locationID openapi_types.UUID, input LocationInput) (sdk.Location, error)
	DisableLocation(ctx context.Context, opaqueCredential string, locationID openapi_types.UUID) error
}

type LocationInput struct {
	Name     string
	Address  string
	Protocol string
	Policy   *sdk.LocationPolicyInput
	Enabled  bool
}

// SDKClient uses only the public generated client and its SDK helpers.
type SDKClient struct {
	client *sdk.ClientWithResponses
}

func NewSDKClient(baseURL string, doer sdk.HttpRequestDoer) (*SDKClient, error) {
	opts := []sdk.ClientOption{}
	if doer != nil {
		opts = append(opts, sdk.WithHTTPClient(doer))
	}
	client, err := sdk.NewClientWithResponses(baseURL, opts...)
	if err != nil {
		return nil, fmt.Errorf("configure Xisnove SDK: %w", err)
	}
	return &SDKClient{client: client}, nil
}

func (c *SDKClient) ExchangeAdministratorCredentials(ctx context.Context, email, password string) (string, error) {
	response, err := c.client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email: openapi_types.Email(email), Password: &password,
	})
	if err != nil {
		return "", err
	}
	if response.JSON201 != nil {
		return response.JSON201.Token, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return "", ErrInvalidCredentials
	}
	return "", responseError(response.HTTPResponse, response.Body, "create session")
}

func (c *SDKClient) RevokeSession(ctx context.Context, credential string) error {
	response, err := c.client.RevokeCurrentSessionWithResponse(ctx, sdk.WithBearerToken(credential))
	if err != nil {
		return err
	}
	if response.StatusCode() == http.StatusNoContent {
		return nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return responseError(response.HTTPResponse, response.Body, "revoke session")
}

func (c *SDKClient) GetPublicStatusPage(ctx context.Context) (sdk.PublicStatusPage, error) {
	response, err := c.client.GetPublicStatusPageWithResponse(ctx)
	if err != nil {
		return sdk.PublicStatusPage{}, err
	}
	if response.JSON200 != nil {
		return *response.JSON200, nil
	}
	return sdk.PublicStatusPage{}, responseError(response.HTTPResponse, response.Body, "get public status")
}

func (c *SDKClient) SearchResources(ctx context.Context, credential, query string, limit int32) ([]sdk.SearchResult, error) {
	response, err := c.client.SearchResourcesWithResponse(ctx, &sdk.SearchResourcesParams{Q: query, Limit: &limit}, sdk.WithBearerToken(credential))
	if err != nil {
		return nil, err
	}
	if response.JSON200 != nil {
		return append([]sdk.SearchResult(nil), response.JSON200.Items...), nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return nil, ErrUnauthorized
	}
	return nil, responseError(response.HTTPResponse, response.Body, "search resources")
}

func (c *SDKClient) ListMonitors(ctx context.Context, credential, cursor string, limit int32) (sdk.Page[sdk.Monitor], error) {
	params := sdk.ListMonitorsParams{Limit: &limit}
	return c.client.MonitorsPageFetcher(params, sdk.WithBearerToken(credential))(ctx, cursor)
}

func (c *SDKClient) ListLocations(ctx context.Context, credential, cursor string, limit int32) (sdk.Page[sdk.Location], error) {
	params := sdk.ListLocationsParams{Limit: &limit}
	return c.client.LocationsPageFetcher(params, sdk.WithBearerToken(credential))(ctx, cursor)
}

func (c *SDKClient) CreateLocation(ctx context.Context, credential string, input LocationInput) (sdk.Location, error) {
	key := sdk.IdempotencyKey(uuid.NewString())
	request := sdk.CreateLocationRequest{Name: input.Name, Policy: input.Policy}
	if input.Address != "" {
		request.Address = &input.Address
	}
	if input.Protocol != "" {
		protocol := sdk.CreateLocationRequestProtocol(input.Protocol)
		request.Protocol = &protocol
	}
	response, err := c.client.CreateLocationWithResponse(ctx, &sdk.CreateLocationParams{IdempotencyKey: &key}, request, sdk.WithBearerToken(credential))
	if err != nil {
		return sdk.Location{}, err
	}
	if response.JSON201 != nil {
		return *response.JSON201, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return sdk.Location{}, ErrUnauthorized
	}
	return sdk.Location{}, responseError(response.HTTPResponse, response.Body, "create location")
}

func (c *SDKClient) UpdateLocation(ctx context.Context, credential string, locationID openapi_types.UUID, input LocationInput) (sdk.Location, error) {
	key := sdk.IdempotencyKey(uuid.NewString())
	name, address, protocol, enabled := input.Name, input.Address, sdk.UpdateLocationRequestProtocol(input.Protocol), input.Enabled
	request := sdk.UpdateLocationRequest{Name: &name, Address: &address, Protocol: &protocol, Policy: input.Policy, Enabled: &enabled}
	response, err := c.client.UpdateLocationWithResponse(ctx, locationID, &sdk.UpdateLocationParams{IdempotencyKey: &key}, request, sdk.WithBearerToken(credential))
	if err != nil {
		return sdk.Location{}, err
	}
	if response.JSON200 != nil {
		return *response.JSON200, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return sdk.Location{}, ErrUnauthorized
	}
	return sdk.Location{}, responseError(response.HTTPResponse, response.Body, "update location")
}

func (c *SDKClient) DisableLocation(ctx context.Context, credential string, locationID openapi_types.UUID) error {
	response, err := c.client.DisableLocationWithResponse(ctx, locationID, sdk.WithBearerToken(credential))
	if err != nil {
		return err
	}
	if response.StatusCode() == http.StatusNoContent {
		return nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	return responseError(response.HTTPResponse, response.Body, "disable location")
}

func (c *SDKClient) GetMonitorHealth(ctx context.Context, credential string, monitorID openapi_types.UUID) (sdk.MonitorHealth, error) {
	response, err := c.client.GetMonitorHealthWithResponse(ctx, monitorID, sdk.WithBearerToken(credential))
	if err != nil {
		return sdk.MonitorHealth{}, err
	}
	if response.JSON200 != nil {
		return *response.JSON200, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return sdk.MonitorHealth{}, ErrUnauthorized
	}
	return sdk.MonitorHealth{}, responseError(response.HTTPResponse, response.Body, "get monitor health")
}

func (c *SDKClient) GetMonitorAvailabilityHistory(ctx context.Context, credential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorAvailabilityHistory, error) {
	response, err := c.client.GetMonitorAvailabilityHistoryWithResponse(ctx, monitorID, &sdk.GetMonitorAvailabilityHistoryParams{
		StartsAt: &startsAt, EndsAt: &endsAt, Limit: &limit,
	}, sdk.WithBearerToken(credential))
	if err != nil {
		return sdk.MonitorAvailabilityHistory{}, err
	}
	if response.JSON200 != nil {
		return *response.JSON200, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return sdk.MonitorAvailabilityHistory{}, ErrUnauthorized
	}
	return sdk.MonitorAvailabilityHistory{}, responseError(response.HTTPResponse, response.Body, "get monitor availability history")
}

func (c *SDKClient) GetMonitorStateHistory(ctx context.Context, credential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorStateHistory, error) {
	response, err := c.client.GetMonitorStateHistoryWithResponse(ctx, monitorID, &sdk.GetMonitorStateHistoryParams{
		StartsAt: &startsAt, EndsAt: &endsAt, Limit: &limit,
	}, sdk.WithBearerToken(credential))
	if err != nil {
		return sdk.MonitorStateHistory{}, err
	}
	if response.JSON200 != nil {
		return *response.JSON200, nil
	}
	if response.StatusCode() == http.StatusUnauthorized {
		return sdk.MonitorStateHistory{}, ErrUnauthorized
	}
	return sdk.MonitorStateHistory{}, responseError(response.HTTPResponse, response.Body, "get monitor state history")
}

func responseError(response *http.Response, body []byte, operation string) error {
	err := sdk.ErrorFromResponse(response, body)
	if err == nil {
		return fmt.Errorf("%s: response has no success body", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// Fake is a deterministic development and test implementation.
type Fake struct {
	username   string
	password   string
	credential string

	mu                 sync.RWMutex
	PublicStatus       sdk.PublicStatusPage
	Monitors           []sdk.Monitor
	Locations          []sdk.Location
	Health             map[openapi_types.UUID]sdk.MonitorHealth
	History            map[openapi_types.UUID]sdk.MonitorAvailabilityHistory
	StateHistory       map[openapi_types.UUID]sdk.MonitorStateHistory
	PublicError        error
	MonitorError       error
	LocationError      error
	SearchError        error
	HealthErrors       map[openapi_types.UUID]error
	HistoryErrors      map[openapi_types.UUID]error
	StateHistoryErrors map[openapi_types.UUID]error
	LocationErrors     map[openapi_types.UUID]error
}

func (f *Fake) SearchResources(ctx context.Context, credential, query string, limit int32) ([]sdk.SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return nil, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.SearchError != nil {
		return nil, f.SearchError
	}
	query = strings.ToLower(strings.TrimSpace(query))
	results := make([]sdk.SearchResult, 0, limit)
	for _, monitor := range f.Monitors {
		if !strings.Contains(strings.ToLower(monitor.Name+" "+monitor.Description+" "+monitor.Id.String()), query) {
			continue
		}
		results = append(results, sdk.SearchResult{ResourceType: sdk.SearchResourceTypeMonitor, ResourceId: monitor.Id, Title: monitor.Name, Description: monitor.Description, Context: strings.ToUpper(string(monitor.Kind)) + " monitor"})
		if int32(len(results)) == limit {
			break
		}
	}
	return results, nil
}

func NewFake(username, password, credential string) *Fake {
	return &Fake{
		username: username, password: password, credential: credential,
		PublicStatus:       sdk.PublicStatusPage{State: sdk.Unknown},
		Health:             map[openapi_types.UUID]sdk.MonitorHealth{},
		History:            map[openapi_types.UUID]sdk.MonitorAvailabilityHistory{},
		StateHistory:       map[openapi_types.UUID]sdk.MonitorStateHistory{},
		HealthErrors:       map[openapi_types.UUID]error{},
		HistoryErrors:      map[openapi_types.UUID]error{},
		StateHistoryErrors: map[openapi_types.UUID]error{},
		LocationErrors:     map[openapi_types.UUID]error{},
	}
}

// RecordDevelopmentProbe appends one immutable synthetic probe evaluation to
// the in-memory development control plane. The real server persists these
// records through the agent/result path; the local fake uses the same public
// shape so the UI can exercise the historical tick stream without a private
// control-plane image.
func (f *Fake) RecordDevelopmentProbe(monitorID openapi_types.UUID, state sdk.HealthState, observedAt time.Time) {
	if f == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordDevelopmentProbeLocked(monitorID, state, observedAt.UTC())
}

// RecordDevelopmentProbeForMonitors advances every configured monitor in the
// development fake. It deliberately emits no availability sample for
// Unknown, preserving the distinction between a missing observation and a
// failed probe while still recording the state-tick reason.
func (f *Fake) RecordDevelopmentProbeForMonitors(observedAt time.Time) {
	if f == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	observedAt = observedAt.UTC()
	for _, monitor := range f.Monitors {
		state := sdk.Unknown
		if health, ok := f.Health[monitor.Id]; ok && health.State != "" {
			state = health.State
		}
		f.recordDevelopmentProbeLocked(monitor.Id, state, observedAt)
	}
}

func (f *Fake) recordDevelopmentProbeLocked(monitorID openapi_types.UUID, state sdk.HealthState, observedAt time.Time) {
	if f.Health == nil {
		f.Health = make(map[openapi_types.UUID]sdk.MonitorHealth)
	}
	if f.History == nil {
		f.History = make(map[openapi_types.UUID]sdk.MonitorAvailabilityHistory)
	}
	if f.StateHistory == nil {
		f.StateHistory = make(map[openapi_types.UUID]sdk.MonitorStateHistory)
	}
	if state == "" {
		state = sdk.Unknown
	}
	previous := f.Health[monitorID]
	if previous.MonitorId == uuid.Nil {
		previous.MonitorId = monitorID
	}
	if previous.State != state || previous.LastTransitionAt.IsZero() {
		previous.LastTransitionAt = observedAt
	}
	previous.State = state
	f.Health[monitorID] = previous

	reason := sdk.StateTickReasonCodeProbeTimeout
	sampleOutcome := sdk.MonitorAvailabilitySampleOutcome("")
	switch state {
	case sdk.Up:
		reason = sdk.StateTickReasonCodeProbeSuccess
		sampleOutcome = sdk.MonitorAvailabilitySampleOutcomePassed
	case sdk.Down, sdk.Degraded:
		reason = sdk.StateTickReasonCodeProbeFailure
		sampleOutcome = sdk.MonitorAvailabilitySampleOutcomeFailed
	}
	history := f.StateHistory[monitorID]
	history.MonitorId = monitorID
	history.GeneratedAt = observedAt
	history.Ticks = append(history.Ticks, sdk.MonitorStateTick{
		ActionId:   uuid.New(),
		Actor:      sdk.StateTickActor{Kind: sdk.StateTickActorKindSystem},
		Health:     state,
		Id:         uuid.New(),
		Lifecycle:  sdk.Active,
		MonitorId:  monitorID,
		OccurredAt: observedAt,
		ReasonCode: reason,
	})
	f.StateHistory[monitorID] = history
	if sampleOutcome == "" {
		return
	}
	availabilityHistory := f.History[monitorID]
	availabilityHistory.MonitorId = monitorID
	availabilityHistory.GeneratedAt = observedAt
	availabilityHistory.Samples = append(availabilityHistory.Samples, sdk.MonitorAvailabilitySample{
		Id:            uuid.New(),
		LocationId:    uuid.NewSHA1(uuid.Nil, []byte("development-location:"+monitorID.String())),
		LatencyMillis: 1,
		ObservedAt:    observedAt,
		Outcome:       sampleOutcome,
	})
	f.History[monitorID] = availabilityHistory
}

func (f *Fake) ExchangeAdministratorCredentials(ctx context.Context, username, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(f.username))
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(f.password))
	if usernameOK&passwordOK != 1 {
		return "", ErrInvalidCredentials
	}
	return f.credential, nil
}

func (f *Fake) RevokeSession(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

func (f *Fake) GetPublicStatusPage(ctx context.Context) (sdk.PublicStatusPage, error) {
	if err := ctx.Err(); err != nil {
		return sdk.PublicStatusPage{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.PublicStatus, f.PublicError
}

func (f *Fake) ListMonitors(ctx context.Context, credential, cursor string, limit int32) (sdk.Page[sdk.Monitor], error) {
	if err := ctx.Err(); err != nil {
		return sdk.Page[sdk.Monitor]{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.Page[sdk.Monitor]{}, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.MonitorError != nil {
		return sdk.Page[sdk.Monitor]{}, f.MonitorError
	}
	start := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "offset:%d", &start)
	}
	end := start + int(limit)
	if end > len(f.Monitors) {
		end = len(f.Monitors)
	}
	next := ""
	if end < len(f.Monitors) {
		next = fmt.Sprintf("offset:%d", end)
	}
	return sdk.Page[sdk.Monitor]{Items: append([]sdk.Monitor(nil), f.Monitors[start:end]...), NextCursor: next}, nil
}

func (f *Fake) ListLocations(ctx context.Context, credential, cursor string, limit int32) (sdk.Page[sdk.Location], error) {
	if err := ctx.Err(); err != nil {
		return sdk.Page[sdk.Location]{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.Page[sdk.Location]{}, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.LocationError != nil {
		return sdk.Page[sdk.Location]{}, f.LocationError
	}
	start := 0
	if cursor != "" {
		_, _ = fmt.Sscanf(cursor, "offset:%d", &start)
	}
	if start < 0 || start > len(f.Locations) {
		start = len(f.Locations)
	}
	end := start + int(limit)
	if end > len(f.Locations) {
		end = len(f.Locations)
	}
	next := ""
	if end < len(f.Locations) {
		next = fmt.Sprintf("offset:%d", end)
	}
	return sdk.Page[sdk.Location]{Items: append([]sdk.Location(nil), f.Locations[start:end]...), NextCursor: next}, nil
}

func (f *Fake) CreateLocation(ctx context.Context, credential string, input LocationInput) (sdk.Location, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Location{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.Location{}, ErrUnauthorized
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.LocationError != nil {
		return sdk.Location{}, f.LocationError
	}
	now := time.Now().UTC()
	enabled := true
	protocol := sdk.LocationProtocol(input.Protocol)
	if protocol == "" {
		protocol = sdk.LocationProtocol("http")
	}
	policy := sdk.LocationPolicy{IntervalSeconds: 60, TimeoutMillis: 5000, FailureThreshold: 3, RecoveryThreshold: 2}
	if input.Policy != nil {
		if input.Policy.IntervalSeconds != nil {
			policy.IntervalSeconds = *input.Policy.IntervalSeconds
		}
		if input.Policy.TimeoutMillis != nil {
			policy.TimeoutMillis = *input.Policy.TimeoutMillis
		}
		if input.Policy.FailureThreshold != nil {
			policy.FailureThreshold = *input.Policy.FailureThreshold
		}
		if input.Policy.RecoveryThreshold != nil {
			policy.RecoveryThreshold = *input.Policy.RecoveryThreshold
		}
	}
	location := sdk.Location{Id: uuid.New(), Name: strings.TrimSpace(input.Name), Address: input.Address, Protocol: protocol, Policy: policy, Enabled: &enabled, CreatedAt: now, UpdatedAt: &now}
	f.Locations = append(f.Locations, location)
	return location, nil
}

func (f *Fake) UpdateLocation(ctx context.Context, credential string, locationID openapi_types.UUID, input LocationInput) (sdk.Location, error) {
	if err := ctx.Err(); err != nil {
		return sdk.Location{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.Location{}, ErrUnauthorized
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.LocationErrors[locationID]; err != nil {
		return sdk.Location{}, err
	}
	for index := range f.Locations {
		if f.Locations[index].Id != locationID {
			continue
		}
		f.Locations[index].Name = strings.TrimSpace(input.Name)
		f.Locations[index].Address = strings.TrimSpace(input.Address)
		f.Locations[index].Protocol = sdk.LocationProtocol(input.Protocol)
		if f.Locations[index].Protocol == "" {
			f.Locations[index].Protocol = sdk.LocationProtocol("http")
		}
		f.Locations[index].Enabled = boolPointer(input.Enabled)
		if input.Policy != nil {
			if input.Policy.IntervalSeconds != nil {
				f.Locations[index].Policy.IntervalSeconds = *input.Policy.IntervalSeconds
			}
			if input.Policy.TimeoutMillis != nil {
				f.Locations[index].Policy.TimeoutMillis = *input.Policy.TimeoutMillis
			}
			if input.Policy.FailureThreshold != nil {
				f.Locations[index].Policy.FailureThreshold = *input.Policy.FailureThreshold
			}
			if input.Policy.RecoveryThreshold != nil {
				f.Locations[index].Policy.RecoveryThreshold = *input.Policy.RecoveryThreshold
			}
		}
		now := time.Now().UTC()
		f.Locations[index].UpdatedAt = &now
		return f.Locations[index], nil
	}
	return sdk.Location{}, &sdk.APIError{StatusCode: http.StatusNotFound}
}

func (f *Fake) DisableLocation(ctx context.Context, credential string, locationID openapi_types.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return ErrUnauthorized
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.LocationErrors[locationID]; err != nil {
		return err
	}
	for index := range f.Locations {
		if f.Locations[index].Id != locationID {
			continue
		}
		f.Locations[index].Enabled = boolPointer(false)
		now := time.Now().UTC()
		f.Locations[index].UpdatedAt = &now
		return nil
	}
	return &sdk.APIError{StatusCode: http.StatusNotFound}
}

func boolPointer(value bool) *bool { return &value }

func (f *Fake) GetMonitorHealth(ctx context.Context, credential string, monitorID openapi_types.UUID) (sdk.MonitorHealth, error) {
	if err := ctx.Err(); err != nil {
		return sdk.MonitorHealth{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.MonitorHealth{}, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if err := f.HealthErrors[monitorID]; err != nil {
		return sdk.MonitorHealth{}, err
	}
	if health, ok := f.Health[monitorID]; ok {
		return health, nil
	}
	return sdk.MonitorHealth{MonitorId: monitorID, State: sdk.Unknown}, nil
}

func (f *Fake) GetMonitorAvailabilityHistory(ctx context.Context, credential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorAvailabilityHistory, error) {
	if err := ctx.Err(); err != nil {
		return sdk.MonitorAvailabilityHistory{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.MonitorAvailabilityHistory{}, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if err := f.HistoryErrors[monitorID]; err != nil {
		return sdk.MonitorAvailabilityHistory{}, err
	}
	if history, ok := f.History[monitorID]; ok {
		history.Samples = append([]sdk.MonitorAvailabilitySample(nil), history.Samples...)
		return history, nil
	}
	return sdk.MonitorAvailabilityHistory{MonitorId: monitorID, StartsAt: startsAt, EndsAt: endsAt, GeneratedAt: time.Now().UTC()}, nil
}

func (f *Fake) GetMonitorStateHistory(ctx context.Context, credential string, monitorID openapi_types.UUID, startsAt, endsAt time.Time, limit int32) (sdk.MonitorStateHistory, error) {
	if err := ctx.Err(); err != nil {
		return sdk.MonitorStateHistory{}, err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return sdk.MonitorStateHistory{}, ErrUnauthorized
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if err := f.StateHistoryErrors[monitorID]; err != nil {
		return sdk.MonitorStateHistory{}, err
	}
	if history, ok := f.StateHistory[monitorID]; ok {
		history.Ticks = append([]sdk.MonitorStateTick(nil), history.Ticks...)
		return history, nil
	}
	return sdk.MonitorStateHistory{MonitorId: monitorID, StartsAt: startsAt, EndsAt: endsAt, GeneratedAt: endsAt.UTC(), Ticks: []sdk.MonitorStateTick{}}, nil
}

var _ Client = (*SDKClient)(nil)
var _ Client = (*Fake)(nil)
var _ LocationClient = (*SDKClient)(nil)
var _ LocationClient = (*Fake)(nil)
