package httpapi

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) UpdateLocation(ctx context.Context, request UpdateLocationRequestObject) (UpdateLocationResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err != nil {
		return updateLocationProblem(err)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return updateLocationProblem(err)
	}
	if request.Body == nil {
		return updateLocationProblem(requiredBodyError())
	}
	location, err := s.management.UpdateLocation(ctx, principal, domain.LocationID(request.LocationId.String()), key, application.UpdateLocationCommand{
		Name: request.Body.Name, Enabled: request.Body.Enabled,
	})
	if err != nil {
		return updateLocationProblem(err)
	}
	mapped, err := mapLocation(location)
	if err != nil {
		return nil, err
	}
	return UpdateLocation200JSONResponse(mapped), nil
}

func (s *Server) DisableLocation(ctx context.Context, request DisableLocationRequestObject) (DisableLocationResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err == nil {
		err = s.management.DisableLocation(ctx, principal, domain.LocationID(request.LocationId.String()))
	}
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return DisableLocationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	return DisableLocation204Response{}, nil
}

func (s *Server) UpdateMonitor(ctx context.Context, request UpdateMonitorRequestObject) (UpdateMonitorResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err != nil {
		return updateMonitorProblem(err)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return updateMonitorProblem(err)
	}
	if request.Body == nil {
		return updateMonitorProblem(requiredBodyError())
	}
	body := request.Body
	if body.FailureThreshold <= 0 || body.FailureThreshold > math.MaxUint16 ||
		body.RecoveryThreshold <= 0 || body.RecoveryThreshold > math.MaxUint16 {
		return updateMonitorProblem(invalidMonitorError())
	}
	probe, err := probeFromAPI(body.Probe)
	if err != nil {
		return updateMonitorProblem(&application.ValidationError{Fields: map[string]string{"probe": "contains invalid configuration"}})
	}
	monitor, err := s.management.UpdateMonitor(ctx, principal, domain.MonitorID(request.MonitorId.String()), key, application.ReplaceMonitorCommand{
		CreateMonitorCommand: application.CreateMonitorCommand{
			Name: body.Name, Description: body.Description, Labels: body.Labels,
			DisplayOrder: body.DisplayOrder, Public: body.Public,
			LocationID: domain.LocationID(body.LocationId.String()), RequiredLocation: body.RequiredLocation,
			Interval:         time.Duration(body.IntervalSeconds) * time.Second,
			Timeout:          time.Duration(body.TimeoutMillis) * time.Millisecond,
			FailureThreshold: uint16(body.FailureThreshold), RecoveryThreshold: uint16(body.RecoveryThreshold),
			Probe: probe,
		},
		Enabled: body.Enabled,
	})
	if err != nil {
		return updateMonitorProblem(err)
	}
	mapped, err := mapMonitor(monitor)
	if err != nil {
		return nil, err
	}
	return UpdateMonitor200JSONResponse(mapped), nil
}

func (s *Server) DisableMonitor(ctx context.Context, request DisableMonitorRequestObject) (DisableMonitorResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err == nil {
		err = s.management.DisableMonitor(ctx, principal, domain.MonitorID(request.MonitorId.String()))
	}
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return DisableMonitordefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	return DisableMonitor204Response{}, nil
}

func (s *Server) UpdateAgent(ctx context.Context, request UpdateAgentRequestObject) (UpdateAgentResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err != nil {
		return updateAgentProblem(err)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return updateAgentProblem(err)
	}
	if request.Body == nil {
		return updateAgentProblem(requiredBodyError())
	}
	var capabilities *[]domain.AgentCapability
	if request.Body.Capabilities != nil {
		mapped := make([]domain.AgentCapability, len(*request.Body.Capabilities))
		for index, capability := range *request.Body.Capabilities {
			mapped[index] = domain.AgentCapability(capability)
		}
		capabilities = &mapped
	}
	agent, err := s.management.UpdateAgent(ctx, principal, domain.AgentID(request.AgentId.String()), key, application.UpdateAgentCommand{
		Name: request.Body.Name, Enabled: request.Body.Enabled, Capabilities: capabilities,
	})
	if err != nil {
		return updateAgentProblem(err)
	}
	mapped, err := mapManagementAgent(agent)
	if err != nil {
		return nil, err
	}
	return UpdateAgent200JSONResponse(mapped), nil
}

func (s *Server) RevokeAgent(ctx context.Context, request RevokeAgentRequestObject) (RevokeAgentResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err == nil {
		err = s.management.RevokeAgent(ctx, principal, domain.AgentID(request.AgentId.String()))
	}
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return RevokeAgentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	return RevokeAgent204Response{}, nil
}

func (s *Server) RotateAgentCredential(ctx context.Context, request RotateAgentCredentialRequestObject) (RotateAgentCredentialResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err != nil {
		return rotateAgentCredentialProblem(err)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return rotateAgentCredentialProblem(err)
	}
	credential, err := s.management.RotateAgentCredential(ctx, principal, domain.AgentID(request.AgentId.String()), key)
	if err != nil {
		return rotateAgentCredentialProblem(err)
	}
	agentID, err := uuid.Parse(string(credential.AgentID))
	if err != nil {
		return nil, err
	}
	raw := credential.Credential
	return RotateAgentCredential201JSONResponse{
		AgentId: agentID, Credential: &raw, CredentialGeneration: int64(credential.CredentialGeneration),
	}, nil
}

func (s *Server) RevokeAgentCredentialGeneration(ctx context.Context, request RevokeAgentCredentialGenerationRequestObject) (RevokeAgentCredentialGenerationResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err == nil {
		if request.Generation <= 0 {
			err = &application.ValidationError{Fields: map[string]string{"generation": "must be at least one"}}
		} else {
			err = s.management.RevokeAgentCredentialGeneration(ctx, principal, domain.AgentID(request.AgentId.String()), uint64(request.Generation))
		}
	}
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if !mapped {
			return nil, err
		}
		if status == 409 {
			return RevokeAgentCredentialGeneration409ApplicationProblemPlusJSONResponse(problem), nil
		}
		return RevokeAgentCredentialGenerationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	return RevokeAgentCredentialGeneration204Response{}, nil
}

func managementPrincipal(ctx context.Context) (application.Principal, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return application.Principal{}, application.ErrInvalidCredentials
	}
	return principal, nil
}

func requiredBodyError() error {
	return &application.ValidationError{Fields: map[string]string{"body": "is required"}}
}

func invalidMonitorError() error {
	return &application.ValidationError{Fields: map[string]string{"monitor": "contains invalid configuration"}}
}

func updateLocationProblem(err error) (UpdateLocationResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	return UpdateLocationdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}

func updateMonitorProblem(err error) (UpdateMonitorResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	return UpdateMonitordefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}

func updateAgentProblem(err error) (UpdateAgentResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	return UpdateAgentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}

func rotateAgentCredentialProblem(err error) (RotateAgentCredentialResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	if status == 409 {
		return RotateAgentCredential409ApplicationProblemPlusJSONResponse(problem), nil
	}
	return RotateAgentCredentialdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
