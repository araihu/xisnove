package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
	"github.com/araihu/xisnove/domain"
)

func (s *store) DiscoveryView(ctx context.Context, fn func(context.Context, port.DiscoveryRepositories) error) error {
	repositories := s.Repositories()
	queries := repositories.Monitors.(*monitorRepository).queries
	return fn(ctx, discoveryRepositories(repositories, queries))
}

func (s *store) DiscoveryTransact(ctx context.Context, fn func(context.Context, port.DiscoveryRepositories) error) error {
	return s.transact(ctx, func(repositories port.Repositories) error {
		queries := repositories.Monitors.(*monitorRepository).queries
		return fn(ctx, discoveryRepositories(repositories, queries))
	})
}

func discoveryRepositories(repositories port.Repositories, queries *dbpostgres.Queries) port.DiscoveryRepositories {
	return port.DiscoveryRepositories{Discovery: &discoveryRepository{queries: queries}, Locations: repositories.Locations, Monitors: repositories.Monitors, Health: repositories.Health}
}

type discoveryRepository struct{ queries *dbpostgres.Queries }

func (r *discoveryRepository) ApplyBatch(ctx context.Context, batch port.DiscoveryBatch) (port.DiscoveryBatchAcknowledgement, error) {
	insertedBatch, err := r.queries.CreateDiscoveryBatch(ctx, dbpostgres.CreateDiscoveryBatchParams{AgentID: string(batch.AgentID), BatchID: batch.ID, RequestHash: batch.RequestHash, CreatedAt: batch.CreatedAt})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, repositoryError("create discovery batch", err)
	}
	if insertedBatch == 0 {
		stored, err := r.queries.GetDiscoveryBatch(ctx, dbpostgres.GetDiscoveryBatchParams{AgentID: string(batch.AgentID), BatchID: batch.ID})
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, repositoryError("get discovery batch", err)
		}
		if stored.RequestHash != batch.RequestHash {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
		}
		if !stored.CompletedAt.Valid {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrRetryableTransaction
		}
		return port.DiscoveryBatchAcknowledgement{Accepted: int(stored.Accepted), Created: int(stored.CreatedCount), Updated: int(stored.UpdatedCount)}, nil
	}
	ack := port.DiscoveryBatchAcknowledgement{Accepted: len(batch.Candidates)}
	for _, candidate := range batch.Candidates {
		created, err := r.insertCandidate(ctx, candidate)
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, err
		}
		if created {
			ack.Created++
			continue
		}
		if err := r.updateCandidate(ctx, candidate); err != nil {
			return port.DiscoveryBatchAcknowledgement{}, err
		}
		ack.Updated++
	}
	completed, err := r.queries.CompleteDiscoveryBatch(ctx, dbpostgres.CompleteDiscoveryBatchParams{Accepted: int32(ack.Accepted), CreatedCount: int32(ack.Created), UpdatedCount: int32(ack.Updated), CompletedAt: sql.NullTime{Time: batch.CreatedAt, Valid: true}, AgentID: string(batch.AgentID), BatchID: batch.ID})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, repositoryError("complete discovery batch", err)
	}
	if completed != 1 {
		return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
	}
	return ack, nil
}

func (r *discoveryRepository) insertCandidate(ctx context.Context, candidate domain.DiscoveryCandidate) (bool, error) {
	labels, err := json.Marshal(candidate.Labels)
	if err != nil {
		return false, err
	}
	rows, err := r.queries.InsertDiscoveryCandidate(ctx, dbpostgres.InsertDiscoveryCandidateParams{
		ID: string(candidate.ID), AgentID: string(candidate.AgentID), LocationID: string(candidate.LocationID), SourceKind: candidate.SourceKind, SourceUid: candidate.SourceUID,
		Namespace: candidate.Namespace, Name: candidate.Name, LabelsJson: labels, Protocol: string(candidate.Protocol), Target: candidate.Target,
		NetworkPerspective: candidate.NetworkPerspective, Present: candidate.Present, LastObservedAt: candidate.LastObservedAt,
		PromotedMonitorID: postgresMonitorID(candidate.PromotedMonitorID), DriftHint: candidate.DriftHint, CreatedAt: candidate.CreatedAt, UpdatedAt: candidate.UpdatedAt,
	})
	if err != nil {
		return false, repositoryError("insert discovery candidate", err)
	}
	return rows == 1, nil
}

