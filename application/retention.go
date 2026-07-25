package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const (
	dailyAggregationLeaseKey = "retention:daily-uptime"
	resultCleanupLeaseKey    = "retention:probe-results"
	dailyCleanupLeaseKey     = "retention:daily-uptime-cleanup"
)

// ErrRetentionLeaseLost means another replica acquired an expired job lease.
var ErrRetentionLeaseLost = errors.New("retention job lease lost")

// RetentionWorkerConfig defines aggregation and cleanup bounds.
type RetentionWorkerConfig struct {
	Store                port.UnitOfWork
	Tokens               TokenIssuer
	NewID                func() string
	Owner                string
	BatchSize            int
	LeaseDuration        time.Duration
	PollInterval         time.Duration
	RawRetention         time.Duration
	DailyRetentionMonths int
	OnError              func(error)
}

// RetentionCycleResult summarizes bounded work without per-row payloads.
type RetentionCycleResult struct {
	AggregationClaimed bool
	ResultsDeleted     int64
	DailyDeleted       int64
}

// RetentionWorker aggregates UTC days and prunes bounded history under leases.
type RetentionWorker struct{ config RetentionWorkerConfig }

// NewRetentionWorker validates configuration and applies v1 defaults.
func NewRetentionWorker(config RetentionWorkerConfig) (*RetentionWorker, error) {
	if config.Store == nil || config.Tokens == nil || config.NewID == nil || strings.TrimSpace(config.Owner) == "" {
		return nil, errors.New("retention worker requires store, token issuer, identifier generator, and owner")
	}
	if config.BatchSize < 0 || config.LeaseDuration < 0 || config.PollInterval < 0 || config.RawRetention < 0 || config.DailyRetentionMonths < 0 {
		return nil, errors.New("retention worker limits cannot be negative")
	}
	if config.BatchSize == 0 {
		config.BatchSize = 500
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Minute
	}
	if config.RawRetention == 0 {
		config.RawRetention = 30 * 24 * time.Hour
	}
	if config.DailyRetentionMonths == 0 {
		config.DailyRetentionMonths = 13
	}
	if config.BatchSize > 10_000 || config.RawRetention < 24*time.Hour || config.DailyRetentionMonths > 120 {
		return nil, errors.New("invalid retention worker operational bounds")
	}
	return &RetentionWorker{config: config}, nil
}

