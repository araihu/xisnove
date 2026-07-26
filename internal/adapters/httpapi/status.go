package httpapi

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
)

// getPublicStatusPage is the strict-operation implementation seam. Root Server
// wiring delegates GetPublicStatusPage to this function once the service is
// installed in the shared ServerConfig.
func getPublicStatusPage(
	ctx context.Context,
	service *application.PublicStatusService,
) (GetPublicStatusPageResponseObject, error) {
	page, err := service.Get(ctx)
	if err != nil {
		return nil, err
	}
	monitors := make([]PublicStatusMonitor, len(page.Monitors))
	for index, monitor := range page.Monitors {
		id, err := uuid.Parse(string(monitor.ID))
		if err != nil {
			return nil, fmt.Errorf("map public status monitor ID: %w", err)
		}
		uptime := make([]DailyUptimePoint, len(monitor.Uptime))
		for uptimeIndex, record := range monitor.Uptime {
			uptime[uptimeIndex] = DailyUptimePoint{
				Date:             openapi_types.Date{Time: record.Day.UTC()},
				UptimePercentage: publicUptimePercentage(record),
			}
		}
		monitors[index] = PublicStatusMonitor{
			Id: id, Name: monitor.Name, Description: monitor.Description,
			State: HealthState(monitor.State), RecentUptime: uptime,
		}
	}
	incidents := make([]PublicIncidentSummary, len(page.ActiveIncidents))
	for index, incident := range page.ActiveIncidents {
		id, err := uuid.Parse(string(incident.ID))
		if err != nil {
			return nil, fmt.Errorf("map public incident ID: %w", err)
		}
		monitorID, err := uuid.Parse(string(incident.MonitorID))
		if err != nil {
			return nil, fmt.Errorf("map public incident monitor ID: %w", err)
		}
		incidents[index] = PublicIncidentSummary{
			Id: id, MonitorId: monitorID, MonitorName: incident.MonitorName,
			State: PublicIncidentSummaryStateOpen, Severity: IncidentSeverity(incident.Severity),
			OpenedAt: incident.OpenedAt, LastTransitionAt: incident.LastTransitionAt,
		}
	}
	return GetPublicStatusPage200JSONResponse{
		GeneratedAt: page.GeneratedAt, State: HealthState(page.State),
		Monitors: monitors, ActiveIncidents: incidents,
	}, nil
}

func publicUptimePercentage(record port.DailyUptimeRecord) float64 {
	total := record.Passing + record.Failing + record.Unknown
	if total == 0 {
		return 0
	}
	return float64(record.Passing) * 100 / float64(total)
}
