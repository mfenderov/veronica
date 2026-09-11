// Package goarch provides architectural boundary and import rule validation for Go packages.
package goarch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// LayerRule defines architectural import constraints for a specific package layer.
type LayerRule struct {
	name          string
	pkg           string
	mustNotImport []string
	mayOnlyImport []string
}

// Layer creates a new LayerRule with the given descriptive name and package path.
func Layer(name, pkg string) *LayerRule {
	return &LayerRule{name: name, pkg: pkg}
}

// MustNotImport adds forbidden package dependencies to the layer rule.
func (r *LayerRule) MustNotImport(pkgs ...string) *LayerRule {
	r.mustNotImport = append(r.mustNotImport, pkgs...)
	return r
}

// MayOnlyImport configures an allowlist of permitted internal package dependencies for the layer.
func (r *LayerRule) MayOnlyImport(pkgs ...string) *LayerRule {
	if r.mayOnlyImport == nil {
		r.mayOnlyImport = []string{}
	}
	r.mayOnlyImport = append(r.mayOnlyImport, pkgs...)
	return r
}

type errorReporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// Check validates that the current module conforms to all specified architectural layer rules.
func Check(t testing.TB, rules ...*LayerRule) {
	t.Helper()

	dir, err := findModuleDir(".")
	if err != nil {
		t.Fatalf("goarch: %v", err)
	}

	modulePath, err := findModulePath(dir)
	if err != nil {
		t.Fatalf("goarch: %v", err)
	}

	for _, rule := range rules {
		imports, err := loadInternalImports(dir, modulePath, rule.pkg)
		if err != nil {
			t.Fatalf("goarch: failed to load imports for %s: %v", rule.pkg, err)
		}
		checkRule(t, rule, imports, modulePath)
	}
}

func checkRule(t errorReporter, rule *LayerRule, imports []string, modulePath string) {
	t.Helper()

	prefix := modulePath + "/"
	for _, imp := range imports {
		rel, ok := strings.CutPrefix(imp, prefix)
		if !ok {
			continue
		}
		checkForbidden(t, rule, rel)
		checkAllowlist(t, rule, rel)
	}
}

func checkForbidden(t errorReporter, rule *LayerRule, rel string) {
	for _, forbidden := range rule.mustNotImport {
		if matchesPkg(rel, forbidden) {
			t.Errorf("goarch: layer %q (%s) must not import %q, but does", rule.name, rule.pkg, forbidden)
		}
	}
}

func checkAllowlist(t errorReporter, rule *LayerRule, rel string) {
	if rule.mayOnlyImport == nil {
		return
	}
	for _, a := range rule.mayOnlyImport {
		if matchesPkg(rel, a) {
			return
		}
	}
	t.Errorf("goarch: layer %q (%s) may only import [%s], but also imports %q",
		rule.name, rule.pkg, strings.Join(rule.mayOnlyImport, ", "), rel)
}

func matchesPkg(rel, pkg string) bool {
	return rel == pkg || strings.HasPrefix(rel, pkg+"/")
}

func findModuleDir(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func findModulePath(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if mod, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(mod), nil
		}
	}
	return "", fmt.Errorf("module directive not found in go.mod")
}

func loadInternalImports(dir, modulePath, pkg string) ([]string, error) {
	cfg := &packages.Config{
		Mode: packages.NeedImports,
		Dir:  dir,
	}

	pkgs, err := packages.Load(cfg, "./"+pkg+"/...")
	if err != nil {
		return nil, err
	}

	return collectInternalImports(pkgs, modulePath), nil
}

func collectInternalImports(pkgs []*packages.Package, modulePath string) []string {
	seen := make(map[string]bool)
	for _, p := range pkgs {
		for imp := range p.Imports {
			if strings.HasPrefix(imp, modulePath+"/") {
				seen[imp] = true
			}
		}
	}
	result := make([]string, 0, len(seen))
	for imp := range seen {
		result = append(result, imp)
	}
	return result
}
