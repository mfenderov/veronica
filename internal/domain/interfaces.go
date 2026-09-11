package domain

import (
	"context"
)

type ToolCall struct {
	ToolName  string `json:"name"`
	Arguments any    `json:"arguments,omitempty"`
}

type ToolContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
}

type ToolResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type DownstreamClient interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, call ToolCall) (ToolResult, error)
	Status() ModuleStatus
}

type AuthStore interface {
	GetToken(ctx context.Context, serverName string) (*AuthToken, error)
	SaveToken(ctx context.Context, token AuthToken) error
	DeleteToken(ctx context.Context, serverName string) error
	ListTokens(ctx context.Context) ([]AuthToken, error)
}

type TokenProvider interface {
	GetToken(ctx context.Context, serverName string) (*AuthToken, error)
	EnsureValidToken(ctx context.Context, cfg OAuthClientConfig) (*AuthToken, error)
	RefreshToken(ctx context.Context, cfg OAuthClientConfig, refreshToken string) (*AuthToken, error)
}
