package config_test

import (
	"path/filepath"
	"testing"

	"github.com/mfenderov/veronica/internal/config"
	"github.com/mfenderov/veronica/internal/domain"
)

func TestConfigLoadAndSave(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	// Default config when file does not exist
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load non-existent config failed: %v", err)
	}

	if cfg.Server.Addr != ":9090" {
		t.Fatalf("expected default addr :9090, got %s", cfg.Server.Addr)
	}

	// Add a module
	mod := domain.ModuleConfig{
		Name:      "test-mod",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
	}
	cfg.AddModule(mod)

	err = cfg.Save(cfgPath)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load back
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load persisted config failed: %v", err)
	}

	gotMod, exists := loaded.GetModule("test-mod")
	if !exists {
		t.Fatal("expected test-mod to exist in loaded config")
	}
	if gotMod.Command != "/bin/echo" {
		t.Fatalf("expected command /bin/echo, got %s", gotMod.Command)
	}

	// Remove module
	loaded.RemoveModule("test-mod")
	_ = loaded.Save(cfgPath)

	reloaded, _ := config.Load(cfgPath)
	if _, exists := reloaded.GetModule("test-mod"); exists {
		t.Fatal("expected test-mod to be removed")
	}
}
