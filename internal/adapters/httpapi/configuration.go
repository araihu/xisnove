package httpapi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/internal/application"
	"github.com/araihu/xisnove/internal/domain"
)

type ServerConfig struct {
	Configuration *application.ConfigurationService
}

type Server struct {
	configuration *application.ConfigurationService
}

func NewServer(config ServerConfig) *Server {
	return &Server{configuration: config.Configuration}
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
		request.Body.RecoveryThreshold > math.MaxUint16 ||
		!request.Body.Http.Method.Valid() {
		response, _ := createMonitorProblem(&application.ValidationError{
			Fields: map[string]string{"monitor": "contains invalid configuration"},
		})
		return response, nil
	}

	bodyContains := []string{}
	if request.Body.Http.BodyContains != "" {
		bodyContains = append(bodyContains, request.Body.Http.BodyContains)
	}
	monitor, err := s.configuration.CreateHTTPMonitor(
		ctx,
		application.CreateHTTPMonitorCommand{
			Name:              request.Body.Name,
			LocationID:        domain.LocationID(request.Body.LocationId.String()),
			RequiredLocation:  request.Body.RequiredLocation,
			Interval:          time.Duration(request.Body.IntervalSeconds) * time.Second,
			Timeout:           time.Duration(request.Body.TimeoutMillis) * time.Millisecond,
			FailureThreshold:  uint16(request.Body.FailureThreshold),
			RecoveryThreshold: uint16(request.Body.RecoveryThreshold),
			HTTP: domain.HTTPProbe{
				Method: string(request.Body.Http.Method),
				URL:    request.Body.Http.Url,
				ExpectedStatus: []domain.StatusRange{{
					Min: int(request.Body.Http.ExpectedStatus),
					Max: int(request.Body.Http.ExpectedStatus),
				}},
				BodyContains:    bodyContains,
				FollowRedirects: request.Body.Http.FollowRedirects,
			},
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
	monitor, err := s.configuration.GetMonitor(
		ctx,
		domain.MonitorID(request.MonitorId.String()),
	)
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
	return Location{Id: id, Name: location.Name, CreatedAt: location.CreatedAt}, nil
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
	if len(configured.HTTP.ExpectedStatus) == 0 {
		return Monitor{}, errors.New("map monitor: expected status is missing")
	}
	bodyContains := ""
	if len(configured.HTTP.BodyContains) != 0 {
		bodyContains = configured.HTTP.BodyContains[0]
	}
	return Monitor{
		Id:                id,
		Kind:              MonitorKindHttp,
		Name:              configured.Name,
		IntervalSeconds:   int32(configured.Interval / time.Second),
		TimeoutMillis:     int32(configured.Timeout / time.Millisecond),
		FailureThreshold:  int32(configured.FailureThreshold),
		RecoveryThreshold: int32(configured.RecoveryThreshold),
		LocationId:        locationID,
		RequiredLocation:  configured.RequiredLocation,
		Http: HTTPProbe{
			Method:          HTTPProbeMethod(configured.HTTP.Method),
			Url:             configured.HTTP.URL,
			ExpectedStatus:  int32(configured.HTTP.ExpectedStatus[0].Min),
			BodyContains:    bodyContains,
			FollowRedirects: configured.HTTP.FollowRedirects,
		},
		CreatedAt: configured.CreatedAt,
		UpdatedAt: configured.UpdatedAt,
	}, nil
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
	return Problem{}, 0, false
}
