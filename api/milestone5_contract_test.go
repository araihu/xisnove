package api_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// These tests protect the public boundary consumed by the Kubernetes operator.
// Removing an owner check, idempotency protection, or write-only credential
// would permit an operator retry to mutate an unrelated resource or disclose a
// bearer credential.
func TestOperatorMutationsFreezeOwnershipScopeAndIdempotency(t *testing.T) {
	doc := loadContract(t)
	adminScopes, ok := stringExtension(doc.Components.SecuritySchemes["adminBearer"].Value.Extensions["x-xisnove-scopes"])
	if !ok || slices.Contains(adminScopes, "operator:provision") {
		t.Fatalf("administrator credential must not advertise operator:provision: %#v", adminScopes)
	}
	want := map[string]struct {
		method string
		path   string
	}{
		"applyOperatorMonitor":          {http.MethodPost, "/v1/operator/monitors:apply"},
		"deleteOperatorMonitor":         {http.MethodPost, "/v1/operator/monitors:delete"},
		"applyOperatorAgent":            {http.MethodPost, "/v1/operator/agents:apply"},
		"putOperatorAgentCredential":    {http.MethodPut, "/v1/operator/agents/{agentId}/credentials/{generation}"},
		"revokeOperatorAgentCredential": {http.MethodPost, "/v1/operator/agents/{agentId}/credentials/{generation}:revoke"},
		"deleteOperatorAgent":           {http.MethodPost, "/v1/operator/agents:delete"},
	}
	for operationID, expected := range want {
		operation := operationByID(t, doc, operationID)
		item := doc.Paths.Value(expected.path)
		if item == nil || operationForMethod(item, expected.method) != operation {
			t.Errorf("%s is not %s %s", operationID, expected.method, expected.path)
		}
		idempotency := parameter(operation.Parameters, "header", "Idempotency-Key")
		if idempotency == nil || !idempotency.Required {
			t.Errorf("%s must require Idempotency-Key", operationID)
		}
		scopes, ok := stringExtension(operation.Extensions["x-xisnove-scopes"])
		if !ok || !slices.Equal(scopes, []string{"operator:provision"}) {
			t.Errorf("%s scopes = %#v, want operator:provision", operationID, scopes)
		}
		if operation.Security == nil || len(*operation.Security) != 1 || len((*operation.Security)[0]) != 1 ||
			(*operation.Security)[0]["apiTokenBearer"] == nil {
			t.Errorf("%s must accept only an API token", operationID)
		}
		if operation.Responses.Value("409") == nil || operation.Responses.Value("409").Ref != "#/components/responses/Problem" {
			t.Errorf("%s omits the RFC 9457 conflict response", operationID)
		}
	}
}

func TestOperatorAgentObservationIsOwnerProvenAndReadOnly(t *testing.T) {
	doc := loadContract(t)
	operation := operationByID(t, doc, "observeOperatorAgent")
	item := doc.Paths.Value("/v1/operator/agents:observe")
	if item == nil || item.Post != operation { t.Fatal("observe operation is not POST /v1/operator/agents:observe") }
	if parameter(operation.Parameters, "header", "Idempotency-Key") != nil { t.Fatal("observation must not require idempotency") }
	if operation.Security == nil || len(*operation.Security) != 1 || (*operation.Security)[0]["apiTokenBearer"] == nil { t.Fatal("observation must require provisioning API token") }
	request := requiredSchema(t, doc, "ObserveOperatorAgentRequest")
	if !slices.Contains(request.Required, "owner") || request.Properties["owner"].Ref != "#/components/schemas/ExternalOwner" { t.Fatal("observation must require owner") }
}

