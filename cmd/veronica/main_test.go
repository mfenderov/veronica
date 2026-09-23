package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/config"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

func TestNewRootCmd_Subcommands(t *testing.T) {
	t.Parallel()

	rootCmd := newRootCmd()
	cmds := rootCmd.Commands()

	names := make(map[string]bool)
	for _, c := range cmds {
		names[c.Name()] = true
	}

	for _, expected := range []string{"serve", "list", "version", "tui", "doctor", "install-service"} {
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

func TestBuildGatewayHandlerProtectsSharedListener(t *testing.T) {
	t.Setenv(auth.GatewayTokenEnv, "test-gateway-token")

	for _, addr := range []string{"0.0.0.0:9090", ":9090"} {
		t.Run(addr, func(t *testing.T) {
			upstream := transport.NewUpstreamServer(registry.New())
			handler, err := buildGatewayHandler(upstream, config.ServerConfig{Addr: addr})
			if err != nil {
				t.Fatalf("buildGatewayHandler failed: %v", err)
			}

			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)

			unauthorizedReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/sse", http.NoBody)
			unauthorizedResp, err := http.DefaultClient.Do(unauthorizedReq)
			if err != nil {
				t.Fatalf("unauthorized request failed: %v", err)
			}
			_ = unauthorizedResp.Body.Close()
			if unauthorizedResp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unauthorized status = %d, want %d", unauthorizedResp.StatusCode, http.StatusUnauthorized)
			}

			authorizedReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/sse", http.NoBody)
			authorizedReq.Header.Set("Authorization", "Bearer test-gateway-token")
			authorizedResp, err := http.DefaultClient.Do(authorizedReq)
			if err != nil {
				t.Fatalf("authorized request failed: %v", err)
			}
			_ = authorizedResp.Body.Close()
			if authorizedResp.StatusCode != http.StatusOK {
				t.Fatalf("authorized status = %d, want %d", authorizedResp.StatusCode, http.StatusOK)
			}
		})
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

func TestDoctorCmd_Flags(t *testing.T) {
	t.Parallel()

	cmd := newDoctorCmd()
	cfgFlag := cmd.Flag("config")
	if cfgFlag == nil {
		t.Fatal("expected config flag on doctor command")
	}
	if !strings.Contains(cfgFlag.Usage, "config.yaml") {
		t.Fatalf("expected config flag usage to mention default, got: %s", cfgFlag.Usage)
	}
}

func TestDoctorCmd_ExecutionWithHealthyConfig(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:      "echo-mod",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
	})
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetArgs([]string{"doctor", "-c", cfgPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("doctor failed for a healthy config: %v (output: %s)", err, buf.String())
	}
	if !strings.Contains(buf.String(), "echo-mod") {
		t.Fatalf("expected module name in doctor output, got: %s", buf.String())
	}
}

func TestDoctorCmd_FailsWhenAModuleIsMisconfigured(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:      "broken-mod",
		Transport: domain.TransportStdio,
	})
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	rootCmd := newRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"doctor", "-c", cfgPath})

	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected doctor to fail for a module with no command")
	}
	if !strings.Contains(buf.String(), "broken-mod") {
		t.Fatalf("expected module name in doctor output, got: %s", buf.String())
	}
}

func TestDoctorCmd_ReportsMissingOAuthToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:      "atlassian",
		Transport: domain.TransportHTTP,
		URL:       "https://mcp.atlassian.com/v1/sse",
		OAuth:     &domain.OAuthClientConfig{ServerName: "atlassian"},
	})
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	buf := new(bytes.Buffer)
	err := runDoctor(cfgPath, filepath.Join(dir, "auth.json"), buf)
	if err == nil {
		t.Fatal("expected doctor to fail when an OAuth module has no stored token")
	}
	if !strings.Contains(buf.String(), "atlassian") {
		t.Fatalf("expected module name in doctor output, got: %s", buf.String())
	}
}

func TestRunDoctorWithNoModulesReportsNoProblems(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := config.DefaultConfig().Save(cfgPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	buf := new(bytes.Buffer)
	if err := runDoctor(cfgPath, filepath.Join(dir, "auth.json"), buf); err != nil {
		t.Fatalf("expected no problems for an empty config, got: %v (output: %s)", err, buf.String())
	}
}

func TestPrintDiagnostics(t *testing.T) {
	t.Parallel()

	diags := []meta.Diagnostic{
		{Module: "atlassian", Check: "auth", Severity: meta.DiagnosticError, Message: "no token stored"},
		{Module: "mark42", Check: "transport", Severity: meta.DiagnosticOK, Message: "stdio target is configured"},
	}

	buf := new(bytes.Buffer)
	printDiagnostics(buf, diags)

	output := buf.String()
	for _, want := range []string{"atlassian", "auth", "no token stored", "mark42", "transport"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in diagnostics output, got: %s", want, output)
		}
	}
}

