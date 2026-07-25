package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

const maxTCPPayloadBytes = 4 << 10

type TCPExecutor struct {
	policy    Policy
	tlsConfig *tls.Config
}

func NewTCPExecutor(policy Policy) *TCPExecutor {
	return &TCPExecutor{policy: policy.withDefaults()}
}

func (e *TCPExecutor) WithTLSConfig(config *tls.Config) *TCPExecutor {
	if config == nil {
		e.tlsConfig = nil
		return e
	}
	e.tlsConfig = config.Clone()
	return e
}

func (e *TCPExecutor) Execute(
	ctx context.Context,
	work controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	startedAt := time.Now().UTC()
	result := controlplane.ProbeResultInput{
		ResultId: uuid.New(), RunId: work.RunId, LeaseToken: work.LeaseToken,
		StartedAt: startedAt, Outcome: controlplane.Failed,
		ErrorCode: controlplane.ProtocolError,
	}
	timings := controlplane.ProtocolTimings{}
	result.ProtocolTimings = &timings
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
	if err != nil || kind != "tcp" {
		result.DiagnosticSample = "invalid TCP probe"
		return finish()
	}
	definition, err := work.Probe.AsTCPProbeDefinition()
	if err != nil ||
		definition.Host == "" ||
		definition.Port <= 0 ||
		definition.Port > 65535 ||
		len(definition.Send) > maxTCPPayloadBytes ||
		len(definition.Expect) > maxTCPPayloadBytes {
		result.DiagnosticSample = "invalid TCP probe"
		return finish()
	}

	timeoutCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(work.TimeoutMillis)*time.Millisecond,
	)
	defer cancel()
	host := strings.TrimSuffix(definition.Host, ".")
	addresses, err := e.policy.resolveHost(timeoutCtx, host)
	if err != nil {
		result.ErrorCode = classifyNetworkError(err)
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}

	connectStarted := time.Now()
	connection, err := dialValidated(
		timeoutCtx,
		addresses,
		uint16(definition.Port),
	)
	connectMillis := max(int64(0), time.Since(connectStarted).Milliseconds())
	timings.ConnectMillis = &connectMillis
	if err != nil {
		result.ErrorCode = classifyNetworkError(err)
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	defer connection.Close()
	if deadline, ok := timeoutCtx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			result.DiagnosticSample = diagnostic(err.Error())
			return finish()
		}
	}

	if definition.TlsMinimumRemainingSeconds != nil {
		tlsStarted := time.Now()
		tlsConnection := tls.Client(connection, e.clientTLSConfig(host))
		if err := tlsConnection.HandshakeContext(timeoutCtx); err != nil {
			result.ErrorCode = classifyNetworkError(err)
			result.DiagnosticSample = diagnostic(err.Error())
			return finish()
		}
		tlsMillis := max(int64(0), time.Since(tlsStarted).Milliseconds())
		timings.TlsMillis = &tlsMillis
		connection = tlsConnection
		state := tlsConnection.ConnectionState()
		if len(state.PeerCertificates) == 0 {
			result.ErrorCode = controlplane.TlsError
			result.DiagnosticSample = "peer certificate is missing"
			return finish()
		}
		notAfter := state.PeerCertificates[0].NotAfter.UTC()
		result.TlsNotAfter = &notAfter
		minimumRemaining := time.Duration(*definition.TlsMinimumRemainingSeconds) * time.Second
		if time.Until(notAfter) < minimumRemaining {
			result.ErrorCode = controlplane.TlsExpiring
			result.DiagnosticSample = "peer certificate expires too soon"
			return finish()
		}
	}

	if len(definition.Send) != 0 {
		if _, err := connection.Write(definition.Send); err != nil {
			result.ErrorCode = tcpIOErrorCode(err)
			result.DiagnosticSample = diagnostic(err.Error())
			return finish()
		}
	}
	if len(definition.Expect) != 0 {
		observed, err := readUntilExpected(connection, definition.Expect)
		if err != nil && !errors.Is(err, io.EOF) {
			result.ErrorCode = tcpIOErrorCode(err)
			result.DiagnosticSample = diagnostic(err.Error())
			return finish()
		}
		if !bytes.Contains(observed, definition.Expect) {
			result.ErrorCode = controlplane.TcpExpectMismatch
			result.DiagnosticSample = diagnostic(string(observed))
			return finish()
		}
	}

	result.Outcome = controlplane.Passed
	result.ErrorCode = controlplane.Empty
	return finish()
}

func dialValidated(
	ctx context.Context,
	addresses []netip.Addr,
	port uint16,
) (net.Conn, error) {
	dialer := &net.Dialer{KeepAlive: -1}
	var lastErr error
	for _, address := range addresses {
		connection, err := dialer.DialContext(
			ctx,
			"tcp",
			net.JoinHostPort(address.String(), strconv.Itoa(int(port))),
		)
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no validated target addresses")
	}
	return nil, fmt.Errorf("dial target: %w", lastErr)
}

func (e *TCPExecutor) clientTLSConfig(serverName string) *tls.Config {
	var config *tls.Config
	if e.tlsConfig == nil {
		config = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		config = e.tlsConfig.Clone()
		if config.MinVersion == 0 {
			config.MinVersion = tls.VersionTLS12
		}
	}
	config.ServerName = serverName
	return config
}

func readUntilExpected(connection net.Conn, expected []byte) ([]byte, error) {
	observed := make([]byte, 0, maxTCPPayloadBytes)
	buffer := make([]byte, 512)
	for len(observed) < maxTCPPayloadBytes {
		remaining := maxTCPPayloadBytes - len(observed)
		count, err := connection.Read(buffer[:min(len(buffer), remaining)])
		if count != 0 {
			observed = append(observed, buffer[:count]...)
			if bytes.Contains(observed, expected) {
				return observed, nil
			}
		}
		if err != nil {
			return observed, err
		}
	}
	return observed, nil
}

func tcpIOErrorCode(err error) controlplane.ProbeResultInputErrorCode {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return controlplane.Timeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return controlplane.Timeout
	}
	return controlplane.ProtocolError
}
