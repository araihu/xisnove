// Package controlplane adapts the generated Xisnove SDK to the UI BFF.
package controlplane

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/araihu/xisnove/sdk"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrUnauthorized       = errors.New("control-plane session is unauthorized")
)

// Client is the UI-facing subset of the public generated SDK.
type Client interface {
	ExchangeAdministratorCredentials(ctx context.Context, email, password string) (opaqueCredential string, err error)
	RevokeSession(ctx context.Context, opaqueCredential string) error
	GetPublicStatusPage(ctx context.Context) (sdk.PublicStatusPage, error)
	SearchResources(ctx context.Context, opaqueCredential, query string, limit int32) ([]sdk.SearchResult, error)
	ListMonitors(ctx context.Context, opaqueCredential, cursor string, limit int32) (sdk.Page[sdk.Monitor], error)
	GetMonitorHealth(ctx context.Context, opaqueCredential string, monitorID openapi_types.UUID) (sdk.MonitorHealth, error)
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

	mu           sync.RWMutex
	PublicStatus sdk.PublicStatusPage
	Monitors     []sdk.Monitor
	Health       map[openapi_types.UUID]sdk.MonitorHealth
	PublicError  error
	MonitorError error
	SearchError  error
	HealthErrors map[openapi_types.UUID]error
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
		PublicStatus: sdk.PublicStatusPage{State: sdk.Unknown},
		Health:       map[openapi_types.UUID]sdk.MonitorHealth{},
		HealthErrors: map[openapi_types.UUID]error{},
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

var _ Client = (*SDKClient)(nil)
var _ Client = (*Fake)(nil)
