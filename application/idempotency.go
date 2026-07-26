package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	MaxIdempotencyKeyBytes     = 255
	DefaultIdempotencyLifetime = 24 * time.Hour
	idempotencyMaxAttempts     = 5
	idempotencyRetryDelay      = 5 * time.Millisecond
)

var (
	ErrIdempotencyKeyReused      = errors.New("idempotency key reused")
	ErrCredentialAlreadyIssued   = errors.New("credential already issued")
	errIdempotencyInsertConflict = errors.New("idempotency insert conflict")
)

type IdempotencyRequest struct {
	Principal          Principal
	OperationID        string
	Key                string
	Request            any
	ResourceKind       string
	ResourceID         string
	CredentialIssuance bool
}

// IdempotencyMutation must keep all side effects inside the supplied
// transaction-scoped repositories. A non-credential mutation may be invoked
// again after a retryable transaction abort, so it must be transaction-only
// and retry-safe. Credential mutations are never invoked more than once.
type IdempotencyMutation[T any] func(context.Context, Repositories) (resourceID string, resource T, err error)

type IdempotencyLoader[T any] func(context.Context, Repositories, string) (T, error)

type IdempotencyService[T any] struct {
	store UnitOfWork
}

func NewIdempotencyService[T any](store UnitOfWork) *IdempotencyService[T] {
	return &IdempotencyService[T]{store: store}
}

func (s *IdempotencyService[T]) Execute(
	ctx context.Context,
	request IdempotencyRequest,
	mutate IdempotencyMutation[T],
	load IdempotencyLoader[T],
) (T, error) {
	var zero T
	if err := validateIdempotencyRequest(request); err != nil {
		return zero, err
	}
	if s == nil || s.store == nil || mutate == nil {
		return zero, errors.New("idempotency service is not configured")
	}
	requestHash, err := CanonicalRequestFingerprint(request.Request)
	if err != nil {
		return zero, err
	}

	for attempt := 0; attempt < idempotencyMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result := zero
		var mutationConflict error
		mutationStarted := false
		err = s.store.Transact(ctx, func(ctx context.Context, repositories Repositories) error {
			databaseNow, err := repositories.Runs.DatabaseNow(ctx)
			if err != nil {
				return fmt.Errorf("read database time for idempotency: %w", err)
			}
			databaseNow = databaseNow.UTC()
			record, err := repositories.Idempotency.Get(
				ctx,
				request.Principal.CredentialID,
				request.OperationID,
				request.Key,
				databaseNow,
			)
			switch {
			case err == nil:
				loaded, replayErr := replayIdempotentResource(ctx, repositories, request, requestHash, record, load)
				if replayErr != nil {
					return replayErr
				}
				result = loaded
				return nil
			case !errors.Is(err, ErrNotFound):
				return fmt.Errorf("read idempotency record: %w", err)
			}

			if request.CredentialIssuance {
				err = repositories.Idempotency.Create(ctx, newIdempotencyRecord(request, requestHash, request.ResourceID, databaseNow))
				if errors.Is(err, ErrConflict) {
					return errIdempotencyInsertConflict
				}
				if err != nil {
					return fmt.Errorf("reserve credential idempotency record: %w", err)
				}
			}

			mutationStarted = true
			resourceID, created, err := mutate(ctx, repositories)
			if err != nil {
				if errors.Is(err, ErrConflict) {
					mutationConflict = err
					return errIdempotencyInsertConflict
				}
				return err
			}
			if resourceID == "" {
				return errors.New("idempotent mutation returned an empty resource ID")
			}
			if request.CredentialIssuance && resourceID != request.ResourceID {
				return errors.New("credential mutation returned an unexpected resource ID")
			}
			result = created
			if !request.CredentialIssuance {
				err = repositories.Idempotency.Create(ctx, newIdempotencyRecord(request, requestHash, resourceID, databaseNow))
				if errors.Is(err, ErrConflict) {
					return errIdempotencyInsertConflict
				}
				if err != nil {
					return fmt.Errorf("create idempotency record: %w", err)
				}
			}
			return nil
		})
		if err == nil {
			return result, nil
		}
		if errors.Is(err, errIdempotencyInsertConflict) {
			err = s.store.View(ctx, func(ctx context.Context, repositories Repositories) error {
				databaseNow, err := repositories.Runs.DatabaseNow(ctx)
				if err != nil {
					return fmt.Errorf("read database time for idempotency conflict: %w", err)
				}
				record, err := repositories.Idempotency.Get(
					ctx,
					request.Principal.CredentialID,
					request.OperationID,
					request.Key,
					databaseNow.UTC(),
				)
				if err != nil {
					if errors.Is(err, ErrNotFound) && mutationConflict != nil {
						return mutationConflict
					}
					return fmt.Errorf("read winning idempotency record: %w", err)
				}
				result, err = replayIdempotentResource(ctx, repositories, request, requestHash, record, load)
				return err
			})
			if err != nil {
				return zero, err
			}
			return result, nil
		}
		if !errors.Is(err, ErrRetryableTransaction) || (request.CredentialIssuance && mutationStarted) {
			return zero, err
		}
		if attempt == idempotencyMaxAttempts-1 {
			return zero, err
		}
		if err := waitForIdempotencyRetry(ctx); err != nil {
			return zero, err
		}
	}
	return zero, errors.New("idempotency retry attempts exhausted")
}

func waitForIdempotencyRetry(ctx context.Context) error {
	timer := time.NewTimer(idempotencyRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func CanonicalRequestFingerprint(request any) (string, error) {
	canonical, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("canonicalize idempotency request: %w", err)
	}
	fingerprint := sha256.Sum256(canonical)
	return hex.EncodeToString(fingerprint[:]), nil
}

func validateIdempotencyRequest(request IdempotencyRequest) error {
	fields := make(map[string]string)
	if request.Principal.CredentialID == "" {
		fields["credential"] = "is required"
	}
	if request.OperationID == "" {
		fields["operation"] = "is required"
	}
	if request.Key == "" {
		fields["idempotencyKey"] = "is required"
	} else if len([]byte(request.Key)) > MaxIdempotencyKeyBytes {
		fields["idempotencyKey"] = "must not exceed 255 bytes"
	}
	if request.ResourceKind == "" {
		fields["resourceKind"] = "is required"
	}
	if request.CredentialIssuance && request.ResourceID == "" {
		fields["resourceId"] = "is required for credential issuance"
	}
	if len(fields) != 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

func newIdempotencyRecord(request IdempotencyRequest, requestHash, resourceID string, databaseNow time.Time) IdempotencyRecord {
	return IdempotencyRecord{
		PrincipalID:  request.Principal.CredentialID,
		OperationID:  request.OperationID,
		Key:          request.Key,
		RequestHash:  requestHash,
		ResourceKind: request.ResourceKind,
		ResourceID:   resourceID,
		CreatedAt:    databaseNow,
		ExpiresAt:    databaseNow.Add(DefaultIdempotencyLifetime),
	}
}

func replayIdempotentResource[T any](
	ctx context.Context,
	repositories Repositories,
	request IdempotencyRequest,
	requestHash string,
	record IdempotencyRecord,
	load IdempotencyLoader[T],
) (T, error) {
	var zero T
	if record.RequestHash != requestHash || record.ResourceKind != request.ResourceKind {
		return zero, ErrIdempotencyKeyReused
	}
	if request.CredentialIssuance {
		return zero, ErrCredentialAlreadyIssued
	}
	if load == nil {
		return zero, errors.New("idempotency resource loader is required for replay")
	}
	return load(ctx, repositories, record.ResourceID)
}
