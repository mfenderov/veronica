// Package transport implements downstream MCP client adapters and the upstream MCP server gateway.
package transport

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mfenderov/veronica/internal/domain"
)

// DownstreamAdapter implements domain.DownstreamClient for connecting to and interacting with an external MCP server.
type DownstreamAdapter struct {
	config        domain.ModuleConfig
	mcpClient     *client.Client
	status        domain.ModuleStatus
	tokenProvider domain.TokenProvider
}

// NewDownstreamClient creates a new downstream client adapter for the given module configuration and token provider.
func NewDownstreamClient(ctx context.Context, cfg domain.ModuleConfig, tokenProvider domain.TokenProvider) (domain.DownstreamClient, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	adapter := &DownstreamAdapter{
		config:        cfg,
		status:        domain.StatusInactive,
		tokenProvider: tokenProvider,
	}

	return adapter, nil
}

// Start launches the downstream transport, connects to the MCP server, and performs initialization.
func (a *DownstreamAdapter) Start(ctx context.Context) error {
	a.status = domain.StatusStarting

	var mcpClient *client.Client
	var err error

	switch a.config.Transport {
	case domain.TransportStdio:
		mcpClient = a.createStdioClient()
	case domain.TransportSSE, domain.TransportHTTP:
		mcpClient, err = a.createHTTPClient()
	default:
		return domain.ErrInvalidTransport
	}

	if err != nil {
		a.status = domain.StatusError
		return fmt.Errorf("failed to create client for %s: %w", a.config.Name, err)
	}

	a.mcpClient = mcpClient

	if err := a.mcpClient.Start(ctx); err != nil {
		a.status = domain.StatusError
		return fmt.Errorf("failed to start downstream transport %s: %w", a.config.Name, err)
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{
				Name:    "veronica-gateway",
				Version: "1.0.0",
			},
		},
	}

	_, err = a.mcpClient.Initialize(ctx, initReq)
	if err != nil {
		a.status = domain.StatusError
		return fmt.Errorf("failed to initialize downstream %s: %w", a.config.Name, err)
	}

	a.status = domain.StatusActive
	return nil
}

func (a *DownstreamAdapter) createStdioClient() *client.Client {
	env := os.Environ()
	for k, v := range a.config.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	stdioTransport := transport.NewStdioWithOptions(a.config.Command, env, a.config.Args)
	//nolint:staticcheck // required for compatibility with Python FastMCP servers that reject server/discover
	return client.NewClient(stdioTransport, client.WithLegacyProtocolOnly())
}

func (a *DownstreamAdapter) getHeaders(ctx context.Context) map[string]string {
	headers := make(map[string]string)
	for k, v := range a.config.Headers {
		headers[k] = v
	}

	if a.tokenProvider == nil {
		return headers
	}

	token := a.resolveToken(ctx)
	if token != nil && token.AccessToken != "" {
		headers["Authorization"] = "Bearer " + token.AccessToken
	}
	return headers
}

func (a *DownstreamAdapter) resolveToken(ctx context.Context) *domain.AuthToken {
	if a.config.OAuth != nil {
		cfg := *a.config.OAuth
		if cfg.ServerName == "" {
			cfg.ServerName = a.config.Name
		}
		tok, err := a.tokenProvider.EnsureValidToken(ctx, cfg)
		if err == nil {
			return tok
		}
		return nil
	}

	tok, err := a.tokenProvider.GetToken(ctx, a.config.Name)
	if err == nil {
		return tok
	}
	return nil
}

func (a *DownstreamAdapter) createHTTPClient() (*client.Client, error) {
	if a.config.Transport == domain.TransportHTTP {
		headerFunc := func(callCtx context.Context) map[string]string {
			return a.getHeaders(callCtx)
		}
		opts := []transport.StreamableHTTPCOption{
			transport.WithHTTPHeaderFunc(headerFunc),
			transport.WithHTTPBasicClient(&http.Client{Timeout: 30 * time.Second}),
		}
		return client.NewStreamableHttpClient(a.config.URL, opts...)
	}

	// Legacy SSE
	return client.NewSSEMCPClient(a.config.URL,
		client.WithHeaderFunc(a.getHeaders),
		client.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
	)
}

// Stop shuts down the downstream client and terminates its underlying transport.
func (a *DownstreamAdapter) Stop(ctx context.Context) error {
	a.status = domain.StatusInactive
	if a.mcpClient != nil {
		return a.mcpClient.Close()
	}
	return nil
}

// ListTools queries the downstream MCP server for its supported tools.
func (a *DownstreamAdapter) ListTools(ctx context.Context) ([]domain.Tool, error) {
	if a.mcpClient == nil {
		return nil, domain.ErrModuleNotFound
	}

	res, err := a.mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	tools := make([]domain.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		tools = append(tools, domain.Tool{
			Name:         t.Name,
			Description:  t.Description,
			InputSchema:  t.InputSchema,
			OriginModule: a.config.Name,
		})
	}

	return tools, nil
}

// CallTool invokes a tool call on the downstream MCP server.
func (a *DownstreamAdapter) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	if a.mcpClient == nil {
		return domain.ToolResult{}, domain.ErrModuleNotFound
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      call.ToolName,
			Arguments: call.Arguments,
		},
	}

	res, err := a.mcpClient.CallTool(ctx, req)
	if err != nil {
		return domain.ToolResult{IsError: true}, fmt.Errorf("tool call failed: %w", err)
	}

	contents := make([]domain.ToolContent, 0, len(res.Content))
	for _, c := range res.Content {
		contents = append(contents, toToolContent(c))
	}

	return domain.ToolResult{
		Content: contents,
		IsError: res.IsError,
	}, nil
}

// toToolContent flattens an MCP content block into the domain model so text-only
// consumers keep working. The verbatim block is retained in Raw so that content
// with no flattened representation survives a gateway round trip unchanged.
func toToolContent(c mcp.Content) domain.ToolContent {
	var content domain.ToolContent

	switch tc := c.(type) {
	case mcp.TextContent:
		content.Type = domain.ContentTypeText
		content.Text = tc.Text
	case mcp.ImageContent:
		content.Type = domain.ContentTypeImage
		content.Data = tc.Data
		content.MIMEType = tc.MIMEType
	case mcp.AudioContent:
		content.Type = domain.ContentTypeAudio
		content.Data = tc.Data
		content.MIMEType = tc.MIMEType
	case mcp.ResourceLink:
		content.Type = domain.ContentTypeLink
		content.MIMEType = tc.MIMEType
	case mcp.EmbeddedResource:
		content.Type = domain.ContentTypeResource
		content.MIMEType = resourceContentsMIMEType(tc.Resource)
	}

	if raw, err := mcp.MarshalContent(c); err == nil {
		content.Raw = raw
	}

	return content
}

// resourceContentsMIMEType reports the MIME type carried by either shape of the
// sealed ResourceContents union.
func resourceContentsMIMEType(resource mcp.ResourceContents) string {
	switch rc := resource.(type) {
	case mcp.TextResourceContents:
		return rc.MIMEType
	case mcp.BlobResourceContents:
		return rc.MIMEType
	default:
		return ""
	}
}

// Status returns the current lifecycle status of the downstream module.
func (a *DownstreamAdapter) Status() domain.ModuleStatus {
	return a.status
}
