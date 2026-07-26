package architecture_test

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestPublicPackagesRespectDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	for _, packagePath := range []string{"./domain", "./application", "./application/port"} {
		command := exec.CommandContext(
			context.Background(),
			"go", "list", "-deps", "-f", "{{.ImportPath}}", packagePath,
		)
		command.Dir = root
		command.Env = append(os.Environ(), "GOWORK=off")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", packagePath, err, output)
		}
		for _, forbidden := range []string{
			"github.com/araihu/xisnove/internal/adapters",
			"github.com/araihu/xisnove/db/generated",
		} {
			if bytes.Contains(output, []byte(forbidden)) {
				t.Fatalf("%s depends on forbidden package prefix %s", packagePath, forbidden)
			}
		}
		if packagePath == "./domain" && bytes.Contains(
			output,
			[]byte("github.com/araihu/xisnove/application"),
		) {
			t.Fatal("domain depends on application")
		}
	}
}

func TestApplicationDoesNotDiscardIncomingContext(t *testing.T) {
	applicationDir := filepath.Join(repositoryRoot(t), "application")
	err := filepath.WalkDir(applicationDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, discarded := range []string{"context.Background()", "context.TODO()"} {
			if bytes.Contains(contents, []byte(discarded)) {
				t.Errorf("%s discards caller context with %s", path, discarded)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyInternalPublicPackagesAreGone(t *testing.T) {
	root := repositoryRoot(t)
	for _, path := range []string{"internal/domain", "internal/application", "internal/adapters/conformance"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("legacy package path still exists: %s", path)
		}
	}
}

func TestAnalyticsArchivePortsRemainDistinctFromOperationalDeclarations(t *testing.T) {
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedTypes,
		Dir:  repositoryRoot(t),
		Env:  append(os.Environ(), "GOWORK=off"),
	}, "github.com/araihu/xisnove/application/port")
	if err != nil || packages.PrintErrors(loaded) != 0 || len(loaded) != 1 {
		t.Fatalf("load public ports: packages=%d err=%v", len(loaded), err)
	}
	violations := analyticsArchivePortViolations(loaded[0].Types)
	if len(violations) != 0 {
		t.Fatal(strings.Join(violations, "; "))
	}
}

func TestAnalyticsArchivePortValidationFixtures(t *testing.T) {
	const operational = `package port
type UnitOfWork interface { View() }
type Store interface { Save() }
type MonitorRepository interface { Get() }
type Repositories struct { Monitors MonitorRepository }
`
	tests := []struct {
		name        string
		declaration string
		wantInvalid bool
	}{
		{"independent", `type AnalyticsWriter interface { Append(string) error }`, false},
		{"names are harmless", `type AnalyticsWriter interface { Append(UnitOfWork string, Store int) error }`, false},
		{"non-interface", `type AnalyticsRecord struct{ Value string }`, true},
		{"direct alias", `type AnalyticsPort = UnitOfWork`, true},
		{"direct embed", `type AnalyticsPort interface { UnitOfWork }`, true},
		{"alias parameter", `type OperationalAlias = UnitOfWork; type AnalyticsPort interface { Use(OperationalAlias) }`, true},
		{"alias result", `type OperationalAlias = Store; type ArchivePort interface { Open() OperationalAlias }`, true},
		{"alias embed", `type OperationalAlias = UnitOfWork; type ArchivePort interface { OperationalAlias }`, true},
		{"repository parameter", `type ArchivePort interface { Store(MonitorRepository) }`, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fileset := token.NewFileSet()
			file, err := parser.ParseFile(fileset, "fixture.go", operational+test.declaration, 0)
			if err != nil {
				t.Fatal(err)
			}
			checked, err := (&types.Config{}).Check("fixture/port", fileset, []*ast.File{file}, nil)
			if err != nil {
				t.Fatal(err)
			}
			violations := analyticsArchivePortViolations(checked)
			if got := len(violations) != 0; got != test.wantInvalid {
				t.Fatalf("invalid = %v, want %v; violations = %v", got, test.wantInvalid, violations)
			}
		})
	}
}

type operationalDeclaration struct {
	name  string
	type_ types.Type
}

