package shoutrrr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"

	"github.com/araihu/xisnove/application"
)

// TransportConfig configures a bounded Shoutrrr transport.
type TransportConfig struct {
	HTTPClient  *http.Client
	Timeout     time.Duration
	MaxParallel int
}

// Transport sends one notification through the reviewed Shoutrrr subset.
type Transport struct {
	client  *http.Client
	timeout time.Duration
	slots   chan struct{}
}

// NewTransport creates a transport that uses the caller-provided HTTP client.
func NewTransport(config TransportConfig) (*Transport, error) {
	if config.HTTPClient == nil || config.Timeout <= 0 || config.MaxParallel <= 0 {
		return nil, errors.New("invalid Shoutrrr transport configuration")
	}
	return &Transport{client: config.HTTPClient, timeout: config.Timeout, slots: make(chan struct{}, config.MaxParallel)}, nil
}

// Send performs one call-local delivery without retaining decrypted configuration.
func (t *Transport) Send(ctx context.Context, delivery application.TransportDelivery) application.TransportResult {
	select {
	case t.slots <- struct{}{}:
		defer func() { <-t.slots }()
	case <-ctx.Done():
		return contextResult(ctx.Err(), delivery)
	}

	var config struct {
		ServiceURL string `json:"serviceUrl"`
	}
	decoder := json.NewDecoder(bytes.NewReader(delivery.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil || strings.TrimSpace(config.ServiceURL) == "" {
		return application.NewTransportResult(application.TransportPermanentFailure, "configuration_invalid", "invalid Shoutrrr configuration", "")
	}
	parsed, err := url.Parse(config.ServiceURL)
	if err != nil || !SchemeReviewed(strings.ToLower(parsed.Scheme)) {
		return application.NewTransportResult(application.TransportPermanentFailure, "scheme_not_reviewed", "Shoutrrr scheme does not support the reviewed HTTP boundary", "", config.ServiceURL)
	}

	sendCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	client := &attemptHTTPClient{ctx: sendCtx, next: t.client}
	sender, err := shoutrrr.CreateSenderWithOptions(types.SenderOptions{HTTPClient: client, Timeout: t.timeout}, config.ServiceURL)
	if err != nil {
		return application.NewTransportResult(application.TransportPermanentFailure, "configuration_invalid", err.Error(), "", config.ServiceURL, delivery.Message, delivery.Title)
	}
	params := types.Params{types.TitleKey: delivery.Title}
	completed := make(chan []error, 1)
	go func() { completed <- sender.Send(delivery.Message, &params) }()
	select {
	case <-sendCtx.Done():
		return contextResult(sendCtx.Err(), delivery, config.ServiceURL)
	case errs := <-completed:
		for _, sendErr := range errs {
			if sendErr != nil {
				return classifyProviderError(sendErr, client.Status(), delivery, config.ServiceURL)
			}
		}
		return application.NewTransportResult(application.TransportDelivered, "", "", "")
	}
}

func classifyProviderError(err error, status int, delivery application.TransportDelivery, sensitive ...string) application.TransportResult {
	sensitive = append(sensitive, delivery.Message, delivery.Title)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, router.ErrServiceTimeout) {
		return application.NewTransportResult(application.TransportTransientFailure, "deadline_exceeded", err.Error(), "", sensitive...)
	}
	if errors.Is(err, context.Canceled) {
		return application.NewTransportResult(application.TransportTransientFailure, "context_canceled", err.Error(), "", sensitive...)
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= 500 {
		return application.NewTransportResult(application.TransportTransientFailure, "provider_retryable", err.Error(), "", sensitive...)
	}
	if status >= 400 {
		return application.NewTransportResult(application.TransportPermanentFailure, "provider_rejected", err.Error(), "", sensitive...)
	}
	return application.NewTransportResult(application.TransportTransientFailure, "transport_error", err.Error(), "", sensitive...)
}

func contextResult(err error, delivery application.TransportDelivery, sensitive ...string) application.TransportResult {
	class := "context_canceled"
	if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline_exceeded"
	}
	sensitive = append(sensitive, delivery.Message, delivery.Title)
	return application.NewTransportResult(application.TransportTransientFailure, class, err.Error(), "", sensitive...)
}

type attemptHTTPClient struct {
	ctx    context.Context
	next   *http.Client
	mu     sync.Mutex
	status int
}

func (c *attemptHTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.next.Do(request.Clone(c.ctx))
	if response != nil {
		c.mu.Lock()
		c.status = response.StatusCode
		c.mu.Unlock()
	}
	return response, err
}

func (c *attemptHTTPClient) Status() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

var _ application.NotificationTransport = (*Transport)(nil)
