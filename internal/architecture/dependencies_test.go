package architecture_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	packageDir := filepath.Join(repositoryRoot(t), "application", "port")
	packages, err := parser.ParseDir(token.NewFileSet(), packageDir, func(info os.FileInfo) bool {
		return strings.HasSuffix(info.Name(), ".go") && !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse public ports: %v", err)
	}
	publicPorts, ok := packages["port"]
	if !ok {
		t.Fatal("public port package was not parsed")
	}

	operational := map[string]bool{
		"UnitOfWork":   true,
		"Store":        true,
		"Repositories": true,
	}
	for _, file := range publicPorts.Files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if typeSpec.Name.Name != "Repositories" {
					continue
				}
				repositories, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					t.Fatal("Repositories must remain a struct")
				}
				for _, field := range repositories.Fields.List {
					if identifier, ok := field.Type.(*ast.Ident); ok {
						operational[identifier.Name] = true
					}
				}
			}
		}
	}

	for _, file := range publicPorts.Files {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				name := strings.ToLower(typeSpec.Name.Name)
				if !strings.Contains(name, "analytics") && !strings.Contains(name, "archive") {
					continue
				}
				if typeSpec.Assign.IsValid() {
					t.Fatalf("%s must be a distinct interface declaration, not an alias", typeSpec.Name)
				}
				interfaceType, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					t.Fatalf("%s must be an interface distinct from operational persistence", typeSpec.Name)
				}
				for _, method := range interfaceType.Methods.List {
					var reused string
					ast.Inspect(method.Type, func(node ast.Node) bool {
						identifier, ok := node.(*ast.Ident)
						if ok && operational[identifier.Name] {
							reused = identifier.Name
							return false
						}
						return true
					})
					if reused != "" {
						t.Fatalf("%s reuses operational declaration %s", typeSpec.Name, reused)
					}
				}
			}
		}
	}
}

func TestOpenCoreGuideDocumentsStablePublicSurface(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "docs", "architecture", "open-core.md"))
	if err != nil {
		t.Fatalf("read Open Core guide: %v", err)
	}
	for _, identifier := range []string{
		"## Stable public identifiers",
		"`application/port.UnitOfWork`",
		"`application/port.Repositories`",
		"`application/port.ErrNotFound`",
		"`application/port.ErrConflict`",
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
