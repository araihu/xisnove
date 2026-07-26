package sqlitecompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
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

func discoveryRepositories(repositories port.Repositories, queries *dbsqlite.Queries) port.DiscoveryRepositories {
	return port.DiscoveryRepositories{Discovery: &discoveryRepository{queries: queries}, Agents: repositories.Agents, Locations: repositories.Locations, Monitors: repositories.Monitors, Health: repositories.Health}
}

type discoveryRepository struct{ queries *dbsqlite.Queries }

func (r *discoveryRepository) ApplyBatch(ctx context.Context, batch port.DiscoveryBatch) (port.DiscoveryBatchAcknowledgement, error) {
	if err := validateDiscoveryBatch(batch); err != nil {
		return port.DiscoveryBatchAcknowledgement{}, err
	}
	insertedBatch, err := r.queries.CreateDiscoveryBatch(ctx, dbsqlite.CreateDiscoveryBatchParams{
		AgentID: string(batch.AgentID), BatchID: batch.ID, RequestHash: batch.RequestHash,
		Complete: boolInt(batch.Complete), ObservedCompletedAt: nullableTimeValue(batch.CompletedAt), CreatedAt: formatDiscoveryTime(batch.CreatedAt),
	})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, repositoryError("create discovery batch", err)
	}
	if insertedBatch == 0 {
		stored, err := r.queries.GetDiscoveryBatch(ctx, dbsqlite.GetDiscoveryBatchParams{AgentID: string(batch.AgentID), BatchID: batch.ID})
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, repositoryError("get discovery batch", err)
		}
		if stored.RequestHash != batch.RequestHash || (stored.Complete == 1) != batch.Complete || !sameNullableDiscoveryTime(stored.ObservedCompletedAt, batch.CompletedAt) {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
		}
		if !stored.CompletedAt.Valid {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrRetryableTransaction
		}
		return port.DiscoveryBatchAcknowledgement{Accepted: int(stored.Accepted), Created: int(stored.CreatedCount), Updated: int(stored.UpdatedCount)}, nil
	}
	if batch.Complete {
		fenced, err := r.queries.FenceAgentLastCompleteDiscovery(ctx, dbsqlite.FenceAgentLastCompleteDiscoveryParams{CompletedAt: nullableTimeValue(batch.CompletedAt), AgentID: string(batch.AgentID)})
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, repositoryError("fence complete discovery", err)
		}
		if fenced != 1 {
			return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
		}
	}
	ack := port.DiscoveryBatchAcknowledgement{Accepted: len(batch.Candidates)}
	for _, candidate := range batch.Candidates {
		if !candidate.Present {
			updated, err := r.updateCandidate(ctx, candidate)
			if err != nil {
				return port.DiscoveryBatchAcknowledgement{}, err
			}
			if updated {
				ack.Updated++
			}
			continue
		}
		created, err := r.insertCandidate(ctx, candidate)
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, err
		}
		if created {
			ack.Created++
			continue
		}
		updated, err := r.updateCandidate(ctx, candidate)
		if err != nil {
			return port.DiscoveryBatchAcknowledgement{}, err
		}
		if updated {
			ack.Updated++
		}
	}
	if batch.Complete {
		if _, err := r.queries.MarkAbsentDiscoveryCandidates(ctx, dbsqlite.MarkAbsentDiscoveryCandidatesParams{UpdatedAt: formatDiscoveryTime(batch.CreatedAt), AgentID: string(batch.AgentID), LastObservedAt: formatDiscoveryTime(batch.CompletedAt)}); err != nil {
			return port.DiscoveryBatchAcknowledgement{}, repositoryError("mark absent discovery candidates", err)
		}
	}
	completed, err := r.queries.CompleteDiscoveryBatch(ctx, dbsqlite.CompleteDiscoveryBatchParams{
		Accepted: int64(ack.Accepted), CreatedCount: int64(ack.Created), UpdatedCount: int64(ack.Updated),
		CompletedAt: sql.NullString{String: formatDiscoveryTime(batch.CreatedAt), Valid: true}, AgentID: string(batch.AgentID), BatchID: batch.ID,
	})
	if err != nil {
		return port.DiscoveryBatchAcknowledgement{}, repositoryError("complete discovery batch", err)
	}
	if completed != 1 {
		return port.DiscoveryBatchAcknowledgement{}, port.ErrConflict
	}
	return ack, nil
}

