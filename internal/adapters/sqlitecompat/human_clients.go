package sqlitecompat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	application "github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
)

type apiTokenRepository struct {
	queries *dbsqlite.Queries
}

type idempotencyRepository struct {
	queries *dbsqlite.Queries
}

func (r *idempotencyRepository) Get(
	ctx context.Context,
	principalID string,
	operationID string,
	key string,
	now time.Time,
) (application.IdempotencyRecord, error) {
	record, err := r.queries.GetActiveIdempotencyRecord(ctx, dbsqlite.GetActiveIdempotencyRecordParams{
		PrincipalID: principalID, OperationID: operationID, IdempotencyKey: key, Now: formatTime(now),
	})
	if err != nil {
		return application.IdempotencyRecord{}, repositoryError("get active idempotency record", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.IdempotencyRecord{}, fmt.Errorf("map idempotency creation: %w", err)
	}
	expiresAt, err := parseTime(record.ExpiresAt)
	if err != nil {
		return application.IdempotencyRecord{}, fmt.Errorf("map idempotency expiry: %w", err)
	}
	return application.IdempotencyRecord{
		PrincipalID: record.PrincipalID, OperationID: record.OperationID, Key: record.IdempotencyKey,
		RequestHash: record.RequestHash, ResourceKind: record.ResourceKind, ResourceID: record.ResourceID,
		CreatedAt: createdAt, ExpiresAt: expiresAt,
	}, nil
}

func (r *idempotencyRepository) Create(ctx context.Context, record application.IdempotencyRecord) error {
	affected, err := r.queries.PutIdempotencyRecord(ctx, dbsqlite.PutIdempotencyRecordParams{
		PrincipalID: record.PrincipalID, OperationID: record.OperationID, IdempotencyKey: record.Key,
		RequestHash: record.RequestHash, ResourceKind: record.ResourceKind, ResourceID: record.ResourceID,
		CreatedAt: formatTime(record.CreatedAt), ExpiresAt: formatTime(record.ExpiresAt),
	})
	if err != nil {
		return repositoryError("put idempotency record", err)
	}
	if affected != 1 {
		return fmt.Errorf("put idempotency record: %w", application.ErrConflict)
	}
	return nil
}

func (r *idempotencyRepository) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	deleted, err := r.queries.DeleteExpiredIdempotencyRecords(ctx, dbsqlite.DeleteExpiredIdempotencyRecordsParams{
		Cutoff: formatTime(now), RowLimit: int64(limit),
	})
	if err != nil {
		return 0, repositoryError("delete expired idempotency records", err)
	}
	return deleted, nil
}

func (r *apiTokenRepository) Create(ctx context.Context, record application.APITokenRecord) error {
	scopes, err := json.Marshal(slices.Clone(record.Scopes))
	if err != nil {
		return fmt.Errorf("encode API token scopes: %w", err)
	}
	err = r.queries.CreateAPIToken(ctx, dbsqlite.CreateAPITokenParams{
		ID: record.ID, AdminID: record.AdminID, Label: record.Label,
		TokenHash: slices.Clone(record.TokenHash), ScopesJson: scopes,
		CreatedAt: formatTime(record.CreatedAt), ExpiresAt: nullableTime(record.ExpiresAt),
		LastUsedAt: nullableTime(record.LastUsedAt), RevokedAt: nullableTime(record.RevokedAt),
	})
	return repositoryError("create API token", err)
}

func (r *apiTokenRepository) FindActiveByTokenHash(
	ctx context.Context,
	tokenHash []byte,
	now time.Time,
) (application.APITokenRecord, error) {
	record, err := r.queries.FindActiveAPITokenByTokenHash(ctx, dbsqlite.FindActiveAPITokenByTokenHashParams{
		TokenHash: slices.Clone(tokenHash), Now: formatTime(now),
	})
	if err != nil {
		return application.APITokenRecord{}, repositoryError("find active API token", err)
	}
	return mapSQLiteAPIToken(record)
}

func (r *apiTokenRepository) List(
	ctx context.Context,
	request application.PageRequest,
) (application.Page[application.APITokenRecord], error) {
	limit := normalizedPageLimit(request.Limit)
	var records []dbsqlite.ApiToken
	var err error
	if request.Cursor == "" {
		records, err = r.queries.ListAPITokens(ctx, int64(limit+1))
	} else {
		cursor, decodeErr := decodeAPITokenCursor(request.Cursor)
		if decodeErr != nil {
			return application.Page[application.APITokenRecord]{}, decodeErr
		}
		records, err = r.queries.ListAPITokensAfter(ctx, dbsqlite.ListAPITokensAfterParams{
			CursorCreatedAt: formatTime(cursor.CreatedAt), CursorID: cursor.ID, RowLimit: int64(limit + 1),
		})
	}
	if err != nil {
		return application.Page[application.APITokenRecord]{}, repositoryError("list API tokens", err)
	}
	return mapSQLiteAPITokenPage(records, limit)
}

