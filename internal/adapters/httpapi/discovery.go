package httpapi

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) UpsertDiscoveryCandidates(
	ctx context.Context,
	request UpsertDiscoveryCandidatesRequestObject,
) (UpsertDiscoveryCandidatesResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != application.PrincipalAgent {
		return upsertDiscoveryProblem(application.ErrInvalidCredentials)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return upsertDiscoveryProblem(err)
	}
	if request.Body == nil {
		return upsertDiscoveryProblem(requiredBodyError())
	}
	agent, err := s.management.GetAgent(ctx, domain.AgentID(principal.SubjectID))
	if err != nil {
		return upsertDiscoveryProblem(err)
	}
	if !slices.Contains(agent.Capabilities, domain.CapabilityKubernetesDiscovery) &&
		!slices.Contains(agent.Capabilities, domain.CapabilityKubernetesWatch) {
		return upsertDiscoveryProblem(&application.ValidationError{Fields: map[string]string{
			"capabilities": "agent is not enabled for discovery",
		}})
	}
	inputs := make([]application.DiscoveryInput, len(request.Body.Candidates))
	for index, candidate := range request.Body.Candidates {
		inputs[index] = application.DiscoveryInput{
			LocationID: agent.LocationID, SourceKind: candidate.SourceKind,
			SourceUID: candidate.SourceUid, Namespace: candidate.Namespace, Name: candidate.Name,
			Labels: candidate.Labels, Protocol: domain.MonitorKind(candidate.Protocol), Target: candidate.Target,
			NetworkPerspective: candidate.NetworkPerspective, Present: candidate.Present,
			ObservedAt: candidate.ObservedAt,
		}
	}
	acknowledgement, err := s.discovery.Publish(ctx, agent.ID, key, inputs)
	if err != nil {
		return upsertDiscoveryProblem(err)
	}
	return UpsertDiscoveryCandidates200JSONResponse{
		Accepted: int32(acknowledgement.Accepted), Created: int32(acknowledgement.Created),
		Updated: int32(acknowledgement.Updated),
	}, nil
}

func (s *Server) ListDiscoveryCandidates(
	ctx context.Context,
	request ListDiscoveryCandidatesRequestObject,
) (ListDiscoveryCandidatesResponseObject, error) {
	filter := port.DiscoveryFilter{Present: request.Params.Present}
	if request.Params.State != nil {
		filter.State = port.DiscoveryState(*request.Params.State)
	}
	page, err := s.discovery.List(ctx, filter, managementPageRequest(request.Params.Limit, request.Params.Cursor))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListDiscoveryCandidatesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]DiscoveryCandidate, len(page.Items))
	for index, candidate := range page.Items {
		items[index], err = mapDiscoveryCandidate(candidate)
		if err != nil {
			return nil, err
		}
	}
	return ListDiscoveryCandidates200JSONResponse{Items: items, Page: mapPageMetadata(page.NextCursor)}, nil
}

func (s *Server) GetDiscoveryCandidate(
	ctx context.Context,
	request GetDiscoveryCandidateRequestObject,
) (GetDiscoveryCandidateResponseObject, error) {
	candidate, err := s.discovery.Get(ctx, domain.DiscoveryCandidateID(request.CandidateId.String()))
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return GetDiscoveryCandidatedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	mapped, err := mapDiscoveryCandidate(candidate)
	if err != nil {
		return nil, err
	}
	return GetDiscoveryCandidate200JSONResponse(mapped), nil
}

func (s *Server) PromoteDiscoveryCandidate(
	ctx context.Context,
	request PromoteDiscoveryCandidateRequestObject,
) (PromoteDiscoveryCandidateResponseObject, error) {
	principal, err := managementPrincipal(ctx)
	if err != nil {
		return promoteDiscoveryProblem(err)
	}
	key, err := requiredIdempotencyKey(request.Params.IdempotencyKey)
	if err != nil {
		return promoteDiscoveryProblem(err)
	}
	if request.Body == nil {
		return promoteDiscoveryProblem(requiredBodyError())
	}
	body := request.Body
	if body.FailureThreshold <= 0 || body.FailureThreshold > math.MaxUint16 ||
		body.RecoveryThreshold <= 0 || body.RecoveryThreshold > math.MaxUint16 {
		return promoteDiscoveryProblem(invalidMonitorError())
	}
	promotion, err := s.discovery.PromoteIdempotently(ctx, principal, key, domain.DiscoveryCandidateID(request.CandidateId.String()), application.DiscoveryPromotionCommand{
		Name: body.Name, Description: pointerValue(body.Description), Labels: mapPointerValue(body.Labels),
		Public: body.Public, LocationID: domain.LocationID(body.LocationId.String()),
		RequiredLocation: body.RequiredLocation, Interval: time.Duration(body.IntervalSeconds) * time.Second,
		Timeout:          time.Duration(body.TimeoutMillis) * time.Millisecond,
		FailureThreshold: uint16(body.FailureThreshold), RecoveryThreshold: uint16(body.RecoveryThreshold),
	})
	if err != nil {
		return promoteDiscoveryProblem(err)
	}
	candidate, err := mapDiscoveryCandidate(promotion.Candidate)
	if err != nil {
		return nil, err
	}
	monitor, err := mapMonitor(promotion.Monitor)
	if err != nil {
		return nil, err
	}
	return PromoteDiscoveryCandidate201JSONResponse{Candidate: candidate, Monitor: monitor}, nil
}

func mapDiscoveryCandidate(candidate domain.DiscoveryCandidate) (DiscoveryCandidate, error) {
	id, err := uuid.Parse(string(candidate.ID))
	if err != nil {
		return DiscoveryCandidate{}, err
	}
	agentID, err := uuid.Parse(string(candidate.AgentID))
	if err != nil {
		return DiscoveryCandidate{}, err
	}
	locationID, err := uuid.Parse(string(candidate.LocationID))
	if err != nil {
		return DiscoveryCandidate{}, err
	}
	mapped := DiscoveryCandidate{
		Id: id, AgentId: agentID, LocationId: locationID,
		SourceKind: candidate.SourceKind, SourceUid: candidate.SourceUID,
		Namespace: candidate.Namespace, Name: candidate.Name, Labels: candidate.Labels,
		Protocol: DiscoveryCandidateProtocol(candidate.Protocol), Target: candidate.Target,
		NetworkPerspective: candidate.NetworkPerspective, Present: candidate.Present,
		State: DiscoveryCandidateStatePending, FirstSeenAt: candidate.CreatedAt,
		LastObservedAt: candidate.LastObservedAt, UpdatedAt: candidate.UpdatedAt,
	}
	if candidate.PromotedMonitorID != nil {
		promotedID, err := uuid.Parse(string(*candidate.PromotedMonitorID))
		if err != nil {
			return DiscoveryCandidate{}, err
		}
		mapped.PromotedMonitorId = &promotedID
		mapped.State = DiscoveryCandidateStatePromoted
	}
	if candidate.DriftHint != "" {
		drift := candidate.DriftHint
		mapped.DriftHint = &drift
	}
	return mapped, nil
}

func upsertDiscoveryProblem(err error) (UpsertDiscoveryCandidatesResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	return UpsertDiscoveryCandidatesdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}

func promoteDiscoveryProblem(err error) (PromoteDiscoveryCandidateResponseObject, error) {
	problem, status, mapped := idempotencyProblemFromError(err)
	if !mapped {
		return nil, err
	}
	return PromoteDiscoveryCandidatedefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
}
