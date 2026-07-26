package manifest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDsAreStructurallyValidAndBoundStatus(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate CRD test")
	}
	base := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../config/crd/bases"))
	for _, filename := range []string{"monitoring.xisnove.io_monitors.yaml", "monitoring.xisnove.io_agents.yaml"} {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join(base, filename))
			if err != nil {
				t.Fatal(err)
			}
			external := &apiextensionsv1.CustomResourceDefinition{}
			if err := yaml.Unmarshal(contents, external); err != nil {
				t.Fatal(err)
			}
			internal := &apiextensions.CustomResourceDefinition{}
			if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(external, internal, nil); err != nil {
				t.Fatal(err)
			}
			// The apiserver strategy initializes this status field on create;
			// generated installation manifests correctly omit status.
			internal.Status.StoredVersions = []string{external.Spec.Versions[0].Name}
			if errs := apiextensionsvalidation.ValidateCustomResourceDefinition(context.Background(), internal); len(errs) != 0 {
				t.Fatalf("invalid CRD: %v", errs.ToAggregate())
			}
			conditions := external.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["conditions"]
			if conditions.MaxItems == nil || *conditions.MaxItems > 8 {
				t.Fatalf("status.conditions maxItems = %v, want at most 8", conditions.MaxItems)
			}
		})
	}
}

func TestAgentCRDRestrictsCapabilitiesAndDiscoveryResources(t *testing.T) {
	t.Parallel()

	_, currentFile, _, _ := runtime.Caller(0)
	filename := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../config/crd/bases/monitoring.xisnove.io_agents.yaml"))
	contents, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.Unmarshal(contents, crd); err != nil {
		t.Fatal(err)
	}
	spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
	capabilities := spec.Properties["capabilities"].Items.Schema
	if capabilities == nil || len(capabilities.Enum) != 5 {
		t.Fatalf("capability enum = %#v, want five supported capabilities", capabilities)
	}
	resources := spec.Properties["discovery"].Properties["resources"].Items.Schema
	if resources == nil || len(resources.Enum) != 6 {
		t.Fatalf("discovery resource enum = %#v, want six read-only resource kinds", resources)
	}
}
