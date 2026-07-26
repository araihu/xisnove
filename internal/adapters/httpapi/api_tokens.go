package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

func (s *Server) CreateAPIToken(
	ctx context.Context,
	request CreateAPITokenRequestObject,
) (CreateAPITokenResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return createAPITokenProblem(application.ErrInvalidCredentials), nil
	}
	if request.Body == nil {
		return createAPITokenProblem(&application.ValidationError{
			Fields: map[string]string{"body": "is required"},
		}), nil
	}
	scopes := make([]application.Scope, len(request.Body.Scopes))
	for i, scope := range request.Body.Scopes {
		scopes[i] = application.Scope(scope)
	}
	credential, err := s.apiTokens.Create(ctx, principal, application.CreateAPITokenCommand{
		Label: request.Body.Name, Scopes: scopes, ExpiresAt: request.Body.ExpiresAt,
	})
	if err != nil {
		if errors.Is(err, application.ErrInvalidScopes) || errors.Is(err, application.ErrInvalidExpiry) ||
			errors.Is(err, domain.ErrInvalidAPITokenLabel) {
			err = &application.ValidationError{Fields: map[string]string{"body": "contains invalid API token configuration"}}
		}
		response := createAPITokenProblem(err)
		if response != nil {
			return response, nil
		}
		return nil, err
	}
	mapped, err := mapAPIToken(credential.Record)
	if err != nil {
		return nil, err
	}
	return CreateAPIToken201JSONResponse{ApiToken: mapped, Token: credential.Token}, nil
}

func (s *Server) ListAPITokens(
	ctx context.Context,
	request ListAPITokensRequestObject,
) (ListAPITokensResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		problem, status, _ := problemFromError(application.ErrInvalidCredentials)
		return ListAPITokensdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	pageRequest := application.PageRequest{}
	if request.Params.Limit != nil {
		pageRequest.Limit = int(*request.Params.Limit)
	}
	if request.Params.Cursor != nil {
		pageRequest.Cursor = string(*request.Params.Cursor)
	}
	page, err := s.apiTokens.List(ctx, principal, pageRequest)
	if err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return ListAPITokensdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	items := make([]APIToken, 0, len(page.Items))
	for _, record := range page.Items {
		mapped, err := mapAPIToken(record)
		if err != nil {
			return nil, err
		}
		items = append(items, mapped)
	}
	metadata := PageMetadata{}
	if page.NextCursor != "" {
		next := page.NextCursor
		metadata.NextCursor = &next
	}
	return ListAPITokens200JSONResponse{Items: items, Page: metadata}, nil
}

func (s *Server) RevokeAPIToken(
	ctx context.Context,
	request RevokeAPITokenRequestObject,
) (RevokeAPITokenResponseObject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		problem, status, _ := problemFromError(application.ErrInvalidCredentials)
		return RevokeAPITokendefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
	}
	if err := s.apiTokens.Revoke(ctx, principal, request.TokenId.String()); err != nil {
		problem, status, mapped := problemFromError(err)
		if mapped {
			return RevokeAPITokendefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}, nil
		}
		return nil, err
	}
	return RevokeAPIToken204Response{}, nil
}

func createAPITokenProblem(err error) CreateAPITokenResponseObject {
	problem, status, mapped := problemFromError(err)
	if !mapped {
		return nil
	}
	return CreateAPITokendefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: status}
}

func mapAPIToken(record application.APITokenRecord) (APIToken, error) {
	id, err := uuid.Parse(record.ID)
	if err != nil {
		return APIToken{}, fmt.Errorf("map API token ID: %w", err)
	}
	scopes := make([]APITokenScope, len(record.Scopes))
	for i, scope := range record.Scopes {
		scopes[i] = APITokenScope(scope)
	}
	updatedAt := record.CreatedAt
	for _, candidate := range []*time.Time{record.LastUsedAt, record.RevokedAt} {
		if candidate != nil && candidate.After(updatedAt) {
			updatedAt = *candidate
		}
	}
	return APIToken{
		Id: id, Name: record.Label, Scopes: scopes, CreatedAt: record.CreatedAt,
		UpdatedAt: updatedAt, ExpiresAt: record.ExpiresAt, LastUsedAt: record.LastUsedAt, RevokedAt: record.RevokedAt,
	}, nil
}
