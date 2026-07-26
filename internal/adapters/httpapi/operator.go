package httpapi

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) ApplyOperatorMonitor(ctx context.Context, request ApplyOperatorMonitorRequestObject) (ApplyOperatorMonitorResponseObject, error) {
	if request.Body == nil {
		return applyOperatorMonitorProblem(requiredBodyError())
	}
	command, err := operatorMonitorCommand(request.Body.Monitor)
	if err != nil {
		return applyOperatorMonitorProblem(err)
	}
	state, err := s.operator.ApplyMonitor(ctx, application.ApplyOperatorMonitor{
		Owner: operatorOwner(request.Body.Owner), Monitor: command, IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return applyOperatorMonitorProblem(err)
	}
	mapped, err := operatorMonitorResult(state)
	if err != nil {
		return nil, err
	}
	return ApplyOperatorMonitor200JSONResponse(mapped), nil
}

func (s *Server) DeleteOperatorMonitor(ctx context.Context, request DeleteOperatorMonitorRequestObject) (DeleteOperatorMonitorResponseObject, error) {
	if request.Body == nil {
		return deleteOperatorMonitorProblem(requiredBodyError())
	}
	var externalID domain.MonitorID
	if request.Body.ExternalId != nil {
		externalID = domain.MonitorID(request.Body.ExternalId.String())
	}
	err := s.operator.DeleteMonitor(ctx, application.DeleteOperatorMonitor{
		Owner: operatorOwner(request.Body.Owner), ExternalID: externalID, IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return deleteOperatorMonitorProblem(err)
	}
	return DeleteOperatorMonitor204Response{}, nil
}

func (s *Server) ApplyOperatorAgent(ctx context.Context, request ApplyOperatorAgentRequestObject) (ApplyOperatorAgentResponseObject, error) {
	if request.Body == nil || request.Body.InitialCredential.Credential == nil {
		return applyOperatorAgentProblem(requiredBodyError())
	}
	capabilities := make([]domain.AgentCapability, len(request.Body.Capabilities))
	for i, capability := range request.Body.Capabilities {
		capabilities[i] = domain.AgentCapability(capability)
	}
	state, err := s.operator.ApplyAgent(ctx, application.ApplyOperatorAgent{
		Owner: operatorOwner(request.Body.Owner), Name: request.Body.Name,
		LocationID: domain.LocationID(request.Body.LocationId.String()), Enabled: request.Body.Enabled,
		Capabilities: capabilities,
		InitialCredential: application.OperatorInitialCredential{
			Generation: uint64(request.Body.InitialCredential.Generation), Credential: *request.Body.InitialCredential.Credential,
		},
		IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return applyOperatorAgentProblem(err)
	}
	mapped, err := operatorAgentResult(state)
	if err != nil {
		return nil, err
	}
	return ApplyOperatorAgent200JSONResponse(mapped), nil
}

func (s *Server) DeleteOperatorAgent(ctx context.Context, request DeleteOperatorAgentRequestObject) (DeleteOperatorAgentResponseObject, error) {
	if request.Body == nil {
		return deleteOperatorAgentProblem(requiredBodyError())
	}
	var externalID domain.AgentID
	if request.Body.ExternalId != nil {
		externalID = domain.AgentID(request.Body.ExternalId.String())
	}
	err := s.operator.DeleteAgent(ctx, application.DeleteOperatorAgent{
		Owner: operatorOwner(request.Body.Owner), ExternalID: externalID, IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return deleteOperatorAgentProblem(err)
	}
	return DeleteOperatorAgent204Response{}, nil
}

func (s *Server) PutOperatorAgentCredential(ctx context.Context, request PutOperatorAgentCredentialRequestObject) (PutOperatorAgentCredentialResponseObject, error) {
	if request.Body == nil || request.Body.Credential == nil {
		return putOperatorCredentialProblem(requiredBodyError())
	}
	err := s.operator.PutAgentCredential(ctx, application.PutOperatorCredential{
		Owner: operatorOwner(request.Body.Owner), AgentID: domain.AgentID(request.AgentId.String()),
		Generation: uint64(request.Generation), Credential: *request.Body.Credential,
		IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return putOperatorCredentialProblem(err)
	}
	return PutOperatorAgentCredential204Response{}, nil
}

func (s *Server) RevokeOperatorAgentCredential(ctx context.Context, request RevokeOperatorAgentCredentialRequestObject) (RevokeOperatorAgentCredentialResponseObject, error) {
	if request.Body == nil {
		return revokeOperatorCredentialProblem(requiredBodyError())
	}
	err := s.operator.RevokeAgentCredential(ctx, application.RevokeOperatorCredential{
		Owner: operatorOwner(request.Body.Owner), AgentID: domain.AgentID(request.AgentId.String()),
		Generation: uint64(request.Generation), IdempotencyKey: string(request.Params.IdempotencyKey),
	})
	if err != nil {
		return revokeOperatorCredentialProblem(err)
	}
	return RevokeOperatorAgentCredential204Response{}, nil
}

func operatorOwner(owner ExternalOwner) port.ExternalOwner {
	return port.ExternalOwner{Key: owner.Key, UID: owner.Uid}
}

func operatorMonitorCommand(body UpdateMonitorRequest) (application.ReplaceMonitorCommand, error) {
	if body.FailureThreshold <= 0 || body.FailureThreshold > math.MaxUint16 || body.RecoveryThreshold <= 0 || body.RecoveryThreshold > math.MaxUint16 {
		return application.ReplaceMonitorCommand{}, invalidMonitorError()
	}
	probe, err := probeFromAPI(body.Probe)
	if err != nil {
		return application.ReplaceMonitorCommand{}, &application.ValidationError{Fields: map[string]string{"probe": "contains invalid configuration"}}
	}
	return application.ReplaceMonitorCommand{CreateMonitorCommand: application.CreateMonitorCommand{
		Name: body.Name, Description: body.Description, Labels: body.Labels, DisplayOrder: body.DisplayOrder,
		Public: body.Public, LocationID: domain.LocationID(body.LocationId.String()), RequiredLocation: body.RequiredLocation,
		Interval: time.Duration(body.IntervalSeconds) * time.Second, Timeout: time.Duration(body.TimeoutMillis) * time.Millisecond,
		FailureThreshold: uint16(body.FailureThreshold), RecoveryThreshold: uint16(body.RecoveryThreshold), Probe: probe,
	}, Enabled: body.Enabled}, nil
}

func operatorMonitorResult(state application.OperatorMonitorState) (OperatorMonitorApplyResult, error) {
	id, err := uuid.Parse(string(state.ExternalID))
	if err != nil {
		return OperatorMonitorApplyResult{}, err
	}
	return OperatorMonitorApplyResult{ExternalId: id, State: HealthState(state.State), LastTransitionAt: state.LastTransitionAt, LastObservedAt: state.LastObservedAt}, nil
}

func operatorAgentResult(state application.OperatorAgentState) (OperatorAgentApplyResult, error) {
	id, err := uuid.Parse(string(state.ExternalID))
	if err != nil {
		return OperatorAgentApplyResult{}, err
	}
	result := OperatorAgentApplyResult{ExternalId: id, CredentialGeneration: int64(state.CredentialGeneration)}
	if !state.LastSeenAt.IsZero() {
		value := state.LastSeenAt
		result.LastSeenAt = &value
	}
	if !state.LastDiscoveryAt.IsZero() {
		value := state.LastDiscoveryAt
		result.LastDiscoveryAt = &value
	}
	return result, nil
}

func operatorProblem(err error) (Problem, int, bool) { return idempotencyProblemFromError(err) }

func applyOperatorMonitorProblem(err error) (ApplyOperatorMonitorResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return ApplyOperatorMonitor409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return ApplyOperatorMonitordefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
func deleteOperatorMonitorProblem(err error) (DeleteOperatorMonitorResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return DeleteOperatorMonitor409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return DeleteOperatorMonitordefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
func applyOperatorAgentProblem(err error) (ApplyOperatorAgentResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return ApplyOperatorAgent409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return ApplyOperatorAgentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
func deleteOperatorAgentProblem(err error) (DeleteOperatorAgentResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return DeleteOperatorAgent409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return DeleteOperatorAgentdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
func putOperatorCredentialProblem(err error) (PutOperatorAgentCredentialResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return PutOperatorAgentCredential409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return PutOperatorAgentCredentialdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
func revokeOperatorCredentialProblem(err error) (RevokeOperatorAgentCredentialResponseObject, error) {
	problem, status, ok := operatorProblem(err)
	if !ok {
		return nil, err
	}
	if status == 409 {
		return RevokeOperatorAgentCredential409ApplicationProblemPlusJSONResponse{ProblemApplicationProblemPlusJSONResponse(problem)}, nil
	}
	return RevokeOperatorAgentCredentialdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
