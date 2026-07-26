package api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

var recognizedAPITokenScopes = map[string]bool{
	"tokens:read": true, "tokens:write": true,
	"locations:read": true, "locations:write": true,
	"monitors:read": true, "monitors:write": true,
	"agents:read": true, "agents:write": true,
	"incidents:read":     true,
	"notifications:read": true, "notifications:write": true,
	"maintenance:read": true, "maintenance:write": true,
	"discovery:read": true, "discovery:write": true,
	"status:read": true,
}

func TestContractUsesOpenAPI312(t *testing.T) {
	doc := loadContract(t)
	if doc.OpenAPI != "3.1.2" {
		t.Fatalf("OpenAPI = %q, want 3.1.2", doc.OpenAPI)
	}
	if doc.JSONSchemaDialect != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("JSON schema dialect = %q", doc.JSONSchemaDialect)
	}
}

func TestHumanClientOperationIDsAreFrozen(t *testing.T) {
	doc := loadContract(t)
	want := map[string]struct {
		method string
		path   string
	}{
		"revokeCurrentSession":      {http.MethodDelete, "/v1/sessions/current"},
		"createAPIToken":            {http.MethodPost, "/v1/api-tokens"},
		"listAPITokens":             {http.MethodGet, "/v1/api-tokens"},
		"revokeAPIToken":            {http.MethodDelete, "/v1/api-tokens/{tokenId}"},
		"listLocations":             {http.MethodGet, "/v1/locations"},
		"getLocation":               {http.MethodGet, "/v1/locations/{locationId}"},
		"listMonitors":              {http.MethodGet, "/v1/monitors"},
		"updateMonitor":             {http.MethodPut, "/v1/monitors/{monitorId}"},
		"disableMonitor":            {http.MethodDelete, "/v1/monitors/{monitorId}"},
		"listAgents":                {http.MethodGet, "/v1/agents"},
		"getAgent":                  {http.MethodGet, "/v1/agents/{agentId}"},
		"revokeAgent":               {http.MethodDelete, "/v1/agents/{agentId}"},
		"rotateAgentCredential":     {http.MethodPost, "/v1/agents/{agentId}/credential-rotations"},
		"listIncidents":             {http.MethodGet, "/v1/incidents"},
		"getIncident":               {http.MethodGet, "/v1/incidents/{incidentId}"},
		"listIncidentEvents":        {http.MethodGet, "/v1/incidents/{incidentId}/events"},
		"upsertDiscoveryCandidates": {http.MethodPost, "/v1/agent/discovery-candidates:batch"},
		"listDiscoveryCandidates":   {http.MethodGet, "/v1/discovery-candidates"},
		"getDiscoveryCandidate":     {http.MethodGet, "/v1/discovery-candidates/{candidateId}"},
		"promoteDiscoveryCandidate": {http.MethodPost, "/v1/discovery-candidates/{candidateId}/promotion"},
		"getPublicStatusPage":       {http.MethodGet, "/v1/status-page"},
	}

	for operationID, expected := range want {
		operation := operationByID(t, doc, operationID)
		item := doc.Paths.Value(expected.path)
		if item == nil || operationForMethod(item, expected.method) != operation {
			t.Errorf("%s is not %s %s", operationID, expected.method, expected.path)
		}
	}
}

func TestProtectedOperationsDeclareRecognizedScopes(t *testing.T) {
	doc := loadContract(t)
	for _, item := range doc.Paths.Map() {
		for _, operation := range operations(item) {
			if operation == nil || isAgentOperation(operation) || isAnonymous(operation) {
				continue
			}
			scopes, ok := stringExtension(operation.Extensions["x-xisnove-scopes"])
			if !ok || len(scopes) != 1 || !recognizedAPITokenScopes[scopes[0]] {
				t.Errorf("%s scopes = %#v, want exactly one recognized API-token scope", operation.OperationID, scopes)
			}
			if operation.Security == nil || len(*operation.Security) == 0 {
				t.Errorf("%s is not explicitly protected", operation.OperationID)
			}
		}
	}
}

