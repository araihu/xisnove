package application

import (
	"context"
	"errors"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

type maintenanceProvenanceAuditRepository struct{}

func (maintenanceProvenanceAuditRepository) Append(context.Context, port.AuditEventRecord) error {
	return nil
}

func (maintenanceProvenanceAuditRepository) ListByIncident(context.Context, domain.IncidentID) ([]port.AuditEventRecord, error) {
	return nil, nil
}

func TestMaintenanceActivationProvenanceFailsClosedWithoutSubjectReader(t *testing.T) {
	_, _, err := maintenanceActivationProvenance(context.Background(), port.Repositories{
		StateTicks:      &stateTickHistoryRepository{},
		StateTickWriter: &recordingStateTickWriter{},
		Audit:           maintenanceProvenanceAuditRepository{},
	}, domain.MaintenanceID("maintenance-1"))
	if !errors.Is(err, ErrAuditSubjectReaderUnavailable) {
		t.Fatalf("maintenanceActivationProvenance() error = %v, want %v", err, ErrAuditSubjectReaderUnavailable)
	}
}
