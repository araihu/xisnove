package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

type HTTPExecutor struct {
	policy Policy
}

func NewHTTPExecutor(policy Policy) *HTTPExecutor {
	return &HTTPExecutor{policy: policy.withDefaults()}
}

func (e *HTTPExecutor) Execute(
	ctx context.Context,
	work controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	startedAt := time.Now().UTC()
	result := controlplane.ProbeResultInput{
		ResultId:       uuid.New(),
		RunId:          work.RunId,
		LeaseToken:     work.LeaseToken,
		StartedAt:      startedAt,
		Outcome:        controlplane.Failed,
		ErrorCode:      controlplane.ProtocolError,
		ObservedStatus: 0,
	}
	finish := func() controlplane.ProbeResultInput {
		result.FinishedAt = time.Now().UTC()
		result.LatencyMillis = max(0, result.FinishedAt.Sub(startedAt).Milliseconds())
		return result
	}

	if work.TimeoutMillis <= 0 {
		result.DiagnosticSample = "invalid timeout"
		return finish()
	}
	kind, err := work.Probe.Discriminator()
	if err != nil || kind != "http" {
		result.DiagnosticSample = "invalid HTTP probe"
		return finish()
	}
	probe, err := work.Probe.AsHTTPProbeDefinition()
	if err != nil {
		result.DiagnosticSample = "invalid HTTP probe"
		return finish()
	}
	target, err := url.Parse(probe.Url)
	if err != nil || validateURL(target) != nil {
		result.ErrorCode = controlplane.TargetDenied
		result.DiagnosticSample = "target URL denied"
		return finish()
	}

	timeoutCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(work.TimeoutMillis)*time.Millisecond,
	)
	defer cancel()
	request, err := http.NewRequestWithContext(
		timeoutCtx,
		string(probe.Method),
		target.String(),
		bytes.NewReader(probe.Body),
	)
	if err != nil {
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	for name, value := range probe.Headers {
		request.Header.Set(name, value)
	}

	transport := e.transport()
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if !probe.FollowRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) > e.policy.MaxRedirects {
				return errors.New("redirect limit exceeded")
			}
			if err := validateURL(request.URL); err != nil {
				return err
			}
			if literal, parseErr := netipFromHost(request.URL.Hostname()); parseErr == nil {
				return e.policy.validateAddress(literal)
			}
			return nil
		},
	}

	response, err := client.Do(request)
	if err != nil {
		result.ErrorCode = classifyNetworkError(err)
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	defer response.Body.Close()
	result.ObservedStatus = int32(response.StatusCode)

	body, err := io.ReadAll(io.LimitReader(response.Body, e.policy.MaxResponseBytes+1))
	if err != nil {
		result.ErrorCode = controlplane.ProtocolError
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	if int64(len(body)) > e.policy.MaxResponseBytes {
		result.ErrorCode = controlplane.ResponseTooLarge
		result.DiagnosticSample = diagnostic(string(body[:e.policy.MaxResponseBytes]))
		return finish()
	}

	statusPassed := statusMatches(response.StatusCode, probe.ExpectedStatus)
	bodyPassed := bodyMatches(body, probe.BodyContains, probe.BodyDoesNotContain)
	result.BodyAssertionPassed = bodyPassed
	switch {
	case !statusPassed:
		result.ErrorCode = controlplane.StatusMismatch
		result.DiagnosticSample = diagnostic(string(body))
	case !bodyPassed:
		result.ErrorCode = controlplane.BodyMismatch
		result.DiagnosticSample = diagnostic(string(body))
	default:
		result.Outcome = controlplane.Passed
		result.ErrorCode = controlplane.Empty
	}
	return finish()
}

func statusMatches(status int, expected []controlplane.StatusRange) bool {
	for _, statusRange := range expected {
		if status >= int(statusRange.Minimum) && status <= int(statusRange.Maximum) {
			return true
		}
	}
	return false
}

func bodyMatches(body []byte, contains []string, excludes []string) bool {
	value := string(body)
	for _, expected := range contains {
		if !strings.Contains(value, expected) {
			return false
		}
	}
	for _, excluded := range excludes {
		if strings.Contains(value, excluded) {
			return false
		}
	}
	return true
}

func (e *HTTPExecutor) transport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	return &http.Transport{
		Proxy:               nil,
		DisableKeepAlives:   true,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid dial address", ErrTargetDenied)
			}
			addresses, err := e.policy.resolveHost(ctx, host)
			if err != nil {
				return nil, err
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, err := dialer.DialContext(
					ctx,
					network,
					net.JoinHostPort(resolved.String(), port),
				)
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}

func classifyNetworkError(err error) controlplane.ProbeResultInputErrorCode {
	switch {
	case errors.Is(err, ErrTargetDenied):
		return controlplane.TargetDenied
	case errors.Is(err, ErrDNS):
		return controlplane.DnsError
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return controlplane.Timeout
	}
	var certificateInvalid x509.CertificateInvalidError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var recordHeader tls.RecordHeaderError
	if errors.As(err, &certificateInvalid) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &recordHeader) {
		return controlplane.TlsError
	}
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		if networkError.Timeout() {
			return controlplane.Timeout
		}
		return controlplane.ConnectError
	}
	return controlplane.ProtocolError
}

func diagnostic(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	var builder strings.Builder
	builder.Grow(min(len(value), 512))
	for _, r := range value {
		if unicode.IsControl(r) {
			r = ' '
		}
		size := utf8.RuneLen(r)
		if builder.Len()+size > 512 {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func netipFromHost(host string) (netip.Addr, error) {
	address, err := netip.ParseAddr(strings.TrimSuffix(host, "."))
	if err != nil {
		return netip.Addr{}, err
	}
	return address.Unmap(), nil
}
