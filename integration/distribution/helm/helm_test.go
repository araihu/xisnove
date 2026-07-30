package helm_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProfileRenderings(t *testing.T) {
	tests := []struct {
		name        string
		values      string
		mustHave    []string
		mustNotHave []string
	}{
		{
			name: "sqlite", values: "sqlite-values.yaml",
			mustHave: []string{
				"kind: StatefulSet", "podManagementPolicy: OrderedReady", "name: migrate",
				"--phase", "expand", "mountPath: /var/lib/xisnove", "volumeClaimTemplates:", "name: data",
			},
			mustNotHave: []string{"helm.sh/hook: pre-install,pre-upgrade"},
		},
		{
			name: "postgres", values: "postgres-values.yaml",
			mustHave: []string{
				"kind: Deployment", "replicas: 3", "helm.sh/hook: pre-install,pre-upgrade",
				"secretName: xisnove-postgres", "key: url",
			},
		},
		{
			name: "managed Turso", values: "turso-managed-values.yaml",
			mustHave: []string{
				"kind: Deployment", "replicas: 3", "helm.sh/hook: pre-install,pre-upgrade",
				"secretName: xisnove-turso", "key: auth-token", "--database-auth-token-file",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, err := helm(t, "template", "xisnove", chart(t), "--namespace", "monitoring", "--values", filepath.Join(directory(t), test.values))
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range test.mustHave {
				if !strings.Contains(manifest, value) {
					t.Errorf("manifest missing %q", value)
				}
			}
			for _, value := range test.mustNotHave {
				if strings.Contains(manifest, value) {
					t.Errorf("manifest unexpectedly contains %q", value)
				}
			}
		})
	}
}

func TestSQLiteRejectsMultipleReplicas(t *testing.T) {
	_, err := helm(t, "template", "xisnove", chart(t), "--set", "database.profile=sqlite", "--set", "server.replicas=2")
	if err == nil || !strings.Contains(strings.ReplaceAll(err.Error(), "/", "."), "server.replicas") {
		t.Fatalf("error = %v, want SQLite singleton refusal", err)
	}
}

func TestInvalidAndIncompleteValuesFailClosed(t *testing.T) {
	tests := [][]string{
		{"--set", "database.profile=unknown"},
		{"--set", "database.profile=postgres", "--set", "database.postgres.existingSecret.name="},
		{"--set", "database.profile=tursoManaged", "--set", "database.tursoManaged.existingSecret.name="},
		{"--set", "ingress.enabled=true", "--set", "gateway.enabled=true"},
		{"--set", "server.secretValue=must-never-exist"},
	}
	for _, args := range tests {
		_, err := helm(t, append([]string{"template", "xisnove", chart(t)}, args...)...)
		if err == nil {
			t.Fatalf("helm template %v unexpectedly succeeded", args)
		}
	}
}

func TestCompleteExistingSecretMatrix(t *testing.T) {
	manifest, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "turso-managed-values.yaml"), "--set", "agent.enabled=true")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"secretName: xisnove-server", "key: cursor-signing-key", "key: notification-master-key",
		"secretName: xisnove-admin", "key: password", "secretName: xisnove-ui", "key: cookie-secret",
		"secretName: xisnove-turso", "key: url", "key: auth-token",
		"secretName: xisnove-agent", "key: credential",
	} {
		if !strings.Contains(manifest, value) {
			t.Errorf("secret matrix missing %q", value)
		}
	}
}

func TestAgentEnrollmentUsesSamePodPVCInitContainer(t *testing.T) {
	manifest, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"),
		"--set", "agent.enabled=true", "--set", "agent.enrollment.enabled=true", "--set", "agent.enrollment.existingSecret.name=xisnove-agent-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"kind: StatefulSet", "app.kubernetes.io/component: agent", "name: enroll", "- enroll",
		"--token-file", "/var/run/secrets/xisnove/agent-enrollment/token", "--credential-file", "/var/lib/xisnove-agent/credential.json",
		"--name", "colocated-public", "--capabilities", "--timeout", "30s", "optional: true",
		"volumeClaimTemplates:", "name: credential-data", "mountPath: /var/lib/xisnove-agent",
	} {
		if !strings.Contains(manifest, value) {
			t.Errorf("enrollment manifest missing %q", value)
		}
	}
	for _, forbidden := range []string{"app.kubernetes.io/component: enrollment", "kind: Role", "kind: RoleBinding", "kind: Secret\n"} {
		if strings.Contains(manifest, forbidden) {
			t.Errorf("enrollment manifest unexpectedly contains %q", forbidden)
		}
	}
}

func TestAgentEnrollmentRequiresTokenSecretReference(t *testing.T) {
	_, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"), "--set", "agent.enabled=true", "--set", "agent.enrollment.enabled=true", "--set", "agent.enrollment.existingSecret.name=")
	if err == nil || !strings.Contains(err.Error(), "agent.enrollment.existingSecret.name") {
		t.Fatalf("error = %v, want enrollment token Secret refusal", err)
	}
}

