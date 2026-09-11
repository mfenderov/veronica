package transport_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/transport"
)

type dummyAuthStore struct {
	mockToken *domain.AuthToken
}

func (d *dummyAuthStore) GetToken(ctx context.Context, serverName string) (*domain.AuthToken, error) {
	return d.mockToken, nil
}
func (d *dummyAuthStore) SaveToken(ctx context.Context, token domain.AuthToken) error {
	return nil
}
func (d *dummyAuthStore) DeleteToken(ctx context.Context, serverName string) error {
	return nil
}
func (d *dummyAuthStore) ListTokens(ctx context.Context) ([]domain.AuthToken, error) {
	return nil, nil
}
func (d *dummyAuthStore) EnsureValidToken(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	return d.mockToken, nil
}
func (d *dummyAuthStore) RefreshToken(ctx context.Context, cfg domain.OAuthClientConfig, refreshToken string) (*domain.AuthToken, error) {
	return d.mockToken, nil
}

func TestDownstreamHTTPClient(t *testing.T) {
	t.Parallel()

	// 1. Create a mock downstream MCP server over SSE
	mcpSrv := server.NewMCPServer("mock-downstream", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("ping", mcp.WithDescription("ping tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		},
	)

	sseSrv := server.NewSSEServer(mcpSrv)
	httpSrv := httptest.NewServer(sseSrv)
	defer httpSrv.Close()

	// 2. Connect Veronica's downstream HTTP client
	cfg := domain.ModuleConfig{
		Name:      "test-service",
		Transport: domain.TransportSSE,
		URL:       httpSrv.URL + "/sse",
	}

	authStore := &dummyAuthStore{}
	client, err := transport.NewDownstreamClient(context.Background(), cfg, authStore)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(context.Background())
	}()

	// 3. List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("client.ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("expected ping tool, got %+v", tools)
	}

	// 4. Call tool
	res, err := client.CallTool(ctx, domain.ToolCall{ToolName: "ping"})
	if err != nil {
		t.Fatalf("client.CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "pong" {
		t.Fatalf("expected pong response, got %+v", res)
	}
}

func TestDownstreamStreamableHTTPClient(t *testing.T) {
	t.Parallel()

	// 1. Create a mock downstream MCP server over Streamable HTTP (like Slack)
	mcpSrv := server.NewMCPServer("mock-slack", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("slack_ping", mcp.WithDescription("slack ping tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("slack pong"), nil
		},
	)

	streamableSrv := server.NewStreamableHTTPServer(mcpSrv)
	httpSrv := httptest.NewServer(streamableSrv)
	defer httpSrv.Close()

	// 2. Connect Veronica's downstream client via TransportHTTP with OAuth
	cfg := domain.ModuleConfig{
		Name:      "slack",
		Transport: domain.TransportHTTP,
		URL:       httpSrv.URL,
		OAuth: &domain.OAuthClientConfig{
			ClientID: "client-id",
		},
	}

	authStore := &dummyAuthStore{
		mockToken: &domain.AuthToken{AccessToken: "test-oauth-token"},
	}
	client, err := transport.NewDownstreamClient(context.Background(), cfg, authStore)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(context.Background())
	}()

	// 3. List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("client.ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "slack_ping" {
		t.Fatalf("expected slack_ping tool, got %+v", tools)
	}

	// 4. Call tool
	res, err := client.CallTool(ctx, domain.ToolCall{ToolName: "slack_ping"})
	if err != nil {
		t.Fatalf("client.CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "slack pong" {
		t.Fatalf("expected slack pong response, got %+v", res)
	}
}

func TestDownstreamStdioClient(t *testing.T) {
	t.Parallel()

	// Build a mock stdio server binary using go build
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "mock-stdio-mcp")

	srcCode := `package main

import (
	"context"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer("mock-stdio", "1.0.0")
	s.AddTool(
		mcp.NewTool("echo", mcp.WithDescription("echo tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("hello stdio"), nil
		},
	)
	_ = server.ServeStdio(s)
}
`
	srcFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcFile, []byte(srcCode), 0o600); err != nil {
		t.Fatalf("failed to write mock src: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", binPath, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build mock binary: %v, out: %s", err, string(out))
	}

	cfg := domain.ModuleConfig{
		Name:      "stdio-test",
		Transport: domain.TransportStdio,
		Command:   binPath,
	}

	client, err := transport.NewDownstreamClient(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(context.Background())
	}()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("expected echo tool, got %+v", tools)
	}

	res, err := client.CallTool(ctx, domain.ToolCall{ToolName: "echo"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "hello stdio" {
		t.Fatalf("unexpected result: %+v", res)
	}
}
