// Package sdk adapts the public Xisnove generated SDK to the narrow
// controller-facing control-plane boundary.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	publicsdk "github.com/araihu/xisnove/sdk"
	"github.com/google/uuid"
)

// HTTPDoer is the small transport seam needed by the adapter. It intentionally
// does not expose a generated SDK type to controller construction.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Option func(*options)

type options struct {
	httpClient HTTPDoer
}

func WithHTTPClient(client HTTPDoer) Option {
	return func(options *options) {
		options.httpClient = client
	}
}

type Client struct {
	client *publicsdk.ClientWithResponses
}

var _ controlplane.Client = (*Client)(nil)

func New(baseURL, bearerToken string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, errors.New("control plane base URL is empty")
	}
	if bearerToken == "" {
		return nil, errors.New("control plane bearer token is empty")
	}

	config := options{}
	for _, option := range opts {
		option(&config)
	}
	clientOptions := []publicsdk.ClientOption{
		publicsdk.WithRequestEditorFn(publicsdk.WithBearerToken(bearerToken)),
	}
	if config.httpClient != nil {
		clientOptions = append(clientOptions, publicsdk.WithHTTPClient(config.httpClient))
	}
	client, err := publicsdk.NewClientWithResponses(baseURL, clientOptions...)
	if err != nil {
		return nil, errors.New("create control plane client")
	}
	return &Client{client: client}, nil
}

func (c *Client) ApplyMonitor(ctx context.Context, request controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
	monitor, err := monitorRequest(request.Name, request.Spec)
	if err != nil {
		return controlplane.MonitorState{}, err
	}
	response, err := c.client.ApplyOperatorMonitorWithResponse(ctx,
		&publicsdk.ApplyOperatorMonitorParams{IdempotencyKey: request.IdempotencyKey},
		publicsdk.ApplyOperatorMonitorRequest{Owner: owner(request.Owner), Monitor: monitor},
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return controlplane.MonitorState{}, transportError(err)
	}
	if err := responseError(response.HTTPResponse, response.Body); err != nil {
		return controlplane.MonitorState{}, err
	}
	if response.JSON200 == nil {
		return controlplane.MonitorState{}, errors.New("control plane response missing monitor state")
	}
	return controlplane.MonitorState{
		ExternalID:             response.JSON200.ExternalId.String(),
		AggregateHealth:        string(response.JSON200.State),
		HealthLastTransitionAt: response.JSON200.LastTransitionAt,
	}, nil
}

func (c *Client) DeleteMonitor(ctx context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	body, err := deleteMonitorRequest(request)
	if err != nil {
		return err
	}
	response, err := c.client.DeleteOperatorMonitorWithResponse(ctx,
		&publicsdk.DeleteOperatorMonitorParams{IdempotencyKey: request.IdempotencyKey}, body,
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return transportError(err)
	}
	return responseError(response.HTTPResponse, response.Body)
}

func (c *Client) ApplyAgent(ctx context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
	locationID, err := uuid.Parse(request.Spec.LocationID)
	if err != nil {
		return controlplane.AgentState{}, errors.New("control plane agent location ID is invalid")
	}
	credential := string(request.InitialCredential)
	response, err := c.client.ApplyOperatorAgentWithResponse(ctx,
		&publicsdk.ApplyOperatorAgentParams{IdempotencyKey: request.IdempotencyKey},
		publicsdk.ApplyOperatorAgentRequest{
			Owner:        owner(request.Owner),
			Name:         request.Name,
			LocationId:   locationID,
			Enabled:      true,
			Capabilities: capabilities(request.Spec.Capabilities),
			InitialCredential: publicsdk.OperatorInitialCredential{
				Generation: 1,
				Credential: &credential,
			},
		},
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return controlplane.AgentState{}, transportError(err)
	}
	if err := responseError(response.HTTPResponse, response.Body); err != nil {
		return controlplane.AgentState{}, err
	}
	if response.JSON200 == nil {
		return controlplane.AgentState{}, errors.New("control plane response missing agent state")
	}
	state := controlplane.AgentState{
		ExternalID:           response.JSON200.ExternalId.String(),
		CredentialGeneration: response.JSON200.CredentialGeneration,
	}
	if response.JSON200.LastSeenAt != nil {
		state.LastHeartbeatAt = *response.JSON200.LastSeenAt
	}
	if response.JSON200.LastDiscoveryAt != nil {
		state.LastDiscoverySyncAt = *response.JSON200.LastDiscoveryAt
	}
	if response.JSON200.PresentedCredentialGeneration != nil {
		state.PresentedCredentialGeneration = *response.JSON200.PresentedCredentialGeneration
	}
	return state, nil
}

