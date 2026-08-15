package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

type ServerConfig struct {
	Auth          *application.AuthService
	APITokens     *application.APITokenService
	Configuration *application.ConfigurationService
	Agents        *application.AgentService
	Lease         *application.LeaseService
	Results       *application.ResultService
	Health        *application.HealthService
	History       *application.MonitorHistoryService
	Notifications *application.NotificationAdminService
	Management    *application.ManagementService
	PublicStatus  *application.PublicStatusService
	Discovery     *application.DiscoveryService
	Operator      application.OperatorService
}

type Server struct {
	// StrictServerInterface keeps the complete generated contract available while
	// milestone implementation slices add concrete handlers operation by operation.
	StrictServerInterface
	auth          *application.AuthService
	apiTokens     *application.APITokenService
	configuration *application.ConfigurationService
	agents        *application.AgentService
	lease         *application.LeaseService
	results       *application.ResultService
	health        *application.HealthService
	history       *application.MonitorHistoryService
	notifications *application.NotificationAdminService
	management    *application.ManagementService
	publicStatus  *application.PublicStatusService
	discovery     *application.DiscoveryService
	operator      application.OperatorService
}

func NewServer(config ServerConfig) *Server {
	return &Server{
		auth:          config.Auth,
		apiTokens:     config.APITokens,
		configuration: config.Configuration,
		agents:        config.Agents,
		lease:         config.Lease,
		results:       config.Results,
		health:        config.Health,
		history:       config.History,
		notifications: config.Notifications,
		management:    config.Management,
		publicStatus:  config.PublicStatus,
		discovery:     config.Discovery,
		operator:      config.Operator,
	}
}

func (s *Server) CreateLocation(
	ctx context.Context,
	request CreateLocationRequestObject,
) (CreateLocationResponseObject, error) {
	if request.Body == nil {
		response, _ := createLocationProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return response, nil
	}
	location, err := s.configuration.CreateLocation(
		ctx,
		application.CreateLocationCommand{Name: request.Body.Name},
	)
	if err != nil {
		response, ok := createLocationProblem(err)
		if ok {
			return response, nil
		}
		return nil, err
	}
	mapped, err := mapLocation(location)
	if err != nil {
		return nil, err
	}
	return CreateLocation201JSONResponse(mapped), nil
}

func (s *Server) CreateMonitor(
	ctx context.Context,
	request CreateMonitorRequestObject,
) (CreateMonitorResponseObject, error) {
	if request.Body == nil {
		response, _ := createMonitorProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return response, nil
	}
	if request.Body.FailureThreshold <= 0 ||
		request.Body.FailureThreshold > math.MaxUint16 ||
		request.Body.RecoveryThreshold <= 0 ||
		request.Body.RecoveryThreshold > math.MaxUint16 {
		response, _ := createMonitorProblem(&application.ValidationError{
			Fields: map[string]string{"monitor": "contains invalid configuration"},
		})
		return response, nil
	}
	probe, err := probeFromAPI(request.Body.Probe)
	if err != nil {
		response, _ := createMonitorProblem(&application.ValidationError{
			Fields: map[string]string{"probe": "contains invalid configuration"},
		})
		return response, nil
	}
	monitor, err := s.configuration.CreateMonitor(
		ctx,
		application.CreateMonitorCommand{
			Name:              request.Body.Name,
			Description:       pointerValue(request.Body.Description),
			Labels:            mapPointerValue(request.Body.Labels),
			DisplayOrder:      pointerValue(request.Body.DisplayOrder),
			Public:            pointerValue(request.Body.Public),
			LocationID:        domain.LocationID(request.Body.LocationId.String()),
			RequiredLocation:  request.Body.RequiredLocation,
			Interval:          time.Duration(request.Body.IntervalSeconds) * time.Second,
			Timeout:           time.Duration(request.Body.TimeoutMillis) * time.Millisecond,
			FailureThreshold:  uint16(request.Body.FailureThreshold),
			RecoveryThreshold: uint16(request.Body.RecoveryThreshold),
			Probe:             probe,
		},
	)
	if err != nil {
		response, ok := createMonitorProblem(err)
		if ok {
			return response, nil
		}
		return nil, err
	}
	mapped, err := mapMonitor(monitor)
	if err != nil {
		return nil, err
	}
	return CreateMonitor201JSONResponse(mapped), nil
}

