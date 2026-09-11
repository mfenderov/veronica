package domain

import (
	"context"
)

// ToolCall represents a request to execute an MCP tool with the specified name and arguments.
type ToolCall struct {
	ToolName  string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

// ToolContent represents a single content item returned by an MCP tool execution.
type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

// ToolResult encapsulates the outcome of executing an MCP tool.
type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// DownstreamClient defines the interface for communicating with a downstream MCP server.
type DownstreamClient interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, call ToolCall) (ToolResult, error)
	Status() ModuleStatus
}

// AuthStore defines persistent storage operations for MCP server authentication tokens.
type AuthStore interface {
	GetToken(ctx context.Context, serverName string) (*AuthToken, error)
	SaveToken(ctx context.Context, token AuthToken) error
	DeleteToken(ctx context.Context, serverName string) error
	ListTokens(ctx context.Context) ([]AuthToken, error)
}

// TokenProvider defines operations for obtaining, validating, and refreshing authentication tokens.
type TokenProvider interface {
	GetToken(ctx context.Context, serverName string) (*AuthToken, error)
	EnsureValidToken(ctx context.Context, cfg OAuthClientConfig) (*AuthToken, error)
	RefreshToken(ctx context.Context, cfg OAuthClientConfig, refreshToken string) (*AuthToken, error)
}
