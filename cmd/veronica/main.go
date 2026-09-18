package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/client"
	"github.com/mfenderov/veronica/internal/config"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
	"github.com/mfenderov/veronica/internal/tui"
	"github.com/spf13/cobra"
)

// Version is the current release version of Veronica.
var Version = "0.1.0"

type downstreamFactory struct {
	tokenProvider domain.TokenProvider
}

func (f *downstreamFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return transport.NewDownstreamClient(ctx, cfg, f.tokenProvider)
}

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var endpoint string

	rootCmd := &cobra.Command{
		Use:   "veronica",
		Short: "Veronica — Autonomous Local MCP Gateway & Dynamic Tool Pod",
		Long:  "Veronica is an autonomous local MCP gateway and dynamic tool pod that unifies MCP tool administration across AI coding assistants.",
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDefaultApp(endpoint)
		},
	}

	rootCmd.Flags().StringVarP(&endpoint, "endpoint", "e", "http://localhost:9090/sse", "Veronica gateway SSE endpoint URL")

	rootCmd.AddCommand(newServeCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newTUICmd())
	rootCmd.AddCommand(newDoctorCmd())
	return rootCmd
}

func defaultPaths() (string, string) {
	home, _ := os.UserHomeDir()
	configDir := filepath.Join(home, ".config", "veronica")
	return filepath.Join(configDir, "config.yaml"), filepath.Join(configDir, "auth.json")
}

type appContext struct {
	cfg         *config.Config
	cfgPath     string
	reg         *registry.Registry
	factory     *downstreamFactory
	metaHandler *meta.Handler
}

func setupApp(cfgPath string) (*appContext, error) {
	defaultCfg, defaultAuth := defaultPaths()
	if cfgPath == "" {
		cfgPath = defaultCfg
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	populateDefaultModules(cfg)
	_ = cfg.Save(cfgPath)

	commandPolicy, err := meta.NewCommandPolicy(cfg.Server.AllowedCommands)
	if err != nil {
		return nil, fmt.Errorf("failed to configure command policy: %w", err)
	}

	authStore, err := auth.NewFileStore(defaultAuth)
	if err != nil {
		return nil, fmt.Errorf("failed to init auth store: %w", err)
	}

	home, _ := os.UserHomeDir()
	opencodeAuth := filepath.Join(home, ".local", "share", "opencode", "mcp-auth.json")
	_ = authStore.ImportFromOpenCode(opencodeAuth)

	oauthMgr := auth.NewOAuthManager(authStore, http.DefaultClient)
	reg := registry.New()
	factory := &downstreamFactory{tokenProvider: oauthMgr}
	metaHandler := meta.NewHandler(reg, authStore, factory)
	metaHandler.SetTokenProvider(oauthMgr)
	metaHandler.SetCommandPolicy(commandPolicy)

	return &appContext{
		cfg:         cfg,
		cfgPath:     cfgPath,
		reg:         reg,
		factory:     factory,
		metaHandler: metaHandler,
	}, nil
}

func newServeCmd() *cobra.Command {
	var cfgPath string
	var stdioMode bool

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Veronica MCP gateway server",
		Long:  "Start the Veronica MCP gateway server in HTTP/SSE daemon mode (default) or stdio mode for direct client integration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := setupApp(cfgPath)
			if err != nil {
				return err
			}
			return runServer(app, stdioMode)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file (default: ~/.config/veronica/config.yaml)")
	cmd.Flags().BoolVar(&stdioMode, "stdio", false, "run MCP gateway over stdio instead of HTTP/SSE")
	return cmd
}

func runServer(app *appContext, stdioMode bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	warnMissingPrerequisites(app.cfg.Modules, exec.LookPath, os.Stderr)

	mountAllModules(ctx, app.cfg, app.factory, app.reg, stdioMode)
	startSupervisor(ctx, app.metaHandler, meta.DefaultSupervisorConfig())

	upstream := transport.NewUpstreamServer(app.reg)
	registerMetaTools(upstream, app.metaHandler, app.cfg, app.cfgPath)

	if stdioMode {
		return upstream.ServeStdio()
	}
	return runHTTPServer(upstream, app.cfg.Server, cancel)
}

func runHTTPServer(upstream *transport.UpstreamServer, cfg config.ServerConfig, cancel context.CancelFunc) error {
	handler, err := buildGatewayHandler(upstream, cfg)
	if err != nil {
		return fmt.Errorf("failed to configure gateway handler: %w", err)
	}

	setupGracefulShutdown(upstream, cancel)
	fmt.Printf("[veronica] 🛰️ Veronica gateway listening on %s/sse\n", cfg.Addr)
	return upstream.StartWithHandler(cfg.Addr, handler)
}

func buildGatewayHandler(upstream *transport.UpstreamServer, cfg config.ServerConfig) (http.Handler, error) {
	handler := upstream.Handler()
	requiresAuth, err := config.RequiresGatewayAuth(cfg.Addr)
	if err != nil {
		return nil, err
	}
	if !requiresAuth {
		return handler, nil
	}

	token, err := auth.LoadGatewayToken(cfg.AuthTokenFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load gateway token: %w", err)
	}
	return transport.WithBearerAuth(handler, token), nil
}

// startSupervisor runs the module supervision loop in the background for the lifetime
// of ctx, restarting modules that stop responding. It returns the supervisor so callers
// can inspect its configuration.
func startSupervisor(ctx context.Context, handler *meta.Handler, cfg meta.SupervisorConfig) *meta.Supervisor {
	sup := meta.NewSupervisor(handler, cfg)
	go func() {
		_ = sup.Run(ctx)
	}()
	return sup
}

func setupGracefulShutdown(upstream *transport.UpstreamServer, cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n[veronica] Shutting down...")
		_ = upstream.Shutdown(context.Background())
		cancel()
	}()
}