// supervisorFakeClient is a controllable downstream client used to exercise the
// background supervisor wiring without spawning real processes.
type supervisorFakeClient struct {
	mu        sync.Mutex
	unhealthy bool
	tools     []domain.Tool
}

func (c *supervisorFakeClient) Start(context.Context) error { return nil }
func (c *supervisorFakeClient) Stop(context.Context) error  { return nil }
func (c *supervisorFakeClient) Status() domain.ModuleStatus { return domain.StatusActive }

func (c *supervisorFakeClient) ListTools(context.Context) ([]domain.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unhealthy {
		return nil, errors.New("write |1: broken pipe")
	}
	return c.tools, nil
}

func (c *supervisorFakeClient) CallTool(context.Context, domain.ToolCall) (domain.ToolResult, error) {
	return domain.ToolResult{}, nil
}

func (c *supervisorFakeClient) markUnhealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unhealthy = true
}

// supervisorFakeFactory hands out fresh clients and signals on restarted every time
// the supervisor asks for one, which is how a background restart is observed.
type supervisorFakeFactory struct {
	restarted chan struct{}
}

func newSupervisorFakeFactory() *supervisorFakeFactory {
	return &supervisorFakeFactory{restarted: make(chan struct{}, 1)}
}

func (f *supervisorFakeFactory) CreateClient(_ context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	select {
	case f.restarted <- struct{}{}:
	default:
	}

	return &supervisorFakeClient{tools: []domain.Tool{{Name: cfg.Name + "_query"}}}, nil
}