func (r *apiTokenRepository) Revoke(ctx context.Context, id string, at time.Time) (bool, error) {
	count, err := r.queries.RevokeAPIToken(ctx, dbsqlite.RevokeAPITokenParams{
		RevokedAt: nullableTimeValue(at), ID: id,
	})
	if err != nil {
		return false, repositoryError("revoke API token", err)
	}
	return count == 1, nil
}

func (r *apiTokenRepository) TouchLastUsed(ctx context.Context, id string, at time.Time) error {
	count, err := r.queries.TouchAPITokenLastUsed(ctx, dbsqlite.TouchAPITokenLastUsedParams{
		LastUsedAt: nullableTimeValue(at), ID: id,
	})
	if err != nil {
		return repositoryError("touch API token", err)
	}
	if count != 1 {
		return fmt.Errorf("touch API token: %w", application.ErrNotFound)
	}
	return nil
}

func mapSQLiteAPIToken(record dbsqlite.ApiToken) (application.APITokenRecord, error) {
	var scopes []application.Scope
	if err := json.Unmarshal(record.ScopesJson, &scopes); err != nil || len(scopes) == 0 {
		if err == nil {
			err = errors.New("empty scope array")
		}
		return application.APITokenRecord{}, fmt.Errorf("map API token scopes: %w", err)
	}
	scopes, err := application.NormalizeScopes(scopes)
	if err != nil {
		return application.APITokenRecord{}, fmt.Errorf("map API token scopes: %w", err)
	}
	createdAt, err := parseTime(record.CreatedAt)
	if err != nil {
		return application.APITokenRecord{}, fmt.Errorf("map API token creation: %w", err)
	}
	expiresAt, err := parseNullableTime(record.ExpiresAt)
	if err != nil {
		return application.APITokenRecord{}, fmt.Errorf("map API token expiry: %w", err)
	}
	lastUsedAt, err := parseNullableTime(record.LastUsedAt)
	if err != nil {
		return application.APITokenRecord{}, fmt.Errorf("map API token last-used: %w", err)
	}
	revokedAt, err := parseNullableTime(record.RevokedAt)
	if err != nil {
		return application.APITokenRecord{}, fmt.Errorf("map API token revocation: %w", err)
	}
	return application.APITokenRecord{
		ID: record.ID, AdminID: record.AdminID, Label: record.Label,
		TokenHash: slices.Clone(record.TokenHash), Scopes: slices.Clone(scopes), CreatedAt: createdAt,
		ExpiresAt: expiresAt, LastUsedAt: lastUsedAt, RevokedAt: revokedAt,
	}, nil
}

func mapSQLiteAPITokenPage(records []dbsqlite.ApiToken, limit int) (application.Page[application.APITokenRecord], error) {
	hasNext := len(records) > limit
	if hasNext {
		records = records[:limit]
	}
	page := application.Page[application.APITokenRecord]{Items: make([]application.APITokenRecord, 0, len(records))}
	for _, record := range records {
		mapped, err := mapSQLiteAPIToken(record)
		if err != nil {
			return application.Page[application.APITokenRecord]{}, err
		}
		page.Items = append(page.Items, mapped)
	}
	if hasNext {
		page.NextCursor = encodeAPITokenCursor(apiTokenCursor{
			CreatedAt: page.Items[len(page.Items)-1].CreatedAt, ID: page.Items[len(page.Items)-1].ID,
		})
	}
	return page, nil
}

type apiTokenCursor struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

func normalizedPageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func encodeAPITokenCursor(cursor apiTokenCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeAPITokenCursor(value string) (apiTokenCursor, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return apiTokenCursor{}, fmt.Errorf("decode API token cursor: %w", err)
	}
	var cursor apiTokenCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.CreatedAt.IsZero() || cursor.ID == "" {
		if err == nil {
			err = errors.New("missing cursor fields")
		}
		return apiTokenCursor{}, fmt.Errorf("decode API token cursor: %w", err)
	}
	return cursor, nil
}
