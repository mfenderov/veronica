package goarch

import (
	"fmt"
	"testing"
)

type fakeTester struct {
	errors []string
}

func (f *fakeTester) Errorf(format string, args ...any) {
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fakeTester) Helper() {}

func TestLayer_CreatesRule(t *testing.T) {
	t.Parallel()
	rule := Layer("domain", "internal/domain")

	if rule.name != "domain" {
		t.Errorf("expected name 'domain', got %q", rule.name)
	}
	if rule.pkg != "internal/domain" {
		t.Errorf("expected pkg 'internal/domain', got %q", rule.pkg)
	}
}

func TestMustNotImport_AppendsForbidden(t *testing.T) {
	t.Parallel()
	rule := Layer("domain", "internal/domain").
		MustNotImport("internal/registry", "internal/transport")

	if len(rule.mustNotImport) != 2 {
		t.Fatalf("expected 2 forbidden, got %d", len(rule.mustNotImport))
	}
	if rule.mustNotImport[0] != "internal/registry" {
		t.Errorf("expected 'internal/registry', got %q", rule.mustNotImport[0])
	}
	if rule.mustNotImport[1] != "internal/transport" {
		t.Errorf("expected 'internal/transport', got %q", rule.mustNotImport[1])
	}
}

func TestMayOnlyImport_SetsAllowlist(t *testing.T) {
	t.Parallel()
	rule := Layer("registry", "internal/registry").
		MayOnlyImport("internal/domain")

	if len(rule.mayOnlyImport) != 1 {
		t.Fatalf("expected 1 allowed, got %d", len(rule.mayOnlyImport))
	}
	if rule.mayOnlyImport[0] != "internal/domain" {
		t.Errorf("expected 'internal/domain', got %q", rule.mayOnlyImport[0])
	}
}

func TestCheckRule_MustNotImport_ReportsViolation(t *testing.T) {
	t.Parallel()
	ft := &fakeTester{}
	rule := Layer("domain", "internal/domain").
		MustNotImport("internal/registry")

	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/internal/registry", "github.com/mfenderov/veronica/internal/other"}, "github.com/mfenderov/veronica")

	if len(ft.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(ft.errors), ft.errors)
	}
	expected := `goarch: layer "domain" (internal/domain) must not import "internal/registry", but does`
	if ft.errors[0] != expected {
		t.Errorf("expected %q, got %q", expected, ft.errors[0])
	}
}

func TestCheckRule_MayOnlyImport_ReportsViolation(t *testing.T) {
	t.Parallel()
	ft := &fakeTester{}
	rule := Layer("registry", "internal/registry").
		MayOnlyImport("internal/domain")

	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/internal/domain", "github.com/mfenderov/veronica/internal/transport"}, "github.com/mfenderov/veronica")

	if len(ft.errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(ft.errors), ft.errors)
	}
	expected := `goarch: layer "registry" (internal/registry) may only import [internal/domain], but also imports "internal/transport"`
	if ft.errors[0] != expected {
		t.Errorf("expected %q, got %q", expected, ft.errors[0])
	}
}

func TestCheckRule_CleanImports_NoErrors(t *testing.T) {
	t.Parallel()
	ft := &fakeTester{}
	rule := Layer("domain", "internal/domain").
		MustNotImport("internal/registry")

	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/internal/other", "github.com/external/lib"}, "github.com/mfenderov/veronica")

	if len(ft.errors) != 0 {
		t.Errorf("expected no errors, got %v", ft.errors)
	}
}

func TestCheckRule_MayOnlyImport_AllowsExternalImports(t *testing.T) {
	t.Parallel()
	ft := &fakeTester{}
	rule := Layer("registry", "internal/registry").
		MayOnlyImport("internal/domain")

	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/internal/domain", "github.com/external/lib"}, "github.com/mfenderov/veronica")

	if len(ft.errors) != 0 {
		t.Errorf("expected no errors for external imports, got %v", ft.errors)
	}
}

func TestCheckRule_NoConstraints_NoErrors(t *testing.T) {
	t.Parallel()
	ft := &fakeTester{}
	rule := Layer("cmd", "cmd/veronica")

	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/cmd/veronica"}, "github.com/mfenderov/veronica")

	if len(ft.errors) != 0 {
		t.Errorf("expected no errors with no constraints, got %v", ft.errors)
	}
}

func TestFindModuleDir(t *testing.T) {
	t.Parallel()
	dir, err := findModuleDir(".")
	if err != nil {
		t.Fatalf("findModuleDir: %v", err)
	}
	if _, err := findModulePath(dir); err != nil {
		t.Errorf("expected go.mod in %s: %v", dir, err)
	}
}

func TestFindModulePath(t *testing.T) {
	t.Parallel()
	dir, err := findModuleDir(".")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := findModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mod != "github.com/mfenderov/veronica" {
		t.Errorf("expected 'github.com/mfenderov/veronica', got %q", mod)
	}
}

func TestFindModuleDir_NotFound(t *testing.T) {
	t.Parallel()
	_, err := findModuleDir(t.TempDir())
	if err == nil {
		t.Error("expected error for dir with no go.mod")
	}
}

func TestLoadInternalImports(t *testing.T) {
	t.Parallel()
	dir, err := findModuleDir(".")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := findModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := loadInternalImports(dir, mod, "tools/goarch")
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 0 {
		t.Errorf("expected no internal imports for tools/goarch, got %v", imports)
	}
}

func TestCheckAllowlist_ZeroArgs_RejectsInternalImport(t *testing.T) {
	t.Parallel()
	rule := Layer("domain", "internal/domain").MayOnlyImport()
	ft := &fakeTester{}
	checkRule(ft, rule, []string{"github.com/mfenderov/veronica/internal/registry"}, "github.com/mfenderov/veronica")
	if len(ft.errors) != 1 {
		t.Fatalf("zero-arg MayOnlyImport should reject internal import, got %d errors: %v", len(ft.errors), ft.errors)
	}
}

func TestCheck_Integration(t *testing.T) {
	t.Parallel()
	Check(t,
		Layer("goarch", "tools/goarch").
			MustNotImport("internal/domain", "internal/registry"),
	)
}

func TestLoadInternalImports_InvalidPattern(t *testing.T) {
	t.Parallel()
	dir, err := findModuleDir(".")
	if err != nil {
		t.Fatal(err)
	}
	mod, err := findModulePath(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadInternalImports("/nonexistent-goarch-dir", mod, "tools/goarch")
	if err == nil {
		t.Fatal("expected packages.Load to fail for a bogus working directory")
	}
}
