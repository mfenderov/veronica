package domain

import (
	"context"
	"time"
)

// DeployParams defines the parameters for dynamically deploying and mounting an MCP module.
type DeployParams struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// DeployResult contains the outcome and details of a module deployment.
type DeployResult struct {
	Name    string       `json:"name"`
	Status  ModuleStatus `json:"status"`
	Tools   []string     `json:"tools"`
	Message string       `json:"message"`
}

// RecallResult contains the result of unmounting and stopping a module.
type RecallResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ToggleResult contains the outcome of enabling or disabling a module.
type ToggleResult struct {
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"`
	Status  ModuleStatus `json:"status"`
	Message string       `json:"message"`
}

// ReauthResult contains the outcome of re-authenticating an OAuth-enabled module.
type ReauthResult struct {
	Name      string    `json:"name"`
	Success   bool      `json:"success"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Message   string    `json:"message"`
}

// ModuleSummary provides a high-level summary of a module's status, target, and registered tools.
type ModuleSummary struct {
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`
	Status    ModuleStatus  `json:"status"`
	Target    string        `json:"target"`
	Tools     []string      `json:"tools"`
	Error     string        `json:"error,omitempty"`
	// Hotswap watch visibility, mirroring the gateway's runtime state. The watch
	// flag is runtime-only and never persisted to the yaml config.
	WatchBinary *bool  `json:"watch_binary,omitempty"`
	WatchStale  bool   `json:"watch_stale,omitempty"`
	Version     string `json:"version,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// WatchBinaryEnabled reports whether binary-change watching applies to this module.
// Unset (nil) means enabled; explicit false opts out.
func (s ModuleSummary) WatchBinaryEnabled() bool {
	return watchBinaryEnabled(s.WatchBinary)
}

// GatewayStatus represents runtime health and resource metrics of the Veronica gateway pod.
type GatewayStatus struct {
	Uptime        string `json:"uptime"`
	ActiveModules int    `json:"active_modules"`
	TotalTools    int    `json:"total_tools"`
	AllocMB       uint64 `json:"alloc_mb"`
}

// RestartResult contains the outcome of reloading the Veronica daemon and modules.
type RestartResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// PodService defines the management interface for querying status and controlling modules in the Veronica pod.
type PodService interface {
	Status(ctx context.Context) (GatewayStatus, error)
	ListModules(ctx context.Context) ([]ModuleSummary, error)
	RecentTraces(ctx context.Context, limit int) ([]ToolTrace, error)
	DeployModule(ctx context.Context, p DeployParams) (DeployResult, error)
	RecallModule(ctx context.Context, name string) (RecallResult, error)
	ToggleModule(ctx context.Context, name string, enable bool) (ToggleResult, error)
	SetWatchBinary(ctx context.Context, name string, enable bool) error
	ReauthModule(ctx context.Context, name string) (ReauthResult, error)
	RestartDaemon(ctx context.Context) (RestartResult, error)
}
