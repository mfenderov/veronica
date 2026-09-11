package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
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
	rootCmd := &cobra.Command{
		Use:   "veronica",
		Short: "Veronica — Autonomous Local MCP Gateway & Dynamic Tool Pod",
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}

	rootCmd.AddCommand(newServeCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newTUICmd())
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
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := setupApp(cfgPath)
			if err != nil {
				return err
			}
			return runServer(app, stdioMode)
		},
	}

	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to config file")
	cmd.Flags().BoolVar(&stdioMode, "stdio", false, "run as stdio MCP server directly")
	return cmd
}

func runServer(app *appContext, stdioMode bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mountAllModules(ctx, app.cfg, app.factory, app.reg, stdioMode)

	upstream := transport.NewUpstreamServer(app.reg)
	registerMetaTools(upstream, app.metaHandler, app.cfg, app.cfgPath)

	if stdioMode {
		return upstream.ServeStdio()
	}

	setupGracefulShutdown(upstream, cancel)
	fmt.Printf("[veronica] 🛰️ Veronica gateway listening on %s/sse\n", app.cfg.Server.Addr)
	return upstream.Start(app.cfg.Server.Addr)
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
	if modCfg.Disabled {
		return
	}
	cli, err := startModuleClient(ctx, modCfg, factory)
	if err != nil {
		logModuleWarn(quiet, err)
		return
	}
	mod := domain.NewModule(modCfg)
	if err := reg.Register(mod, cli); err != nil {
		logModuleWarn(quiet, fmt.Errorf("register %s: %w", modCfg.Name, err))
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
}

func registerReauthModuleTool(upstream *transport.UpstreamServer, h *meta.Handler) {
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_reauth_module", Description: "Re-trigger and refresh authentication for an MCP module"},
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
		domain.Tool{Name: "veronica_recall_module", Description: "Recall and unmount an MCP module from Veronica"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct{ Name string `json:"name"` }
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
		domain.Tool{Name: "veronica_toggle_module", Description: "Enable or disable an MCP module in Veronica"},
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
			ServerName: "atlassian",
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
			ServerName: "slack",
			ClientID:   "1601185624273.8899143856786",
			TokenURL:   "https://slack.com/api/oauth.v2.user.access",
		},
	})
}

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured Veronica MCP modules",
		RunE: func(cmd *cobra.Command, args []string) error {
			defaultCfg, _ := defaultPaths()
			cfg, err := config.Load(defaultCfg)
			if err != nil {
				return err
			}
			printModulesList(cmd.OutOrStdout(), cfg)
			return nil
		},
	}
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

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print Veronica version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("veronica version %s\n", Version)
		},
	}
}

func newTUICmd() *cobra.Command {
	var endpoint string

	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Launch the interactive Veronica TUI dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI(endpoint)
		},
	}

	cmd.Flags().StringVarP(&endpoint, "endpoint", "e", "http://localhost:9090/sse", "Veronica gateway endpoint")
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