// Run polls until cancellation and reports recoverable cycle errors through OnError.
func (w *RetentionWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx); err != nil && ctx.Err() == nil && w.config.OnError != nil {
			w.config.OnError(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunOnce performs one aggregation page and one bounded cleanup per history type.
func (w *RetentionWorker) RunOnce(ctx context.Context) (RetentionCycleResult, error) {
	aggregated, err := w.AggregateOnce(ctx)
	if err != nil {
		return RetentionCycleResult{}, err
	}
	resultsDeleted := int64(0)
	if aggregated {
		resultsDeleted, err = w.cleanupOnce(ctx, resultCleanupLeaseKey, true)
		if err != nil {
			return RetentionCycleResult{AggregationClaimed: aggregated}, err
		}
	}
	dailyDeleted, err := w.cleanupOnce(ctx, dailyCleanupLeaseKey, false)
	return RetentionCycleResult{
		AggregationClaimed: aggregated,
		ResultsDeleted:     resultsDeleted,
		DailyDeleted:       dailyDeleted,
	}, err
}

type uptimeTotals struct {
	Passing  uint64 `json:"passing"`
	Failing  uint64 `json:"failing"`
	Unknown  uint64 `json:"unknown"`
	Observed int64  `json:"observedNanos"`
}

type aggregationCursor struct {
	Day       string                  `json:"day"`
	AfterAt   time.Time               `json:"afterAt"`
	AfterID   string                  `json:"afterId"`
	ByMonitor map[string]uptimeTotals `json:"byMonitor"`
}

// AggregateOnce advances one restart-safe page of the UTC daily aggregation.
func (w *RetentionWorker) AggregateOnce(ctx context.Context) (bool, error) {
	lease, now, err := w.claimLease(ctx, dailyAggregationLeaseKey, nil)
	if errors.Is(err, port.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	earliest := utcDay(now.Add(-w.config.RawRetention))
	today := utcDay(now)
	cursor, err := decodeAggregationCursor(lease.Cursor, earliest, today)
	if err != nil {
		return true, errors.Join(err, w.releaseLease(ctx, lease))
	}
	err = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		databaseNow, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		day, err := time.Parse(time.DateOnly, cursor.Day)
		if err != nil {
			return err
		}
		day = day.UTC()
		results, err := repositories.Retention.ListAggregationResults(
			ctx, day, day.AddDate(0, 0, 1), cursor.AfterAt, cursor.AfterID, w.config.BatchSize,
		)
		if err != nil {
			return err
		}
		for _, result := range results {
			totals := cursor.ByMonitor[string(result.MonitorID)]
			if result.Passed {
				if totals.Passing == math.MaxUint64 {
					return errors.New("daily uptime passing count overflow")
				}
				totals.Passing++
			} else {
				if totals.Failing == math.MaxUint64 {
					return errors.New("daily uptime failing count overflow")
				}
				totals.Failing++
			}
			if result.Latency > 0 {
				if result.Latency > time.Duration(math.MaxInt64-totals.Observed) {
					return errors.New("daily uptime observed duration overflow")
				}
				totals.Observed += int64(result.Latency)
			}
			cursor.ByMonitor[string(result.MonitorID)] = totals
		}
		if len(results) == w.config.BatchSize {
			last := results[len(results)-1]
			cursor.AfterAt, cursor.AfterID = last.ReceivedAt, last.ID
		} else {
			for monitorID, totals := range cursor.ByMonitor {
				if totals.Observed < 0 {
					return errors.New("daily uptime observed duration overflow")
				}
				if err := repositories.Retention.UpsertDailyUptime(ctx, port.DailyUptimeRecord{
					MonitorID: domain.MonitorID(monitorID), Day: day,
					Passing: totals.Passing, Failing: totals.Failing, Unknown: totals.Unknown,
					Observed: time.Duration(totals.Observed), UpdatedAt: databaseNow,
				}); err != nil {
					return err
				}
			}
			if len(cursor.ByMonitor) > 0 {
				payload, err := json.Marshal(struct {
					Day          string `json:"day"`
					MonitorCount int    `json:"monitorCount"`
					ResultCount  int    `json:"resultCount"`
				}{Day: cursor.Day, MonitorCount: len(cursor.ByMonitor), ResultCount: totalResults(cursor.ByMonitor)})
				if err != nil {
					return err
				}
				if err := repositories.Audit.Append(ctx, port.AuditEventRecord{
					ID: w.config.NewID(), Kind: "retention.uptime-aggregated",
					SubjectKind: "uptime-day", SubjectID: cursor.Day,
					Payload: payload, CreatedAt: databaseNow,
				}); err != nil {
					return err
				}
			}
			next := day.AddDate(0, 0, 1)
			if next.After(today) {
				next = utcDay(databaseNow.Add(-w.config.RawRetention))
			}
			cursor = newAggregationCursor(next)
		}
		encoded, err := json.Marshal(cursor)
		if err != nil {
			return err
		}
		lease.Cursor = encoded
		lease.ExpiresAt = databaseNow
		lease.UpdatedAt = databaseNow
		changed, err := repositories.Retention.UpdateLease(ctx, lease)
		if err != nil {
			return err
		}
		if !changed {
			return ErrRetentionLeaseLost
		}
		return nil
	})
	if err != nil {
		err = errors.Join(err, w.releaseLease(ctx, lease))
	}
	return true, err
}

func (w *RetentionWorker) cleanupOnce(ctx context.Context, key string, results bool) (int64, error) {
	lease, _, err := w.claimLease(ctx, key, []byte(`{}`))
	if errors.Is(err, port.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var deleted int64
	err = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		kind := "retention.daily-pruned"
		if results {
			kind = "retention.results-pruned"
			deleted, err = repositories.Retention.DeleteExpiredResults(ctx, lease.UpdatedAt.Add(-w.config.RawRetention), w.config.BatchSize)
		} else {
			cutoff := utcDay(lease.UpdatedAt.AddDate(0, -w.config.DailyRetentionMonths, 0))
			deleted, err = repositories.Retention.DeleteExpiredDailyUptime(ctx, cutoff, w.config.BatchSize)
		}
		if err != nil {
			return err
		}
		if deleted > 0 {
			payload, err := json.Marshal(struct {
				Rows int64 `json:"rows"`
			}{Rows: deleted})
			if err != nil {
				return err
			}
			if err := repositories.Audit.Append(ctx, port.AuditEventRecord{
				ID: w.config.NewID(), Kind: kind, SubjectKind: "retention",
				SubjectID: key, Payload: payload, CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		changed, err := repositories.Retention.ReleaseLease(ctx, lease.Key, lease.TokenHash)
		if err != nil {
			return err
		}
		if !changed {
			return ErrRetentionLeaseLost
		}
		return nil
	})
	if err != nil {
		err = errors.Join(err, w.releaseLease(ctx, lease))
	}
	return deleted, err
}

func (w *RetentionWorker) releaseLease(ctx context.Context, lease port.OperationLeaseRecord) error {
	return w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		_, err := repositories.Retention.ReleaseLease(ctx, lease.Key, lease.TokenHash)
		return err
	})
}

func (w *RetentionWorker) claimLease(ctx context.Context, key string, initialCursor []byte) (port.OperationLeaseRecord, time.Time, error) {
	token, err := w.config.Tokens.New()
	if err != nil {
		return port.OperationLeaseRecord{}, time.Time{}, err
	}
	var lease port.OperationLeaseRecord
	var now time.Time
	err = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		now, err = repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return err
		}
		cursor := initialCursor
		if key == dailyAggregationLeaseKey {
			cursor, err = json.Marshal(newAggregationCursor(utcDay(now.Add(-w.config.RawRetention))))
			if err != nil {
				return err
			}
		}
		lease, err = repositories.Retention.ClaimLease(ctx, port.OperationLeaseRecord{
			Key: key, Owner: w.config.Owner, TokenHash: token.Hash,
			ExpiresAt: now.Add(w.config.LeaseDuration), Cursor: cursor, UpdatedAt: now,
		}, now)
		return err
	})
	return lease, now, err
}

