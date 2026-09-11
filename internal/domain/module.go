package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type TransportType string

const (
	TransportStdio TransportType = "stdio"
	TransportSSE   TransportType = "sse"
	TransportHTTP  TransportType = "http"
)

type ModuleStatus string

const (
	StatusInactive ModuleStatus = "inactive"
	StatusStarting ModuleStatus = "starting"
	StatusActive   ModuleStatus = "active"
	StatusError    ModuleStatus = "error"
)

type ModuleConfig struct {
	Name        string             `json:"name" yaml:"name"`
	Transport   TransportType      `json:"transport" yaml:"transport"`
	Command     string             `json:"command,omitempty" yaml:"command,omitempty"`
	Args        []string           `json:"args,omitempty" yaml:"args,omitempty"`
	URL         string             `json:"url,omitempty" yaml:"url,omitempty"`
	Env         map[string]string  `json:"env,omitempty" yaml:"env,omitempty"`
	Headers     map[string]string  `json:"headers,omitempty" yaml:"headers,omitempty"`
	OAuth       *OAuthClientConfig `json:"oauth,omitempty" yaml:"oauth,omitempty"`
	Disabled    bool               `json:"disabled,omitempty" yaml:"disabled,omitempty"`
	AutoRestart bool               `json:"auto_restart,omitempty" yaml:"auto_restart,omitempty"`
}

var (
	ErrEmptyModuleName   = errors.New("module name cannot be empty")
	ErrInvalidTransport  = errors.New("invalid transport type: must be stdio, sse, or http")
	ErrMissingCommand    = errors.New("command is required for stdio transport")
	ErrMissingURL        = errors.New("url is required for sse and http transports")
	ErrModuleNotFound    = errors.New("module not found")
	ErrToolNotFound      = errors.New("tool not found")
	ErrToolExecutionFail = errors.New("tool execution failed")
)

func (c ModuleConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return ErrEmptyModuleName
	}

	switch c.Transport {
	case TransportStdio:
		if strings.TrimSpace(c.Command) == "" {
			return ErrMissingCommand
		}
	case TransportSSE, TransportHTTP:
		if strings.TrimSpace(c.URL) == "" {
			return ErrMissingURL
		}
	default:
		return fmt.Errorf("%w: %s", ErrInvalidTransport, c.Transport)
	}

	return nil
}

type Tool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"inputSchema,omitempty"`
	OriginModule string `json:"originModule"`
}

type Module struct {
	Name         string        `json:"name"`
	Config       ModuleConfig  `json:"config"`
	Status       ModuleStatus  `json:"status"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
	Tools        []Tool        `json:"tools"`
	StartedAt    time.Time     `json:"startedAt,omitempty"`
}

func NewModule(cfg ModuleConfig) *Module {
	return &Module{
		Name:   cfg.Name,
		Config: cfg,
		Status: StatusInactive,
		Tools:  make([]Tool, 0),
	}
}

func (m *Module) MarkStarting() {
	m.Status = StatusStarting
	m.ErrorMessage = ""
}

func (m *Module) MarkActive(tools []Tool) {
	m.Status = StatusActive
	m.ErrorMessage = ""
	m.Tools = tools
	m.StartedAt = time.Now()
}

func (m *Module) MarkError(errMsg string) {
	m.Status = StatusError
	m.ErrorMessage = errMsg
}

func (m *Module) MarkInactive() {
	m.Status = StatusInactive
	m.Tools = nil
}
