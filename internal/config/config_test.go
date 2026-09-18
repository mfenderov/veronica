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

	if cfg.Server.Addr != "127.0.0.1:9090" {
		t.Fatalf("expected default addr 127.0.0.1:9090, got %s", cfg.Server.Addr)
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

func TestRequiresGatewayAuth(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		want    bool
		wantErr bool
	}{
		{name: "IPv4 loopback", addr: "127.0.0.1:9090", want: false},
		{name: "IPv6 loopback", addr: "[::1]:9090", want: false},
		{name: "localhost", addr: "localhost:9090", want: false},
		{name: "empty host wildcard", addr: ":9090", want: true},
		{name: "IPv4 wildcard", addr: "0.0.0.0:9090", want: true},
		{name: "IPv6 wildcard", addr: "[::]:9090", want: true},
		{name: "named remote host", addr: "gateway.example.com:9090", want: true},
		{name: "missing port", addr: "127.0.0.1", wantErr: true},
		{name: "invalid port", addr: ":not-a-port", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := config.RequiresGatewayAuth(tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("RequiresGatewayAuth(%q) error = %v, wantErr %v", tt.addr, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("RequiresGatewayAuth(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
