package domain

import (
	"context"
	"encoding/json"
)

// MCP content block types, mirroring the string values used on the wire by the
// Model Context Protocol content union.
const (
	ContentTypeText     = "text"
	ContentTypeImage    = "image"
	ContentTypeAudio    = "audio"
	ContentTypeLink     = "resource_link"
	ContentTypeResource = "resource"
)

// ToolCall represents a request to execute an MCP tool with the specified name and arguments.
type ToolCall struct {
	ToolName  string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

// ToolContent represents a single content item returned by an MCP tool execution.
//
// Type, Text, Data and MIMEType are the flattened view of the MCP content union
// that text-only consumers can rely on. Raw holds the verbatim MCP content block
// (base64 payloads, resource URIs, annotations, ...) so that block types with no
// flattened representation survive a gateway round trip unchanged.
type ToolContent struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`
	MIMEType string          `json:"mimeType,omitempty"`
	Raw      json.RawMessage `json:"raw,omitempty"`
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
// GetToken returns an error wrapping ErrTokenNotFound when no token is stored.
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
	StartInteractiveFlow(ctx context.Context, cfg OAuthClientConfig) (*AuthToken, error)
}