func mountAllModules(ctx context.Context, cfg *config.Config, factory *downstreamFactory, reg *registry.Registry, quiet bool) {
	for _, modCfg := range cfg.Modules {
		mountSingleModule(ctx, modCfg, factory, reg, quiet)
	}
}

func mountSingleModule(ctx context.Context, modCfg domain.ModuleConfig, factory *downstreamFactory, reg *registry.Registry, quiet bool) {
	if modCfg.Disabled || strings.TrimSpace(modCfg.Name) == "" {
		return
	}
	mod := domain.NewModule(modCfg)
	cli, err := startModuleClient(ctx, modCfg, factory)
	if err != nil {
		logModuleWarn(quiet, err)
		reg.RegisterError(mod, err)
		return
	}
	if err := reg.Register(mod, cli); err != nil {
		logModuleWarn(quiet, fmt.Errorf("register %s: %w", modCfg.Name, err))
		reg.RegisterError(mod, err)
		return
	}
	if !quiet {
		fmt.Printf("[veronica] Mounted module %s (%d tools)\n", modCfg.Name, len(mod.Tools))
	}
}

func startModuleClient(ctx context.Context, modCfg domain.ModuleConfig, factory *downstreamFactory) (domain.DownstreamClient, error) {
	cli, err := factory.CreateClient(ctx, modCfg)
	if err != nil {
		return nil, fmt.Errorf("create client %s: %w", modCfg.Name, err)
	}
	if err := cli.Start(ctx); err != nil {
		return nil, fmt.Errorf("start %s: %w", modCfg.Name, err)
	}
	return cli, nil
}

func logModuleWarn(quiet bool, err error) {
	if !quiet {
		fmt.Printf("[veronica] Warn: %v\n", err)
	}
}

func registerMetaTools(upstream *transport.UpstreamServer, h *meta.Handler, cfg *config.Config, cfgPath string) {
	registerStatusTool(upstream, h)
	registerListModulesTool(upstream, h)
	registerDeployModuleTool(upstream, h, cfg, cfgPath)
	registerRecallModuleTool(upstream, h, cfg, cfgPath)
	registerToggleModuleTool(upstream, h, cfg, cfgPath)
	registerReauthModuleTool(upstream, h)
	registerRestartDaemonTool(upstream, h)
	registerTracesTool(upstream, h)
}

func registerTracesTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{
			Name:        "veronica_traces",
			Description: "Get recent tool execution traces with latency and status metrics",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{
						"type":        "integer",
						"description": "Maximum number of traces to return (default 20)",
					},
				},
			},
		},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			limit := parseTracesLimit(args)
			traces, err := h.RecentTraces(ctx, limit)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(traces), nil
		},
	)
}

func parseTracesLimit(args any) int {
	var p struct {
		Limit int `json:"limit"`
	}
	if args != nil {
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &p)
	}
	if p.Limit <= 0 {
		return 20
	}
	return p.Limit
}

func registerRestartDaemonTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_restart_daemon", Description: "Reload all active downstream MCP modules"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			res, err := h.RestartDaemon(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
}

func registerReauthModuleTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{
			Name:        "veronica_reauth_module",
			Description: "Re-trigger and refresh authentication for an MCP module",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the module to re-authenticate (e.g. atlassian, slack)",
					},
				},
				"required": []string{"name"},
			},
		},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct {
				Name string `json:"name"`
			}
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)

			res, err := h.ReauthModule(ctx, p.Name)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
}

func registerStatusTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_status", Description: "Get Veronica gateway health and resource metrics"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			st, err := h.Status(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(st), nil
		},
	)
}

func registerListModulesTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_list_modules", Description: "List all active and available MCP modules in Veronica"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			list, err := h.ListModules(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(list), nil
		},
	)
}

func registerDeployModuleTool(upstream *transport.UpstreamServer, h *meta.Handler, cfg *config.Config, cfgPath string) {
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_deploy_module", Description: "Dynamically deploy and mount an MCP module into Veronica"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p meta.DeployParams
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)

			res, err := h.DeployModule(ctx, p)
			if err != nil {
				return transport.ResultError(err), nil
			}

			cfg.AddModule(domain.ModuleConfig{
				Name:      p.Name,
				Transport: domain.TransportType(p.Transport),
				Command:   p.Command,
				Args:      p.Args,
				URL:       p.URL,
				Env:       p.Env,
				Headers:   p.Headers,
			})
			_ = cfg.Save(cfgPath)

			return transport.ResultJSON(res), nil
		},
	)
}

func registerRecallModuleTool(upstream *transport.UpstreamServer, h *meta.Handler, cfg *config.Config, cfgPath string) {
	upstream.RegisterCustomTool(
		domain.Tool{
			Name:        "veronica_recall_module",
			Description: "Recall and unmount an MCP module from Veronica",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the module to recall",
					},
				},
				"required": []string{"name"},
			},
		},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct {
				Name string `json:"name"`
			}
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)

			res, err := h.RecallModule(ctx, p.Name)
			if err != nil {
				return transport.ResultError(err), nil
			}

			cfg.RemoveModule(p.Name)
			_ = cfg.Save(cfgPath)

			return transport.ResultJSON(res), nil
		},
	)
}

func registerToggleModuleTool(upstream *transport.UpstreamServer, h *meta.Handler, cfg *config.Config, cfgPath string) {
	upstream.RegisterCustomTool(
		domain.Tool{
			Name:        "veronica_toggle_module",
			Description: "Enable or disable an MCP module in Veronica",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Name of the module to toggle",
					},
					"enable": map[string]any{
						"type":        "boolean",
						"description": "Whether to enable (true) or disable (false)",
					},
				},
				"required": []string{"name", "enable"},
			},
		},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct {
				Name   string `json:"name"`
				Enable bool   `json:"enable"`
			}
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)

			res, err := h.ToggleModule(ctx, p.Name, p.Enable)
			if err != nil {
				return transport.ResultError(err), nil
			}

			updateConfigModuleDisabled(cfg, p.Name, !p.Enable)
			_ = cfg.Save(cfgPath)

			return transport.ResultJSON(res), nil
		},
	)
}

