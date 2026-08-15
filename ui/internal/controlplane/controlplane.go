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
	Health             map[openapi_types.UUID]sdk.MonitorHealth
	History            map[openapi_types.UUID]sdk.MonitorAvailabilityHistory
	StateHistory       map[openapi_types.UUID]sdk.MonitorStateHistory
	PublicError        error
	MonitorError       error
	SearchError        error
	HealthErrors       map[openapi_types.UUID]error
	HistoryErrors      map[openapi_types.UUID]error
	StateHistoryErrors map[openapi_types.UUID]error
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
	}
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
		return history, nil
	}
	return sdk.MonitorStateHistory{MonitorId: monitorID, StartsAt: startsAt, EndsAt: endsAt, GeneratedAt: endsAt.UTC(), Ticks: []sdk.MonitorStateTick{}}, nil
}

var _ Client = (*SDKClient)(nil)
var _ Client = (*Fake)(nil)