func TestPortsProbesHooksAndSecrets(t *testing.T) {
	manifest, err := helm(t, "template", "xisnove", chart(t), "--namespace", "monitoring", "--values", filepath.Join(directory(t), "postgres-values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"containerPort: 8080", "name: http", "path: /livez", "path: /readyz",
		"containerPort: 8081", "XISNOVE_UI_COOKIE_SECRET_FILE", "cursor-signing-key",
		"notification-master-key", "name: admin-secret", "helm.sh/hook-weight: \"-20\"",
		"activeDeadlineSeconds: 300", "backoffLimit: 2", "--timeout", "240s", "runAsUser: 101",
		"runAsGroup: 101", "fsGroup: 101", "defaultMode: 0440",
	} {
		if !strings.Contains(manifest, value) {
			t.Errorf("manifest missing %q", value)
		}
	}
	for _, secret := range []string{"cursor-value-must-not-render", "cookie-value-must-not-render", "password-value-must-not-render", "database-password-must-not-render"} {
		if strings.Contains(manifest, secret) {
			t.Errorf("rendered secret literal %q", secret)
		}
	}
	if strings.Contains(manifest, "kind: Secret\n") {
		t.Error("chart rendered a Secret resource")
	}
}

func TestDatabaseURLUsesSafeProfileSpecificInput(t *testing.T) {
	sqlite, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "sqlite-values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlite, "- --database-url\n") || strings.Contains(sqlite, "--database-url-file") {
		t.Error("SQLite must pass its non-secret PVC path directly")
	}
	postgres, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(postgres, "--database-url-file") || strings.Contains(postgres, "- --database-url\n") {
		t.Error("remote profile must load database URL from mounted existingSecret file")
	}
}

func TestHookJobsDoNotDependOnChartManagedServiceAccount(t *testing.T) {
	manifest, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"), "--show-only", "templates/migration-job.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(manifest, "serviceAccountName:") {
		t.Error("pre-install hook references a ServiceAccount that Helm has not created")
	}
	if strings.Contains(manifest, "migrate-1") {
		t.Error("revision-suffixed hook name prevents before-hook-creation cleanup of prior failures")
	}
	if !strings.Contains(manifest, "automountServiceAccountToken: false") {
		t.Error("hook must disable API credential projection")
	}
}

func TestExistingServiceAccountRequiresName(t *testing.T) {
	_, err := helm(t, "template", "xisnove", chart(t), "--set", "server.serviceAccount.create=false", "--set", "server.serviceAccount.name=")
	if err == nil || !strings.Contains(err.Error(), "server.serviceAccount.name") {
		t.Fatalf("error = %v, want existing ServiceAccount name refusal", err)
	}
}

func TestOptionalSurfacesRender(t *testing.T) {
	manifest, err := helm(t, "template", "xisnove", chart(t), "--namespace", "monitoring", "--values", filepath.Join(directory(t), "postgres-values.yaml"),
		"--set", "ingress.enabled=true", "--set", "ingress.host=xisnove.example.test",
		"--set", "networkPolicy.enabled=true", "--set", "pdb.enabled=true",
		"--set", "serviceMonitor.enabled=true", "--set", "agent.enabled=true")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"kind: Ingress", "kind: NetworkPolicy", "kind: PodDisruptionBudget", "kind: ServiceMonitor", "app.kubernetes.io/component: agent", "containerPort: 9090"} {
		if !strings.Contains(manifest, value) {
			t.Errorf("manifest missing %q", value)
		}
	}
	if !strings.Contains(manifest, "from:\n        - podSelector: {}") || strings.Contains(manifest, "namespaceSelector: {}") {
		t.Error("default NetworkPolicy must allow same-namespace ingress, not every namespace")
	}
}

func TestGatewayAndIngressTLSRender(t *testing.T) {
	gateway, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"), "--set", "gateway.enabled=true", "--set", "gateway.parentRef.name=public")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gateway, "kind: HTTPRoute") || !strings.Contains(gateway, "name: public") {
		t.Error("Gateway API route missing")
	}
	ingress, err := helm(t, "template", "xisnove", chart(t), "--values", filepath.Join(directory(t), "postgres-values.yaml"), "--set", "ingress.enabled=true", "--set", "ingress.host=xisnove.example.test", "--set", "ingress.tls.enabled=true", "--set", "ingress.tls.existingSecretName=xisnove-tls")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"kind: Ingress", "secretName: xisnove-tls", "xisnove.example.test"} {
		if !strings.Contains(ingress, value) {
			t.Errorf("Ingress missing %q", value)
		}
	}
}

func TestChartContainsNoSecretValueInputs(t *testing.T) {
	root := chart(t)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lower := strings.ToLower(string(contents))
		for _, forbidden := range []string{"secretvalue", "passwordvalue", "authtokenvalue", "cookievalue"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("%s contains forbidden literal value field %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackagedChartContainsLicenseAndNotice(t *testing.T) {
	destination := t.TempDir()
	if _, err := helm(t, "package", chart(t), "--destination", destination); err != nil {
		t.Fatal(err)
	}
	archives, err := filepath.Glob(filepath.Join(destination, "xisnove-*.tgz"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("packaged charts = %v, error = %v", archives, err)
	}
	file, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	found := map[string][]byte{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "xisnove/LICENSE" || header.Name == "xisnove/NOTICE" {
			contents, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			found[header.Name] = contents
		}
	}
	for _, name := range []string{"xisnove/LICENSE", "xisnove/NOTICE"} {
		contents, ok := found[name]
		if !ok {
			t.Errorf("package missing %s", name)
			continue
		}
		source, err := os.ReadFile(filepath.Join(chart(t), "..", "..", filepath.Base(name)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(contents, source) {
			t.Errorf("packaged %s differs from repository source", name)
		}
	}
}

func helm(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := exec.Command("helm", args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err != nil {
		return "", &helmError{err: err, output: stderr.String()}
	}
	return stdout.String(), nil
}

type helmError struct {
	err    error
	output string
}

func (e *helmError) Error() string { return e.err.Error() + ": " + e.output }

func directory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test directory")
	}
	return filepath.Dir(file)
}

func chart(t *testing.T) string {
	t.Helper()
	return filepath.Clean(filepath.Join(directory(t), "..", "..", "..", "charts", "xisnove"))
}
