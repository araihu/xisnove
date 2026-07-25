package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

const maxRenderedNotificationBytes = 64 << 10

// ErrNotificationLeaseLost means another worker owns or has finalized a delivery.
var ErrNotificationLeaseLost = errors.New("notification delivery lease lost")

// DeliveryWorkerConfig defines durable claim, send, and retry behavior.
type DeliveryWorkerConfig struct {
	Store           port.UnitOfWork
	Sealer          port.ConfigSealer
	Tokens          TokenIssuer
	NewID           func() string
	Owner           string
	Transports      map[domain.NotificationChannelKind]NotificationTransport
	BatchSize       int
	Concurrency     int
	LeaseDuration   time.Duration
	PollInterval    time.Duration
	SendTimeout     time.Duration
	MaxAttempts     uint32
	BackoffBase     time.Duration
	BackoffCap      time.Duration
	Jitter          func() float64
	OnError         func(error)
	ObserveDelivery func(DeliveryObservation)
}

// DeliveryWorker durably dispatches claimed notification outbox rows.
type DeliveryWorker struct {
	config   DeliveryWorkerConfig
	jitterMu sync.Mutex
}

// NewDeliveryWorker validates configuration and applies safe operational defaults.
func NewDeliveryWorker(config DeliveryWorkerConfig) (*DeliveryWorker, error) {
	if config.Store == nil || config.Sealer == nil || config.Tokens == nil || config.NewID == nil || strings.TrimSpace(config.Owner) == "" {
		return nil, errors.New("notification worker requires store, sealer, token issuer, identifier generator, and owner")
	}
	if config.BatchSize < 0 || config.Concurrency < 0 || config.LeaseDuration < 0 || config.PollInterval < 0 || config.SendTimeout < 0 || config.BackoffBase < 0 || config.BackoffCap < 0 {
		return nil, errors.New("notification worker durations and limits cannot be negative")
	}
	if config.BatchSize == 0 {
		config.BatchSize = 20
	}
	if config.Concurrency == 0 {
		config.Concurrency = 4
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 45 * time.Second
	}
	if config.PollInterval == 0 {
		config.PollInterval = time.Second
	}
	if config.SendTimeout == 0 {
		config.SendTimeout = 15 * time.Second
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 8
	}
	if config.BackoffBase == 0 {
		config.BackoffBase = 5 * time.Second
	}
	if config.BackoffCap == 0 {
		config.BackoffCap = 15 * time.Minute
	}
	if config.BatchSize > 1000 || config.Concurrency > 100 || config.LeaseDuration <= config.SendTimeout || config.BackoffCap < config.BackoffBase {
		return nil, errors.New("invalid notification worker operational bounds")
	}
	if config.Jitter == nil {
		config.Jitter = secureJitter
	}
	config.Transports = cloneNotificationTransports(config.Transports)
	return &DeliveryWorker{config: config}, nil
}

// Run polls until the context is canceled. Individual cycle errors are reported
// through OnError and do not stop later durable retries.
func (w *DeliveryWorker) Run(ctx context.Context) error {
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

// RunOnce claims and drains at most BatchSize due deliveries.
func (w *DeliveryWorker) RunOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	slots := make(chan struct{}, w.config.Concurrency)
	var group sync.WaitGroup
	var errorMu sync.Mutex
	var cycleErrors []error
	claimed := 0
	for claimed < w.config.BatchSize {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			group.Wait()
			return claimed, errors.Join(append(cycleErrors, ctx.Err())...)
		}
		record, tokenHash, err := w.claim(ctx)
		if err != nil {
			<-slots
			if errors.Is(err, port.ErrNotFound) {
				break
			}
			group.Wait()
			return claimed, errors.Join(append(cycleErrors, err)...)
		}
		claimed++
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-slots }()
			if err := w.deliver(ctx, record, tokenHash); err != nil {
				errorMu.Lock()
				cycleErrors = append(cycleErrors, err)
				errorMu.Unlock()
			}
		}()
	}
	group.Wait()
	return claimed, errors.Join(cycleErrors...)
}