func (s *Server) GetMonitor(
	ctx context.Context,
	request GetMonitorRequestObject,
) (GetMonitorResponseObject, error) {
	var monitor application.ConfiguredMonitor
	var err error
	if s.management != nil {
		monitor, err = s.management.GetMonitor(ctx, domain.MonitorID(request.MonitorId.String()))
	} else {
		monitor, err = s.configuration.GetMonitor(ctx, domain.MonitorID(request.MonitorId.String()))
	}
	if err != nil {
		response, ok := getMonitorProblem(err)
		if ok {
			return response, nil
		}
		return nil, err
	}
	mapped, err := mapMonitor(monitor)
	if err != nil {
		return nil, err
	}
	return GetMonitor200JSONResponse(mapped), nil
}

func mapLocation(location domain.Location) (Location, error) {
	id, err := uuid.Parse(string(location.ID))
	if err != nil {
		return Location{}, fmt.Errorf("map location ID: %w", err)
	}
	return Location{
		Id: id, Name: location.Name, Enabled: &location.Enabled,
		CreatedAt: location.CreatedAt, UpdatedAt: &location.UpdatedAt,
	}, nil
}

func mapMonitor(configured application.ConfiguredMonitor) (Monitor, error) {
	id, err := uuid.Parse(string(configured.ID))
	if err != nil {
		return Monitor{}, fmt.Errorf("map monitor ID: %w", err)
	}
	locationID, err := uuid.Parse(string(configured.LocationID))
	if err != nil {
		return Monitor{}, fmt.Errorf("map monitor location ID: %w", err)
	}
	probe, err := probeToAPI(configured.Probe())
	if err != nil {
		return Monitor{}, err
	}
	return Monitor{
		Id:                id,
		Kind:              MonitorKind(configured.Kind),
		Name:              configured.Name,
		Description:       configured.Description,
		Labels:            configured.MetadataLabels(),
		DisplayOrder:      configured.DisplayOrder,
		Public:            configured.Public,
		Enabled:           configured.Enabled,
		IntervalSeconds:   int32(configured.Interval / time.Second),
		TimeoutMillis:     int32(configured.Timeout / time.Millisecond),
		FailureThreshold:  int32(configured.FailureThreshold),
		RecoveryThreshold: int32(configured.RecoveryThreshold),
		LocationId:        locationID,
		RequiredLocation:  configured.RequiredLocation,
		Probe:             probe,
		CreatedAt:         configured.CreatedAt,
		UpdatedAt:         configured.UpdatedAt,
	}, nil
}

func pointerValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}

func mapPointerValue(value *map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	return *value
}