func TestStartSupervisorRestartsUnresponsiveModulesInBackground(t *testing.T) {
	t.Parallel()

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("creating auth store: %v", err)
	}

	reg := registry.New()
	factory := newSupervisorFakeFactory()
	handler := meta.NewHandler(reg, store, factory)

	modCfg := domain.ModuleConfig{
		Name:        "flaky",
		Transport:   domain.TransportStdio,
		Command:     "/bin/echo",
		AutoRestart: true,
	}
	client := &supervisorFakeClient{tools: []domain.Tool{{Name: "flaky_query"}}}
	if regErr := reg.Register(domain.NewModule(modCfg), client); regErr != nil {
		t.Fatalf("mounting module: %v", regErr)
	}
	client.markUnhealthy()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sup := startSupervisor(ctx, handler, meta.SupervisorConfig{
		Interval:       10 * time.Millisecond,
		ProbeTimeout:   time.Second,
		RestartTimeout: time.Second,
	})
	if sup == nil {
		t.Fatal("startSupervisor returned nil supervisor")
	}

	select {
	case <-factory.restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("background supervisor never restarted the unresponsive module")
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
	if atlassian.URL != "https://mcp.atlassian.com/v2/mcp" {
		t.Fatalf("expected atlassian URL https://mcp.atlassian.com/v2/mcp, got %s", atlassian.URL)
	}
	if atlassian.OAuth == nil {
		t.Fatal("expected atlassian OAuth config")
	}
	if atlassian.OAuth.AuthURL != "https://auth.atlassian.com/authorize" {
		t.Fatalf("expected atlassian AuthURL https://auth.atlassian.com/authorize, got %s", atlassian.OAuth.AuthURL)
	}
	if atlassian.OAuth.TokenURL != "https://auth.atlassian.com/oauth/token" {
		t.Fatalf("expected atlassian TokenURL https://auth.atlassian.com/oauth/token, got %s", atlassian.OAuth.TokenURL)
	}
	if atlassian.OAuth.ClientID == "" || atlassian.OAuth.ClientSecret == "" {
		t.Fatal("expected atlassian ClientID and ClientSecret to be set")
	}
	if atlassian.OAuth.AuthParams == nil || atlassian.OAuth.AuthParams["audience"] != "api.atlassian.com" {
		t.Fatalf("expected audience param api.atlassian.com, got %v", atlassian.OAuth.AuthParams)
	}

	slack, ok := cfg.Modules["slack"]
	if !ok {
		t.Fatal("expected slack module in default config")
	}
	if slack.OAuth == nil {
		t.Fatal("expected slack OAuth config")
	}
	if slack.OAuth.AuthURL != "https://slack.com/oauth/v2_user/authorize" {
		t.Fatalf("expected slack AuthURL https://slack.com/oauth/v2_user/authorize, got %s", slack.OAuth.AuthURL)
	}
	if slack.OAuth.RedirectURL != "http://localhost:3118/callback" {
		t.Fatalf("expected slack RedirectURL http://localhost:3118/callback, got %s", slack.OAuth.RedirectURL)
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

func TestSetupApp_RejectsInvalidAllowedCommand(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	cfg := config.DefaultConfig()
	cfg.Server.AllowedCommands = []string{filepath.Join(tmpDir, "missing-command")}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	if _, err := setupApp(cfgPath); err == nil {
		t.Fatal("expected setupApp to reject an invalid allowed command")
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

func TestIsDaemonReachableUsesGatewayToken(t *testing.T) {
	t.Setenv(auth.GatewayTokenEnv, "test-gateway-token")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-gateway-token" {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !isDaemonReachable(srv.URL) {
		t.Fatal("expected protected daemon to be reachable with the configured gateway token")
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

func TestRegisterTracesTool(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	store, _ := auth.NewFileStore(t.TempDir() + "/auth.json")
	h := meta.NewHandler(reg, store, nil)
	upstream := transport.NewUpstreamServer(reg)

	registerTracesTool(upstream, h)

	reg.RecordTrace(domain.ToolTrace{
		ID:         "test-trace",
		ModuleName: "mod",
		ToolName:   "echo",
		Duration:   time.Millisecond,
	})

	traces, err := h.RecentTraces(t.Context(), 10)
	if err != nil {
		t.Fatalf("RecentTraces failed: %v", err)
	}
	if len(traces) != 1 || traces[0].ID != "test-trace" {
		t.Fatalf("expected trace to be returned, got %+v", traces)
	}

	if parseTracesLimit(nil) != 20 {
		t.Errorf("expected default limit 20, got %d", parseTracesLimit(nil))
	}
	if parseTracesLimit(map[string]any{"limit": 5}) != 5 {
		t.Errorf("expected limit 5, got %d", parseTracesLimit(map[string]any{"limit": 5}))
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

// stubLookPath returns a lookPath implementation that resolves exactly the
// commands present in resolvable and reports an error for everything else.
func stubLookPath(resolvable ...string) func(string) (string, error) {
	return func(command string) (string, error) {
		for _, candidate := range resolvable {
			if candidate == command {
				return "/fake/bin/" + command, nil
			}
		}
		return "", errors.New("executable file not found in $PATH")
	}
}

func TestMissingPrerequisitesReportsUnresolvableCommands(t *testing.T) {
	t.Parallel()

	mods := map[string]domain.ModuleConfig{
		"present": {Name: "present", Transport: domain.TransportStdio, Command: "uvx"},
		"absent":  {Name: "absent", Transport: domain.TransportStdio, Command: "definitely-not-installed"},
	}

	missing := missingPrerequisites(mods, stubLookPath("uvx"))

	if len(missing) != 1 {
		t.Fatalf("got %d missing prerequisites, want 1: %+v", len(missing), missing)
	}
	if missing[0].Module != "absent" || missing[0].Command != "definitely-not-installed" {
		t.Fatalf("unexpected prerequisite: %+v", missing[0])
	}
}

func TestMissingPrerequisitesSkipsModulesNeedingNoLocalCommand(t *testing.T) {
	t.Parallel()

	mods := map[string]domain.ModuleConfig{
		"paused":  {Name: "paused", Transport: domain.TransportStdio, Command: "missing-paused", Disabled: true},
		"remote":  {Name: "remote", Transport: domain.TransportHTTP, URL: "https://example.com/mcp"},
		"invalid": {Name: "invalid", Transport: domain.TransportStdio},
	}

	if missing := missingPrerequisites(mods, stubLookPath()); len(missing) != 0 {
		t.Fatalf("got %d missing prerequisites, want 0: %+v", len(missing), missing)
	}
}

func TestMissingPrerequisitesSortsByModuleName(t *testing.T) {
	t.Parallel()

	mods := map[string]domain.ModuleConfig{
		"zeta":  {Name: "zeta", Transport: domain.TransportStdio, Command: "missing-zeta"},
		"alpha": {Name: "alpha", Transport: domain.TransportStdio, Command: "missing-alpha"},
	}

	missing := missingPrerequisites(mods, stubLookPath())

	if len(missing) != 2 {
		t.Fatalf("got %d missing prerequisites, want 2: %+v", len(missing), missing)
	}
	if missing[0].Module != "alpha" || missing[1].Module != "zeta" {
		t.Fatalf("prerequisites are not name-sorted: %+v", missing)
	}
}

func TestPrintPrerequisites(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	printPrerequisites(&out, []prerequisite{{Module: "markitdown", Command: "uvx"}})

	printed := out.String()
	for _, want := range []string{"markitdown", "uvx", installHint("uvx")} {
		if !strings.Contains(printed, want) {
			t.Fatalf("printed output %q does not mention %q", printed, want)
		}
	}

	out.Reset()
	printPrerequisites(&out, nil)
	if out.Len() != 0 {
		t.Fatalf("expected no output when nothing is missing, got: %q", out.String())
	}
}

func TestInstallHint(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"uvx", "npx", "mark42-server"} {
		if installHint(command) == "" {
			t.Fatalf("expected an install hint for %q", command)
		}
	}
	if installHint("some-random-binary") != "" {
		t.Fatalf("expected no install hint for an unknown command, got: %q", installHint("some-random-binary"))
	}
}
