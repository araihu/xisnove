package api_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestMilestone4OperationFamiliesAreFrozen(t *testing.T) {
	doc := loadContract(t)
	want := map[string]struct {
		method string
		path   string
	}{
		"createSession":                  {http.MethodPost, "/v1/sessions"},
		"revokeSession":                  {http.MethodDelete, "/v1/sessions/current"},
		"createAPIToken":                 {http.MethodPost, "/v1/api-tokens"},
		"listAPITokens":                  {http.MethodGet, "/v1/api-tokens"},
		"getAPIToken":                    {http.MethodGet, "/v1/api-tokens/{apiTokenId}"},
		"updateAPIToken":                 {http.MethodPatch, "/v1/api-tokens/{apiTokenId}"},
		"revokeAPIToken":                 {http.MethodDelete, "/v1/api-tokens/{apiTokenId}"},
		"listLocations":                  {http.MethodGet, "/v1/locations"},
		"getLocation":                    {http.MethodGet, "/v1/locations/{locationId}"},
		"updateLocation":                 {http.MethodPatch, "/v1/locations/{locationId}"},
		"disableLocation":                {http.MethodDelete, "/v1/locations/{locationId}"},
		"listMonitors":                   {http.MethodGet, "/v1/monitors"},
		"updateMonitor":                  {http.MethodPatch, "/v1/monitors/{monitorId}"},
		"disableMonitor":                 {http.MethodDelete, "/v1/monitors/{monitorId}"},
		"listAgents":                     {http.MethodGet, "/v1/agents"},
		"getAgent":                       {http.MethodGet, "/v1/agents/{agentId}"},
		"updateAgent":                    {http.MethodPatch, "/v1/agents/{agentId}"},
		"disableAgent":                   {http.MethodDelete, "/v1/agents/{agentId}"},
		"listIncidents":                  {http.MethodGet, "/v1/incidents"},
		"getIncident":                    {http.MethodGet, "/v1/incidents/{incidentId}"},
		"listIncidentEvents":             {http.MethodGet, "/v1/incidents/{incidentId}/events"},
		"upsertDiscoveryCandidatesBatch": {http.MethodPost, "/v1/agent/discovery-candidates:batch"},
		"listDiscoveryCandidates":        {http.MethodGet, "/v1/discovery-candidates"},
		"getDiscoveryCandidate":          {http.MethodGet, "/v1/discovery-candidates/{candidateId}"},
		"promoteDiscoveryCandidate":      {http.MethodPost, "/v1/discovery-candidates/{candidateId}:promote"},
		"getPublicStatus":                {http.MethodGet, "/v1/status"},
	}

	for operationID, expected := range want {
		operation := operationByID(t, doc, operationID)
		item := doc.Paths.Value(expected.path)
		if item == nil {
			t.Errorf("%s path %s is missing", operationID, expected.path)
			continue
		}
		if operationForMethod(item, expected.method) != operation {
			t.Errorf("%s is not %s %s", operationID, expected.method, expected.path)
		}
	}
}

func TestContractSecurityIsExplicitAndDenyByDefault(t *testing.T) {
	doc := loadContract(t)
	if len(doc.Security) == 0 {
		t.Fatal("top-level security must deny unauthenticated access by default")
	}

	public := map[string]bool{
		"createSession":   true,
		"enrollAgent":     true,
		"getPublicStatus": true,
	}
	for _, item := range doc.Paths.Map() {
		for _, operation := range operations(item) {
			if operation == nil {
				continue
			}
			if operation.Security == nil {
				t.Errorf("%s inherits security instead of declaring it explicitly", operation.OperationID)
				continue
			}
			scopes, ok := stringExtension(operation.Extensions["x-xisnove-scopes"])
			if !ok {
				t.Errorf("%s has no x-xisnove-scopes declaration", operation.OperationID)
				continue
			}
			if public[operation.OperationID] {
				if len(*operation.Security) != 0 || len(scopes) != 0 {
					t.Errorf("%s public security = %#v, scopes = %#v", operation.OperationID, *operation.Security, scopes)
				}
				continue
			}
			if len(*operation.Security) == 0 || len(scopes) == 0 {
				t.Errorf("%s protected security = %#v, scopes = %#v", operation.OperationID, *operation.Security, scopes)
			}
		}
	}
}

func TestRetryableMutationsAcceptIdempotencyKeys(t *testing.T) {
	doc := loadContract(t)
	for _, operationID := range []string{
		"updateLocation", "updateMonitor", "updateAgent",
		"createAPIToken", "updateAPIToken",
		"upsertDiscoveryCandidatesBatch", "promoteDiscoveryCandidate",
	} {
		operation := operationByID(t, doc, operationID)
		if !hasParameter(operation.Parameters, "header", "Idempotency-Key") {
			t.Errorf("%s does not accept Idempotency-Key", operationID)
		}
	}
}

func TestListOperationsUseBoundedOpaqueCursors(t *testing.T) {
	doc := loadContract(t)
	for operationID, pageSchema := range map[string]string{
		"listAPITokens":              "APITokenPage",
		"listLocations":              "LocationPage",
		"listMonitors":               "MonitorPage",
		"listAgents":                 "AgentPage",
		"listIncidents":              "IncidentPage",
		"listIncidentEvents":         "IncidentEventPage",
		"listDiscoveryCandidates":    "DiscoveryCandidatePage",
		"listNotificationChannels":   "NotificationChannelPage",
		"listNotificationRoutes":     "NotificationRoutePage",
		"listNotificationDeliveries": "NotificationDeliveryPage",
		"listMaintenance":            "MaintenancePage",
	} {
		operation := operationByID(t, doc, operationID)
		if !hasParameter(operation.Parameters, "query", "cursor") {
			t.Errorf("%s has no opaque cursor", operationID)
		}
		limit := parameter(operation.Parameters, "query", "limit")
		if limit == nil || limit.Schema == nil || limit.Schema.Value == nil ||
			limit.Schema.Value.Max == nil || *limit.Schema.Value.Max > 100 {
			t.Errorf("%s limit is not bounded to 100: %#v", operationID, limit)
		}
		page := doc.Components.Schemas[pageSchema]
		if page == nil || page.Value == nil || page.Value.Properties["nextCursor"] == nil {
			t.Errorf("%s has no nextCursor", pageSchema)
		}
	}
}

func TestDiscoveryBatchIsBoundedAndPromotionIsExplicit(t *testing.T) {
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
	promotion := operationByID(t, doc, "promoteDiscoveryCandidate")
	if promotion.RequestBody == nil ||
		promotion.RequestBody.Value.Content.Get("application/json").Schema.Ref !=
			"#/components/schemas/PromoteDiscoveryCandidateRequest" {
		t.Fatalf("promotion request = %#v", promotion.RequestBody)
	}
}

func TestProblemResponseFollowsRFC9457(t *testing.T) {
	doc := loadContract(t)
	response := doc.Components.Responses["Problem"]
	if response == nil || response.Value == nil {
		t.Fatal("Problem response is missing")
	}
	if response.Value.Content.Get("application/problem+json") == nil {
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
	for _, field := range []string{"detail", "instance"} {
		if problem.Value.Properties[field] == nil {
			t.Errorf("Problem has no RFC 9457 %s member", field)
		}
	}
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
