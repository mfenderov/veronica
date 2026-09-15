package transport_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mfenderov/veronica/internal/auth"
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
func (d *dummyAuthStore) StartInteractiveFlow(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
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
	client, err := transport.NewDownstreamClient(t.Context(), cfg, authStore)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(t.Context())
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
	client, err := transport.NewDownstreamClient(t.Context(), cfg, authStore)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = client.Start(ctx)
	if err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(t.Context())
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

	client, err := transport.NewDownstreamClient(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(t.Context())
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

// richModuleName is the module name used by the rich-content fixture downstream.
const richModuleName = "rich-service"

// richToolName is the tool exposed by the rich-content fixture downstream.
const richToolName = "rich"

// richContentBlocks returns one content block of every type in the MCP content
// union, including the binary and resource-linked payloads that a text-only
// gateway would silently drop.
func richContentBlocks() []mcp.Content {
	return []mcp.Content{
		mcp.NewTextContent("caption"),
		mcp.NewImageContent("aW1nLWJ5dGVz", "image/png"),
		mcp.NewAudioContent("YXVkaW8tYnl0ZXM=", "audio/wav"),
		mcp.NewResourceLink("file:///tmp/report.txt", "report.txt", "a report", "text/plain"),
		mcp.NewEmbeddedResource(mcp.TextResourceContents{
			URI:      "file:///tmp/notes.txt",
			MIMEType: "text/plain",
			Text:     "notes body",
		}),
		mcp.NewEmbeddedResource(mcp.BlobResourceContents{
			URI:      "file:///tmp/blob.bin",
			MIMEType: "application/octet-stream",
			Blob:     "YmluYXJ5",
		}),
	}
}

// startRichDownstream serves a single tool returning every MCP content type over
// SSE and returns a started client bound to it.
func startRichDownstream(t *testing.T) domain.DownstreamClient {
	t.Helper()

	mcpSrv := server.NewMCPServer("mock-rich", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool(richToolName, mcp.WithDescription("returns every MCP content type")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: richContentBlocks()}, nil
		},
	)

	httpSrv := httptest.NewServer(server.NewSSEServer(mcpSrv))
	t.Cleanup(httpSrv.Close)

	client, err := transport.NewDownstreamClient(t.Context(), domain.ModuleConfig{
		Name:      richModuleName,
		Transport: domain.TransportSSE,
		URL:       httpSrv.URL + "/sse",
	}, &dummyAuthStore{})
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	startCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	if err := client.Start(startCtx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Stop(t.Context())
	})

	return client
}

func TestCallToolPreservesNonTextContent(t *testing.T) {
	t.Parallel()

	client := startRichDownstream(t)

	res, err := client.CallTool(t.Context(), domain.ToolCall{ToolName: richToolName})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	want := []domain.ToolContent{
		{Type: domain.ContentTypeText, Text: "caption"},
		{Type: domain.ContentTypeImage, Data: "aW1nLWJ5dGVz", MIMEType: "image/png"},
		{Type: domain.ContentTypeAudio, Data: "YXVkaW8tYnl0ZXM=", MIMEType: "audio/wav"},
		{Type: domain.ContentTypeLink, MIMEType: "text/plain"},
		{Type: domain.ContentTypeResource, MIMEType: "text/plain"},
		{Type: domain.ContentTypeResource, MIMEType: "application/octet-stream"},
	}
	if len(res.Content) != len(want) {
		t.Fatalf("got %d content blocks, want %d; non-text blocks must not be dropped: %+v",
			len(res.Content), len(want), res.Content)
	}
	for i, w := range want {
		got := res.Content[i]
		if got.Type != w.Type {
			t.Errorf("content[%d].Type = %q, want %q", i, got.Type, w.Type)
		}
		if got.Text != w.Text {
			t.Errorf("content[%d].Text = %q, want %q", i, got.Text, w.Text)
		}
		if got.Data != w.Data {
			t.Errorf("content[%d].Data = %q, want %q", i, got.Data, w.Data)
		}
		if got.MIMEType != w.MIMEType {
			t.Errorf("content[%d].MIMEType = %q, want %q", i, got.MIMEType, w.MIMEType)
		}
	}
}

