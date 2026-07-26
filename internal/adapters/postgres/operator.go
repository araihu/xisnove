package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
)

type operatorRepository struct{ queries *dbpostgres.Queries }

func (r *operatorRepository) Resolve(ctx context.Context, owner port.ExternalOwner, kind string) (port.OperatorBinding, error) {
	row, err := r.queries.GetOperatorResource(ctx, dbpostgres.GetOperatorResourceParams{OwnerKey: owner.Key, OwnerUid: owner.UID, Kind: kind})
	if err != nil {
		return port.OperatorBinding{}, repositoryError("resolve operator resource", err)
	}
	deletedAt, err := parseNullableTime(row.DeletedAt)
	if err != nil {
		return port.OperatorBinding{}, err
	}
	return port.OperatorBinding{Owner: port.ExternalOwner{Key: row.OwnerKey, UID: row.OwnerUid}, Kind: row.Kind, ResourceID: row.ResourceID, DeletedAt: deletedAt}, nil
}

func (r *operatorRepository) Bind(ctx context.Context, binding port.OperatorBinding) error {
	existing, err := r.Resolve(ctx, binding.Owner, binding.Kind)
	if err == nil {
		if existing.ResourceID != binding.ResourceID {
			return port.ErrConflict
		}
		rows, err := r.queries.RestoreOperatorResource(ctx, dbpostgres.RestoreOperatorResourceParams{OwnerKey: binding.Owner.Key, OwnerUid: binding.Owner.UID, Kind: binding.Kind, ResourceID: binding.ResourceID})
		if err != nil {
			return repositoryError("restore operator resource", err)
		}
		if rows != 1 {
			return port.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, port.ErrNotFound) {
		return err
	}
	if err := r.queries.InsertOperatorResource(ctx, dbpostgres.InsertOperatorResourceParams{OwnerKey: binding.Owner.Key, OwnerUid: binding.Owner.UID, Kind: binding.Kind, ResourceID: binding.ResourceID}); err != nil {
		return repositoryError("bind operator resource", err)
	}
	return nil
}

func (r *operatorRepository) Tombstone(ctx context.Context, owner port.ExternalOwner, kind string, at time.Time) error {
	rows, err := r.queries.TombstoneOperatorResource(ctx, dbpostgres.TombstoneOperatorResourceParams{DeletedAt: sql.NullTime{Time: formatTime(at), Valid: true}, OwnerKey: owner.Key, OwnerUid: owner.UID, Kind: kind})
	if err != nil {
		return repositoryError("tombstone operator resource", err)
	}
	if rows > 1 {
		return port.ErrConflict
	}
	return nil
}

var _ port.OperatorRepository = (*operatorRepository)(nil)
