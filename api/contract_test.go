package api_test

import (
	"context"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestContractIsOpenAPI312AndValid(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		t.Fatal(err)
	}
	if doc.OpenAPI != "3.1.2" {
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
		"getActiveMonitorIncident": false,
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
