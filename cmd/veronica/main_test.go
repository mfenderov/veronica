package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(endpointFlag.Usage, "SSE") {
		t.Fatalf("expected endpoint flag usage to mention SSE, got: %s", endpointFlag.Usage)
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
	if !strings.Contains(buf.String(), "veronica version "+Version) {
		t.Fatalf("expected version output to contain version string, got: %s", buf.String())
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

func TestListCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newListCmd()
	cfgFlag := cmd.Flag("config")
	if cfgFlag == nil {
		t.Fatal("expected config flag on list command")
	}
	if !strings.Contains(cfgFlag.Usage, "config.yaml") {
		t.Fatalf("expected config flag usage to mention default, got: %s", cfgFlag.Usage)
	}

	// Test list with a custom config file
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom.yaml")
	customCfg := config.DefaultConfig()
	customCfg.AddModule(domain.ModuleConfig{
		Name:      "custom-mod",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
	})
	if err := customCfg.Save(customPath); err != nil {
		t.Fatalf("failed to save custom config: %v", err)
	}

	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"list", "-c", customPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("list -c failed: %v", err)
	}
	if !strings.Contains(buf.String(), "custom-mod") {
		t.Fatalf("expected custom-mod in list output, got: %s", buf.String())
	}
}

func TestServeCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newServeCmd()
	if cmd.Use != "serve" {
		t.Fatalf("expected serve command use, got %s", cmd.Use)
	}
	cfgFlag := cmd.Flag("config")
	if cfgFlag == nil {
		t.Fatal("expected config flag on serve command")
	}
	if !strings.Contains(cfgFlag.Usage, "config.yaml") {
		t.Fatalf("expected config flag usage to mention default, got: %s", cfgFlag.Usage)
	}
	stdioFlag := cmd.Flag("stdio")
	if stdioFlag == nil {
		t.Fatal("expected stdio flag on serve command")
	}
	if !strings.Contains(stdioFlag.Usage, "stdio") {
		t.Fatalf("expected stdio flag usage to mention stdio, got: %s", stdioFlag.Usage)
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

func TestPopulateDefaultModules_OAuthEndpointsValid(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	populateDefaultModules(cfg)

	atlassian, ok := cfg.Modules["atlassian"]
	if !ok {
		t.Fatal("expected atlassian module in default config")
	}
	if atlassian.URL != "https://mcp.atlassian.com/v1/mcp" {
		t.Fatalf("expected atlassian URL https://mcp.atlassian.com/v1/mcp, got %s", atlassian.URL)
	}
	if atlassian.OAuth == nil {
		t.Fatal("expected atlassian OAuth config")
	}
	if atlassian.OAuth.AuthURL != "https://mcp.atlassian.com/v1/authorize" {
		t.Fatalf("expected atlassian AuthURL https://mcp.atlassian.com/v1/authorize, got %s", atlassian.OAuth.AuthURL)
	}
	if atlassian.OAuth.TokenURL != "https://mcp.atlassian.com/v1/token" {
		t.Fatalf("expected atlassian TokenURL https://mcp.atlassian.com/v1/token, got %s", atlassian.OAuth.TokenURL)
	}
	if atlassian.OAuth.ClientID == "" || atlassian.OAuth.ClientSecret == "" {
		t.Fatal("expected atlassian ClientID and ClientSecret to be set")
	}

	slack, ok := cfg.Modules["slack"]
	if !ok {
		t.Fatal("expected slack module in default config")
	}
	if slack.OAuth == nil {
		t.Fatal("expected slack OAuth config")
	}
	if slack.OAuth.AuthURL != "https://slack.com/oauth/v2/authorize" {
		t.Fatalf("expected slack AuthURL https://slack.com/oauth/v2/authorize, got %s", slack.OAuth.AuthURL)
	}
	if slack.OAuth.RedirectURL != "http://localhost:9091/oauth/callback" {
		t.Fatalf("expected slack RedirectURL http://localhost:9091/oauth/callback, got %s", slack.OAuth.RedirectURL)
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

	mountSingleModule(t.Context(), cfg, factory, reg, true)

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

	mountSingleModule(t.Context(), cfg, factory, reg, true)

	modules := reg.ListModules()
	if len(modules) != 1 {
		t.Fatalf("expected 1 module registered in error state, got %d", len(modules))
	}
	if modules[0].Status != domain.StatusError {
		t.Fatalf("expected module status error, got %s", modules[0].Status)
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

	mountSingleModule(t.Context(), cfg, factory, reg, true)
}

func TestLogModuleWarn(t *testing.T) {
	t.Parallel()

	// Should not panic with quiet=true or quiet=false
	logModuleWarn(true, os.ErrNotExist)
	logModuleWarn(false, os.ErrNotExist)
}

func TestIsDaemonReachable(t *testing.T) {
	t.Parallel()

	// Unreachable endpoint
	if isDaemonReachable("http://127.0.0.1:59999/sse") {
		t.Fatal("expected unreachable for nonexistent server")
	}

	// Reachable endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !isDaemonReachable(srv.URL) {
		t.Fatal("expected reachable for active mock server")
	}
}

func TestWaitForDaemon(t *testing.T) {
	t.Parallel()

	// Unreachable should timeout
	err := waitForDaemon("http://127.0.0.1:59999/sse", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout waiting for nonexistent daemon")
	}

	// Active server should resolve immediately
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := waitForDaemon(srv.URL, 500*time.Millisecond); err != nil {
		t.Fatalf("expected active server to be detected: %v", err)
	}
}

func TestDetachedProcAttr(t *testing.T) {
	t.Parallel()

	attr := detachedProcAttr()
	if attr == nil || !attr.Setsid {
		t.Fatal("expected non-nil SysProcAttr with Setsid=true")
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Parallel()

	if resolveEndpoint("") != "http://localhost:9090/sse" {
		t.Fatalf("expected default endpoint, got: %s", resolveEndpoint(""))
	}
	custom := "http://127.0.0.1:8080/sse"
	if resolveEndpoint(custom) != custom {
		t.Fatalf("expected custom endpoint, got: %s", resolveEndpoint(custom))
	}
}

func TestResolveBinaryPath(t *testing.T) {
	t.Parallel()

	path := resolveBinaryPath()
	if path == "" {
		t.Fatal("expected non-empty binary path")
	}
}

func TestBuildDaemonCommand(t *testing.T) {
	t.Parallel()

	cmd := buildDaemonCommand("veronica")
	if cmd == nil || len(cmd.Args) != 2 || cmd.Args[1] != "serve" {
		t.Fatalf("unexpected daemon command args: %+v", cmd)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("expected Setsid=true in daemon command")
	}
}

func TestCheckAndStartDaemon_Reachable(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := checkAndStartDaemon(srv.URL); err != nil {
		t.Fatalf("expected no error when daemon is already reachable: %v", err)
	}
}