func updateConfigModuleDisabled(cfg *config.Config, name string, disabled bool) {
	if mod, ok := cfg.Modules[name]; ok {
		mod.Disabled = disabled
		cfg.Modules[name] = mod
	}
}

func populateDefaultModules(cfg *config.Config) {
	if len(cfg.Modules) > 0 {
		return
	}

	home, _ := os.UserHomeDir()

	cfg.AddModule(domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/opt/homebrew/bin/mark42-server",
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "atlassian",
		Transport: domain.TransportHTTP,
		URL:       "https://mcp.atlassian.com/v2/mcp",
		OAuth: &domain.OAuthClientConfig{
			ServerName:   "atlassian",
			ClientID:     "FH0b8WMW9mXV1tQAq4z9D97LYR7yRD64",
			ClientSecret: "ATOACZ2ZmLYoyutKWGoKp5q85sHYmBiiwIxLwstCBKuZUKH3BjufWh7wlu6RdAseMxFC229CCC8E",
			AuthURL:      "https://auth.atlassian.com/authorize",
			TokenURL:     "https://auth.atlassian.com/oauth/token",
			RedirectURL:  "http://localhost:9091/oauth/callback",
			AuthParams: map[string]string{
				"audience": "api.atlassian.com",
				"prompt":   "consent",
			},
			Scopes: []string{
				"email",
				"offline_access",
				"read:account",
				"read:me",
				"read:jira:agent-interface",
				"write:jira:agent-interface",
				"search:jira:agent-interface",
				"delete:jira:agent-interface",
				"manage:jira:agent-interface",
				"read:confluence:agent-interface",
				"write:confluence:agent-interface",
				"search:confluence:agent-interface",
				"search:rovo:agent-interface",
				"search:code:agent-interface",
			},
		},
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "markitdown",
		Transport: domain.TransportStdio,
		Command:   filepath.Join(home, ".local", "bin", "uvx"),
		Args:      []string{"markitdown-mcp==0.0.1a4"},
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "honeycomb",
		Transport: domain.TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "mcp-remote", "https://mcp.honeycomb.io/mcp"},
		Disabled:  true,
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "context7",
		Transport: domain.TransportStdio,
		Command:   "npx",
		Args:      []string{"--yes", "@upstash/context7-mcp@3.1.0"},
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "slack",
		Transport: domain.TransportHTTP,
		URL:       "https://mcp.slack.com/mcp",
		OAuth: &domain.OAuthClientConfig{
			ServerName:  "slack",
			ClientID:    "1601185624273.8899143856786",
			AuthURL:     "https://slack.com/oauth/v2_user/authorize",
			TokenURL:    "https://slack.com/api/oauth.v2.user.access",
			RedirectURL: "http://localhost:3118/callback",
			Scopes: []string{
				"identify",
				"channels:history",
				"channels:read",
				"channels:write",
				"chat:write",
				"groups:history",
				"groups:read",
				"groups:write",
				"im:history",
				"im:read",
				"im:write",
				"mpim:history",
				"mpim:read",
				"mpim:write",
				"users:read",
				"users:read.email",
				"emoji:read",
				"files:read",
				"canvases:read",
				"canvases:write",
				"reactions:read",
				"reactions:write",
				"search:read.public",
				"search:read.private",
				"search:read.files",
				"search:read.users",
			},
		},
	})
}

func newListCmd() *cobra.Command {
	var cfgPath string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List configured Veronica MCP modules",
		Long:  "List all configured Veronica MCP modules, their transports, and target commands/URLs.",
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultCfg, _ := defaultPaths()
			if cfgPath == "" {
				cfgPath = defaultCfg
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			printModulesList(cmd.OutOrStdout(), cfg)
			return nil
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file (default: ~/.config/veronica/config.yaml)")
	return cmd
}

func printModulesList(w io.Writer, cfg *config.Config) {
	fmt.Fprintf(w, "%-15s %-10s %s\n", "NAME", "TRANSPORT", "TARGET")
	fmt.Fprintln(w, "-----------------------------------------------------------------")

	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		m := cfg.Modules[name]
		target := m.Command
		if m.URL != "" {
			target = m.URL
		}
		fmt.Fprintf(w, "%-15s %-10s %s\n", m.Name, m.Transport, target)
	}
}

