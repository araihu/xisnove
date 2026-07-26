package sdk_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/sdk"
)

func TestTypedPageFetchersPreserveFiltersAndPassOpaqueCursor(t *testing.T) {
	type observedRequest struct {
		path  string
		query url.Values
	}
	var observed []observedRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		observed = append(observed, observedRequest{path: request.URL.Path, query: request.URL.Query()})
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"items":[],"page":{"nextCursor":"next-page"}}`))
	}))
	defer server.Close()
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state := sdk.DiscoveryCandidateStatePending
	present := true
	callerCursor := sdk.Cursor("caller-owned-cursor")
	discoveryParams := sdk.ListDiscoveryCandidatesParams{Cursor: &callerCursor, State: &state, Present: &present}
	incidentState := sdk.ListIncidentsParamsStateOpen
	incidentID := uuid.MustParse("00000000-0000-4000-8000-000000000123")
	offset := sdk.Offset(17)

	assertTypedPage(t, client.AgentsPageFetcher(sdk.ListAgentsParams{}), ctx)
	assertTypedPage(t, client.APITokensPageFetcher(sdk.ListAPITokensParams{}), ctx)
	assertTypedPage(t, client.DiscoveryCandidatesPageFetcher(discoveryParams), ctx)
	assertTypedPage(t, client.IncidentsPageFetcher(sdk.ListIncidentsParams{State: &incidentState}), ctx)
	assertTypedPage(t, client.IncidentEventsPageFetcher(incidentID, sdk.ListIncidentEventsParams{}), ctx)
	assertTypedPage(t, client.LocationsPageFetcher(sdk.ListLocationsParams{}), ctx)
	assertTypedPage(t, client.MaintenancePageFetcher(sdk.ListMaintenanceParams{Offset: &offset}), ctx)
	assertTypedPage(t, client.MonitorsPageFetcher(sdk.ListMonitorsParams{}), ctx)
	assertTypedPage(t, client.NotificationChannelsPageFetcher(sdk.ListNotificationChannelsParams{Offset: &offset}), ctx)
	assertTypedPage(t, client.NotificationDeliveriesPageFetcher(sdk.ListNotificationDeliveriesParams{Offset: &offset}), ctx)
	assertTypedPage(t, client.NotificationRoutesPageFetcher(sdk.ListNotificationRoutesParams{Offset: &offset}), ctx)

	if len(observed) != 11 {
		t.Fatalf("observed %d requests", len(observed))
	}
	for _, request := range observed {
		if request.query.Get("cursor") != "opaque-cursor" {
			t.Errorf("%s cursor = %q", request.path, request.query.Get("cursor"))
		}
	}
	if observed[2].query.Get("state") != "pending" || observed[2].query.Get("present") != "true" {
		t.Fatalf("discovery filters = %v", observed[2].query)
	}
	if discoveryParams.Cursor == nil || *discoveryParams.Cursor != "caller-owned-cursor" {
		t.Fatalf("caller parameters mutated: %#v", discoveryParams)
	}
	if observed[3].query.Get("state") != "open" {
		t.Fatalf("incident filters = %v", observed[3].query)
	}
	for _, index := range []int{6, 8, 9, 10} {
		if observed[index].query.Get("offset") != "17" {
			t.Errorf("%s compatibility filter = %v", observed[index].path, observed[index].query)
		}
	}
}

func TestTypedPageFetcherReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/problem+json")
		response.WriteHeader(http.StatusForbidden)
		_, _ = response.Write([]byte(`{"type":"https://xisnove.dev/problems/forbidden","title":"Forbidden","status":403,"code":"insufficient_scope","correlationId":"request-page"}`))
	}))
	defer server.Close()
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.LocationsPageFetcher(sdk.ListLocationsParams{})(context.Background(), "")
	var apiError *sdk.APIError
	if !errors.As(err, &apiError) || apiError.Problem.Code != "insufficient_scope" {
		t.Fatalf("page error = %#v", err)
	}
}

func TestTypedPageFetcherReturnsStructuredAPIErrorForMalformedProblem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/problem+json")
		response.Header().Set("X-Request-ID", "request-malformed")
		response.WriteHeader(http.StatusBadGateway)
		_, _ = response.Write([]byte(`{"code":`))
	}))
	defer server.Close()
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AgentsPageFetcher(sdk.ListAgentsParams{})(context.Background(), "")
	var apiError *sdk.APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("page error = %#v", err)
	}
	if apiError.StatusCode != http.StatusBadGateway || apiError.Problem.Status != http.StatusBadGateway ||
		apiError.Problem.Code != "http_error" || apiError.Problem.CorrelationId != "request-malformed" {
		t.Fatalf("API error = %#v", apiError)
	}
}

func assertTypedPage[T any](t *testing.T, fetch sdk.PageFetcher[T], ctx context.Context) {
	t.Helper()
	page, err := fetch(ctx, "opaque-cursor")
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "next-page" || len(page.Items) != 0 {
		t.Fatalf("page = %#v", page)
	}
}