func TestOperatorOwnerAndCredentialSchemasProtectReconciliation(t *testing.T) {
	doc := loadContract(t)
	for _, name := range []string{
		"ApplyOperatorMonitorRequest", "DeleteOperatorMonitorRequest",
		"ApplyOperatorAgentRequest", "DeleteOperatorAgentRequest",
	} {
		schema := requiredSchema(t, doc, name)
		owner := schema.Properties["owner"]
		if owner == nil || owner.Ref != "#/components/schemas/ExternalOwner" {
			t.Errorf("%s.owner = %#v, want ExternalOwner", name, owner)
		}
	}
	owner := requiredSchema(t, doc, "ExternalOwner")
	for _, field := range []string{"key", "uid"} {
		if !slices.Contains(owner.Required, field) || owner.Properties[field] == nil {
			t.Errorf("ExternalOwner must require %s", field)
		}
	}
	for _, name := range []string{"DeleteOperatorMonitorRequest", "DeleteOperatorAgentRequest"} {
		schema := requiredSchema(t, doc, name)
		if schema.Properties["externalId"] == nil || slices.Contains(schema.Required, "externalId") {
			t.Errorf("%s.externalId must be optional", name)
		}
	}
	apply := requiredSchema(t, doc, "ApplyOperatorAgentRequest")
	initial := apply.Properties["initialCredential"]
	if initial == nil || initial.Ref != "#/components/schemas/OperatorInitialCredential" || !slices.Contains(apply.Required, "initialCredential") {
		t.Fatalf("ApplyOperatorAgentRequest must require initialCredential: %#v", initial)
	}
	for _, name := range []string{"OperatorInitialCredential", "PutOperatorAgentCredentialRequest"} {
		schema := requiredSchema(t, doc, name)
		credential := schema.Properties["credential"]
		if credential == nil || credential.Value == nil || !credential.Value.WriteOnly || !slices.Contains(schema.Required, "credential") {
			t.Errorf("%s must require a write-only credential", name)
		}
	}
	initialCredential := requiredSchema(t, doc, "OperatorInitialCredential")
	if !slices.Contains(initialCredential.Required, "generation") || initialCredential.Properties["generation"] == nil {
		t.Error("OperatorInitialCredential must require generation")
	}
	generation := initialCredential.Properties["generation"]
	if generation.Value == nil || generation.Value.Min == nil || generation.Value.Max == nil ||
		*generation.Value.Min != 1 || *generation.Value.Max != 1 {
		t.Errorf("OperatorInitialCredential.generation must be exactly 1: %#v", generation)
	}
	for _, response := range []string{"OperatorMonitorApplyResult", "OperatorAgentApplyResult"} {
		schema := requiredSchema(t, doc, response)
		if schema.Properties["credential"] != nil || schema.Properties["initialCredential"] != nil {
			t.Errorf("%s leaks credential material", response)
		}
	}
}

func TestDiscoveryBatchFreezesCompleteSnapshotEnvelope(t *testing.T) {
	doc := loadContract(t)
	batch := requiredSchema(t, doc, "DiscoveryCandidateBatch")
	for _, field := range []string{"candidates", "complete", "completedAt"} {
		if !slices.Contains(batch.Required, field) || batch.Properties[field] == nil {
			t.Errorf("DiscoveryCandidateBatch must require %s", field)
		}
	}
	candidates := batch.Properties["candidates"]
	if candidates == nil || candidates.Value == nil || candidates.Value.MinItems != 0 {
		t.Errorf("DiscoveryCandidateBatch must allow an empty complete snapshot: %#v", candidates)
	}
	complete := batch.Properties["complete"]
	if complete == nil || complete.Value == nil || complete.Value.Type == nil || !complete.Value.Type.Is("boolean") {
		t.Error("DiscoveryCandidateBatch.complete must be boolean")
	}
	completedAt := batch.Properties["completedAt"]
	if completedAt == nil || completedAt.Value == nil || completedAt.Value.Format != "date-time" {
		t.Errorf("DiscoveryCandidateBatch.completedAt = %#v, want date-time", completedAt)
	}
}

func requiredSchema(t *testing.T, doc *openapi3.T, name string) *openapi3.Schema {
	t.Helper()
	ref := doc.Components.Schemas[name]
	if ref == nil || ref.Value == nil {
		t.Fatalf("%s schema is missing", name)
	}
	return ref.Value
}