func newDoctorCmd() *cobra.Command {
	var cfgPath string
	var authPath string

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration, credentials, and health of Veronica modules",
		Long: "Check every configured module for configuration mistakes and credential problems\n" +
			"using the on-disk config and token store. Exits with a non-zero status when any\n" +
			"module reports an error.",
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultCfg, defaultAuth := defaultPaths()
			if cfgPath == "" {
				cfgPath = defaultCfg
			}
			if authPath == "" {
				authPath = defaultAuth
			}
			return runDoctor(cfgPath, authPath, cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file (default: ~/.config/veronica/config.yaml)")
	cmd.Flags().StringVar(&authPath, "auth", "", "path to token store (default: ~/.config/veronica/auth.json)")
	return cmd
}

// runDoctor diagnoses every configured module against the on-disk config and token
// store, prints the findings, and returns an error when any finding is an error.
func runDoctor(cfgPath, authPath string, out io.Writer) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	store, err := auth.NewFileStore(authPath)
	if err != nil {
		return fmt.Errorf("failed to open token store: %w", err)
	}

	handler := meta.NewHandler(registry.New(), store, nil)
	diags := handler.Diagnose(context.Background(), moduleConfigsInOrder(cfg), time.Now())
	printDiagnostics(out, diags)
	printPrerequisites(out, missingPrerequisites(cfg.Modules, exec.LookPath))

	if n := countDiagnosticErrors(diags); n > 0 {
		return fmt.Errorf("doctor found %d problem(s) that need attention", n)
	}
	return nil
}

// moduleConfigsInOrder returns the configured modules sorted by name so doctor output
// is stable across runs.
func moduleConfigsInOrder(cfg *config.Config) []domain.ModuleConfig {
	names := make([]string, 0, len(cfg.Modules))
	for name := range cfg.Modules {
		names = append(names, name)
	}
	sort.Strings(names)

	modules := make([]domain.ModuleConfig, 0, len(names))
	for _, name := range names {
		modules = append(modules, cfg.Modules[name])
	}
	return modules
}

func countDiagnosticErrors(diags []meta.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Severity == meta.DiagnosticError {
			n++
		}
	}
	return n
}

func printDiagnostics(w io.Writer, diags []meta.Diagnostic) {
	if len(diags) == 0 {
		fmt.Fprintln(w, "No modules configured - nothing to check.")
		return
	}
	for _, d := range diags {
		fmt.Fprintf(w, "%-9s %-15s %-10s %s\n", d.Severity, d.Module, d.Check, d.Message)
	}
}

type prerequisite struct {
	Module  string
	Command string
}

func missingPrerequisites(mods map[string]domain.ModuleConfig, lookPath func(string) (string, error)) []prerequisite {
	names := make([]string, 0, len(mods))
	for name := range mods {
		names = append(names, name)
	}
	sort.Strings(names)

	var missing []prerequisite
	for _, name := range names {
		m := mods[name]
		if m.Disabled || m.Transport != domain.TransportStdio || strings.TrimSpace(m.Command) == "" {
			continue
		}
		if _, err := lookPath(m.Command); err != nil {
			missing = append(missing, prerequisite{Module: m.Name, Command: m.Command})
		}
	}
	return missing
}

func printPrerequisites(w io.Writer, missing []prerequisite) {
	if len(missing) == 0 {
		return
	}
	fmt.Fprintln(w, "Missing prerequisites:")
	for _, p := range missing {
		hint := installHint(p.Command)
		if hint != "" {
			hint = " " + hint
		}
		fmt.Fprintf(w, "- %s: command %q not found.%s\n", p.Module, p.Command, hint)
	}
}

