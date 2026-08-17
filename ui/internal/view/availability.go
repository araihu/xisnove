package view

import (
	"bytes"
	"context"
	"html"
	"io"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso-charts/components/chartcontrol"
	"github.com/araihu/goshtoso-charts/components/charttheme"
	"github.com/araihu/goshtoso-charts/components/interactive"
	"github.com/araihu/xisnove/sdk"
	"github.com/araihu/xisnove/ui/internal/availability"
)

const (
	// availabilityLookback is the visible dashboard window. The SSE endpoint
	// replaces this seed with its complete renderer-neutral history snapshot.
	availabilityLookback = 3 * time.Hour
	availabilitySeedStep = 5 * time.Minute
)

func monitorAvailabilityChart(monitor sdk.Monitor, health sdk.MonitorHealth) templ.Component {
	return renderAvailabilityChart(monitor, health, "168px", "xis-availability-chart", true, true, "4px", "")
}

// monitorAvailabilityMiniChart renders a compact recent trend for the monitor
// inventory table. The compact SSE stream keeps the row readable while the
// detail page continues to expose the complete three-hour history.
func monitorAvailabilityMiniChart(monitor sdk.Monitor, health sdk.MonitorHealth) templ.Component {
	return renderAvailabilityChart(monitor, health, "32px", "xis-availability-chart xis-availability-mini-chart", false, false, "2px", "?compact=1", availability.CompactWindow)
}

func renderAvailabilityChart(monitor sdk.Monitor, health sdk.MonitorHealth, height, class string, showLegend, showTooltip bool, barWidth, streamQuery string, compactPoints ...int) templ.Component {
	snapshot := availabilitySeedSnapshot(health.State, time.Now().UTC(), compactPoints...)

	series := make([]interactive.BarSeries, 0, len(snapshot.Series))
	for _, snapshotSeries := range snapshot.Series {
		data := make([]interactive.BarData, len(snapshot.Categories))
		for index, value := range snapshotSeries.Values {
			data[index] = interactive.BarData{Value: value}
		}
		series = append(series, interactive.BarSeries{
			Name: snapshotSeries.Name,
			Data: data,
			Options: interactive.SeriesOptions{
				Stack:    "availability",
				BarWidth: barWidth,
				BarGap:   "-100%",
			},
		})
	}

	chart := interactive.Bar(interactive.BarConfig{
		Label:  "Live availability for " + monitor.Name,
		XAxis:  snapshot.Categories,
		Series: series,
		Width:  "100%",
		Height: height,
		Options: interactive.ChartOptions{
			Legend:    &interactive.LegendOptions{Show: interactive.Bool(showLegend), Orient: "horizontal", Bottom: "0"},
			Tooltip:   &interactive.TooltipOptions{Show: interactive.Bool(showTooltip), Trigger: "axis"},
			XAxis:     &interactive.AxisOptions{Show: interactive.Bool(false), ShowFirstLabel: interactive.Bool(false), ShowLastLabel: interactive.Bool(false)},
			YAxis:     &interactive.AxisOptions{Show: interactive.Bool(false), Min: interactive.Float(0), Max: interactive.Float(1)},
			Animation: interactive.Bool(false),
			Controls:  chartcontrol.Options{Expand: interactive.Bool(false)},
			Export:    &chartcontrol.ExportOptions{Disabled: true},
		},
		Style: charttheme.Style{Palette: charttheme.PaletteStatus, Class: class},
		Live: &interactive.LiveData{
			URL:   "/monitors/" + monitor.Id.String() + "/availability/events" + streamQuery,
			Event: "chart",
		},
	})
	return nonceChart(chart)
}

// availabilitySeedSnapshot adapts the current health-only UI input to the
// renderer-neutral CartesianSnapshot consumed by Goshtoso Charts. The latest
// observation is placed at the right edge; the live SSE stream then replaces
// this seed with historical samples without changing the chart component.
func availabilitySeedSnapshot(state sdk.HealthState, now time.Time, compactPoints ...int) interactive.CartesianSnapshot {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC().Truncate(time.Second)
	lookback := availabilityLookback
	sampleCount := int(lookback/availabilitySeedStep) + 1
	if len(compactPoints) > 0 && compactPoints[0] > 0 {
		sampleCount = compactPoints[0]
		lookback = time.Duration(sampleCount-1) * availabilitySeedStep
	}
	categories := make([]string, sampleCount)
	series := make([]interactive.CartesianSnapshotSeries, len(availability.SeriesNames()))
	for index, name := range availability.SeriesNames() {
		series[index] = interactive.CartesianSnapshotSeries{Name: name, Values: make([]float64, sampleCount)}
	}
	for index := range categories {
		categories[index] = now.Add(-lookback + time.Duration(index)*availabilitySeedStep).Format("15:04:05")
	}
	values := availability.StateSeries(state)
	for index, value := range values {
		series[index].Values[sampleCount-1] = value
	}
	return interactive.CartesianSnapshot{Categories: categories, Series: series}
}

// nonceChart keeps chart-generated inline runtimes compatible with the UI CSP.
// Chart dependencies already read the templ nonce; the interactive renderer
// emits inline snippets, so attach the same request nonce before writing them.
func nonceChart(component templ.Component) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		var body bytes.Buffer
		if err := component.Render(ctx, &body); err != nil {
			return err
		}
		nonce := templ.GetNonce(ctx)
		bodyBytes := append([]byte(nil), body.Bytes()...)
		// go-echarts emits top-level `let` declarations. HTMX history swaps can
		// execute the same fragment again; `var` keeps those replays legal.
		bodyBytes = bytes.ReplaceAll(bodyBytes, []byte("let goecharts_"), []byte("var goecharts_"))
		bodyBytes = bytes.ReplaceAll(bodyBytes, []byte("let option_"), []byte("var option_"))
		if nonce != "" {
			bodyBytes = bytes.ReplaceAll(bodyBytes, []byte("<script"), []byte(`<script nonce="`+html.EscapeString(nonce)+`"`))
		}
		_, err := writer.Write(bodyBytes)
		return err
	})
}