func (c *Client) ObserveAgent(ctx context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
	body := publicsdk.ObserveOperatorAgentRequest{Owner: owner(request.Owner)}
	if request.ExternalID != "" {
		id, err := uuid.Parse(request.ExternalID)
		if err != nil {
			return controlplane.AgentState{}, errors.New("control plane agent identifier is invalid")
		}
		body.ExternalId = &id
	}
	response, err := c.client.ObserveOperatorAgentWithResponse(ctx, body)
	if err != nil {
		return controlplane.AgentState{}, transportError(err)
	}
	if err := responseError(response.HTTPResponse, response.Body); err != nil {
		return controlplane.AgentState{}, err
	}
	if response.JSON200 == nil {
		return controlplane.AgentState{}, errors.New("control plane response missing agent state")
	}
	state := controlplane.AgentState{ExternalID: response.JSON200.ExternalId.String(), CredentialGeneration: response.JSON200.CredentialGeneration}
	if response.JSON200.PresentedCredentialGeneration != nil {
		state.PresentedCredentialGeneration = *response.JSON200.PresentedCredentialGeneration
	}
	if response.JSON200.LastSeenAt != nil {
		state.LastHeartbeatAt = *response.JSON200.LastSeenAt
	}
	if response.JSON200.LastDiscoveryAt != nil {
		state.LastDiscoverySyncAt = *response.JSON200.LastDiscoveryAt
	}
	return state, nil
}

func (c *Client) PutAgentCredential(ctx context.Context, request controlplane.PutAgentCredentialRequest) error {
	agentID, err := uuid.Parse(request.ExternalID)
	if err != nil {
		return errors.New("control plane agent identifier is invalid")
	}
	credential := string(request.Credential)
	response, err := c.client.PutOperatorAgentCredentialWithResponse(ctx, agentID, request.Generation,
		&publicsdk.PutOperatorAgentCredentialParams{IdempotencyKey: request.IdempotencyKey},
		publicsdk.PutOperatorAgentCredentialRequest{Owner: owner(request.Owner), Credential: &credential},
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return transportError(err)
	}
	return responseError(response.HTTPResponse, response.Body)
}

func (c *Client) RevokeAgentCredential(ctx context.Context, request controlplane.RevokeAgentCredentialRequest) error {
	agentID, err := uuid.Parse(request.ExternalID)
	if err != nil {
		return errors.New("control plane agent identifier is invalid")
	}
	response, err := c.client.RevokeOperatorAgentCredentialWithResponse(ctx, agentID, request.Generation,
		&publicsdk.RevokeOperatorAgentCredentialParams{IdempotencyKey: request.IdempotencyKey},
		publicsdk.RevokeOperatorAgentCredentialRequest{Owner: owner(request.Owner)},
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return transportError(err)
	}
	return responseError(response.HTTPResponse, response.Body)
}

func (c *Client) DeleteAgent(ctx context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	body, err := deleteAgentRequest(request)
	if err != nil {
		return err
	}
	response, err := c.client.DeleteOperatorAgentWithResponse(ctx,
		&publicsdk.DeleteOperatorAgentParams{IdempotencyKey: request.IdempotencyKey}, body,
		publicsdk.WithIdempotencyKey(request.IdempotencyKey),
	)
	if err != nil {
		return transportError(err)
	}
	return responseError(response.HTTPResponse, response.Body)
}

func owner(reference controlplane.OwnerReference) publicsdk.ExternalOwner {
	return publicsdk.ExternalOwner{Key: reference.Key, Uid: reference.UID}
}

func capabilities(values []monitoringv1alpha1.AgentCapability) []publicsdk.AgentCapability {
	result := make([]publicsdk.AgentCapability, len(values))
	for index, value := range values {
		result[index] = publicsdk.AgentCapability(value)
	}
	return result
}

func deleteMonitorRequest(request controlplane.DeleteRemoteObjectRequest) (publicsdk.DeleteOperatorMonitorRequest, error) {
	result := publicsdk.DeleteOperatorMonitorRequest{Owner: owner(request.Owner)}
	if request.ExternalID == "" {
		return result, nil
	}
	id, err := uuid.Parse(request.ExternalID)
	if err != nil {
		return publicsdk.DeleteOperatorMonitorRequest{}, errors.New("control plane monitor identifier is invalid")
	}
	result.ExternalId = &id
	return result, nil
}

func deleteAgentRequest(request controlplane.DeleteRemoteObjectRequest) (publicsdk.DeleteOperatorAgentRequest, error) {
	result := publicsdk.DeleteOperatorAgentRequest{Owner: owner(request.Owner)}
	if request.ExternalID == "" {
		return result, nil
	}
	id, err := uuid.Parse(request.ExternalID)
	if err != nil {
		return publicsdk.DeleteOperatorAgentRequest{}, errors.New("control plane agent identifier is invalid")
	}
	result.ExternalId = &id
	return result, nil
}

