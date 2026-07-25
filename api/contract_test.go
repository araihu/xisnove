package api_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestContractUsesLatestOAPICodegenSupportedOpenAPIAndIsValid(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.0.3" {
		t.Fatalf("OpenAPI = %q", doc.OpenAPI)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{
		"createSession": false, "createLocation": false, "createMonitor": false,
		"getMonitor": false, "createAgentEnrollmentToken": false,
		"enrollAgent": false, "heartbeatAgent": false, "leaseAgentWork": false,
		"uploadProbeResults": false, "getMonitorHealth": false,
		"getActiveMonitorIncident":  false,
		"createNotificationChannel": false, "listNotificationChannels": false,
		"getNotificationChannel": false, "updateNotificationChannel": false,
		"disableNotificationChannel": false, "createNotificationRoute": false,
		"listNotificationRoutes": false, "getNotificationRoute": false,
		"updateNotificationRoute": false, "disableNotificationRoute": false,
		"listNotificationDeliveries": false, "getNotificationDelivery": false,
		"replayNotificationDelivery": false, "createMaintenance": false,
		"listMaintenance": false, "getMaintenance": false,
		"deleteMaintenance": false, "endMaintenance": false,
	}
	for _, item := range doc.Paths.Map() {
		for _, op := range []*openapi3.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op != nil {
				if _, ok := want[op.OperationID]; ok {
					want[op.OperationID] = true
				}
			}
		}
	}
	for operationID, found := range want {
		if !found {
			t.Errorf("missing operationId %s", operationID)
		}
	}
}

func TestNotificationContractIsTypedRedactedBoundedAndAdminOnly(t *testing.T) {
	doc := loadContract(t)
	operations := []string{
		"createNotificationChannel", "listNotificationChannels", "getNotificationChannel",
		"updateNotificationChannel", "disableNotificationChannel", "createNotificationRoute",
		"listNotificationRoutes", "getNotificationRoute", "updateNotificationRoute",
		"disableNotificationRoute", "listNotificationDeliveries", "getNotificationDelivery",
		"replayNotificationDelivery", "createMaintenance", "listMaintenance",
		"getMaintenance", "deleteMaintenance", "endMaintenance",
	}
	for _, operationID := range operations {
		operation := operationByID(t, doc, operationID)
		if operation.Security == nil || len(*operation.Security) == 0 {
			t.Errorf("%s is not admin protected", operationID)
		}
	}

	configuration := doc.Components.Schemas["NotificationChannelConfigurationInput"].Value
	if configuration == nil || configuration.Discriminator == nil ||
		configuration.Discriminator.PropertyName != "kind" || len(configuration.OneOf) != 2 {
		t.Fatalf("notification configuration = %#v", configuration)
	}
	for schemaName, field := range map[string]string{
		"ShoutrrrChannelConfigurationInput":     "serviceUrl",
		"AlertmanagerChannelConfigurationInput": "bearerToken",
	} {
		schema := doc.Components.Schemas[schemaName].Value
		property := schema.Properties[field]
		if property == nil || property.Value == nil || !property.Value.WriteOnly {
			t.Errorf("%s.%s is not writeOnly", schemaName, field)
		}
	}
	channel := doc.Components.Schemas["NotificationChannel"].Value
	for _, forbidden := range []string{"configuration", "serviceUrl", "bearerToken", "ciphertext"} {
		if _, found := channel.Properties[forbidden]; found {
			t.Errorf("NotificationChannel reflects %s", forbidden)
		}
	}
	monitor := doc.Components.Schemas["Monitor"].Value
	for _, field := range []string{"description", "labels", "displayOrder", "public", "enabled"} {
		if monitor.Properties[field] == nil {
			t.Errorf("Monitor is missing %s", field)
		}
	}

	for _, operationID := range []string{
		"listNotificationChannels", "listNotificationRoutes",
		"listNotificationDeliveries", "listMaintenance",
	} {
		operation := operationByID(t, doc, operationID)
		parameters := map[string]*openapi3.Parameter{}
		for _, reference := range operation.Parameters {
			parameters[reference.Value.Name] = reference.Value
		}
		if parameters["limit"] == nil || parameters["offset"] == nil ||
			parameters["limit"].Schema.Value.Max == nil ||
			*parameters["limit"].Schema.Value.Max != 100 {
			t.Errorf("%s pagination = %#v", operationID, parameters)
		}
	}

	state := doc.Components.Schemas["NotificationDeliveryState"].Value
	wantStates := map[string]bool{
		"pending": true, "claimed": true, "retrying": true,
		"delivered": true, "permanent-failure": true, "suppressed": true,
	}
	if state == nil || len(state.Enum) != len(wantStates) {
		t.Fatalf("delivery states = %#v", state)
	}
	for _, value := range state.Enum {
		if text, ok := value.(string); !ok || !wantStates[text] {
			t.Errorf("unexpected delivery state %#v", value)
		}
	}
}

func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func operationByID(t *testing.T, doc *openapi3.T, operationID string) *openapi3.Operation {
	t.Helper()
	for _, item := range doc.Paths.Map() {
		for _, operation := range []*openapi3.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if operation != nil && operation.OperationID == operationID {
				return operation
			}
		}
	}
	t.Fatalf("operation %s is missing", operationID)
	return nil
}

func TestProbeDefinitionHasThreeDiscriminatedVariants(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	probeRef, ok := doc.Components.Schemas["ProbeDefinition"]
	if !ok || probeRef.Value == nil {
		t.Fatal("ProbeDefinition schema is missing")
	}
	probe := probeRef.Value
	if probe.Discriminator == nil || probe.Discriminator.PropertyName != "kind" {
		t.Fatalf("discriminator = %#v", probe.Discriminator)
	}
	if len(probe.OneOf) != 3 {
		t.Fatalf("variants = %d", len(probe.OneOf))
	}
	want := map[string]string{
		"http": "#/components/schemas/HTTPProbeDefinition",
		"tcp":  "#/components/schemas/TCPProbeDefinition",
		"dns":  "#/components/schemas/DNSProbeDefinition",
	}
	for kind, reference := range want {
		if probe.Discriminator.Mapping[kind].Ref != reference {
			t.Fatalf("mapping[%q] = %q", kind, probe.Discriminator.Mapping[kind].Ref)
		}
	}
}

func TestMonitorEndpointsUseProbeDefinition(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CreateMonitorRequest", "Monitor"} {
		schema := doc.Components.Schemas[name].Value
		if schema == nil {
			t.Fatalf("%s schema is missing", name)
		}
		probe, ok := schema.Properties["probe"]
		if !ok || probe.Ref != "#/components/schemas/ProbeDefinition" {
			t.Fatalf("%s probe = %#v", name, probe)
		}
		if _, legacy := schema.Properties["http"]; legacy {
			t.Fatalf("%s still exposes legacy http field", name)
		}
		required := false
		for _, field := range schema.Required {
			required = required || field == "probe"
		}
		if !required {
			t.Fatalf("%s probe is not required", name)
		}
	}
}

func TestLeaseResponseUsesProtocolNeutralWork(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := openapi3.NewLoader().LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	response := doc.Paths.Value("/v1/agent/work:lease").Post.Responses.Value("200")
	schema := response.Value.Content.Get("application/json").Schema
	if schema.Ref != "#/components/schemas/ProbeWork" {
		t.Fatalf("lease response schema = %q", schema.Ref)
	}
}
