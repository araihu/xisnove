package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

func (s *Server) UploadProbeResults(
	ctx context.Context,
	request UploadProbeResultsRequestObject,
) (UploadProbeResultsResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Kind != application.PrincipalAgent {
		response, _ := uploadProbeResultsProblem(application.ErrInvalidCredentials)
		return response, nil
	}
	if request.Body == nil {
		response, _ := uploadProbeResultsProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		})
		return response, nil
	}

	commands := make([]application.ProbeResultCommand, len(request.Body.Results))
	for index, result := range request.Body.Results {
		status := int(result.ObservedStatus)
		bodyPassed := result.BodyAssertionPassed
		observedValues := []string(nil)
		if result.ObservedValues != nil {
			observedValues = append(observedValues, (*result.ObservedValues)...)
		}
		var timings application.ProtocolTimings
		if result.ProtocolTimings != nil {
			timings = application.ProtocolTimings{
				DNS:       milliseconds(result.ProtocolTimings.DnsMillis),
				Connect:   milliseconds(result.ProtocolTimings.ConnectMillis),
				TLS:       milliseconds(result.ProtocolTimings.TlsMillis),
				FirstByte: milliseconds(result.ProtocolTimings.FirstByteMillis),
			}
		}
		commands[index] = application.ProbeResultCommand{
			ID:                  result.ResultId.String(),
			RunID:               domain.CheckRunID(result.RunId.String()),
			LeaseToken:          result.LeaseToken,
			StartedAt:           result.StartedAt,
			FinishedAt:          result.FinishedAt,
			Outcome:             application.ProbeOutcome(result.Outcome),
			Latency:             time.Duration(result.LatencyMillis) * time.Millisecond,
			ObservedStatus:      &status,
			BodyAssertionPassed: &bodyPassed,
			ErrorCode:           string(result.ErrorCode),
			DiagnosticSample:    result.DiagnosticSample,
			ObservedValues:      observedValues,
			TLSNotAfter:         result.TlsNotAfter,
			ProtocolTimings:     timings,
		}
	}
	acknowledgements, err := s.results.UploadBatch(
		ctx,
		domain.AgentID(principal.SubjectID),
		commands,
	)
	if err != nil {
		response, mapped := uploadProbeResultsProblem(err)
		if mapped {
			return response, nil
		}
		return nil, err
	}
	mapped := make([]ProbeResultAcknowledgement, len(acknowledgements))
	for index, acknowledgement := range acknowledgements {
		resultID, err := uuid.Parse(acknowledgement.ResultID)
		if err != nil {
			return nil, err
		}
		mapped[index] = ProbeResultAcknowledgement{
			ResultId: resultID,
			Status:   ProbeResultAcknowledgementStatus(acknowledgement.Status),
		}
	}
	return UploadProbeResults200JSONResponse{
		Acknowledgements: mapped,
	}, nil
}

func milliseconds(value *int64) time.Duration {
	if value == nil {
		return 0
	}
	return time.Duration(*value) * time.Millisecond
}

func uploadProbeResultsProblem(err error) (UploadProbeResultsResponseObject, bool) {
	problem, status, ok := problemFromError(err)
	if !ok {
		return nil, false
	}
	return UploadProbeResultsdefaultApplicationProblemPlusJSONResponse{
		Body: problem, StatusCode: status,
	}, true
}
