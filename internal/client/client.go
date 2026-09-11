package client

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mfenderov/veronica/internal/domain"
)

type RemotePodClient struct {
	endpoint  string
	mcpClient *client.Client
	cancel    context.CancelFunc
}

var _ domain.PodService = (*RemotePodClient)(nil)

func NewRemotePodClient(endpoint string) (*RemotePodClient, error) {
	if endpoint == "" {
		endpoint = "http://localhost:9090/sse"
	}

	c, err := client.NewSSEMCPClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &RemotePodClient{
		endpoint:  endpoint,
		mcpClient: c,
	}, nil
}

func (c *RemotePodClient) Connect(ctx context.Context) error {
	connCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	if err := c.mcpClient.Start(connCtx); err != nil {
		cancel()
		return fmt.Errorf("connect failed: %w", err)
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{
				Name:    "veronica-remote-client",
				Version: "1.0.0",
			},
		},
	}
	_, err := c.mcpClient.Initialize(ctx, initReq)
	if err != nil {
		cancel()
		return err
	}
	return nil
}

func (c *RemotePodClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.mcpClient != nil {
		_ = c.mcpClient.Close()
	}
}

func (c *RemotePodClient) Status(ctx context.Context) (domain.GatewayStatus, error) {
	res, err := c.callTool(ctx, "veronica_status", nil)
	if err != nil {
		return domain.GatewayStatus{}, err
	}

	var status domain.GatewayStatus
	if err := json.Unmarshal([]byte(res), &status); err != nil {
		return domain.GatewayStatus{}, fmt.Errorf("decode status failed: %w", err)
	}
	return status, nil
}

func (c *RemotePodClient) ListModules(ctx context.Context) ([]domain.ModuleSummary, error) {
	res, err := c.callTool(ctx, "veronica_list_modules", nil)
	if err != nil {
		return nil, err
	}

	var list []domain.ModuleSummary
	if err := json.Unmarshal([]byte(res), &list); err != nil {
		return nil, fmt.Errorf("decode modules list failed: %w", err)
	}
	return list, nil
}

func (c *RemotePodClient) DeployModule(ctx context.Context, p domain.DeployParams) (domain.DeployResult, error) {
	res, err := c.callTool(ctx, "veronica_deploy_module", p)
	if err != nil {
		return domain.DeployResult{}, err
	}

	var result domain.DeployResult
	if err := json.Unmarshal([]byte(res), &result); err != nil {
		return domain.DeployResult{}, fmt.Errorf("decode deploy result failed: %w", err)
	}
	return result, nil
}

func (c *RemotePodClient) RecallModule(ctx context.Context, name string) (domain.RecallResult, error) {
	res, err := c.callTool(ctx, "veronica_recall_module", map[string]string{"name": name})
	if err != nil {
		return domain.RecallResult{}, err
	}

	var result domain.RecallResult
	if err := json.Unmarshal([]byte(res), &result); err != nil {
		return domain.RecallResult{}, fmt.Errorf("decode recall result failed: %w", err)
	}
	return result, nil
}

func (c *RemotePodClient) ToggleModule(ctx context.Context, name string, enable bool) (domain.ToggleResult, error) {
	args := map[string]any{"name": name, "enable": enable}
	res, err := c.callTool(ctx, "veronica_toggle_module", args)
	if err != nil {
		return domain.ToggleResult{}, err
	}

	var result domain.ToggleResult
	if err := json.Unmarshal([]byte(res), &result); err != nil {
		return domain.ToggleResult{}, fmt.Errorf("decode toggle result failed: %w", err)
	}
	return result, nil
}

func (c *RemotePodClient) ReauthModule(ctx context.Context, name string) (domain.ReauthResult, error) {
	args := map[string]any{"name": name}
	res, err := c.callTool(ctx, "veronica_reauth_module", args)
	if err != nil {
		return domain.ReauthResult{}, err
	}

	var result domain.ReauthResult
	if err := json.Unmarshal([]byte(res), &result); err != nil {
		return domain.ReauthResult{}, fmt.Errorf("decode reauth result failed: %w", err)
	}
	return result, nil
}

func (c *RemotePodClient) callTool(ctx context.Context, toolName string, args any) (string, error) {
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}
	res, err := c.mcpClient.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("mcp tool error: %+v", res.Content)
	}
	if len(res.Content) == 0 {
		return "", fmt.Errorf("empty response from %s", toolName)
	}

	if textContent, ok := res.Content[0].(mcp.TextContent); ok {
		return textContent.Text, nil
	}
	return "", fmt.Errorf("unexpected content type from %s", toolName)
}
