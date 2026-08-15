package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/sdk"
)

func TestCreateLocationReturnsGeneratedCreatedResponse(t *testing.T) {
	server := newConfigurationServer(t)

	response, err := server.CreateLocation(
		context.Background(),
		httpapi.CreateLocationRequestObject{
			Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateLocation201JSONResponse)
	if !ok || created.Name != "public" {
		t.Fatalf("response = %#v", response)
	}
	if created.Id.String() != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("Id = %s", created.Id)
	}
	if created.Address != "" || created.Protocol != httpapi.LocationProtocol("http") {
		t.Fatalf("location identity defaults = %#v", created)
	}
	if created.Policy.IntervalSeconds != 60 || created.Policy.TimeoutMillis != 5000 ||
		created.Policy.FailureThreshold != 3 || created.Policy.RecoveryThreshold != 2 {
		t.Fatalf("location policy defaults = %#v", created.Policy)
	}
}

func TestCreateLocationMapsAddressProtocolAndPolicy(t *testing.T) {
	server := newConfigurationServer(t)
	interval, timeout, failures, recoveries := int32(120), int32(2500), int32(4), int32(3)
	address := "192.0.2.10"
	protocol := httpapi.CreateLocationRequestProtocol("tcp")
	response, err := server.CreateLocation(context.Background(), httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{
			Name: "edge", Address: &address, Protocol: &protocol,
			Policy: &httpapi.LocationPolicyInput{IntervalSeconds: &interval, TimeoutMillis: &timeout, FailureThreshold: &failures, RecoveryThreshold: &recoveries},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateLocation201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	if created.Address != "192.0.2.10" || created.Protocol != httpapi.LocationProtocol("tcp") ||
		created.Policy.IntervalSeconds != interval || created.Policy.TimeoutMillis != timeout ||
		created.Policy.FailureThreshold != failures || created.Policy.RecoveryThreshold != recoveries {
		t.Fatalf("location = %#v", created)
	}
}

func TestCreateLocationMapsDuplicateNameToConflictProblem(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	request := httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
	}
	if _, err := server.CreateLocation(ctx, request); err != nil {
		t.Fatal(err)
	}

	response, err := server.CreateLocation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateLocationdefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 409 || problem.Body.Code != "conflict" {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateAndGetMonitorMapsAPIDurationsAndAssignment(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(
		ctx,
		httpapi.CreateLocationRequestObject{
			Body: &httpapi.CreateLocationJSONRequestBody{Name: "public"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)

	response, err := server.CreateMonitor(
		ctx,
		httpapi.CreateMonitorRequestObject{
			Body: &httpapi.CreateMonitorJSONRequestBody{
				Name:              "website",
				LocationId:        location.Id,
				RequiredLocation:  true,
				IntervalSeconds:   60,
				TimeoutMillis:     5000,
				FailureThreshold:  3,
				RecoveryThreshold: 2,
				Probe: mustHTTPProbe(t, httpapi.HTTPProbeDefinition{
					Method: httpapi.GET,
					Url:    "https://example.com/health",
					ExpectedStatus: []httpapi.StatusRange{{
						Minimum: 200, Maximum: 299,
					}},
					BodyContains: []string{"ok"},
					Headers:      map[string]string{},
				}),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateMonitor201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	if created.IntervalSeconds != 60 || created.TimeoutMillis != 5000 {
		t.Fatalf("created monitor = %#v", created)
	}

	gotResponse, err := server.GetMonitor(
		ctx,
		httpapi.GetMonitorRequestObject{MonitorId: created.Id},
	)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := gotResponse.(httpapi.GetMonitor200JSONResponse)
	if !ok || got.LocationId != location.Id || !got.RequiredLocation {
		t.Fatalf("response = %#v", gotResponse)
	}
	probe, err := got.Probe.AsHTTPProbeDefinition()
	if err != nil || probe.Url != "https://example.com/health" {
		t.Fatalf("probe = %#v error = %v", probe, err)
	}
}

func TestCreateTCPMonitorRoundTripsGeneratedProbe(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(ctx, httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)
	tlsSeconds := int64(86400)
	response, err := server.CreateMonitor(ctx, httpapi.CreateMonitorRequestObject{
		Body: &httpapi.CreateMonitorJSONRequestBody{
			Name: "postgres", LocationId: location.Id, RequiredLocation: true,
			IntervalSeconds: 60, TimeoutMillis: 5000,
			FailureThreshold: 3, RecoveryThreshold: 2,
			Probe: mustTCPProbe(t, httpapi.TCPProbeDefinition{
				Host: "postgres.internal", Port: 5432,
				Send: []byte("PING"), Expect: []byte("PONG"),
				TlsMinimumRemainingSeconds: &tlsSeconds,
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateMonitor201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	probe, err := created.Probe.AsTCPProbeDefinition()
	if err != nil ||
		probe.Host != "postgres.internal" ||
		probe.Port != 5432 ||
		string(probe.Send) != "PING" ||
		string(probe.Expect) != "PONG" {
		t.Fatalf("probe = %#v error = %v", probe, err)
	}
}

func TestCreateDNSMonitorRoundTripsGeneratedProbe(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(ctx, httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)
	response, err := server.CreateMonitor(ctx, httpapi.CreateMonitorRequestObject{
		Body: &httpapi.CreateMonitorJSONRequestBody{
			Name: "cluster dns", LocationId: location.Id, RequiredLocation: true,
			IntervalSeconds: 60, TimeoutMillis: 5000,
			FailureThreshold: 3, RecoveryThreshold: 2,
			Probe: mustDNSProbe(t, httpapi.DNSProbeDefinition{
				Resolver: "10.43.0.10:53", Name: "kubernetes.default.svc",
				RecordType: "A", ExpectedValues: []string{"10.43.0.2", "10.43.0.1"},
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, ok := response.(httpapi.CreateMonitor201JSONResponse)
	if !ok {
		t.Fatalf("response = %#v", response)
	}
	probe, err := created.Probe.AsDNSProbeDefinition()
	if err != nil ||
		probe.Resolver != "10.43.0.10:53" ||
		probe.Name != "kubernetes.default.svc" ||
		probe.RecordType != "A" ||
		len(probe.ExpectedValues) != 2 ||
		probe.ExpectedValues[0] != "10.43.0.1" {
		t.Fatalf("probe = %#v error = %v", probe, err)
	}
}

func TestCreateMonitorRejectsProbeVariantMismatch(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(ctx, httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)
	var mismatch httpapi.ProbeDefinition
	if err := json.Unmarshal([]byte(`{
		"kind":"tcp",
		"method":"GET",
		"url":"https://example.com",
		"headers":{},
		"body":"",
		"expectedStatus":[{"minimum":200,"maximum":299}],
		"bodyContains":[],
		"bodyDoesNotContain":[],
		"followRedirects":false
	}`), &mismatch); err != nil {
		t.Fatal(err)
	}
	response, err := server.CreateMonitor(ctx, httpapi.CreateMonitorRequestObject{
		Body: &httpapi.CreateMonitorJSONRequestBody{
			Name: "bad", LocationId: location.Id, IntervalSeconds: 60,
			TimeoutMillis: 5000, FailureThreshold: 1, RecoveryThreshold: 1,
			Probe: mismatch,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateMonitordefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 400 {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateMonitorRejectsSecretLiteralHeader(t *testing.T) {
	server := newConfigurationServer(t)
	ctx := context.Background()
	locationResponse, err := server.CreateLocation(ctx, httpapi.CreateLocationRequestObject{
		Body: &httpapi.CreateLocationJSONRequestBody{Name: "private"},
	})
	if err != nil {
		t.Fatal(err)
	}
	location := locationResponse.(httpapi.CreateLocation201JSONResponse)
	response, err := server.CreateMonitor(ctx, httpapi.CreateMonitorRequestObject{
		Body: &httpapi.CreateMonitorJSONRequestBody{
			Name: "secret", LocationId: location.Id, IntervalSeconds: 60,
			TimeoutMillis: 5000, FailureThreshold: 1, RecoveryThreshold: 1,
			Probe: mustHTTPProbe(t, httpapi.HTTPProbeDefinition{
				Method: httpapi.GET,
				Url:    "https://example.com",
				Headers: map[string]string{
					"Authorization": "Bearer should-not-be-persisted",
				},
				ExpectedStatus: []httpapi.StatusRange{{Minimum: 200, Maximum: 299}},
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateMonitordefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 400 {
		t.Fatalf("response = %#v", response)
	}
}

func TestCreateTCPAndDNSMonitorsRoundTripThroughSDK(t *testing.T) {
	strict := httpapi.NewStrictHandler(newConfigurationServer(t), nil)
	server := httptest.NewServer(httpapi.HandlerWithOptions(strict, httpapi.StdHTTPServerOptions{
		BaseRouter: httpapi.NewOperatorActionServeMux(),
	}))
	t.Cleanup(server.Close)
	client, err := sdk.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	locationResponse, err := client.CreateLocationWithResponse(
		ctx, nil, sdk.CreateLocationRequest{Name: "private"},
	)
	if err != nil || locationResponse.JSON201 == nil {
		t.Fatalf("location response = %#v, error = %v", locationResponse, err)
	}
	locationID := locationResponse.JSON201.Id

	var tcp sdk.ProbeDefinition
	tlsSeconds := int64(86400)
	if err := tcp.FromTCPProbeDefinition(sdk.TCPProbeDefinition{
		Host: "postgres.internal", Port: 5432, Send: []byte("PING"), Expect: []byte("PONG"),
		TlsMinimumRemainingSeconds: &tlsSeconds,
	}); err != nil {
		t.Fatal(err)
	}
	tcpResponse, err := client.CreateMonitorWithResponse(ctx, nil, sdk.CreateMonitorRequest{
		Name: "postgres", LocationId: locationID, RequiredLocation: true,
		IntervalSeconds: 60, TimeoutMillis: 5000,
		FailureThreshold: 3, RecoveryThreshold: 2, Probe: tcp,
	})
	if err != nil || tcpResponse.JSON201 == nil {
		t.Fatalf("TCP response = %#v, error = %v", tcpResponse, err)
	}
	gotTCP, err := client.RequireMonitor(ctx, tcpResponse.JSON201.Id.String())
	if err != nil {
		t.Fatal(err)
	}
	tcpProbe, err := gotTCP.Probe.AsTCPProbeDefinition()
	if err != nil ||
		gotTCP.Kind != sdk.MonitorKindTcp ||
		gotTCP.LocationId != locationID ||
		!gotTCP.RequiredLocation ||
		gotTCP.FailureThreshold != 3 ||
		gotTCP.RecoveryThreshold != 2 ||
		tcpProbe.Host != "postgres.internal" ||
		tcpProbe.Port != 5432 ||
		string(tcpProbe.Send) != "PING" ||
		string(tcpProbe.Expect) != "PONG" ||
		tcpProbe.TlsMinimumRemainingSeconds == nil ||
		*tcpProbe.TlsMinimumRemainingSeconds != tlsSeconds {
		t.Fatalf("TCP probe = %#v, error = %v", tcpProbe, err)
	}

	var dns sdk.ProbeDefinition
	if err := dns.FromDNSProbeDefinition(sdk.DNSProbeDefinition{
		Resolver: "10.43.0.10:53", Name: "kubernetes.default.svc",
		RecordType: "A", ExpectedValues: []string{"10.43.0.2", "10.43.0.1"},
	}); err != nil {
		t.Fatal(err)
	}
	dnsResponse, err := client.CreateMonitorWithResponse(ctx, nil, sdk.CreateMonitorRequest{
		Name: "cluster dns", LocationId: locationID, RequiredLocation: true,
		IntervalSeconds: 60, TimeoutMillis: 5000,
		FailureThreshold: 3, RecoveryThreshold: 2, Probe: dns,
	})
	if err != nil || dnsResponse.JSON201 == nil {
		t.Fatalf("DNS response = %#v, error = %v", dnsResponse, err)
	}
	gotDNS, err := client.RequireMonitor(ctx, dnsResponse.JSON201.Id.String())
	if err != nil {
		t.Fatal(err)
	}
	dnsProbe, err := gotDNS.Probe.AsDNSProbeDefinition()
	if err != nil ||
		gotDNS.Kind != sdk.MonitorKindDns ||
		gotDNS.LocationId != locationID ||
		!gotDNS.RequiredLocation ||
		gotDNS.FailureThreshold != 3 ||
		gotDNS.RecoveryThreshold != 2 ||
		dnsProbe.Resolver != "10.43.0.10:53" ||
		dnsProbe.Name != "kubernetes.default.svc" ||
		dnsProbe.RecordType != "A" ||
		len(dnsProbe.ExpectedValues) != 2 ||
		dnsProbe.ExpectedValues[0] != "10.43.0.1" {
		t.Fatalf("DNS probe = %#v, error = %v", dnsProbe, err)
	}
}

func TestCreateMonitorMapsMissingLocationToNotFoundProblem(t *testing.T) {
	server := newConfigurationServer(t)
	response, err := server.CreateMonitor(
		context.Background(),
		httpapi.CreateMonitorRequestObject{
			Body: &httpapi.CreateMonitorJSONRequestBody{
				Name:              "website",
				LocationId:        uuid.MustParse("99999999-9999-4999-8999-999999999999"),
				RequiredLocation:  true,
				IntervalSeconds:   60,
				TimeoutMillis:     5000,
				FailureThreshold:  3,
				RecoveryThreshold: 2,
				Probe: mustHTTPProbe(t, httpapi.HTTPProbeDefinition{
					Method: httpapi.GET,
					Url:    "https://example.com/health",
					ExpectedStatus: []httpapi.StatusRange{{
						Minimum: 200, Maximum: 299,
					}},
					Headers: map[string]string{},
				}),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	problem, ok := response.(httpapi.CreateMonitordefaultApplicationProblemPlusJSONResponse)
	if !ok || problem.StatusCode != 404 || problem.Body.Code != "not_found" {
		t.Fatalf("response = %#v", response)
	}
}

func mustHTTPProbe(t *testing.T, definition httpapi.HTTPProbeDefinition) httpapi.ProbeDefinition {
	t.Helper()
	var probe httpapi.ProbeDefinition
	if err := probe.FromHTTPProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	return probe
}

func mustTCPProbe(t *testing.T, definition httpapi.TCPProbeDefinition) httpapi.ProbeDefinition {
	t.Helper()
	var probe httpapi.ProbeDefinition
	if err := probe.FromTCPProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	return probe
}

func mustDNSProbe(t *testing.T, definition httpapi.DNSProbeDefinition) httpapi.ProbeDefinition {
	t.Helper()
	var probe httpapi.ProbeDefinition
	if err := probe.FromDNSProbeDefinition(definition); err != nil {
		t.Fatal(err)
	}
	return probe
}

func newConfigurationServer(t *testing.T) *httpapi.Server {
	t.Helper()
	db, err := sqlitestore.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqlitestore.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		"33333333-3333-4333-8333-333333333333",
	}
	nextID := 0
	service := application.NewConfigurationService(
		sqlitestore.NewStore(db),
		func() time.Time {
			return time.Date(2026, 7, 25, 1, 2, 3, 0, time.UTC)
		},
		func() string {
			id := ids[nextID]
			nextID++
			return id
		},
	)
	return httpapi.NewServer(httpapi.ServerConfig{Configuration: service})
}