func (r *discoveryRepository) LastCompleteAt(ctx context.Context, agentID domain.AgentID) (*time.Time, error) {
	value, err := r.queries.GetAgentLastCompleteDiscovery(ctx, string(agentID))
	if err != nil {
		return nil, repositoryError("get last complete discovery", err)
	}
	return parseNullableTime(value)
}

func validateDiscoveryBatch(batch port.DiscoveryBatch) error {
	if !batch.Complete && len(batch.Candidates) == 0 {
		return port.ErrConflict
	}
	if !batch.Complete {
		return nil
	}
	if batch.CompletedAt.IsZero() {
		return port.ErrConflict
	}
	for _, candidate := range batch.Candidates {
		if !candidate.LastObservedAt.Equal(batch.CompletedAt) {
			return port.ErrConflict
		}
	}
	return nil
}

func sameNullableDiscoveryTime(stored sql.NullString, expected time.Time) bool {
	if expected.IsZero() {
		return !stored.Valid
	}
	if !stored.Valid {
		return false
	}
	parsed, err := parseTime(stored.String)
	return err == nil && parsed.Equal(expected)
}

func (r *discoveryRepository) insertCandidate(ctx context.Context, candidate domain.DiscoveryCandidate) (bool, error) {
	labels, err := json.Marshal(candidate.Labels)
	if err != nil {
		return false, err
	}
	rows, err := r.queries.InsertDiscoveryCandidate(ctx, dbsqlite.InsertDiscoveryCandidateParams{
		ID: string(candidate.ID), AgentID: string(candidate.AgentID), LocationID: string(candidate.LocationID),
		SourceKind: candidate.SourceKind, SourceUid: candidate.SourceUID, Namespace: candidate.Namespace, Name: candidate.Name,
		LabelsJson: labels, Protocol: string(candidate.Protocol), Target: candidate.Target, NetworkPerspective: candidate.NetworkPerspective,
		Present: boolInt(candidate.Present), LastObservedAt: formatDiscoveryTime(candidate.LastObservedAt), PromotedMonitorID: discoveryNullableMonitorID(candidate.PromotedMonitorID),
		DriftHint: candidate.DriftHint, CreatedAt: formatDiscoveryTime(candidate.CreatedAt), UpdatedAt: formatDiscoveryTime(candidate.UpdatedAt),
	})
	if err != nil {
		return false, repositoryError("insert discovery candidate", err)
	}
	return rows == 1, nil
}

func (r *discoveryRepository) updateCandidate(ctx context.Context, candidate domain.DiscoveryCandidate) (bool, error) {
	labels, err := json.Marshal(candidate.Labels)
	if err != nil {
		return false, err
	}
	rows, err := r.queries.UpdateDiscoveryCandidateByIdentity(ctx, dbsqlite.UpdateDiscoveryCandidateByIdentityParams{
		Namespace: candidate.Namespace, Name: candidate.Name, LabelsJson: labels, NetworkPerspective: candidate.NetworkPerspective,
		Present: boolInt(candidate.Present), LastObservedAt: formatDiscoveryTime(candidate.LastObservedAt), UpdatedAt: formatDiscoveryTime(candidate.UpdatedAt),
		AgentID: string(candidate.AgentID), LocationID: string(candidate.LocationID), SourceKind: candidate.SourceKind,
		SourceUid: candidate.SourceUID, Protocol: string(candidate.Protocol), Target: candidate.Target,
	})
	if err != nil {
		return false, repositoryError("update discovery candidate", err)
	}
	return rows == 1, nil
}

func (r *discoveryRepository) Get(ctx context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	row, err := r.queries.GetDiscoveryCandidate(ctx, string(id))
	if err != nil {
		return domain.DiscoveryCandidate{}, repositoryError("get discovery candidate", err)
	}
	return mapSQLiteDiscoveryCandidate(row)
}

