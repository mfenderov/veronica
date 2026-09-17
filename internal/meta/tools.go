// Package meta implements administrative meta-tools and management handlers for the Veronica pod.
package meta

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
)

// ClientFactory defines an abstraction for instantiating downstream MCP clients.
type ClientFactory interface {
	CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error)
}

// Handler implements domain.PodService to manage modules, authentication, and gateway status.
type Handler struct {
	registry      *registry.Registry
	authStore     domain.AuthStore
	tokenProvider domain.TokenProvider
	factory       ClientFactory
	startedAt     time.Time
}

// NewHandler creates a new Handler with the given registry, auth store, and client factory.
func NewHandler(reg *registry.Registry, store domain.AuthStore, factory ClientFactory) *Handler {
	return &Handler{
		registry:  reg,
		authStore: store,
		factory:   factory,
		startedAt: time.Now(),
	}
}

// SetTokenProvider configures the token provider used for OAuth module authentication.
func (h *Handler) SetTokenProvider(provider domain.TokenProvider) {
	h.tokenProvider = provider
}

var _ domain.PodService = (*Handler)(nil)

// DeployParams is an alias for domain.DeployParams.
type DeployParams = domain.DeployParams

// DeployResult is an alias for domain.DeployResult.
type DeployResult = domain.DeployResult

// RecallResult is an alias for domain.RecallResult.
type RecallResult = domain.RecallResult

// ModuleSummary is an alias for domain.ModuleSummary.
type ModuleSummary = domain.ModuleSummary

// GatewayStatus is an alias for domain.GatewayStatus.
type GatewayStatus = domain.GatewayStatus

// DeployModule validates configuration, starts a downstream client, and registers the module.
func (h *Handler) DeployModule(ctx context.Context, p DeployParams) (DeployResult, error) {
	cfg := domain.ModuleConfig{
		Name:      p.Name,
		Transport: domain.TransportType(p.Transport),
		Command:   p.Command,
		Args:      p.Args,
		URL:       p.URL,
		Env:       p.Env,
		Headers:   p.Headers,
	}

	if err := cfg.Validate(); err != nil {
		return DeployResult{}, fmt.Errorf("invalid module config: %w", err)
	}

	client, err := h.factory.CreateClient(ctx, cfg)
	if err != nil {
		return DeployResult{}, fmt.Errorf("failed to create client: %w", err)
	}

	if err := client.Start(ctx); err != nil {
		return DeployResult{}, fmt.Errorf("failed to start client: %w", err)
	}

	mod := domain.NewModule(cfg)
	if err := h.registry.Register(mod, client); err != nil {
		_ = client.Stop(ctx)
		return DeployResult{}, fmt.Errorf("failed to register module: %w", err)
	}

	toolNames := make([]string, 0, len(mod.Tools))
	for _, t := range mod.Tools {
		toolNames = append(toolNames, t.Name)
	}

	return DeployResult{
		Name:    mod.Name,
		Status:  mod.Status,
		Tools:   toolNames,
		Message: fmt.Sprintf("Module %s successfully deployed with %d tool(s)", mod.Name, len(toolNames)),
	}, nil
}

// RecallModule unregisters and stops the specified module.
func (h *Handler) RecallModule(ctx context.Context, name string) (RecallResult, error) {
	if err := h.registry.Unregister(name); err != nil {
		return RecallResult{}, err
	}

	return RecallResult{
		Name:    name,
		Success: true,
		Message: fmt.Sprintf("Module %s recalled successfully", name),
	}, nil
}

// ToggleModule enables or disables a module by starting or stopping its client and updating registry state.
func (h *Handler) ToggleModule(ctx context.Context, name string, enable bool) (domain.ToggleResult, error) {
	mod, exists := h.registry.GetModule(name)
	if !exists {
		return domain.ToggleResult{}, domain.ErrModuleNotFound
	}

	if !enable {
		if err := h.registry.Deactivate(name); err != nil {
			return domain.ToggleResult{}, err
		}
		return domain.ToggleResult{
			Name:    name,
			Enabled: false,
			Status:  domain.StatusInactive,
			Message: fmt.Sprintf("Module %s disabled", name),
		}, nil
	}

	client, err := h.factory.CreateClient(ctx, mod.Config)
	if err != nil {
		return domain.ToggleResult{}, err
	}
	if err := client.Start(ctx); err != nil {
		return domain.ToggleResult{}, err
	}
	if err := h.registry.Register(mod, client); err != nil {
		return domain.ToggleResult{}, err
	}

	return domain.ToggleResult{
		Name:    name,
		Enabled: true,
		Status:  domain.StatusActive,
		Message: fmt.Sprintf("Module %s enabled", name),
	}, nil
}

