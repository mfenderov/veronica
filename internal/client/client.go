// Package client provides an MCP client adapter for communicating with a remote Veronica gateway pod.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mfenderov/veronica/internal/domain"
)

const gatewayTokenEnv = "VERONICA_GATEWAY_TOKEN"

// RemotePodClient implements domain.PodService over an MCP transport connecting to a remote Veronica gateway.
type RemotePodClient struct {
	endpoint  string
	mcpClient *client.Client
	cancel    context.CancelFunc
}

var _ domain.PodService = (*RemotePodClient)(nil)

// NewRemotePodClient creates a RemotePodClient configured to connect to the given endpoint.
func NewRemotePodClient(endpoint string) (*RemotePodClient, error) {
	if endpoint == "" {
		endpoint = "http://localhost:9090/sse"
	}

	c, err := client.NewSSEMCPClient(endpoint, client.WithHeaderFunc(func(context.Context) map[string]string {
		token := strings.TrimSpace(os.Getenv(gatewayTokenEnv))
		if token == "" {
			return map[string]string{}
		}
		return map[string]string{"Authorization": "Bearer " + token}
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &RemotePodClient{
		endpoint:  endpoint,
		mcpClient: c,
	}, nil
}

// Connect establishes an MCP session with the remote Veronica gateway.
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

// Close cancels the connection context and closes the underlying MCP client.
func (c *RemotePodClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.mcpClient != nil {
		_ = c.mcpClient.Close()
	}
}

// Status retrieves runtime status metrics from the remote Veronica gateway.
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

// ListModules retrieves the list of modules registered in the remote Veronica gateway.
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

// DeployModule requests the remote Veronica gateway to deploy and mount a new module.
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

// RecallModule requests the remote Veronica gateway to recall and stop a module.
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

// ToggleModule requests the remote Veronica gateway to enable or disable a module.
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

// ReauthModule requests the remote Veronica gateway to re-authenticate an OAuth module.
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

// RestartDaemon calls the veronica_restart_daemon tool to reload daemon modules.
func (c *RemotePodClient) RestartDaemon(ctx context.Context) (domain.RestartResult, error) {
	res, err := c.callTool(ctx, "veronica_restart_daemon", nil)
	if err != nil {
		return domain.RestartResult{}, err
	}

	var result domain.RestartResult
	if err := json.Unmarshal([]byte(res), &result); err != nil {
		return domain.RestartResult{}, fmt.Errorf("decode restart result failed: %w", err)
	}
	return result, nil
}

// RecentTraces retrieves the list of recent tool execution traces from the remote Veronica gateway.
func (c *RemotePodClient) RecentTraces(ctx context.Context, limit int) ([]domain.ToolTrace, error) {
	args := map[string]any{"limit": limit}
	res, err := c.callTool(ctx, "veronica_traces", args)
	if err != nil {
		return nil, err
	}

	var traces []domain.ToolTrace
	if err := json.Unmarshal([]byte(res), &traces); err != nil {
		return nil, fmt.Errorf("decode traces failed: %w", err)
	}
	return traces, nil
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