func (w *DeliveryWorker) claim(ctx context.Context) (port.NotificationOutboxRecord, []byte, error) {
	token, err := w.config.Tokens.New()
	if err != nil {
		return port.NotificationOutboxRecord{}, nil, fmt.Errorf("create notification claim token: %w", err)
	}
	var record port.NotificationOutboxRecord
	err = w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		now, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for notification claim: %w", err)
		}
		record, err = repositories.NotificationOutbox.ClaimDue(ctx, port.ClaimNotificationParams{
			Owner: w.config.Owner, ClaimTokenHash: token.Hash,
			ClaimExpiresAt: now.Add(w.config.LeaseDuration), Now: now,
		})
		return err
	})
	if err != nil {
		return port.NotificationOutboxRecord{}, nil, err
	}
	return record, append([]byte(nil), token.Hash...), nil
}

func (w *DeliveryWorker) deliver(ctx context.Context, record port.NotificationOutboxRecord, tokenHash []byte) error {
	result := w.prepareAndSend(ctx, record)
	observation, err := w.finalize(ctx, record, tokenHash, result)
	if err == nil && w.config.ObserveDelivery != nil {
		w.config.ObserveDelivery(observation)
	}
	return err
}

func (w *DeliveryWorker) prepareAndSend(ctx context.Context, record port.NotificationOutboxRecord) TransportResult {
	var channel port.NotificationChannelRecord
	err := w.config.Store.View(ctx, func(ctx context.Context, repositories port.Repositories) error {
		var err error
		channel, err = repositories.NotificationChannels.Get(ctx, record.ChannelID)
		return err
	})
	if err != nil {
		return NewTransportResult(TransportTransientFailure, "channel_unavailable", err.Error(), "")
	}
	if !channel.Channel.Enabled {
		return NewTransportResult(TransportPermanentFailure, "channel_disabled", "notification channel is disabled", "")
	}
	if channel.Channel.Kind != record.RenderSnapshot.ChannelKind {
		return NewTransportResult(TransportPermanentFailure, "channel_kind_changed", "notification channel kind no longer matches its immutable snapshot", "")
	}
	transport := w.config.Transports[channel.Channel.Kind]
	if transport == nil {
		return NewTransportResult(TransportPermanentFailure, "transport_unavailable", "notification transport is not configured", "")
	}
	configuration, err := w.config.Sealer.Open(ctx, port.ConfigIdentity{
		ChannelID: channel.Channel.ID, Kind: channel.Channel.Kind,
	}, port.SealedConfig{KeyVersion: channel.KeyVersion, Ciphertext: channel.EncryptedConfig})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return workerContextResult(err)
		}
		return NewTransportResult(TransportPermanentFailure, "configuration_unavailable", err.Error(), "", string(channel.EncryptedConfig))
	}
	defer clear(configuration)
	message, err := renderNotification(record.RenderSnapshot)
	if err != nil {
		return NewTransportResult(TransportPermanentFailure, "template_invalid", err.Error(), "")
	}
	title := strings.TrimSpace(record.RenderSnapshot.MonitorName + " is " + string(record.RenderSnapshot.State))
	delivery := TransportDelivery{
		DeliveryID: record.ID, ChannelID: record.ChannelID,
		ChannelKind: channel.Channel.Kind, Configuration: configuration,
		Title: title, Message: message, Snapshot: record.RenderSnapshot.Clone(),
	}
	sendCtx, cancel := context.WithTimeout(ctx, w.config.SendTimeout)
	defer cancel()
	result := safeTransportSend(sendCtx, transport, delivery)
	return NewTransportResult(
		result.Outcome, result.ErrorClass, result.Diagnostic, result.ProviderReceipt,
		string(configuration), message, title,
	)
}

