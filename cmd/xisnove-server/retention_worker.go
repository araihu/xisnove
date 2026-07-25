package main

import (
	"errors"
	"flag"
	"log/slog"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
)

type retentionWorkerFlagValues struct {
	batchSize            int
	leaseDuration        time.Duration
	pollInterval         time.Duration
	rawRetention         time.Duration
	dailyRetentionMonths int
}

func addRetentionWorkerFlags(flags *flag.FlagSet) *retentionWorkerFlagValues {
	values := &retentionWorkerFlagValues{}
	flags.IntVar(&values.batchSize, "retention-batch-size", 500, "maximum rows aggregated or pruned per retention transaction")
	flags.DurationVar(&values.leaseDuration, "retention-lease", 45*time.Second, "retention job claim lease duration")
	flags.DurationVar(&values.pollInterval, "retention-poll", time.Minute, "retention job poll interval")
	flags.DurationVar(&values.rawRetention, "retention-probe-results", 30*24*time.Hour, "raw probe-result retention period")
	flags.IntVar(&values.dailyRetentionMonths, "retention-daily-months", 13, "calendar months of daily uptime history to retain")
	return values
}

func (v *retentionWorkerFlagValues) build(
	store port.UnitOfWork,
	tokens application.TokenIssuer,
	newID func() string,
	owner string,
) (*application.RetentionWorker, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	return application.NewRetentionWorker(application.RetentionWorkerConfig{
		Store: store, Tokens: tokens, NewID: newID, Owner: owner,
		BatchSize: v.batchSize, LeaseDuration: v.leaseDuration,
		PollInterval: v.pollInterval, RawRetention: v.rawRetention,
		DailyRetentionMonths: v.dailyRetentionMonths,
		OnError: func(err error) {
			slog.Error("retention cycle failed", "error_class", "retention_cycle", "error", err)
		},
	})
}

func (v *retentionWorkerFlagValues) validate() error {
	if v.batchSize <= 0 || v.batchSize > 10_000 || v.leaseDuration <= 0 ||
		v.pollInterval <= 0 || v.rawRetention < 24*time.Hour ||
		v.dailyRetentionMonths <= 0 || v.dailyRetentionMonths > 120 {
		return errors.New("invalid retention worker operational bounds")
	}
	return nil
}
