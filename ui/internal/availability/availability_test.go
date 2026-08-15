package availability

import (
	"testing"
	"time"

	"github.com/araihu/xisnove/sdk"
)

func TestStateSeriesKeepsUnknownExplicit(t *testing.T) {
	for _, test := range []struct {
		name  string
		state sdk.HealthState
		want  []float64
	}{
		{name: "up", state: sdk.Up, want: []float64{1, 0, 0, 0}},
		{name: "degraded", state: sdk.Degraded, want: []float64{0, 1, 0, 0}},
		{name: "pending", state: sdk.Pending, want: []float64{0, 1, 0, 0}},
		{name: "down", state: sdk.Down, want: []float64{0, 0, 1, 0}},
		{name: "unknown", state: sdk.Unknown, want: []float64{0, 0, 0, 1}},
		{name: "empty", state: "", want: []float64{0, 0, 0, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := StateSeries(test.state)
			if len(got) != len(test.want) {
				t.Fatalf("series length = %d, want %d", len(got), len(test.want))
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Errorf("series[%d] = %v, want %v", index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestHistoryReplacesOldestSamplesAtBound(t *testing.T) {
	base := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	history := NewHistory(2)
	history.Add(sdk.Up, base)
	history.Add(sdk.Down, base.Add(time.Second))
	history.Add(sdk.Degraded, base.Add(2*time.Second))

	snapshot := history.Snapshot()
	if got, want := snapshot.Categories, []string{"12:00:01", "12:00:02"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("categories = %#v, want %#v", got, want)
	}
	if len(snapshot.Series) != 4 || snapshot.Series[0].Values[0] != 0 || snapshot.Series[1].Values[1] != 1 {
		t.Fatalf("series = %#v", snapshot.Series)
	}
}

func TestHistoryUnknownWindowKeepsCurrentStateAtRightEdge(t *testing.T) {
	history := NewHistory(int(HistoryLookback/HistoryStep) + 1)
	end := time.Date(2026, time.August, 15, 11, 3, 42, 0, time.UTC)
	history.AddUnknownWindow(end.Add(-HistoryLookback), end)
	history.Add(sdk.Up, end)

	snapshot := history.Snapshot()
	wantPoints := int(HistoryLookback/HistoryStep) + 1
	if len(snapshot.Categories) != wantPoints {
		t.Fatalf("fallback history points = %d, want %d", len(snapshot.Categories), wantPoints)
	}
	if snapshot.Series[0].Values[wantPoints-1] != 1 || snapshot.Series[3].Values[wantPoints-1] != 0 {
		t.Fatalf("current state was not placed at right edge: %#v", snapshot.Series)
	}
	for index := 0; index < wantPoints-1; index++ {
		if snapshot.Series[3].Values[index] != 1 {
			t.Fatalf("unknown fallback point %d = %#v", index, snapshot.Series[3].Values)
		}
	}
}
