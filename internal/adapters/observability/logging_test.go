package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestJSONLoggerAddsContextAndRedactsRecursively(t *testing.T) {
	var output bytes.Buffer
	const secret = "provider-secret-value"
	logger := NewJSONLogger(&output, LogConfig{SensitiveValues: []string{secret}})
	ctx := ContextWithIDs(context.Background(), IDs{Correlation: "request-1", Monitor: "monitor-1", Delivery: "delivery-1"})
	logger.InfoContext(ctx, "attempt", "authorization", "Bearer "+secret, "error", "request failed: "+secret, "nested", slog.GroupValue(slog.String("provider_diagnostic", "unsafe")))
	line := output.String()
	for _, wanted := range []string{`"correlation_id":"request-1"`, `"monitor_id":"monitor-1"`, `"delivery_id":"delivery-1"`, `"authorization":"<redacted>"`, `"provider_diagnostic":"<redacted>"`} {
		if !strings.Contains(line, wanted) {
			t.Errorf("log missing %s: %s", wanted, line)
		}
	}
	if strings.Contains(line, secret) {
		t.Fatal("configured sensitive value leaked")
	}
}

func TestJSONLoggerIsConcurrentSafe(t *testing.T) {
	var output lockedBuffer
	logger := NewJSONLogger(&output, LogConfig{SensitiveValues: []string{"secret"}})
	var workers sync.WaitGroup
	for i := range 20 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			logger.InfoContext(ContextWithIDs(context.Background(), IDs{Run: "run"}), "event", "ordinal", i, "token", "secret")
		}()
	}
	workers.Wait()
	if strings.Contains(output.String(), `"token":"secret"`) {
		t.Fatal("secret leaked")
	}
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