func installHint(command string) string {
	switch filepath.Base(command) {
	case "uvx":
		return "Install with: brew install uv (then uvx markitdown-mcp)."
	case "npx":
		return "Install with: brew install node (then npx -y mcp-remote)."
	case "mark42-server":
		return "Install with: brew install mark42-server (or check /opt/homebrew/bin/mark42-server)."
	default:
		return ""
	}
}

func warnMissingPrerequisites(mods map[string]domain.ModuleConfig, lookPath func(string) (string, error), w io.Writer) {
	printPrerequisites(w, missingPrerequisites(mods, lookPath))
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Veronica version",
		Long:  "Print the current version of the Veronica binary.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "veronica version %s\n", Version)
		},
	}
}

func newTUICmd() *cobra.Command {
	var endpoint string

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive Veronica TUI dashboard",
		Long:  "Launch the interactive Veronica TUI dashboard to monitor, deploy, toggle, and manage MCP modules.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(endpoint)
		},
	}

	cmd.Flags().StringVarP(&endpoint, "endpoint", "e", "http://localhost:9090/sse", "Veronica gateway SSE endpoint URL")
	return cmd
}

func runTUI(endpoint string) error {
	remote, err := connectRemotePod(endpoint)
	if err != nil {
		return err
	}
	defer remote.Close()
	return launchTUIProgram(remote)
}

func connectRemotePod(endpoint string) (*client.RemotePodClient, error) {
	if endpoint == "" {
		endpoint = "http://localhost:9090/sse"
	}

	remote, err := client.NewRemotePodClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := remote.Connect(ctx); err != nil {
		remote.Close()
		return nil, fmt.Errorf("could not connect to Veronica gateway on %s: %w\nMake sure the daemon is running ('veronica serve' or launchd)", endpoint, err)
	}
	return remote, nil
}

func launchTUIProgram(remote *client.RemotePodClient) error {
	prog := tea.NewProgram(tui.NewModel(remote), tea.WithAltScreen())
	_, err := prog.Run()
	return err
}

func runDefaultApp(endpoint string) error {
	resolvedEndpoint := resolveEndpoint(endpoint)
	if err := checkAndStartDaemon(resolvedEndpoint); err != nil {
		return err
	}
	return runTUI(resolvedEndpoint)
}

func resolveEndpoint(endpoint string) string {
	if endpoint == "" {
		return "http://localhost:9090/sse"
	}
	return endpoint
}

func checkAndStartDaemon(endpoint string) error {
	if isDaemonReachable(endpoint) {
		return nil
	}
	return ensureDaemonStarted(endpoint)
}

func ensureDaemonStarted(endpoint string) error {
	if err := startDetachedDaemon(); err != nil {
		return fmt.Errorf("failed to auto-start Veronica daemon: %w", err)
	}
	return waitForDaemon(endpoint, 3*time.Second)
}

func isDaemonReachable(endpoint string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return false
	}
	if token := strings.TrimSpace(os.Getenv(auth.GatewayTokenEnv)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func waitForDaemon(endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isDaemonReachable(endpoint) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("timed out waiting for background daemon to become ready")
}

func startDetachedDaemon() error {
	binPath := resolveBinaryPath()
	cmd := buildDaemonCommand(binPath)
	return cmd.Start()
}

func resolveBinaryPath() string {
	binPath, err := os.Executable()
	if err != nil {
		return "veronica"
	}
	return binPath
}

func buildDaemonCommand(binPath string) *exec.Cmd {
	cmd := exec.Command(binPath, "serve")
	cmd.SysProcAttr = detachedProcAttr()
	attachDaemonLogs(cmd)
	return cmd
}

func attachDaemonLogs(cmd *exec.Cmd) {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".config", "veronica")
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.OpenFile(filepath.Join(logDir, "daemon.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
}

func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true,
	}
}
