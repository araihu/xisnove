package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestMaintenanceHandlerRejectsMissingBodyAndMapsTypedRecord(t *testing.T) {
	server := NewServer(ServerConfig{})
	response, err := server.CreateMaintenance(context.Background(), CreateMaintenanceRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(CreateMaintenancedefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 400 || problem.Body.Code != "validation_failed" {
		t.Fatalf("CreateMaintenance(nil body) = %#v", response)
	}

	now := time.Date(2026, 7, 25, 16, 0, 0, 0, time.UTC)
	end := now.Add(time.Hour)
	interval, err := domain.NewMaintenanceInterval(
		"00000000-0000-4000-8000-000000000001",
		"00000000-0000-4000-8000-000000000002",
		now, &end, "upgrade",
	)
	if err != nil {
		t.Fatal(err)
	}
	interval.CreatedAt = now.Add(-time.Minute)
	mapped, err := mapMaintenance(port.MaintenanceRecord{Interval: interval, UpdatedAt: now})
	if err != nil || mapped.Reason != "upgrade" || mapped.EndsAt == nil || !mapped.EndsAt.Equal(end) || mapped.UpdatedAt != now {
		t.Fatalf("mapMaintenance() = %#v, %v", mapped, err)
	}
}
