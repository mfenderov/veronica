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
	DeployModule(ctx context.Context, p DeployParams) (DeployResult, error)
	RecallModule(ctx context.Context, name string) (RecallResult, error)
	ToggleModule(ctx context.Context, name string, enable bool) (ToggleResult, error)
	ReauthModule(ctx context.Context, name string) (ReauthResult, error)
	RestartDaemon(ctx context.Context) (RestartResult, error)
}
