package observability

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const Redacted = "<redacted>"

// IDs contains correlation identifiers that are safe to emit. Callers must
// never place names, URLs, secrets, or provider diagnostics in these fields.
type IDs struct {
	Correlation string
	Run         string
	Monitor     string
	Location    string
	Agent       string
	Incident    string
	Delivery    string
}

type idsKey struct{}

func ContextWithIDs(ctx context.Context, ids IDs) context.Context {
	return context.WithValue(ctx, idsKey{}, ids)
}

func IDsFromContext(ctx context.Context) IDs {
	ids, _ := ctx.Value(idsKey{}).(IDs)
	return ids
}

type LogConfig struct {
	Level           slog.Leveler
	SensitiveValues []string
}

func NewJSONLogger(output io.Writer, config LogConfig) *slog.Logger {
	options := &slog.HandlerOptions{Level: config.Level}
	handler := slog.NewJSONHandler(output, options)
	return slog.New(NewContextHandler(NewRedactingHandler(handler, config.SensitiveValues)))
}

type contextHandler struct{ next slog.Handler }

func NewContextHandler(next slog.Handler) slog.Handler { return &contextHandler{next: next} }

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{next: h.next.WithAttrs(attrs)}
}
func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{next: h.next.WithGroup(name)}
}
func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	ids := IDsFromContext(ctx)
	appendID(&record, "correlation_id", ids.Correlation)
	appendID(&record, "run_id", ids.Run)
	appendID(&record, "monitor_id", ids.Monitor)
	appendID(&record, "location_id", ids.Location)
	appendID(&record, "agent_id", ids.Agent)
	appendID(&record, "incident_id", ids.Incident)
	appendID(&record, "delivery_id", ids.Delivery)
	span := trace.SpanContextFromContext(ctx)
	if span.IsValid() {
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return h.next.Handle(ctx, record)
}

func appendID(record *slog.Record, key, value string) {
	if value != "" {
		record.AddAttrs(slog.String(key, value))
	}
}

type redactingHandler struct {
	next      slog.Handler
	sensitive []string
}

func NewRedactingHandler(next slog.Handler, sensitive []string) slog.Handler {
	values := make([]string, 0, len(sensitive))
	for _, value := range sensitive {
		if value != "" {
			values = append(values, value)
		}
	}
	return &redactingHandler{next: next, sensitive: values}
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clone := slog.NewRecord(record.Time, record.Level, h.redactString(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool { clone.AddAttrs(h.redactAttr(attr)); return true })
	return h.next.Handle(ctx, clone)
}
func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		clean[i] = h.redactAttr(attr)
	}
	return &redactingHandler{next: h.next.WithAttrs(clean), sensitive: h.sensitive}
}
func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name), sensitive: h.sensitive}
}

func (h *redactingHandler) redactAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		for i := range group {
			group[i] = h.redactAttr(group[i])
		}
		return slog.Group(attr.Key, attrsToAny(group)...)
	}
	if attr.Value.Kind() == slog.KindString {
		return slog.String(attr.Key, h.redactString(attr.Value.String()))
	}
	if attr.Value.Kind() == slog.KindAny {
		switch value := attr.Value.Any().(type) {
		case error:
			return slog.String(attr.Key, h.redactString(value.Error()))
		case string:
			return slog.String(attr.Key, h.redactString(value))
		}
	}
	return attr
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for i := range attrs {
		values[i] = attrs[i]
	}
	return values
}

func (h *redactingHandler) redactString(value string) string {
	for _, secret := range h.sensitive {
		value = strings.ReplaceAll(value, secret, Redacted)
	}
	return value
}

func sensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{"authorization", "cookie", "password", "passwd", "token", "secret", "credential", "private_key", "encrypted", "envelope", "diagnostic"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
