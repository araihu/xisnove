package main

import (
	"errors"
	"flag"
	"log/slog"
	"strings"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	alertmanageradapter "github.com/araihu/xisnove/internal/adapters/alertmanager"
	"github.com/araihu/xisnove/internal/adapters/egress"
	shoutrrradapter "github.com/araihu/xisnove/internal/adapters/shoutrrr"
)

type notificationWorkerFlagValues struct {
	batchSize     int
	concurrency   int
	leaseDuration time.Duration
	pollInterval  time.Duration
	sendTimeout   time.Duration
	maxAttempts   uint
	backoffBase   time.Duration
	backoffCap    time.Duration
	allowedCIDRs  string
	deniedCIDRs   string
}

func addNotificationWorkerFlags(flags *flag.FlagSet) *notificationWorkerFlagValues {
	values := &notificationWorkerFlagValues{}
	flags.IntVar(&values.batchSize, "notification-batch-size", 20, "maximum deliveries claimed per notification cycle")
	flags.IntVar(&values.concurrency, "notification-concurrency", 4, "maximum concurrent notification sends")
	flags.DurationVar(&values.leaseDuration, "notification-lease", 45*time.Second, "notification claim lease duration")
	flags.DurationVar(&values.pollInterval, "notification-poll", time.Second, "notification outbox poll interval")
	flags.DurationVar(&values.sendTimeout, "notification-send-timeout", 15*time.Second, "deadline for one notification send")
	flags.UintVar(&values.maxAttempts, "notification-max-attempts", 8, "maximum automatic notification attempts")
	flags.DurationVar(&values.backoffBase, "notification-backoff-base", 5*time.Second, "initial notification retry backoff")
	flags.DurationVar(&values.backoffCap, "notification-backoff-cap", 15*time.Minute, "maximum notification retry backoff")
	flags.StringVar(&values.allowedCIDRs, "notification-egress-allow-cidrs", "", "comma-separated private CIDRs explicitly allowed for notification egress")
	flags.StringVar(&values.deniedCIDRs, "notification-egress-deny-cidrs", "", "comma-separated CIDRs always denied for notification egress")
	return values
}

func (v *notificationWorkerFlagValues) build(
	store port.UnitOfWork,
	sealer port.ConfigSealer,
	tokens application.TokenIssuer,
	newID func() string,
	owner string,
) (*application.DeliveryWorker, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	if sealer == nil {
		return nil, nil
	}
	policy, err := egress.NewPolicy(egress.Config{
		AllowedCIDRs: splitCIDRs(v.allowedCIDRs),
		DeniedCIDRs:  splitCIDRs(v.deniedCIDRs),
	})
	if err != nil {
		return nil, err
	}
	client := policy.HTTPClient(v.sendTimeout)
	shoutrrrTransport, err := shoutrrradapter.NewTransport(shoutrrradapter.TransportConfig{
		HTTPClient: client, Timeout: v.sendTimeout, MaxParallel: v.concurrency,
	})
	if err != nil {
		return nil, err
	}
	alertmanagerTransport, err := alertmanageradapter.NewTransport(alertmanageradapter.TransportConfig{
		HTTPClient: client, Timeout: v.sendTimeout, MaxParallel: v.concurrency,
	})
	if err != nil {
		return nil, err
	}
	return application.NewDeliveryWorker(application.DeliveryWorkerConfig{
		Store: store, Sealer: sealer, Tokens: tokens, NewID: newID, Owner: owner,
		Transports: map[domain.NotificationChannelKind]application.NotificationTransport{
			domain.NotificationChannelShoutrrr:     shoutrrrTransport,
			domain.NotificationChannelAlertmanager: alertmanagerTransport,
		},
		BatchSize: v.batchSize, Concurrency: v.concurrency,
		LeaseDuration: v.leaseDuration, PollInterval: v.pollInterval,
		SendTimeout: v.sendTimeout, MaxAttempts: uint32(v.maxAttempts),
		BackoffBase: v.backoffBase, BackoffCap: v.backoffCap,
		OnError: func(err error) {
			slog.Error("notification delivery cycle failed", "error_class", "delivery_cycle", "error", err)
		},
	})
}

func (v *notificationWorkerFlagValues) validate() error {
	if v.maxAttempts == 0 || uint64(v.maxAttempts) > uint64(^uint32(0)) {
		return errors.New("--notification-max-attempts must fit a positive uint32")
	}
	if v.batchSize <= 0 || v.batchSize > 1000 || v.concurrency <= 0 || v.concurrency > 100 ||
		v.leaseDuration <= v.sendTimeout || v.pollInterval <= 0 || v.sendTimeout <= 0 ||
		v.backoffBase <= 0 || v.backoffCap < v.backoffBase {
		return errors.New("invalid notification worker operational bounds")
	}
	return nil
}

func splitCIDRs(value string) []string {
	var result []string
	for _, candidate := range strings.Split(value, ",") {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			result = append(result, candidate)
		}
	}
	return result
}
