package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mfenderov/veronica/internal/config"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
)

func TestNewRootCmd_Subcommands(t *testing.T) {
	t.Parallel()

	rootCmd := newRootCmd()
	cmds := rootCmd.Commands()

	names := make(map[string]bool)
	for _, c := range cmds {
		names[c.Name()] = true
	}

	for _, expected := range []string{"serve", "list", "version", "tui"} {
		if !names[expected] {
			t.Errorf("expected command %s to exist", expected)
		}
	}
}

func TestNewTUICmd_Config(t *testing.T) {
	t.Parallel()

	cmd := newTUICmd()
	if cmd.Use != "tui" {
		t.Fatalf("expected command use tui, got %s", cmd.Use)
	}

	endpointFlag := cmd.Flag("endpoint")
	if endpointFlag == nil {
		t.Fatal("expected endpoint flag to exist")
	}
	if endpointFlag.DefValue != "http://localhost:9090/sse" {
		t.Fatalf("unexpected default endpoint: %s", endpointFlag.DefValue)
	}
}

func TestConnectRemotePod_Error(t *testing.T) {
	t.Parallel()

	// An unreachable endpoint should return error within timeout
	_, err := connectRemotePod("http://127.0.0.1:54321/sse")
	if err == nil {
		t.Fatal("expected error connecting to nonexistent gateway")
	}

	// runTUI should fail gracefully on bad endpoint
	err = runTUI("http://127.0.0.1:54321/sse")
	if err == nil {
		t.Fatal("expected error running TUI on bad endpoint")
	}
}

func TestVersionCmd_Output(t *testing.T) {
	t.Parallel()

	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"version"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
}

func TestListCmd_Execution(t *testing.T) {
	t.Parallel()

	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"list"})

	err := rootCmd.Execute()
	if err != nil {
		t.Fatalf("list command failed: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "NAME") || !strings.Contains(output, "TRANSPORT") {
		t.Fatalf("expected header columns in list output, got: %s", output)
	}
}

func TestServeCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newServeCmd()
	if cmd.Use != "serve" {
		t.Fatalf("expected serve command use, got %s", cmd.Use)
	}
	if cmd.Flag("config") == nil {
		t.Fatal("expected config flag on serve command")
	}
	if cmd.Flag("stdio") == nil {
		t.Fatal("expected stdio flag on serve command")
	}
}

func TestPrintModulesList(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:      "test-cli",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "test-url",
		Transport: domain.TransportHTTP,
		URL:       "http://example.com/mcp",
	})

	buf := new(bytes.Buffer)
	printModulesList(buf, cfg)

	output := buf.String()
	if output == "" {
		t.Fatal("expected non-empty output from printModulesList")
	}
}

func TestPopulateDefaultModules(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	populateDefaultModules(cfg)

	if len(cfg.Modules) == 0 {
		t.Fatal("expected default modules to be populated")
	}

	// Calling again should be no-op
	count := len(cfg.Modules)
	populateDefaultModules(cfg)
	if len(cfg.Modules) != count {
		t.Errorf("expected count %d, got %d", count, len(cfg.Modules))
	}
}

func TestDefaultPaths(t *testing.T) {
	t.Parallel()

	cfgPath, authPath := defaultPaths()
	if filepath.Base(cfgPath) != "config.yaml" {
		t.Errorf("unexpected cfgPath: %s", cfgPath)
	}
	if filepath.Base(authPath) != "auth.json" {
		t.Errorf("unexpected authPath: %s", authPath)
	}
}

func TestSetupApp_WithTempConfig(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")

	app, err := setupApp(cfgPath)
	if err != nil {
		t.Fatalf("setupApp failed: %v", err)
	}

	if app.cfg == nil || app.reg == nil || app.metaHandler == nil {
		t.Fatal("expected app fields to be initialized")
	}
}

func TestMountSingleModule_Disabled(t *testing.T) {
	t.Parallel()

	cfg := domain.ModuleConfig{
		Name:      "disabled-mod",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
		Disabled:  true,
	}

	reg := registry.New()
	factory := &downstreamFactory{}

	mountSingleModule(context.Background(), cfg, factory, reg, true)

	if len(reg.ListModules()) != 0 {
		t.Fatalf("expected 0 modules mounted when disabled, got %d", len(reg.ListModules()))
	}
}

func TestMountSingleModule_Enabled_Error(t *testing.T) {
	t.Parallel()

	cfg := domain.ModuleConfig{
		Name:      "test-mod",
		Transport: domain.TransportStdio,
		Command:   "/nonexistent-bin-xyz",
	}

	reg := registry.New()
	factory := &downstreamFactory{}

	mountSingleModule(context.Background(), cfg, factory, reg, true)

	if len(reg.ListModules()) != 0 {
		t.Fatalf("expected 0 modules mounted when start fails, got %d", len(reg.ListModules()))
	}
}

func TestMountSingleModule_InvalidConfig(t *testing.T) {
	t.Parallel()

	cfg := domain.ModuleConfig{
		Name:      "",
		Transport: "invalid",
	}

	reg := registry.New()
	factory := &downstreamFactory{}

	mountSingleModule(context.Background(), cfg, factory, reg, true)
}

func TestLogModuleWarn(t *testing.T) {
	t.Parallel()

	// Should not panic with quiet=true or quiet=false
	logModuleWarn(true, os.ErrNotExist)
	logModuleWarn(false, os.ErrNotExist)
}