func decodeAggregationCursor(raw []byte, earliest, today time.Time) (aggregationCursor, error) {
	var cursor aggregationCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return aggregationCursor{}, fmt.Errorf("decode daily aggregation cursor: %w", err)
	}
	day, err := time.Parse(time.DateOnly, cursor.Day)
	if err != nil {
		return aggregationCursor{}, errors.New("daily aggregation cursor contains an invalid day")
	}
	day = day.UTC()
	if day.Before(earliest) || day.After(today) {
		return newAggregationCursor(earliest), nil
	}
	if cursor.ByMonitor == nil {
		cursor.ByMonitor = map[string]uptimeTotals{}
	}
	if cursor.AfterAt.IsZero() || cursor.AfterAt.Before(day) || !cursor.AfterAt.Before(day.AddDate(0, 0, 1)) {
		cursor.AfterAt = day
		cursor.AfterID = ""
	}
	return cursor, nil
}

func newAggregationCursor(day time.Time) aggregationCursor {
	day = utcDay(day)
	return aggregationCursor{
		Day: day.Format(time.DateOnly), AfterAt: day,
		ByMonitor: map[string]uptimeTotals{},
	}
}

func utcDay(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func totalResults(values map[string]uptimeTotals) int {
	total := uint64(0)
	for _, value := range values {
		valueTotal := value.Passing + value.Failing
		if valueTotal < value.Passing || math.MaxUint64-valueTotal < value.Unknown {
			return math.MaxInt
		}
		valueTotal += value.Unknown
		if math.MaxUint64-total < valueTotal {
			return math.MaxInt
		}
		total += valueTotal
	}
	if total > uint64(math.MaxInt) {
		return math.MaxInt
	}
	return int(total)
}
