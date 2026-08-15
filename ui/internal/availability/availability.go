package availability

import (
	"time"

	"github.com/araihu/goshtoso-charts/components/interactive"
	"github.com/araihu/xisnove/sdk"
)

const (
	Healthy  = "Healthy"
	Degraded = "Degraded"
	Down     = "Down"
	Unknown  = "Unknown"

	DefaultWindow   = 24
	HistoryWindow   = 4096
	HistoryLookback = 3 * time.Hour
)

var seriesNames = []string{Healthy, Degraded, Down, Unknown}

// SeriesNames returns the stable series order shared by the chart and SSE
// snapshots. Callers receive a copy so the contract cannot be mutated.
func SeriesNames() []string {
	return append([]string(nil), seriesNames...)
}

// StateSeries returns a one-hot availability point. Unknown and unsupported
// states remain explicit; they must never be presented as uptime.
func StateSeries(state sdk.HealthState) []float64 {
	values := make([]float64, len(seriesNames))
	index := len(seriesNames) - 1
	switch state {
	case sdk.Up:
		index = 0
	case sdk.Degraded, sdk.Pending:
		index = 1
	case sdk.Down:
		index = 2
	}
	values[index] = 1
	return values
}

// NewHistory creates a bounded replacement-snapshot history for one SSE
// subscriber. Each event contains the full window, so reconnects are safe.
func NewHistory(window int) *History {
	if window <= 0 {
		window = DefaultWindow
	}
	return &History{window: window}
}

// History accumulates one-hot availability states in chronological order.
type History struct {
	window     int
	categories []string
	values     [][]float64
}

// Add appends one observation and trims the oldest point when the window is
// full. Timestamp labels are deliberately compact for the mini dashboard.
func (h *History) Add(state sdk.HealthState, observedAt time.Time) {
	if h == nil {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	h.categories = append(h.categories, observedAt.UTC().Format("15:04:05"))
	h.values = append(h.values, StateSeries(state))
	if len(h.categories) <= h.window {
		return
	}
	remove := len(h.categories) - h.window
	h.categories = append([]string(nil), h.categories[remove:]...)
	h.values = append([][]float64(nil), h.values[remove:]...)
}

// AddOutcome maps an accepted probe result into the chart's health palette.
// Missing observations remain absent so callers can represent gaps as
// Unknown without conflating them with failed probes.
func (h *History) AddOutcome(outcome sdk.MonitorAvailabilitySampleOutcome, observedAt time.Time) {
	state := sdk.Down
	if outcome == sdk.MonitorAvailabilitySampleOutcomePassed {
		state = sdk.Up
	}
	h.Add(state, observedAt)
}

// Snapshot returns a complete renderer-neutral payload for goshtoso-charts.
func (h *History) Snapshot() interactive.CartesianSnapshot {
	if h == nil {
		return interactive.CartesianSnapshot{}
	}
	series := make([]interactive.CartesianSnapshotSeries, len(seriesNames))
	for index, name := range seriesNames {
		series[index] = interactive.CartesianSnapshotSeries{Name: name, Values: make([]float64, len(h.categories))}
	}
	for point, values := range h.values {
		for index := range series {
			if index < len(values) {
				series[index].Values[point] = values[index]
			}
		}
	}
	return interactive.CartesianSnapshot{
		Categories: append([]string(nil), h.categories...),
		Series:     series,
	}
}