func analyticsArchivePortViolations(checked *types.Package) []string {
	scope := checked.Scope()
	operational := make([]operationalDeclaration, 0)
	for _, name := range []string{"UnitOfWork", "Store", "Repositories"} {
		object := scope.Lookup(name)
		if object == nil {
			return []string{fmt.Sprintf("required operational declaration %s is missing", name)}
		}
		operational = append(operational, operationalDeclaration{name, object.Type()})
	}
	repositories, ok := scope.Lookup("Repositories").Type().Underlying().(*types.Struct)
	if !ok {
		return []string{"Repositories must remain a struct"}
	}
	for index := range repositories.NumFields() {
		field := repositories.Field(index)
		operational = append(operational, operationalDeclaration{field.Type().String(), field.Type()})
	}

	var violations []string
	for _, name := range scope.Names() {
		lowerName := strings.ToLower(name)
		if !strings.Contains(lowerName, "analytics") && !strings.Contains(lowerName, "archive") {
			continue
		}
		object, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		if object.IsAlias() {
			violations = append(violations, name+" must be a distinct interface declaration, not an alias")
			continue
		}
		named, ok := object.Type().(*types.Named)
		if !ok {
			violations = append(violations, name+" must be a named interface")
			continue
		}
		interfaceType, ok := named.Underlying().(*types.Interface)
		if !ok {
			violations = append(violations, name+" must be an interface distinct from operational persistence")
			continue
		}
		interfaceType.Complete()
		for index := range interfaceType.NumEmbeddeds() {
			if reused := reusedOperationalType(interfaceType.EmbeddedType(index), operational); reused != "" {
				violations = append(violations, fmt.Sprintf("%s embeds operational declaration %s", name, reused))
			}
		}
		for index := range interfaceType.NumMethods() {
			method := interfaceType.Method(index)
			signature := method.Type().(*types.Signature)
			for _, tuple := range []*types.Tuple{signature.Params(), signature.Results()} {
				for item := range tuple.Len() {
					if reused := reusedOperationalType(tuple.At(item).Type(), operational); reused != "" {
						violations = append(violations, fmt.Sprintf("%s.%s reuses operational declaration %s", name, method.Name(), reused))
					}
				}
			}
		}
	}
	return violations
}

func reusedOperationalType(candidate types.Type, operational []operationalDeclaration) string {
	candidate = types.Unalias(candidate)
	for _, declaration := range operational {
		if types.Identical(candidate, types.Unalias(declaration.type_)) {
			return declaration.name
		}
	}
	switch typed := candidate.(type) {
	case *types.Pointer:
		return reusedOperationalType(typed.Elem(), operational)
	case *types.Slice:
		return reusedOperationalType(typed.Elem(), operational)
	case *types.Array:
		return reusedOperationalType(typed.Elem(), operational)
	case *types.Map:
		if reused := reusedOperationalType(typed.Key(), operational); reused != "" {
			return reused
		}
		return reusedOperationalType(typed.Elem(), operational)
	case *types.Chan:
		return reusedOperationalType(typed.Elem(), operational)
	}
	return ""
}

func TestOpenCoreGuideDocumentsStablePublicSurface(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs", "architecture", "open-core.md"))
	if err != nil {
		t.Fatalf("read Open Core guide: %v", err)
	}
	for _, identifier := range []string{
		"## Stable public identifiers",
		"`domain.NewLocation`",
		"`domain.NewAgent`",
		"`domain.NewHTTPMonitor`",
		"`domain.NewTCPMonitor`",
		"`domain.NewDNSMonitor`",
		"`domain.NewMaintenanceInterval`",
		"`domain.NewNotificationChannel`",
		"`domain.NewNotificationRoute`",
		"`domain.NewNotificationIdentity`",
		"`application/port.UnitOfWork`",
		"`application/port.Repositories`",
		"`application/port.ErrNotFound`",
		"`application/port.ErrConflict`",
		"`application.NewConfigurationService`",
		"`application.NewAuthService`",
		"`application.NewAgentService`",
		"`application.NewLeaseService`",
		"`application.NewResultService`",
		"`application.NewHealthService`",
		"`application.NewStalenessService`",
		"`application.NewStalenessServiceWithObserver`",
		"`application.NewScheduler`",
		"`application.NewNotificationAdminService`",
		"`application.NewNotificationSecretService`",
		"`application.NewDeliveryWorker`",
		"`application.NewMaintenanceWorker`",
		"`application.NewRetentionWorker`",
		"`application.ErrAlreadyBootstrapped`",
		"`application.ErrInvalidCredentials`",
		"`application.ErrInvalidEmail`",
		"`application.ErrWeakPassword`",
		"`application.ErrInvalidEnrollmentToken`",
		"`application.ErrNoWork`",
		"`application.ErrNotificationKeyUnavailable`",
		"`application.ErrNotificationLeaseLost`",
		"`application.ErrMaintenanceLeaseLost`",
		"`application.ErrRetentionLeaseLost`",
		"`contracttest.Factory`",
		"`contracttest.Run`",
		"application compatibility aliases",
	} {
		if !bytes.Contains(contents, []byte(identifier)) {
			t.Errorf("Open Core guide does not document %s", identifier)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