func TestCallToolPreservesRawContentBlocks(t *testing.T) {
	t.Parallel()

	client := startRichDownstream(t)

	res, err := client.CallTool(t.Context(), domain.ToolCall{ToolName: richToolName})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	originals := richContentBlocks()
	if len(res.Content) != len(originals) {
		t.Fatalf("got %d content blocks, want %d", len(res.Content), len(originals))
	}

	for i, got := range res.Content {
		want, err := mcp.MarshalContent(originals[i])
		if err != nil {
			t.Fatalf("marshaling original content block %d failed: %v", i, err)
		}
		if len(got.Raw) == 0 {
			t.Errorf("content[%d] (%s) did not retain its raw MCP block, want %s", i, got.Type, want)
			continue
		}
		if !bytes.Equal(got.Raw, want) {
			t.Errorf("content[%d] raw block = %s, want %s", i, got.Raw, want)
		}
	}
}

func TestDownstreamSSE_MissingTokenOmitsAuthorizationHeader(t *testing.T) {
	t.Parallel()

	mcpSrv := server.NewMCPServer("mock-noauth", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("ping", mcp.WithDescription("ping tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		},
	)

	sseSrv := server.NewSSEServer(mcpSrv)
	var mu sync.Mutex
	var seen []string
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		sseSrv.ServeHTTP(w, r)
	}))
	defer httpSrv.Close()

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	mgr := auth.NewOAuthManager(store, nil)

	cfg := domain.ModuleConfig{
		Name:      "no-token-svc",
		Transport: domain.TransportSSE,
		URL:       httpSrv.URL + "/sse",
		OAuth: &domain.OAuthClientConfig{
			ServerName: "no-token-svc",
		},
	}
	client, err := transport.NewDownstreamClient(t.Context(), cfg, mgr)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(t.Context())
	}()

	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("client.ListTools failed: %v", err)
	}
	if _, err := client.CallTool(ctx, domain.ToolCall{ToolName: "ping"}); err != nil {
		t.Fatalf("client.CallTool failed: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("expected GET stream + POST initialize requests, got %d", len(got))
	}
	for _, h := range got {
		if h != "" {
			t.Fatalf("expected no Authorization header on token miss, got %q", h)
		}
	}
}

type rotatingTokenProvider struct {
	mu    sync.Mutex
	calls int
}

func (r *rotatingTokenProvider) GetToken(ctx context.Context, serverName string) (*domain.AuthToken, error) {
	return nil, auth.ErrTokenNotFound
}

func (r *rotatingTokenProvider) EnsureValidToken(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	access := "tok-v2"
	if r.calls == 1 {
		access = "tok-v1"
	}
	return &domain.AuthToken{ServerName: cfg.ServerName, AccessToken: access}, nil
}

func (r *rotatingTokenProvider) RefreshToken(ctx context.Context, cfg domain.OAuthClientConfig, refreshToken string) (*domain.AuthToken, error) {
	return nil, nil
}

func (r *rotatingTokenProvider) StartInteractiveFlow(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	return nil, nil
}

func TestDownstreamSSE_ReResolvesAuthorizationPerRequest(t *testing.T) {
	t.Parallel()

	mcpSrv := server.NewMCPServer("mock-rotating", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("ping", mcp.WithDescription("ping tool")),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("pong"), nil
		},
	)

	sseSrv := server.NewSSEServer(mcpSrv)
	var mu sync.Mutex
	var seen []string
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		sseSrv.ServeHTTP(w, r)
	}))
	defer httpSrv.Close()

	cfg := domain.ModuleConfig{
		Name:      "rotating-sse",
		Transport: domain.TransportSSE,
		URL:       httpSrv.URL + "/sse",
		OAuth: &domain.OAuthClientConfig{
			ServerName: "rotating-sse",
		},
	}
	client, err := transport.NewDownstreamClient(t.Context(), cfg, &rotatingTokenProvider{})
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := client.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = client.Stop(t.Context())
	}()

	if _, err := client.ListTools(ctx); err != nil {
		t.Fatalf("client.ListTools failed: %v", err)
	}

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("expected GET stream + POST initialize requests, got %d", len(got))
	}
	if got[0] != "Bearer tok-v1" {
		t.Fatalf("first SSE request Authorization = %q, want Bearer tok-v1", got[0])
	}
	refreshed := false
	for _, h := range got[1:] {
		if h == "Bearer tok-v2" {
			refreshed = true
		}
	}
	if !refreshed {
		t.Fatalf("SSE client reused stale Bearer %q; want per-request re-resolution to Bearer tok-v2", got[0])
	}
}