func (w *DeliveryWorker) finalize(ctx context.Context, record port.NotificationOutboxRecord, tokenHash []byte, result TransportResult) (DeliveryObservation, error) {
	observation := DeliveryObservation{
		AttemptOutcome:  DeliveryAttemptPermanentFailure,
		FinalOutcome:    DeliveryFinalPermanent,
		DiagnosticClass: deliveryDiagnosticClass(result.ErrorClass),
	}
	err := w.config.Store.Transact(ctx, func(ctx context.Context, repositories port.Repositories) error {
		finishedAt, err := repositories.Runs.DatabaseNow(ctx)
		if err != nil {
			return fmt.Errorf("read database time for notification finalize: %w", err)
		}
		ordinal := record.AttemptCount + 1
		attemptOutcome := port.NotificationAttemptPermanentFailure
		switch result.Outcome {
		case TransportDelivered:
			attemptOutcome = port.NotificationAttemptDelivered
			observation.AttemptOutcome = DeliveryAttemptDelivered
		case TransportTransientFailure:
			attemptOutcome = port.NotificationAttemptTransientFailure
			observation.AttemptOutcome = DeliveryAttemptTransientFailure
		}
		if err := repositories.NotificationOutbox.AppendAttempt(ctx, port.NotificationDeliveryAttemptRecord{
			ID: w.config.NewID(), OutboxID: record.ID, Ordinal: ordinal,
			StartedAt: record.UpdatedAt, FinishedAt: finishedAt,
			Outcome: attemptOutcome, ErrorClass: result.ErrorClass,
			Diagnostic: result.Diagnostic, ProviderReceipt: result.ProviderReceipt,
		}); err != nil {
			return err
		}
		params := port.FinalizeNotificationParams{
			ID: record.ID, ClaimTokenHash: tokenHash, At: finishedAt,
			ErrorClass: result.ErrorClass, Diagnostic: result.Diagnostic,
		}
		var changed bool
		switch result.Outcome {
		case TransportDelivered:
			changed, err = repositories.NotificationOutbox.MarkDelivered(ctx, params)
			observation.FinalOutcome = DeliveryFinalDelivered
		case TransportTransientFailure:
			if ordinal >= w.config.MaxAttempts {
				params.ErrorClass = "attempt_limit_exceeded"
				observation.DiagnosticClass = DeliveryDiagnosticPolicy
				changed, err = repositories.NotificationOutbox.MarkPermanentFailure(ctx, params)
			} else {
				observation.FinalOutcome = DeliveryFinalRetry
				params.AvailableAt, err = domain.NextNotificationRetry(
					finishedAt, ordinal, w.config.BackoffBase, w.config.BackoffCap, w.nextJitter(),
				)
				if err == nil {
					changed, err = repositories.NotificationOutbox.MarkRetrying(ctx, params)
				}
			}
		default:
			changed, err = repositories.NotificationOutbox.MarkPermanentFailure(ctx, params)
		}
		if err != nil {
			return err
		}
		if !changed {
			return ErrNotificationLeaseLost
		}
		return nil
	})
	return observation, err
}

func (w *DeliveryWorker) nextJitter() float64 {
	w.jitterMu.Lock()
	defer w.jitterMu.Unlock()
	value := w.config.Jitter()
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func renderNotification(snapshot domain.RenderSnapshot) (string, error) {
	if strings.TrimSpace(snapshot.Template) == "" {
		return fmt.Sprintf("%s transitioned from %s to %s", snapshot.MonitorName, snapshot.PreviousState, snapshot.State), nil
	}
	tmpl, err := template.New("notification").Option("missingkey=error").Parse(snapshot.Template)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, snapshot.Clone()); err != nil {
		return "", err
	}
	if output.Len() > maxRenderedNotificationBytes {
		return "", errors.New("rendered notification exceeds size limit")
	}
	return output.String(), nil
}

func safeTransportSend(ctx context.Context, transport NotificationTransport, delivery TransportDelivery) (result TransportResult) {
	defer func() {
		if recover() != nil {
			result = NewTransportResult(TransportTransientFailure, "transport_panic", "notification transport panicked", "")
		}
	}()
	return transport.Send(ctx, delivery)
}

func workerContextResult(err error) TransportResult {
	class := "context_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline_exceeded"
	}
	return NewTransportResult(TransportTransientFailure, class, err.Error(), "")
}

func cloneNotificationTransports(source map[domain.NotificationChannelKind]NotificationTransport) map[domain.NotificationChannelKind]NotificationTransport {
	result := make(map[domain.NotificationChannelKind]NotificationTransport, len(source))
	for kind, transport := range source {
		result[kind] = transport
	}
	return result
}

func secureJitter() float64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0
	}
	return float64(binary.BigEndian.Uint64(raw[:])>>11) / float64(uint64(1)<<53)
}