func (r *discoveryRepository) GetForUpdate(ctx context.Context, id domain.DiscoveryCandidateID) (domain.DiscoveryCandidate, error) {
	row, err := r.queries.GetDiscoveryCandidateForUpdate(ctx, string(id))
	if err != nil {
		return domain.DiscoveryCandidate{}, repositoryError("get discovery candidate for update", err)
	}
	return mapSQLiteDiscoveryCandidate(row)
}

func (r *discoveryRepository) List(ctx context.Context, request port.DiscoveryListRequest) ([]domain.DiscoveryCandidate, error) {
	present := sql.NullBool{}
	if request.Filter.Present != nil {
		present = sql.NullBool{Bool: *request.Filter.Present, Valid: true}
	}
	rows, err := r.queries.ListDiscoveryCandidates(ctx, dbsqlite.ListDiscoveryCandidatesParams{
		State: string(request.Filter.State), PresentFilter: present,
		AfterID: string(request.After), RowLimit: int64(request.Limit),
	})
	if err != nil {
		return nil, repositoryError("list discovery candidates", err)
	}
	result := make([]domain.DiscoveryCandidate, 0, len(rows))
	for _, row := range rows {
		candidate, err := mapSQLiteDiscoveryCandidate(row)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (r *discoveryRepository) LinkPromotion(ctx context.Context, id domain.DiscoveryCandidateID, monitorID domain.MonitorID, at time.Time) (bool, error) {
	rows, err := r.queries.LinkDiscoveryPromotion(ctx, dbsqlite.LinkDiscoveryPromotionParams{PromotedMonitorID: sql.NullString{String: string(monitorID), Valid: true}, UpdatedAt: formatDiscoveryTime(at), ID: string(id)})
	if err != nil {
		return false, repositoryError("link discovery promotion", err)
	}
	return rows == 1, nil
}

func sqliteDiscoveryIdentity(identity domain.DiscoveryIdentity) dbsqlite.GetDiscoveryCandidateByIdentityParams {
	return dbsqlite.GetDiscoveryCandidateByIdentityParams{AgentID: string(identity.AgentID), LocationID: string(identity.LocationID), SourceKind: identity.SourceKind, SourceUid: identity.SourceUID, Protocol: string(identity.Protocol), Target: identity.Target}
}

func mapSQLiteDiscoveryCandidate(row dbsqlite.DiscoveryCandidate) (domain.DiscoveryCandidate, error) {
	var labels map[string]string
	if err := json.Unmarshal(row.LabelsJson, &labels); err != nil {
		return domain.DiscoveryCandidate{}, fmt.Errorf("decode discovery labels: %w", err)
	}
	lastObserved, err := parseTime(row.LastObservedAt)
	if err != nil {
		return domain.DiscoveryCandidate{}, err
	}
	createdAt, err := parseTime(row.CreatedAt)
	if err != nil {
		return domain.DiscoveryCandidate{}, err
	}
	updatedAt, err := parseTime(row.UpdatedAt)
	if err != nil {
		return domain.DiscoveryCandidate{}, err
	}
	var promoted *domain.MonitorID
	if row.PromotedMonitorID.Valid {
		id := domain.MonitorID(row.PromotedMonitorID.String)
		promoted = &id
	}
	return domain.DiscoveryCandidate{ID: domain.DiscoveryCandidateID(row.ID), AgentID: domain.AgentID(row.AgentID), LocationID: domain.LocationID(row.LocationID), SourceKind: row.SourceKind, SourceUID: row.SourceUid, Namespace: row.Namespace, Name: row.Name, Labels: labels, Protocol: domain.MonitorKind(row.Protocol), Target: row.Target, NetworkPerspective: row.NetworkPerspective, Present: row.Present == 1, LastObservedAt: lastObserved, PromotedMonitorID: promoted, DriftHint: row.DriftHint, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func discoveryNullableMonitorID(id *domain.MonitorID) sql.NullString {
	if id == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*id), Valid: true}
}

func formatDiscoveryTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}

var _ port.DiscoveryUnitOfWork = (*store)(nil)
var _ port.DiscoveryRepository = (*discoveryRepository)(nil)
var _ port.CompleteDiscoveryRepository = (*discoveryRepository)(nil)
