package observability

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Label values are deliberately closed sets. Unknown values are folded into
// "other" so operational data can never create an unbounded number of series.
type (
	MonitorState       string
	ProbeOutcome       string
	CycleOutcome       string
	LeaseState         string
	DuplicateKind      string
	OutboxState        string
	AttemptOutcome     string
	DiagnosticClass    string
	PoolResource       string
	PoolState          string
	TransactionKind    string
	TransactionOutcome string
	MigrationOutcome   string
)

const (
	MonitorUnknown MonitorState = "unknown"
	MonitorPassing MonitorState = "passing"
	MonitorFailing MonitorState = "failing"

	ProbeSuccess ProbeOutcome = "success"
	ProbeFailure ProbeOutcome = "failure"
	ProbeTimeout ProbeOutcome = "timeout"

	CycleSuccess CycleOutcome = "success"
	CycleFailure CycleOutcome = "failure"

	LeaseAvailable LeaseState = "available"
	LeaseClaimed   LeaseState = "claimed"
	LeaseExpired   LeaseState = "expired"

	DuplicateRun    DuplicateKind = "run"
	DuplicateResult DuplicateKind = "result"

	OutboxPending    OutboxState = "pending"
	OutboxClaimed    OutboxState = "claimed"
	OutboxRetry      OutboxState = "retry"
	OutboxDelivered  OutboxState = "delivered"
	OutboxPermanent  OutboxState = "permanent"
	OutboxSuppressed OutboxState = "suppressed"

	AttemptDelivered AttemptOutcome = "delivered"
	AttemptRetry     AttemptOutcome = "retry"
	AttemptPermanent AttemptOutcome = "permanent"
	AttemptCanceled  AttemptOutcome = "canceled"

	DiagnosticNone      DiagnosticClass = "none"
	DiagnosticTimeout   DiagnosticClass = "timeout"
	DiagnosticTransport DiagnosticClass = "transport"
	DiagnosticProvider  DiagnosticClass = "provider"
	DiagnosticPolicy    DiagnosticClass = "policy"
	DiagnosticInternal  DiagnosticClass = "internal"

	PoolDatabase PoolResource = "database"
	PoolHTTP     PoolResource = "http"
	PoolIdle     PoolState    = "idle"
	PoolInUse    PoolState    = "in_use"
	PoolWaiting  PoolState    = "waiting"

	TransactionView     TransactionKind    = "view"
	TransactionTransact TransactionKind    = "transact"
	TransactionCommit   TransactionOutcome = "commit"
	TransactionRollback TransactionOutcome = "rollback"
	TransactionError    TransactionOutcome = "error"

	MigrationSuccess      MigrationOutcome = "success"
	MigrationFailure      MigrationOutcome = "failure"
	MigrationIncompatible MigrationOutcome = "incompatible"
)

type Metrics struct {
	registry *prometheus.Registry

	monitorState        *prometheus.GaugeVec
	transitions         *prometheus.CounterVec
	probeDuration       *prometheus.HistogramVec
	schedulerCycles     *prometheus.CounterVec
	leases              *prometheus.GaugeVec
	leaseEvents         *prometheus.CounterVec
	heartbeatAge        *prometheus.GaugeVec
	duplicates          *prometheus.CounterVec
	outboxOldestAge     *prometheus.GaugeVec
	attempts            *prometheus.CounterVec
	poolConnections     *prometheus.GaugeVec
	transactionDuration *prometheus.HistogramVec
	migrationRuns       *prometheus.CounterVec
	schemaVersion       *prometheus.GaugeVec
}