func TestAPITokenScopeVocabularyIsExact(t *testing.T) {
	doc := loadContract(t)
	schema := doc.Components.Schemas["APITokenScope"]
	if schema == nil || schema.Value == nil {
		t.Fatal("APITokenScope is missing")
	}
	got := make(map[string]bool, len(schema.Value.Enum))
	for _, value := range schema.Value.Enum {
		scope, ok := value.(string)
		if !ok {
			t.Fatalf("APITokenScope contains non-string value %#v", value)
		}
		got[scope] = true
	}
	if len(got) != len(recognizedAPITokenScopes) {
		t.Fatalf("APITokenScope count = %d, want %d: %v", len(got), len(recognizedAPITokenScopes), got)
	}
	for scope := range recognizedAPITokenScopes {
		if !got[scope] {
			t.Errorf("APITokenScope omits %s", scope)
		}
	}
}

func TestAPITokenScopeCapacityCoversRecognizedVocabulary(t *testing.T) {
	doc := loadContract(t)
	for schemaName, propertyName := range map[string]string{
		"CreateAPITokenRequest": "scopes",
		"UpdateAPITokenRequest": "scopes",
		"APIToken":              "scopes",
	} {
		schema := doc.Components.Schemas[schemaName]
		if schema == nil || schema.Value == nil || schema.Value.Properties[propertyName] == nil {
			t.Fatalf("%s.%s is missing", schemaName, propertyName)
		}
		maxItems := schema.Value.Properties[propertyName].Value.MaxItems
		if maxItems == nil {
			t.Errorf("%s.%s maxItems is unset, want at least %d", schemaName, propertyName, len(recognizedAPITokenScopes))
		} else if *maxItems < uint64(len(recognizedAPITokenScopes)) {
			t.Errorf("%s.%s maxItems = %d, want at least %d", schemaName, propertyName, *maxItems, len(recognizedAPITokenScopes))
		}
	}
}

