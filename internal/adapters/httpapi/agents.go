package httpapi

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) CreateAgentEnrollmentToken(
	ctx context.Context,
	request CreateAgentEnrollmentTokenRequestObject,
) (CreateAgentEnrollmentTokenResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != application.PrincipalAdmin {
		response, _ := createAgentEnrollmentTokenProblem(application.ErrInvalidCredentials)
		return response, nil
	}
	if request.Body == nil {
		response, _ := createAgentEnrollmentTokenProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return response, nil
	}

	enrollment, err := s.agents.CreateEnrollmentToken(
		ctx,
		domain.LocationID(request.Body.LocationId.String()),
		time.Duration(request.Body.ExpiresInSeconds)*time.Second,
	)
	if err != nil {
		response, mapped := createAgentEnrollmentTokenProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	return CreateAgentEnrollmentToken201JSONResponse(AgentEnrollmentToken{
		Token: enrollment.Token, ExpiresAt: enrollment.ExpiresAt,
	}), nil
}

func (s *Server) EnrollAgent(
	ctx context.Context,
	request EnrollAgentRequestObject,
) (EnrollAgentResponseObject, error) {
	if request.Body == nil || request.Body.Token == nil {
		response, _ := enrollAgentProblem(application.ErrInvalidEnrollmentToken)
		return response, nil
	}
	capabilities := make([]domain.AgentCapability, len(request.Body.Capabilities))
	for i, capability := range request.Body.Capabilities {
		capabilities[i] = domain.AgentCapability(capability)
	}
	enrolled, err := s.agents.Enroll(ctx, application.EnrollAgentCommand{
		Token:        *request.Body.Token,
		Name:         request.Body.Name,
		Capabilities: capabilities,
	})
	if err != nil {
		response, mapped := enrollAgentProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	agentID, err := uuid.Parse(string(enrolled.ID))
	if err != nil {
		return nil, fmt.Errorf("map enrolled agent ID: %w", err)
	}
	return EnrollAgent201JSONResponse(EnrolledAgent{
		AgentId:              agentID,
		Credential:           enrolled.Credential,
		CredentialGeneration: int64(enrolled.CredentialGeneration),
	}), nil
}

func (s *Server) HeartbeatAgent(
	ctx context.Context,
	request HeartbeatAgentRequestObject,
) (HeartbeatAgentResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != application.PrincipalAgent {
		response, _ := heartbeatAgentProblem(application.ErrInvalidCredentials)
		return response, nil
	}
	if request.Body == nil || request.Body.CredentialGeneration <= 0 {
		response, _ := heartbeatAgentProblem(&application.ValidationError{
			Fields: map[string]string{"body": "contains invalid heartbeat"},
		})
		return response, nil
	}
	capabilities := make([]domain.AgentCapability, len(request.Body.Capabilities))
	for i, capability := range request.Body.Capabilities {
		capabilities[i] = domain.AgentCapability(capability)
	}
	err := s.agents.Heartbeat(
		ctx,
		principal,
		uint64(request.Body.CredentialGeneration),
		request.Body.Version,
		capabilities,
	)
	if err != nil {
		response, mapped := heartbeatAgentProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	return HeartbeatAgent204Response{}, nil
}

func createAgentEnrollmentTokenProblem(
	err error,
) (CreateAgentEnrollmentTokenResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return CreateAgentEnrollmentTokendefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func enrollAgentProblem(err error) (EnrollAgentResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return EnrollAgentdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}

func heartbeatAgentProblem(err error) (HeartbeatAgentResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return HeartbeatAgentdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}
