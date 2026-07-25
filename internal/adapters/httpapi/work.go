package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

func (s *Server) LeaseAgentWork(
	ctx context.Context,
	request LeaseAgentWorkRequestObject,
) (LeaseAgentWorkResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != application.PrincipalAgent {
		response, _ := leaseAgentWorkProblem(application.ErrInvalidCredentials)
		return response, nil
	}
	if request.Body == nil {
		response, _ := leaseAgentWorkProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return response, nil
	}
	capabilities, err := capabilitiesFromAPI(request.Body.Capabilities)
	if err != nil {
		response, _ := leaseAgentWorkProblem(&application.ValidationError{
			Fields: map[string]string{"capabilities": "contains invalid values"},
		})
		return response, nil
	}
	work, err := s.lease.LeaseProbe(
		ctx,
		domain.AgentID(principal.SubjectID),
		capabilities,
		time.Duration(request.Body.WaitSeconds)*time.Second,
	)
	if errors.Is(err, application.ErrNoWork) {
		return LeaseAgentWork204Response{}, nil
	}
	if err != nil {
		response, mapped := leaseAgentWorkProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	mapped, err := mapProbeWork(*work)
	if err != nil {
		return nil, err
	}
	return LeaseAgentWork200JSONResponse(mapped), nil
}

func mapProbeWork(work application.ProbeWork) (ProbeWork, error) {
	runID, err := uuid.Parse(string(work.RunID))
	if err != nil {
		return ProbeWork{}, fmt.Errorf("map run ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(work.MonitorID))
	if err != nil {
		return ProbeWork{}, fmt.Errorf("map work monitor ID: %w", err)
	}
	probe, err := probeToAPI(work.Probe)
	if err != nil {
		return ProbeWork{}, err
	}
	return ProbeWork{
		RunId:         runID,
		MonitorId:     monitorID,
		ScheduledFor:  work.ScheduledFor,
		LeaseToken:    work.LeaseToken,
		TimeoutMillis: int32(work.Timeout / time.Millisecond),
		Probe:         probe,
	}, nil
}

func capabilitiesFromAPI(
	capabilities []AgentCapability,
) ([]domain.AgentCapability, error) {
	if len(capabilities) == 0 {
		return nil, errors.New("capabilities are required")
	}
	mapped := make([]domain.AgentCapability, 0, len(capabilities))
	seen := make(map[AgentCapability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !capability.Valid() {
			return nil, errors.New("invalid capability")
		}
		if _, duplicate := seen[capability]; duplicate {
			return nil, errors.New("duplicate capability")
		}
		seen[capability] = struct{}{}
		mapped = append(mapped, domain.AgentCapability(capability))
	}
	return mapped, nil
}

func leaseAgentWorkProblem(err error) (LeaseAgentWorkResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return LeaseAgentWorkdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}