func TestAnonymousOperationsAreExplicit(t *testing.T) {
	doc := loadContract(t)
	want := []string{"createSession", "enrollAgent", "getPublicStatusPage"}
	var got []string
	for _, item := range doc.Paths.Map() {
		for _, operation := range operations(item) {
			if operation != nil && isAnonymous(operation) {
				got = append(got, operation.OperationID)
			}
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("anonymous operations = %v, want %v", got, want)
	}
}

func TestRetryableMutationsDeclareIdempotencyKey(t *testing.T) {
	doc := loadContract(t)
	for _, operationID := range []string{
		"createAPIToken", "createLocation", "createMonitor", "updateMonitor",
		"createAgentEnrollmentToken", "rotateAgentCredential",
		"upsertDiscoveryCandidates", "promoteDiscoveryCandidate",
		"createNotificationChannel", "updateNotificationChannel",
		"createNotificationRoute", "updateNotificationRoute", "replayNotificationDelivery",
		"createMaintenance", "endMaintenance",
	} {
		operation := operationByID(t, doc, operationID)
		if !hasParameter(operation.Parameters, "header", "Idempotency-Key") {
			t.Errorf("%s does not accept Idempotency-Key", operationID)
		}
	}
}

func TestAgentGenerationSubset(t *testing.T) {
	doc := loadContract(t)
	want := []string{
		"enrollAgent", "heartbeatAgent", "leaseAgentWork", "uploadProbeResults",
		"upsertDiscoveryCandidates",
	}
	configPath := filepath.Join("..", "agent", "oapi-codegen.yaml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(config)
	for _, operationID := range want {
		if !strings.Contains(text, "- "+operationID+"\n") {
			t.Errorf("Agent config omits %s", operationID)
		}
		operation := operationByID(t, doc, operationID)
		if operation == nil {
			t.Errorf("contract omits Agent operation %s", operationID)
		}
	}
	for _, forbidden := range []string{"listAgents", "listMonitors", "getPublicStatusPage", "createAPIToken"} {
		if strings.Contains(text, "- "+forbidden+"\n") {
			t.Errorf("Agent config includes forbidden operation %s", forbidden)
		}
	}
	if _, err := os.Stat("oapi-codegen-agent.yaml"); !os.IsNotExist(err) {
		t.Errorf("api/oapi-codegen-agent.yaml is a second Agent generation source")
	}
}

func TestProductionStrictGenerationIncludesFrozenOperations(t *testing.T) {
	config, err := os.ReadFile("oapi-codegen-server.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "include-operation-ids:") {
		t.Fatal("production strict generation is filtered instead of covering the canonical contract")
	}
}

func TestCursorPagesUsePageMetadataEnvelope(t *testing.T) {
	doc := loadContract(t)
	metadata := doc.Components.Schemas["PageMetadata"]
	if metadata == nil || metadata.Value == nil || metadata.Value.Properties["nextCursor"] == nil {
		t.Fatal("PageMetadata.nextCursor is missing")
	}
	for _, pageSchema := range []string{
		"APITokenPage", "LocationPage", "MonitorPage", "AgentPage", "IncidentPage",
		"IncidentEventPage", "DiscoveryCandidatePage", "NotificationChannelPage",
		"NotificationRoutePage", "NotificationDeliveryPage", "MaintenancePage",
	} {
		page := doc.Components.Schemas[pageSchema]
		if page == nil || page.Value == nil || page.Value.Properties["page"] == nil {
			t.Errorf("%s.page is missing", pageSchema)
			continue
		}
		if page.Value.Properties["nextCursor"] != nil {
			t.Errorf("%s keeps nextCursor outside the page envelope", pageSchema)
		}
	}
}

func TestPublicStatusPageHasRecentUptimeWithoutPrivateFields(t *testing.T) {
	doc := loadContract(t)
	for _, schema := range []string{"PublicStatusPage", "PublicStatusMonitor", "PublicIncidentSummary", "DailyUptimePoint"} {
		if doc.Components.Schemas[schema] == nil {
			t.Errorf("%s is missing", schema)
		}
	}
	monitor := doc.Components.Schemas["PublicStatusMonitor"]
	if monitor != nil && monitor.Value != nil && monitor.Value.Properties["recentUptime"] == nil {
		t.Error("PublicStatusMonitor.recentUptime is missing")
	}
	for _, schemaName := range []string{"PublicStatusPage", "PublicStatusMonitor", "PublicIncidentSummary", "DailyUptimePoint"} {
		schema := doc.Components.Schemas[schemaName]
		if schema == nil || schema.Value == nil {
			continue
		}
		for _, forbidden := range []string{"probe", "configuration", "credential", "diagnosticSample"} {
			if schema.Value.Properties[forbidden] != nil {
				t.Errorf("%s exposes private field %s", schemaName, forbidden)
			}
		}
	}
}

func TestDiscoveryBatchIsBounded(t *testing.T) {
	doc := loadContract(t)
	batch := doc.Components.Schemas["DiscoveryCandidateBatch"]
	if batch == nil || batch.Value == nil {
		t.Fatal("DiscoveryCandidateBatch is missing")
	}
	candidates := batch.Value.Properties["candidates"]
	if candidates == nil || candidates.Value == nil || candidates.Value.MaxItems == nil ||
		candidates.Value.MinItems != 1 || *candidates.Value.MaxItems != 100 {
		t.Fatalf("discovery candidates bound = %#v", candidates)
	}
}

func TestProblemResponseFollowsRFC9457(t *testing.T) {
	doc := loadContract(t)
	response := doc.Components.Responses["Problem"]
	if response == nil || response.Value == nil || response.Value.Content.Get("application/problem+json") == nil {
		t.Fatal("Problem response is not application/problem+json")
	}
	problem := doc.Components.Schemas["Problem"]
	if problem == nil || problem.Value == nil {
		t.Fatal("Problem schema is missing")
	}
	for _, field := range []string{"type", "title", "status", "code", "correlationId"} {
		if !slices.Contains(problem.Value.Required, field) {
			t.Errorf("Problem does not require %s", field)
		}
	}
}

func isAgentOperation(operation *openapi3.Operation) bool {
	return slices.Contains(operation.Tags, "agent")
}

func isAnonymous(operation *openapi3.Operation) bool {
	return operation.Security != nil && len(*operation.Security) == 0
}

func operationForMethod(item *openapi3.PathItem, method string) *openapi3.Operation {
	switch method {
	case http.MethodGet:
		return item.Get
	case http.MethodPost:
		return item.Post
	case http.MethodPatch:
		return item.Patch
	case http.MethodPut:
		return item.Put
	case http.MethodDelete:
		return item.Delete
	default:
		return nil
	}
}

func operations(item *openapi3.PathItem) []*openapi3.Operation {
	return []*openapi3.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete}
}

func parameter(parameters openapi3.Parameters, in, name string) *openapi3.Parameter {
	for _, reference := range parameters {
		if reference.Value != nil && reference.Value.In == in && reference.Value.Name == name {
			return reference.Value
		}
	}
	return nil
}

func hasParameter(parameters openapi3.Parameters, in, name string) bool {
	return parameter(parameters, in, name) != nil
}

func stringExtension(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}