func monitorRequest(name string, spec monitoringv1alpha1.MonitorSpec) (publicsdk.UpdateMonitorRequest, error) {
	locationID, err := uuid.Parse(spec.LocationID)
	if err != nil {
		return publicsdk.UpdateMonitorRequest{}, errors.New("control plane monitor location ID is invalid")
	}
	probe, err := monitorProbe(spec.Probe)
	if err != nil {
		return publicsdk.UpdateMonitorRequest{}, err
	}
	return publicsdk.UpdateMonitorRequest{
		Name:              name,
		Description:       spec.Description,
		Labels:            stringMap(spec.Labels),
		DisplayOrder:      spec.DisplayOrder,
		Public:            spec.Public,
		Enabled:           true,
		IntervalSeconds:   spec.IntervalSeconds,
		TimeoutMillis:     spec.TimeoutMillis,
		FailureThreshold:  spec.FailureThreshold,
		RecoveryThreshold: spec.RecoveryThreshold,
		LocationId:        locationID,
		RequiredLocation:  spec.RequiredLocation,
		Probe:             probe,
	}, nil
}

func monitorProbe(spec monitoringv1alpha1.MonitorProbeSpec) (publicsdk.ProbeDefinition, error) {
	var result publicsdk.ProbeDefinition
	switch spec.Kind {
	case "http":
		if spec.HTTP == nil {
			return result, errors.New("control plane monitor HTTP probe is missing")
		}
		method := spec.HTTP.Method
		if method == "" {
			method = http.MethodGet
		}
		err := result.FromHTTPProbeDefinition(publicsdk.HTTPProbeDefinition{
			Body:                       []byte(spec.HTTP.Body),
			BodyContains:               stringsOrEmpty(spec.HTTP.BodyContains),
			BodyDoesNotContain:         stringsOrEmpty(spec.HTTP.BodyDoesNotContain),
			ExpectedStatus:             statusRanges(spec.HTTP.ExpectedStatus),
			FollowRedirects:            spec.HTTP.FollowRedirects,
			Headers:                    stringMap(spec.HTTP.Headers),
			Kind:                       publicsdk.HTTPProbeDefinitionKind("http"),
			Method:                     publicsdk.HTTPProbeDefinitionMethod(method),
			TlsMinimumRemainingSeconds: spec.HTTP.TLSMinimumRemainingSeconds,
			Url:                        spec.HTTP.URL,
		})
		if err != nil {
			return result, errors.New("encode control plane HTTP probe")
		}
	case "tcp":
		if spec.TCP == nil {
			return result, errors.New("control plane monitor TCP probe is missing")
		}
		err := result.FromTCPProbeDefinition(publicsdk.TCPProbeDefinition{
			Expect:                     []byte(spec.TCP.Expect),
			Host:                       spec.TCP.Host,
			Kind:                       publicsdk.TCPProbeDefinitionKind("tcp"),
			Port:                       spec.TCP.Port,
			Send:                       []byte(spec.TCP.Send),
			TlsMinimumRemainingSeconds: spec.TCP.TLSMinimumRemainingSeconds,
		})
		if err != nil {
			return result, errors.New("encode control plane TCP probe")
		}
	case "dns":
		if spec.DNS == nil {
			return result, errors.New("control plane monitor DNS probe is missing")
		}
		err := result.FromDNSProbeDefinition(publicsdk.DNSProbeDefinition{
			ExpectedValues: stringsOrEmpty(spec.DNS.ExpectedValues),
			Kind:           publicsdk.DNSProbeDefinitionKind("dns"),
			Name:           spec.DNS.Name,
			RecordType:     publicsdk.DNSProbeDefinitionRecordType(spec.DNS.RecordType),
			Resolver:       spec.DNS.Resolver,
		})
		if err != nil {
			return result, errors.New("encode control plane DNS probe")
		}
	default:
		return result, errors.New("control plane monitor probe kind is invalid")
	}
	return result, nil
}

func statusRanges(values []monitoringv1alpha1.StatusRange) []publicsdk.StatusRange {
	result := make([]publicsdk.StatusRange, len(values))
	for index, value := range values {
		result[index] = publicsdk.StatusRange{Minimum: value.Minimum, Maximum: value.Maximum}
	}
	return result
}

func stringsOrEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func stringMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}

func transportError(error) error {
	return errors.New("control plane request failed")
}

func responseError(response *http.Response, body []byte) error {
	err := publicsdk.ErrorFromResponse(response, body)
	if err == nil {
		return nil
	}
	apiError, ok := err.(*publicsdk.APIError)
	if !ok {
		return errors.New("control plane request failed")
	}
	if apiError.StatusCode == http.StatusNotFound {
		return controlplane.ErrNotFound
	}
	if apiError.StatusCode == http.StatusConflict {
		switch apiError.Problem.Code {
		case "operator_ownership_conflict":
			return controlplane.ErrOwnershipConflict
		case "operator_credential_hash_conflict":
			return controlplane.ErrCredentialConflict
		}
	}
	return fmt.Errorf("control plane request failed: HTTP %d", apiError.StatusCode)
}
