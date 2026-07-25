// Package alertmanager implements semantic Alertmanager v2 notification delivery.
package alertmanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
)

const maxProviderResponseBytes = 4 << 10

// TransportConfig configures bounded Alertmanager HTTP delivery.
type TransportConfig struct {
	HTTPClient  *http.Client
	Timeout     time.Duration
	MaxParallel int
}

// Transport posts semantic alerts through an injected, egress-controlled client.
type Transport struct {
	client  *http.Client
	timeout time.Duration
	slots   chan struct{}
}

// NewTransport creates an Alertmanager transport.
func NewTransport(config TransportConfig) (*Transport, error) {
	if config.HTTPClient == nil || config.Timeout <= 0 || config.MaxParallel <= 0 {
		return nil, errors.New("invalid Alertmanager transport configuration")
	}
	return &Transport{
		client: config.HTTPClient, timeout: config.Timeout,
		slots: make(chan struct{}, config.MaxParallel),
	}, nil
}

type channelConfig struct {
	Endpoint    string `json:"endpoint"`
	BearerToken string `json:"bearerToken,omitempty"`
}

type postableAlert struct {
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
}

// Send performs one call-local delivery without retaining decrypted configuration.
func (t *Transport) Send(ctx context.Context, delivery application.TransportDelivery) application.TransportResult {
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return contextResult(ctx.Err(), delivery)
	}

	config, endpoint, err := decodeConfig(delivery.Configuration)
	if err != nil {
		return application.NewTransportResult(application.TransportPermanentFailure, "configuration_invalid", err.Error(), "", string(delivery.Configuration))
	}
	alert, err := newAlert(delivery)
	if err != nil {
		return application.NewTransportResult(application.TransportPermanentFailure, "delivery_invalid", err.Error(), "", delivery.Message, delivery.Title)
	}
	payload, err := json.Marshal([]postableAlert{alert})
	if err != nil {
		return application.NewTransportResult(application.TransportPermanentFailure, "delivery_invalid", err.Error(), "", delivery.Message, delivery.Title)
	}

	sendCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(sendCtx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return application.NewTransportResult(application.TransportPermanentFailure, "configuration_invalid", err.Error(), "", config.Endpoint, config.BearerToken)
	}
	request.Header.Set("Content-Type", "application/json")
	if config.BearerToken != "" {
		request.Header.Set("Authorization", "Bearer "+config.BearerToken)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return classifyError(err, delivery, config)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
	if readErr != nil {
		return application.NewTransportResult(application.TransportTransientFailure, "transport_error", readErr.Error(), "", config.Endpoint, config.BearerToken, delivery.Message, delivery.Title)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return application.NewTransportResult(application.TransportDelivered, "", "", response.Header.Get("X-Request-ID"), config.Endpoint, config.BearerToken)
	}
	class := "provider_rejected"
	outcome := application.TransportPermanentFailure
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		class = "provider_retryable"
		outcome = application.TransportTransientFailure
	}
	diagnostic := fmt.Sprintf("Alertmanager returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	return application.NewTransportResult(outcome, class, diagnostic, "", config.Endpoint, config.BearerToken, delivery.Message, delivery.Title)
}

func decodeConfig(raw []byte) (channelConfig, *url.URL, error) {
	var config channelConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return channelConfig{}, nil, errors.New("invalid Alertmanager configuration")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return channelConfig{}, nil, errors.New("invalid Alertmanager configuration")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil {
		return channelConfig{}, nil, errors.New("Alertmanager endpoint must be an absolute HTTP URL without user info")
	}
	endpoint.Fragment = ""
	path := strings.TrimRight(endpoint.Path, "/")
	if !strings.HasSuffix(path, "/api/v2/alerts") {
		path += "/api/v2/alerts"
	}
	endpoint.Path = path
	return config, endpoint, nil
}

func newAlert(delivery application.TransportDelivery) (postableAlert, error) {
	snapshot := delivery.Snapshot
	if snapshot.IncidentID == "" || snapshot.MonitorID == "" || snapshot.OccurredAt.IsZero() {
		return postableAlert{}, errors.New("Alertmanager delivery requires incident, monitor, and occurrence time")
	}
	digest := sha256.Sum256([]byte(string(snapshot.IncidentID) + "\x00" + string(snapshot.MonitorID)))
	fingerprint := hex.EncodeToString(digest[:])
	alert := postableAlert{
		Labels: map[string]string{
			"alertname":           "XisnoveMonitorHealth",
			"xisnove_fingerprint": fingerprint,
			"xisnove_incident_id": string(snapshot.IncidentID),
			"xisnove_monitor_id":  string(snapshot.MonitorID),
		},
		Annotations: map[string]string{
			"summary":             delivery.Title,
			"description":         delivery.Message,
			"xisnove_action":      string(snapshot.Action),
			"xisnove_delivery_id": string(delivery.DeliveryID),
			"xisnove_severity":    string(snapshot.Severity),
			"xisnove_state":       string(snapshot.State),
		},
		StartsAt: snapshot.OccurredAt.UTC().Format(time.RFC3339Nano),
	}
	if snapshot.Action == domain.NotificationRecover {
		alert.EndsAt = snapshot.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	return alert, nil
}

func classifyError(err error, delivery application.TransportDelivery, config channelConfig) application.TransportResult {
	class := "transport_error"
	if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline_exceeded"
	} else if errors.Is(err, context.Canceled) {
		class = "context_canceled"
	}
	return application.NewTransportResult(application.TransportTransientFailure, class, err.Error(), "", config.Endpoint, config.BearerToken, delivery.Message, delivery.Title)
}

func contextResult(err error, delivery application.TransportDelivery) application.TransportResult {
	class := "context_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline_exceeded"
	}
	return application.NewTransportResult(application.TransportTransientFailure, class, err.Error(), "", delivery.Message, delivery.Title)
}

var _ application.NotificationTransport = (*Transport)(nil)
