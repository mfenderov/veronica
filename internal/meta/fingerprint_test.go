package meta

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

func TestResolveModuleBinary_SkipsShims(t *testing.T) {
	for _, cmd := range []string{"npx", "bunx", "npm", "uvx", "pnpm", "yarn", "deno"} {
		cfg := domain.ModuleConfig{Name: "x", Transport: domain.TransportStdio, Command: cmd}
		if _, ok := resolveModuleBinary(cfg); ok {
			t.Errorf("expected shim %q to be skipped", cmd)
		}
	}
}

func TestResolveModuleBinary_LocalBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "srv")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := domain.ModuleConfig{Name: "x", Transport: domain.TransportStdio, Command: bin}
	got, ok := resolveModuleBinary(cfg)
	if !ok || got != bin {
		t.Errorf("expected (%q, true), got (%q, %v)", bin, got, ok)
	}
}

func TestHashFile_StableAcrossMtimeTouch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "b")
	if err := os.WriteFile(p, []byte("bytes"), 0o755); err != nil {
		t.Fatal(err)
	}
	h1, err := hashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatal(err)
	}
	h2, err := hashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || h1 == "" {
		t.Errorf("hash must be stable across mtime change, got %q vs %q", h1, h2)
	}
}

func TestResolveModuleBinary_NonStdioSkipped(t *testing.T) {
	cfg := domain.ModuleConfig{Name: "x", Transport: domain.TransportHTTP, URL: "http://a/b"}
	if _, ok := resolveModuleBinary(cfg); ok {
		t.Error("expected non-stdio module to be skipped")
	}
}

func TestResolveModuleBinary_RelativeWithSeparatorIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "rel"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "rel", "srv")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg := domain.ModuleConfig{Name: "x", Transport: domain.TransportStdio, Command: "./rel/srv"}
	got, ok := resolveModuleBinary(cfg)
	if !ok {
		t.Fatal("expected relative command with separator to resolve")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if got != bin {
		t.Errorf("expected %q, got %q", bin, got)
	}
}