func probeFromAPI(probe ProbeDefinition) (domain.ProbeDefinition, error) {
	kind, err := probe.Discriminator()
	if err != nil {
		return domain.ProbeDefinition{}, fmt.Errorf("read probe discriminator: %w", err)
	}
	switch kind {
	case "http":
		if err := validateProbeFields(probe,
			"kind", "method", "url", "headers", "body", "expectedStatus",
			"bodyContains", "bodyDoesNotContain", "followRedirects",
			"tlsMinimumRemainingSeconds",
		); err != nil {
			return domain.ProbeDefinition{}, err
		}
		value, err := probe.AsHTTPProbeDefinition()
		if err != nil ||
			!value.Method.Valid() ||
			len(value.ExpectedStatus) == 0 ||
			len(value.Body) > 4<<10 {
			return domain.ProbeDefinition{}, errors.New("invalid HTTP probe")
		}
		headers := make(map[string]string, len(value.Headers))
		for name, headerValue := range value.Headers {
			if secretHeader(name) {
				return domain.ProbeDefinition{}, errors.New("secret HTTP header is not accepted")
			}
			headers[name] = headerValue
		}
		statuses := make([]domain.StatusRange, len(value.ExpectedStatus))
		for i, status := range value.ExpectedStatus {
			statuses[i] = domain.StatusRange{
				Min: int(status.Minimum), Max: int(status.Maximum),
			}
		}
		return domain.ProbeDefinition{
			Kind: domain.MonitorKindHTTP,
			HTTP: domain.HTTPProbe{
				Method:             string(value.Method),
				URL:                value.Url,
				Headers:            headers,
				Body:               append([]byte(nil), value.Body...),
				ExpectedStatus:     statuses,
				BodyContains:       append([]string(nil), value.BodyContains...),
				BodyDoesNotContain: append([]string(nil), value.BodyDoesNotContain...),
				FollowRedirects:    value.FollowRedirects,
				TLS:                tlsFromSeconds(value.TlsMinimumRemainingSeconds),
			},
		}, nil
	case "tcp":
		if err := validateProbeFields(probe,
			"kind", "host", "port", "send", "expect", "tlsMinimumRemainingSeconds",
		); err != nil {
			return domain.ProbeDefinition{}, err
		}
		value, err := probe.AsTCPProbeDefinition()
		if err != nil ||
			value.Port <= 0 ||
			value.Port > math.MaxUint16 ||
			len(value.Send) > 4<<10 ||
			len(value.Expect) > 4<<10 {
			return domain.ProbeDefinition{}, errors.New("invalid TCP probe")
		}
		return domain.ProbeDefinition{
			Kind: domain.MonitorKindTCP,
			TCP: domain.TCPProbe{
				Host: value.Host, Port: uint16(value.Port),
				Send:   append([]byte(nil), value.Send...),
				Expect: append([]byte(nil), value.Expect...),
				TLS:    tlsFromSeconds(value.TlsMinimumRemainingSeconds),
			},
		}, nil
	case "dns":
		if err := validateProbeFields(
			probe, "kind", "resolver", "name", "recordType", "expectedValues",
		); err != nil {
			return domain.ProbeDefinition{}, err
		}
		value, err := probe.AsDNSProbeDefinition()
		if err != nil || !value.RecordType.Valid() {
			return domain.ProbeDefinition{}, errors.New("invalid DNS probe")
		}
		return domain.ProbeDefinition{
			Kind: domain.MonitorKindDNS,
			DNS: domain.DNSProbe{
				Resolver: value.Resolver, Name: value.Name,
				RecordType:     string(value.RecordType),
				ExpectedValues: append([]string(nil), value.ExpectedValues...),
			},
		}, nil
	default:
		return domain.ProbeDefinition{}, errors.New("unsupported probe kind")
	}
}

func probeToAPI(probe domain.ProbeDefinition) (ProbeDefinition, error) {
	var mapped ProbeDefinition
	switch probe.Kind {
	case domain.MonitorKindHTTP:
		headers := make(map[string]string, len(probe.HTTP.Headers))
		names := make([]string, 0, len(probe.HTTP.Headers))
		for name := range probe.HTTP.Headers {
			if secretHeader(name) {
				return ProbeDefinition{}, errors.New("map probe: secret HTTP header")
			}
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			headers[name] = probe.HTTP.Headers[name]
		}
		statuses := make([]StatusRange, len(probe.HTTP.ExpectedStatus))
		for i, status := range probe.HTTP.ExpectedStatus {
			statuses[i] = StatusRange{Minimum: int32(status.Min), Maximum: int32(status.Max)}
		}
		err := mapped.FromHTTPProbeDefinition(HTTPProbeDefinition{
			Kind: HTTPProbeDefinitionKindHttp, Method: HTTPProbeDefinitionMethod(probe.HTTP.Method),
			Url: probe.HTTP.URL, Headers: headers, Body: cloneBytesForAPI(probe.HTTP.Body),
			ExpectedStatus:             statuses,
			BodyContains:               cloneStringsForAPI(probe.HTTP.BodyContains),
			BodyDoesNotContain:         cloneStringsForAPI(probe.HTTP.BodyDoesNotContain),
			FollowRedirects:            probe.HTTP.FollowRedirects,
			TlsMinimumRemainingSeconds: tlsToSeconds(probe.HTTP.TLS),
		})
		return mapped, err
	case domain.MonitorKindTCP:
		err := mapped.FromTCPProbeDefinition(TCPProbeDefinition{
			Kind: TCPProbeDefinitionKindTcp, Host: probe.TCP.Host, Port: int32(probe.TCP.Port),
			Send:                       cloneBytesForAPI(probe.TCP.Send),
			Expect:                     cloneBytesForAPI(probe.TCP.Expect),
			TlsMinimumRemainingSeconds: tlsToSeconds(probe.TCP.TLS),
		})
		return mapped, err
	case domain.MonitorKindDNS:
		values := cloneStringsForAPI(probe.DNS.ExpectedValues)
		sort.Strings(values)
		err := mapped.FromDNSProbeDefinition(DNSProbeDefinition{
			Kind: DNSProbeDefinitionKindDns, Resolver: probe.DNS.Resolver,
			Name: probe.DNS.Name, RecordType: DNSProbeDefinitionRecordType(probe.DNS.RecordType),
			ExpectedValues: values,
		})
		return mapped, err
	default:
		return ProbeDefinition{}, errors.New("map probe: unsupported kind")
	}
}

