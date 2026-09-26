package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestWatchBinaryRoundTrip pins the nil-vs-false distinction for watch_binary across
// the yaml and json tag paths: an explicit false must survive marshaling, while unset
// must stay nil so it keeps defaulting to on.
func TestWatchBinaryRoundTrip(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	off := false
	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:        "explicit-off",
		Transport:   domain.TransportStdio,
		Command:     "/bin/a",
		WatchBinary: &off,
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "unset",
		Transport: domain.TransportStdio,
		Command:   "/bin/b",
	})

	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// The explicit false must be written out verbatim, not dropped by omitempty.
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), "watch_binary: false") {
		t.Fatalf("expected explicit watch_binary: false in yaml, got:\n%s", data)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	gotOff, exists := loaded.GetModule("explicit-off")
	if !exists {
		t.Fatal("expected explicit-off to exist in loaded config")
	}
	if gotOff.WatchBinary == nil || *gotOff.WatchBinary {
		t.Fatalf("expected explicit false to survive yaml round trip, got %+v", gotOff.WatchBinary)
	}
	if gotOff.WatchBinaryEnabled() {
		t.Fatal("expected watch disabled after yaml round trip of explicit false")
	}

	gotUnset, exists := loaded.GetModule("unset")
	if !exists {
		t.Fatal("expected unset to exist in loaded config")
	}
	if gotUnset.WatchBinary != nil {
		t.Fatalf("expected unset to stay nil after yaml round trip, got %+v", gotUnset.WatchBinary)
	}
	if !gotUnset.WatchBinaryEnabled() {
		t.Fatal("expected unset to keep defaulting to on")
	}

	// The json tags must keep the same nil-vs-false distinction.
	for _, tc := range []struct {
		name string
		mod  domain.ModuleConfig
	}{
		{name: "explicit-off", mod: gotOff},
		{name: "unset", mod: gotUnset},
	} {
		b, err := json.Marshal(tc.mod)
		if err != nil {
			t.Fatalf("Marshal %s failed: %v", tc.name, err)
		}
		var back domain.ModuleConfig
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("Unmarshal %s failed: %v", tc.name, err)
		}
		if back.WatchBinaryEnabled() != tc.mod.WatchBinaryEnabled() {
			t.Fatalf("expected json round trip to keep watch flag of %s, got enabled=%v for %s",
				tc.name, back.WatchBinaryEnabled(), b)
		}
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
