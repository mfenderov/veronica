package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TransportType represents the communication transport mechanism used to connect to an MCP module.
type TransportType string

const (
	// TransportStdio indicates standard input/output process communication.
	TransportStdio TransportType = "stdio"
	// TransportSSE indicates Server-Sent Events HTTP communication.
	TransportSSE TransportType = "sse"
	// TransportHTTP indicates modern streamable HTTP communication.
	TransportHTTP TransportType = "http"
)

// ModuleStatus represents the operational lifecycle state of an MCP module.
type ModuleStatus string

const (
	// StatusInactive indicates the module is not running.
	StatusInactive ModuleStatus = "inactive"
	// StatusStarting indicates the module is currently initializing.
	StatusStarting ModuleStatus = "starting"
	// StatusActive indicates the module is running and healthy.
	StatusActive ModuleStatus = "active"
	// StatusError indicates the module encountered an error during initialization or runtime.
	StatusError ModuleStatus = "error"
)

// ModuleConfig defines the configuration needed to launch and manage an MCP module.
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
	// ErrEmptyModuleName is returned when a module name is empty or whitespace.
	ErrEmptyModuleName = errors.New("module name cannot be empty")
	// ErrInvalidTransport is returned when an unsupported transport type is specified.
	ErrInvalidTransport = errors.New("invalid transport type: must be stdio, sse, or http")
	// ErrMissingCommand is returned when a stdio module is configured without a command.
	ErrMissingCommand = errors.New("command is required for stdio transport")
	// ErrMissingURL is returned when an HTTP/SSE module is configured without a URL.
	ErrMissingURL = errors.New("url is required for sse and http transports")
	// ErrModuleNotFound is returned when an operation references a non-existent module.
	ErrModuleNotFound = errors.New("module not found")
	// ErrToolNotFound is returned when an operation references an unregistered tool.
	ErrToolNotFound = errors.New("tool not found")
	// ErrToolExecutionFail is returned when a tool invocation fails downstream.
	ErrToolExecutionFail = errors.New("tool execution failed")
)

// Validate verifies that the module configuration has valid and sufficient settings for its transport.
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

// Tool represents an MCP tool definition with its schema and origin module metadata.
type Tool struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"inputSchema,omitempty"`
	OriginModule string `json:"originModule"`
}

// Module represents a managed downstream MCP module, including its configuration, state, and discovered tools.
type Module struct {
	Name         string       `json:"name"`
	Config       ModuleConfig `json:"config"`
	Status       ModuleStatus `json:"status"`
	ErrorMessage string       `json:"errorMessage,omitempty"`
	Tools        []Tool       `json:"tools"`
	StartedAt    time.Time    `json:"startedAt,omitempty"`
}

// NewModule creates a new Module instance in the inactive state with the given configuration.
func NewModule(cfg ModuleConfig) *Module {
	return &Module{
		Name:   cfg.Name,
		Config: cfg,
		Status: StatusInactive,
		Tools:  make([]Tool, 0),
	}
}

// MarkStarting transitions the module state to starting and clears any previous error message.
func (m *Module) MarkStarting() {
	m.Status = StatusStarting
	m.ErrorMessage = ""
}

// MarkActive transitions the module state to active, sets its exposed tools, and records the start time.
func (m *Module) MarkActive(tools []Tool) {
	m.Status = StatusActive
	m.ErrorMessage = ""
	m.Tools = tools
	m.StartedAt = time.Now()
}

// MarkError transitions the module state to error and records the associated error message.
func (m *Module) MarkError(errMsg string) {
	m.Status = StatusError
	m.ErrorMessage = errMsg
}

// MarkInactive transitions the module state to inactive and removes its registered tools.
func (m *Module) MarkInactive() {
	m.Status = StatusInactive
	m.Tools = nil
}