func validateProbeFields(probe ProbeDefinition, allowed ...string) error {
	data, err := json.Marshal(probe)
	if err != nil {
		return fmt.Errorf("encode probe: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("decode probe fields: %w", err)
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name := range fields {
		if _, ok := allowedSet[name]; !ok {
			return fmt.Errorf("unexpected probe field %q", name)
		}
	}
	return nil
}

func secretHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "cookie", "proxy-authorization", "set-cookie":
		return true
	default:
		return false
	}
}

func tlsFromSeconds(seconds *int64) *domain.TLSExpectation {
	if seconds == nil {
		return nil
	}
	return &domain.TLSExpectation{MinimumRemaining: time.Duration(*seconds) * time.Second}
}

func tlsToSeconds(expectation *domain.TLSExpectation) *int64 {
	if expectation == nil {
		return nil
	}
	seconds := int64(expectation.MinimumRemaining / time.Second)
	return &seconds
}

func cloneBytesForAPI(value []byte) []byte {
	return append([]byte{}, value...)
}

func cloneStringsForAPI(value []string) []string {
	return append([]string{}, value...)
}

func createLocationProblem(err error) (CreateLocationResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return CreateLocationdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func createMonitorProblem(err error) (CreateMonitorResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return CreateMonitordefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func getMonitorProblem(err error) (GetMonitorResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return GetMonitordefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func problemFromError(err error) (Problem, int, bool) {
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		keys := make([]string, 0, len(validation.Fields))
		for field := range validation.Fields {
			keys = append(keys, field)
		}
		sort.Strings(keys)
		fieldErrors := make([]FieldError, 0, len(keys))
		for _, field := range keys {
			fieldErrors = append(fieldErrors, FieldError{
				Field: field, Message: validation.Fields[field],
			})
		}
		return Problem{
			Type:          "https://xisnove.dev/problems/validation",
			Title:         "Request validation failed",
			Status:        400,
			Code:          "validation_failed",
			CorrelationId: "unknown",
			FieldErrors:   &fieldErrors,
		}, 400, true
	}
	if errors.Is(err, application.ErrNotFound) {
		return Problem{
			Type:          "https://xisnove.dev/problems/not-found",
			Title:         "Resource not found",
			Status:        404,
			Code:          "not_found",
			CorrelationId: "unknown",
		}, 404, true
	}
	if errors.Is(err, application.ErrConflict) {
		return Problem{
			Type:          "https://xisnove.dev/problems/conflict",
			Title:         "Resource already exists",
			Status:        409,
			Code:          "conflict",
			CorrelationId: "unknown",
		}, 409, true
	}
	if errors.Is(err, application.ErrInvalidCredentials) ||
		errors.Is(err, application.ErrInvalidEnrollmentToken) {
		return Problem{
			Type:          "https://xisnove.dev/problems/unauthorized",
			Title:         "Authentication required",
			Status:        401,
			Code:          "unauthorized",
			CorrelationId: "unknown",
		}, 401, true
	}
	return Problem{}, 0, false
}
