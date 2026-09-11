package domain

import (
	"context"
	"time"
)

type DeployParams struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	URL       string            `json:"url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type DeployResult struct {
	Name    string       `json:"name"`
	Status  ModuleStatus `json:"status"`
	Tools   []string     `json:"tools"`
	Message string       `json:"message"`
}

type RecallResult struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type ToggleResult struct {
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"`
	Status  ModuleStatus `json:"status"`
	Message string       `json:"message"`
}

type ReauthResult struct {
	Name      string    `json:"name"`
	Success   bool      `json:"success"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Message   string    `json:"message"`
}

type ModuleSummary struct {
	Name      string        `json:"name"`
	Transport TransportType `json:"transport"`
	Status    ModuleStatus  `json:"status"`
	Target    string        `json:"target"`
	Tools     []string      `json:"tools"`
	Error     string        `json:"error,omitempty"`
}

type GatewayStatus struct {
	Uptime        string `json:"uptime"`
	ActiveModules int    `json:"active_modules"`
	TotalTools    int    `json:"total_tools"`
	AllocMB       uint64 `json:"alloc_mb"`
}

type PodService interface {
	Status(ctx context.Context) (GatewayStatus, error)
	ListModules(ctx context.Context) ([]ModuleSummary, error)
	DeployModule(ctx context.Context, p DeployParams) (DeployResult, error)
	RecallModule(ctx context.Context, name string) (RecallResult, error)
	ToggleModule(ctx context.Context, name string, enable bool) (ToggleResult, error)
	ReauthModule(ctx context.Context, name string) (ReauthResult, error)
}