func NewMetrics() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	m.monitorState = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_monitor_state", Help: "Number of monitor-location projections by bounded state."}, []string{"state"})
	m.transitions = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_monitor_transitions_total", Help: "Monitor state transitions."}, []string{"from", "to"})
	m.probeDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "xisnove_probe_duration_seconds", Help: "Probe duration by bounded outcome.", Buckets: prometheus.DefBuckets}, []string{"outcome"})
	m.schedulerCycles = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_scheduler_cycles_total", Help: "Scheduler cycles by outcome."}, []string{"outcome"})
	m.leases = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_leases", Help: "Lease count by state."}, []string{"state"})
	m.leaseEvents = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_lease_events_total", Help: "Lease terminal events by bounded outcome."}, []string{"outcome"})
	m.heartbeatAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_agent_heartbeat_age_seconds", Help: "Age of the oldest current agent heartbeat."}, nil)
	m.duplicates = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_duplicates_total", Help: "Idempotently ignored duplicate submissions."}, []string{"kind"})
	m.outboxOldestAge = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_notification_outbox_oldest_age_seconds", Help: "Age of the oldest outbox row by state."}, []string{"state"})
	m.attempts = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_notification_attempts_total", Help: "Notification attempts by outcome and bounded diagnostic class."}, []string{"outcome", "diagnostic_class"})
	m.poolConnections = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_pool_connections", Help: "Pool resources by bounded resource and state."}, []string{"resource", "state"})
	m.transactionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "xisnove_transaction_duration_seconds", Help: "Transaction duration by operation and outcome.", Buckets: prometheus.DefBuckets}, []string{"operation", "outcome"})
	m.migrationRuns = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "xisnove_migration_runs_total", Help: "Migration checks by outcome."}, []string{"outcome"})
	m.schemaVersion = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "xisnove_schema_version", Help: "Currently applied relational schema version."}, nil)
	m.registry.MustRegister(m.monitorState, m.transitions, m.probeDuration, m.schedulerCycles, m.leases, m.leaseEvents, m.heartbeatAge, m.duplicates, m.outboxOldestAge, m.attempts, m.poolConnections, m.transactionDuration, m.migrationRuns, m.schemaVersion)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
func (m *Metrics) Gatherer() prometheus.Gatherer { return m.registry }

func (m *Metrics) SetMonitorState(state MonitorState, count float64) {
	m.monitorState.WithLabelValues(closed(string(state), "unknown", "passing", "failing")).Set(nonNegative(count))
}
func (m *Metrics) ObserveTransition(from, to MonitorState) {
	m.transitions.WithLabelValues(closed(string(from), "unknown", "passing", "failing"), closed(string(to), "unknown", "passing", "failing")).Inc()
}
func (m *Metrics) ObserveProbe(outcome ProbeOutcome, duration time.Duration) {
	m.probeDuration.WithLabelValues(closed(string(outcome), "success", "failure", "timeout")).Observe(nonNegative(duration.Seconds()))
}
func (m *Metrics) ObserveSchedulerCycle(outcome CycleOutcome) {
	m.schedulerCycles.WithLabelValues(closed(string(outcome), "success", "failure")).Inc()
}
func (m *Metrics) SetLeases(state LeaseState, count float64) {
	m.leases.WithLabelValues(closed(string(state), "available", "claimed", "expired")).Set(nonNegative(count))
}
func (m *Metrics) ObserveLeaseEvent(outcome LeaseState) {
	m.leaseEvents.WithLabelValues(closed(string(outcome), "available", "claimed", "expired")).Inc()
}
func (m *Metrics) SetHeartbeatAge(age time.Duration) {
	m.heartbeatAge.WithLabelValues().Set(nonNegative(age.Seconds()))
}
func (m *Metrics) ObserveDuplicate(kind DuplicateKind) {
	m.duplicates.WithLabelValues(closed(string(kind), "run", "result")).Inc()
}
func (m *Metrics) SetOutboxOldestAge(state OutboxState, age time.Duration) {
	m.outboxOldestAge.WithLabelValues(closed(string(state), "pending", "claimed", "retry", "delivered", "permanent", "suppressed")).Set(nonNegative(age.Seconds()))
}
func (m *Metrics) ObserveAttempt(outcome AttemptOutcome, class DiagnosticClass) {
	m.attempts.WithLabelValues(closed(string(outcome), "delivered", "retry", "permanent", "canceled"), closed(string(class), "none", "timeout", "transport", "provider", "policy", "internal")).Inc()
}
func (m *Metrics) SetPool(resource PoolResource, state PoolState, count float64) {
	m.poolConnections.WithLabelValues(closed(string(resource), "database", "http"), closed(string(state), "idle", "in_use", "waiting")).Set(nonNegative(count))
}
func (m *Metrics) ObserveTransaction(kind TransactionKind, outcome TransactionOutcome, duration time.Duration) {
	m.transactionDuration.WithLabelValues(closed(string(kind), "view", "transact"), closed(string(outcome), "commit", "rollback", "error")).Observe(nonNegative(duration.Seconds()))
}
func (m *Metrics) ObserveMigration(outcome MigrationOutcome) {
	m.migrationRuns.WithLabelValues(closed(string(outcome), "success", "failure", "incompatible")).Inc()
}
func (m *Metrics) SetSchemaVersion(version int64) {
	m.schemaVersion.WithLabelValues().Set(nonNegative(float64(version)))
}

func closed(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "other"
}

func nonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}