// RestartDaemon gracefully reloads all active downstream MCP modules.
func (h *Handler) RestartDaemon(ctx context.Context) (domain.RestartResult, error) {
	for _, mod := range h.registry.ListModules() {
		if mod.Status == domain.StatusActive {
			_ = h.restartModuleClient(ctx, mod)
		}
	}
	return domain.RestartResult{
		Success: true,
		Message: "Daemon and all MCP modules reloaded",
	}, nil
}

// ReauthModule refreshes the OAuth token for a module and restarts its downstream client.
func (h *Handler) ReauthModule(ctx context.Context, name string) (domain.ReauthResult, error) {
	mod, exists := h.registry.GetModule(name)
	if !exists {
		return domain.ReauthResult{}, domain.ErrModuleNotFound
	}
	if mod.Config.OAuth == nil {
		return domain.ReauthResult{}, fmt.Errorf("module %s does not use OAuth", name)
	}

	oauthCfg := *mod.Config.OAuth
	var tok *domain.AuthToken
	var err error

	if oauthCfg.AuthURL != "" && h.tokenProvider != nil {
		tok, err = h.tokenProvider.StartInteractiveFlow(ctx, oauthCfg)
	} else {
		tok, err = h.refreshModuleToken(ctx, oauthCfg)
	}

	if err != nil {
		return domain.ReauthResult{}, err
	}

	if restartErr := h.restartModuleClient(ctx, mod); restartErr != nil {
		h.registry.RegisterError(mod, restartErr)
		return domain.ReauthResult{}, fmt.Errorf("auth succeeded but failed to start module %s: %w", name, restartErr)
	}

	return domain.ReauthResult{
		Name:      name,
		Success:   true,
		ExpiresAt: tok.ExpiresAt,
		Message:   "Auth completed for " + name,
	}, nil
}

func (h *Handler) refreshModuleToken(ctx context.Context, oauthCfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	tok, err := h.authStore.GetToken(ctx, oauthCfg.ServerName)
	if err != nil || tok == nil {
		return nil, fmt.Errorf("no token found for %s", oauthCfg.ServerName)
	}
	return h.tryTokenRenewal(ctx, oauthCfg, tok), nil
}

func (h *Handler) tryTokenRenewal(ctx context.Context, oauthCfg domain.OAuthClientConfig, tok *domain.AuthToken) *domain.AuthToken {
	if !h.canRefreshToken(oauthCfg, tok) {
		return tok
	}
	refreshed, err := h.tokenProvider.RefreshToken(ctx, oauthCfg, tok.RefreshToken)
	if err == nil && refreshed != nil {
		return refreshed
	}
	return tok
}

func (h *Handler) canRefreshToken(oauthCfg domain.OAuthClientConfig, tok *domain.AuthToken) bool {
	return tok.RefreshToken != "" && h.tokenProvider != nil && oauthCfg.TokenURL != ""
}

func (h *Handler) restartModuleClient(ctx context.Context, mod *domain.Module) error {
	client, err := h.factory.CreateClient(ctx, mod.Config)
	if err != nil {
		return err
	}
	if err := client.Start(ctx); err != nil {
		return err
	}
	return h.registry.Register(mod, client)
}

// RecentTraces returns recent tool execution traces from the registry up to limit.
func (h *Handler) RecentTraces(ctx context.Context, limit int) ([]domain.ToolTrace, error) {
	return h.registry.RecentTraces(limit), nil
}

// ListModules returns summaries of all registered modules and their exposed tools.
func (h *Handler) ListModules(ctx context.Context) ([]ModuleSummary, error) {
	modules := h.registry.ListModules()
	summaries := make([]ModuleSummary, 0, len(modules))

	for _, m := range modules {
		toolNames := make([]string, 0, len(m.Tools))
		for _, t := range m.Tools {
			toolNames = append(toolNames, t.Name)
		}
		target := m.Config.Command
		if m.Config.URL != "" {
			target = m.Config.URL
		}
		summaries = append(summaries, ModuleSummary{
			Name:      m.Name,
			Transport: m.Config.Transport,
			Status:    m.Status,
			Target:    target,
			Tools:     toolNames,
			Error:     m.ErrorMessage,
		})
	}

	return summaries, nil
}

// Status returns gateway uptime, active module and tool counts, and memory allocation.
func (h *Handler) Status(ctx context.Context) (GatewayStatus, error) {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	active := 0
	for _, m := range h.registry.ListModules() {
		if m.Status == domain.StatusActive {
			active++
		}
	}

	return GatewayStatus{
		Uptime:        time.Since(h.startedAt).Round(time.Second).String(),
		ActiveModules: active,
		TotalTools:    len(h.registry.ListTools()),
		AllocMB:       memStats.Alloc / 1024 / 1024,
	}, nil
}
