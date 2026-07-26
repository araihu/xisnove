package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
)

func TestPublicStatusAuthorizationIgnoresInvalidBearer(t *testing.T) {
	authorization := testOperationAuthorization(t)
	called := false
	handler := authorization.middleware(
		func(context.Context, string) (application.Principal, error) {
			t.Fatal("public status attempted authentication")
			return application.Principal{}, errors.New("unreachable")
		},
		nil,
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := authorizeRequest(handler, http.MethodGet, "/v1/status-page", "definitely-invalid")
	if !called || response.Code != http.StatusNoContent {
		t.Fatalf("called=%v response=%d %s", called, response.Code, response.Body)
	}
}

func TestPublicStatusHandlerMapsOnlyPublishedAggregate(t *testing.T) {
	now := time.Date(2026, 7, 26, 15, 0, 0, 0, time.UTC)
	incident := domain.Incident{
		ID:        "00000000-0000-4000-8000-000000000303",
		MonitorID: "00000000-0000-4000-8000-000000000301",
		State:     domain.HealthDown, Severity: domain.IncidentCritical,
		OpenedAt: now.Add(-time.Hour), LastTransitionAt: now.Add(-30 * time.Minute),
	}
	repository := &httpPublicStatusRepository{
		monitors: []port.PublicMonitorProjection{{
			ID: "00000000-0000-4000-8000-000000000301", Name: "edge",
			Description: "public edge", State: domain.HealthDown,
			ActiveIncident: &incident,
		}},
		uptime: []port.DailyUptimeRecord{{
			MonitorID: "00000000-0000-4000-8000-000000000301",
			Day:       now.AddDate(0, 0, -1), Passing: 9, Failing: 1,
		}},
	}
	service, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &httpPublicStatusStore{repository: repository}, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := getPublicStatusPage(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}
	page, ok := response.(GetPublicStatusPage200JSONResponse)
	if !ok || page.State != HealthStateDown || !page.GeneratedAt.Equal(now) ||
		len(page.Monitors) != 1 || len(page.ActiveIncidents) != 1 {
		t.Fatalf("response = %#v", response)
	}
	if page.Monitors[0].RecentUptime[0].UptimePercentage != 90 ||
		page.Monitors[0].RecentUptime[0].Date.Time.Format(time.DateOnly) != "2026-07-25" {
		t.Fatalf("uptime = %#v", page.Monitors[0].RecentUptime)
	}
	if page.ActiveIncidents[0].State != PublicIncidentSummaryStateOpen ||
		page.ActiveIncidents[0].MonitorName != "edge" {
		t.Fatalf("incident = %#v", page.ActiveIncidents[0])
	}
}

func TestPublicStatusHandlerReturnsApplicationFailureToStrictErrorMapper(t *testing.T) {
	service, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &httpPublicStatusStore{err: errors.New("database failed with secret")},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := getPublicStatusPage(context.Background(), service)
	if err == nil || response != nil {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
}

func TestPublicStatusStrictErrorResponseDoesNotLeakStorageDetail(t *testing.T) {
	service, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: &httpPublicStatusStore{err: errors.New("database failed with secret")},
	})
	if err != nil {
		t.Fatal(err)
	}
	strict := NewStrictHandlerWithOptions(
		&publicStatusStrictFixture{service: service}, nil,
		StrictHTTPServerOptions{ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, ToProblem(err, correlationID(r)))
		}},
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/status-page", nil)
	request.Header.Set("X-Request-ID", "status-correlation")
	response := httptest.NewRecorder()
	strict.GetPublicStatusPage(response, request)
	if response.Code != http.StatusInternalServerError ||
		strings.Contains(response.Body.String(), "database") ||
		strings.Contains(response.Body.String(), "secret") ||
		!strings.Contains(response.Body.String(), `"code":"internal_error"`) ||
		!strings.Contains(response.Body.String(), `"correlationId":"status-correlation"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body)
	}
}

type publicStatusStrictFixture struct {
	StrictServerInterface
	service *application.PublicStatusService
}

func (f *publicStatusStrictFixture) GetPublicStatusPage(
	ctx context.Context,
	_ GetPublicStatusPageRequestObject,
) (GetPublicStatusPageResponseObject, error) {
	return getPublicStatusPage(ctx, f.service)
}

type httpPublicStatusStore struct {
	repository *httpPublicStatusRepository
	err        error
}

func (s *httpPublicStatusStore) View(ctx context.Context, fn func(context.Context, port.PublicStatusRepositories) error) error {
	if s.err != nil {
		return s.err
	}
	return fn(ctx, port.PublicStatusRepositories{Status: s.repository, Retention: s.repository})
}

type httpPublicStatusRepository struct {
	monitors []port.PublicMonitorProjection
	uptime   []port.DailyUptimeRecord
}

func (r *httpPublicStatusRepository) ListMonitors(context.Context) ([]port.PublicMonitorProjection, error) {
	return r.monitors, nil
}
func (r *httpPublicStatusRepository) ListDailyUptime(context.Context, domain.MonitorID, time.Time, time.Time) ([]port.DailyUptimeRecord, error) {
	return r.uptime, nil
}
func (*httpPublicStatusRepository) ClaimLease(context.Context, port.OperationLeaseRecord, time.Time) (port.OperationLeaseRecord, error) {
	panic("unexpected ClaimLease")
}
func (*httpPublicStatusRepository) UpdateLease(context.Context, port.OperationLeaseRecord) (bool, error) {
	panic("unexpected UpdateLease")
}
func (*httpPublicStatusRepository) ReleaseLease(context.Context, string, []byte) (bool, error) {
	panic("unexpected ReleaseLease")
}
func (*httpPublicStatusRepository) ListAggregationResults(context.Context, time.Time, time.Time, time.Time, string, int) ([]port.AggregationResultRecord, error) {
	panic("unexpected ListAggregationResults")
}
func (*httpPublicStatusRepository) UpsertDailyUptime(context.Context, port.DailyUptimeRecord) error {
	panic("unexpected UpsertDailyUptime")
}
func (*httpPublicStatusRepository) DeleteExpiredResults(context.Context, time.Time, int) (int64, error) {
	panic("unexpected DeleteExpiredResults")
}
func (*httpPublicStatusRepository) DeleteExpiredDailyUptime(context.Context, time.Time, int) (int64, error) {
	panic("unexpected DeleteExpiredDailyUptime")
}
