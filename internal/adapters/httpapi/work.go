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
	if request.Body == nil || !hasHTTPAPICapability(request.Body.Capabilities) {
		response, _ := leaseAgentWorkProblem(&application.ValidationError{
			Fields: map[string]string{"capabilities": "must include http"},
		})
		return response, nil
	}
	work, err := s.lease.LeaseHTTP(
		ctx,
		domain.AgentID(principal.SubjectID),
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
	mapped, err := mapHTTPWork(*work)
	if err != nil {
		return nil, err
	}
	return LeaseAgentWork200JSONResponse(mapped), nil
}

func mapHTTPWork(work application.HTTPWork) (HTTPWork, error) {
	runID, err := uuid.Parse(string(work.RunID))
	if err != nil {
		return HTTPWork{}, fmt.Errorf("map run ID: %w", err)
	}
	monitorID, err := uuid.Parse(string(work.MonitorID))
	if err != nil {
		return HTTPWork{}, fmt.Errorf("map work monitor ID: %w", err)
	}
	if len(work.Probe.ExpectedStatus) == 0 {
		return HTTPWork{}, errors.New("map HTTP work: expected status is missing")
	}
	bodyContains := ""
	if len(work.Probe.BodyContains) != 0 {
		bodyContains = work.Probe.BodyContains[0]
	}
	return HTTPWork{
		RunId:         runID,
		MonitorId:     monitorID,
		ScheduledFor:  work.ScheduledFor,
		LeaseToken:    work.LeaseToken,
		TimeoutMillis: int32(work.Timeout / time.Millisecond),
		Http: HTTPProbe{
			Method:          HTTPProbeMethod(work.Probe.Method),
			Url:             work.Probe.URL,
			ExpectedStatus:  int32(work.Probe.ExpectedStatus[0].Min),
			BodyContains:    bodyContains,
			FollowRedirects: work.Probe.FollowRedirects,
		},
	}, nil
}

func hasHTTPAPICapability(capabilities []AgentCapability) bool {
	for _, capability := range capabilities {
		if capability == AgentCapabilityHttp {
			return true
		}
	}
	return false
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