func (r *discoveryRepository) updateCandidate(ctx context.Context, candidate domain.DiscoveryCandidate) error {
	existingRow, err := r.queries.GetDiscoveryCandidateByIdentity(ctx, postgresDiscoveryIdentity(candidate.Identity()))
	if err != nil {
		return repositoryError("get discovery candidate identity", err)
	}
	existing, err := mapPostgresDiscoveryCandidate(existingRow)
	if err != nil {
		return err
	}
	if candidate.LastObservedAt.Before(existing.LastObservedAt) {
		return nil
	}
	drift := existing.DriftHint
	if existing.PromotedMonitorID != nil && (existing.Name != candidate.Name || existing.Namespace != candidate.Namespace || existing.NetworkPerspective != candidate.NetworkPerspective || !maps.Equal(existing.Labels, candidate.Labels)) {
		drift = "source metadata changed after promotion"
	}
	labels, err := json.Marshal(candidate.Labels)
	if err != nil {
		return err
	}
	rows, err := r.queries.UpdateDiscoveryCandidateByIdentity(ctx, dbpostgres.UpdateDiscoveryCandidateByIdentityParams{
		Namespace: candidate.Namespace, Name: candidate.Name, LabelsJson: labels, NetworkPerspective: candidate.NetworkPerspective,
		Present: candidate.Present, LastObservedAt: candidate.LastObservedAt, DriftHint: drift, UpdatedAt: candidate.UpdatedAt,
		AgentID: string(candidate.AgentID), LocationID: string(candidate.LocationID), SourceKind: candidate.SourceKind, SourceUid: candidate.SourceUID,
		Protocol: string(candidate.Protocol), Target: candidate.Target,
	})
	if err != nil {
		return repositoryError("update discovery candidate", err)
	}
	if rows != 1 {
		return port.ErrConflict
	}
	return nil
}

func (r *discoveryRepository) Get(ctx context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	row, err := r.queries.GetDiscoveryCandidate(ctx, string(id))
	if err != nil {
		return domain.DiscoveryCandidate{}, repositoryError("get discovery candidate", err)
	}
	return mapPostgresDiscoveryCandidate(row)
}

func (r *discoveryRepository) List(ctx context.Context, request port.DiscoveryListRequest) ([]domain.DiscoveryCandidate, error) {
	after := sql.NullString{}
	if request.After != "" {
		after = sql.NullString{String: string(request.After), Valid: true}
	}
	rows, err := r.queries.ListDiscoveryCandidates(ctx, dbpostgres.ListDiscoveryCandidatesParams{State: string(request.Filter.State), AfterID: after, RowLimit: int32(request.Limit)})
	if err != nil {
		return nil, repositoryError("list discovery candidates", err)
	}
	result := make([]domain.DiscoveryCandidate, 0, len(rows))
	for _, row := range rows {
		candidate, err := mapPostgresDiscoveryCandidate(row)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (r *discoveryRepository) LinkPromotion(ctx context.Context, id domain.DiscoveryCandidateID, monitorID domain.MonitorID, at time.Time) (bool, error) {
	rows, err := r.queries.LinkDiscoveryPromotion(ctx, dbpostgres.LinkDiscoveryPromotionParams{PromotedMonitorID: sql.NullString{String: string(monitorID), Valid: true}, UpdatedAt: at, ID: string(id)})
	if err != nil {
		return false, repositoryError("link discovery promotion", err)
	}
	return rows == 1, nil
}

func postgresDiscoveryIdentity(identity domain.DiscoveryIdentity) dbpostgres.GetDiscoveryCandidateByIdentityParams {
	return dbpostgres.GetDiscoveryCandidateByIdentityParams{AgentID: string(identity.AgentID), LocationID: string(identity.LocationID), SourceKind: identity.SourceKind, SourceUid: identity.SourceUID, Protocol: string(identity.Protocol), Target: identity.Target}
}

func mapPostgresDiscoveryCandidate(row dbpostgres.DiscoveryCandidate) (domain.DiscoveryCandidate, error) {
	var labels map[string]string
	if err := json.Unmarshal(row.LabelsJson, &labels); err != nil {
		return domain.DiscoveryCandidate{}, fmt.Errorf("decode discovery labels: %w", err)
	}
	var promoted *domain.MonitorID
	if row.PromotedMonitorID.Valid {
		id := domain.MonitorID(row.PromotedMonitorID.String)
		promoted = &id
	}
	return domain.DiscoveryCandidate{ID: domain.DiscoveryCandidateID(row.ID), AgentID: domain.AgentID(row.AgentID), LocationID: domain.LocationID(row.LocationID), SourceKind: row.SourceKind, SourceUID: row.SourceUid, Namespace: row.Namespace, Name: row.Name, Labels: labels, Protocol: domain.MonitorKind(row.Protocol), Target: row.Target, NetworkPerspective: row.NetworkPerspective, Present: row.Present, LastObservedAt: row.LastObservedAt.UTC(), PromotedMonitorID: promoted, DriftHint: row.DriftHint, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func postgresMonitorID(id *domain.MonitorID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

var _ port.DiscoveryUnitOfWork = (*store)(nil)
var _ port.DiscoveryRepository = (*discoveryRepository)(nil)
