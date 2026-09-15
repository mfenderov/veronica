package transport_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

func TestUpstreamServer(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	// Register veronica_status custom tool
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_status", Description: "gateway status"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			return transport.ResultText("status: ok, uptime: 10s"), nil
		},
	)

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	// Connect a client to Veronica's upstream SSE server
	mcpClient, err := client.NewSSEMCPClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = mcpClient.Close()
	}()

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{
				Name:    "copilot-mock",
				Version: "1.0.0",
			},
		},
	}
	_, err = mcpClient.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("mcpClient.Initialize failed: %v", err)
	}

	// List tools should show Veronica meta-tools
	toolsRes, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("mcpClient.ListTools failed: %v", err)
	}

	foundStatusTool := false
	for _, tool := range toolsRes.Tools {
		if tool.Name == "veronica_status" {
			foundStatusTool = true
			break
		}
	}
	if !foundStatusTool {
		t.Fatalf("expected veronica_status meta-tool, got: %+v", toolsRes.Tools)
	}

	// Call veronica_status
	callRes, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "veronica_status",
		},
	})
	if err != nil {
		t.Fatalf("CallTool veronica_status failed: %v", err)
	}
	if callRes.IsError {
		t.Fatalf("expected success, got error: %+v", callRes)
	}
}

func TestUpstreamServerStreamablePost(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	// Simulate VS Code sending POST to /sse (or /mcp)
	initPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"vscode","version":"1.0"},"capabilities":{}}}`)

	for _, endpoint := range []string{"/sse", "/mcp"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, httpSrv.URL+endpoint, bytes.NewReader(initPayload))
		if err != nil {
			t.Fatalf("failed to create req for %s: %v", endpoint, err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s failed: %v", endpoint, err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 OK on %s, got %d, body: %s", endpoint, resp.StatusCode, string(body))
		}
	}
}

func TestUpstreamServer_ConcurrentLoad(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	initPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"stress-client","version":"1.0"},"capabilities":{}}}`)

	var wg sync.WaitGroup
	errCount := 0
	var mu sync.Mutex

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, httpSrv.URL+"/sse", bytes.NewReader(initPayload))
			if err != nil {
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				mu.Lock()
				errCount++
				mu.Unlock()
				return
			}
			_ = resp.Body.Close()
		}()
	}

	wg.Wait()

	if errCount > 0 {
		t.Fatalf("%d out of 50 concurrent requests failed", errCount)
	}
}

func TestUpstreamServer_PreservesToolSchemaAndDescription(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"module_name": map[string]any{"type": "string"},
		},
		"required": []string{"module_name"},
	}

	upstream.RegisterCustomTool(
		domain.Tool{
			Name:        "custom_inspect",
			Description: "Custom inspect tool with schema",
			InputSchema: schema,
		},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			return transport.ResultText("inspected"), nil
		},
	)

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	mcpClient, err := client.NewSSEMCPClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = mcpClient.Close()
	}()

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "schema-client", Version: "1.0"},
		},
	}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	toolsRes, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}

	for _, tool := range toolsRes.Tools {
		if tool.Name == "custom_inspect" {
			if tool.Description != "Custom inspect tool with schema" {
				t.Fatalf("expected custom description, got: %s", tool.Description)
			}
			if tool.InputSchema.Type != "object" {
				t.Fatalf("expected object type schema, got: %s", tool.InputSchema.Type)
			}
			if tool.InputSchema.Properties == nil || tool.InputSchema.Properties["module_name"] == nil {
				t.Fatalf("expected module_name property in schema, got: %+v", tool.InputSchema.Properties)
			}
			return
		}
	}
	t.Fatal("custom_inspect tool not found")
}

type stubDownstreamClient struct {
	tools []domain.Tool
}

func (s *stubDownstreamClient) Start(ctx context.Context) error { return nil }

func (s *stubDownstreamClient) Stop(ctx context.Context) error { return nil }

func (s *stubDownstreamClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	return s.tools, nil
}

func (s *stubDownstreamClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return transport.ResultText("downstream: " + call.ToolName), nil
}

func (s *stubDownstreamClient) Status() domain.ModuleStatus { return domain.StatusActive }

// fixedResultClient is a downstream stub that always returns the same result,
// used to exercise how the gateway renders arbitrary content back to a client.
type fixedResultClient struct {
	stubDownstreamClient
	result domain.ToolResult
}

func (s *fixedResultClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return s.result, nil
}

// startResultGateway registers a downstream module whose single tool returns the
// supplied result, and returns a client initialized against the gateway together
// with the context it was started with.
func startResultGateway(t *testing.T, result domain.ToolResult) (*client.Client, context.Context) {
	t.Helper()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "renderer",
		Transport: domain.TransportStdio,
		Command:   "/bin/renderer",
	})
	stub := &fixedResultClient{
		stubDownstreamClient: stubDownstreamClient{
			tools: []domain.Tool{{Name: "render", OriginModule: "renderer"}},
		},
		result: result,
	}
	if err := reg.Register(mod, stub); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	httpSrv := httptest.NewServer(upstream.Handler())
	t.Cleanup(httpSrv.Close)

	mcpClient, err := client.NewSSEMCPClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = mcpClient.Close()
	})

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "render-client", Version: "1.0"},
		},
	}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	return mcpClient, ctx
}

func renderContent(t *testing.T, mcpClient *client.Client, ctx context.Context) []mcp.Content {
	t.Helper()

	res, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "renderer_render"},
	})
	if err != nil {
		t.Fatalf("CallTool renderer_render failed: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected a successful result, got error: %+v", res)
	}
	return res.Content
}

// TestUpstreamServer_RoundTripsRichContent exercises both conversion legs at once:
// a downstream server answering with every content type, through the gateway, out
// to a client that must observe the original blocks rather than flattened text.
func TestUpstreamServer_RoundTripsRichContent(t *testing.T) {
	t.Parallel()

	downstream := startRichDownstream(t)
	inbound, err := downstream.CallTool(t.Context(), domain.ToolCall{ToolName: richToolName})
	if err != nil {
		t.Fatalf("downstream CallTool failed: %v", err)
	}

	mcpClient, ctx := startResultGateway(t, inbound)
	content := renderContent(t, mcpClient, ctx)

	if len(content) != len(richContentBlocks()) {
		t.Fatalf("got %d content blocks, want %d; rich content must survive the gateway: %+v",
			len(content), len(richContentBlocks()), content)
	}

	checks := []struct {
		kind string
		ok   func(mcp.Content) bool
	}{
		{"text", func(c mcp.Content) bool {
			tc, isText := c.(mcp.TextContent)
			return isText && tc.Text == "caption"
		}},
		{"image", func(c mcp.Content) bool {
			tc, isImage := c.(mcp.ImageContent)
			return isImage && tc.Data == "aW1nLWJ5dGVz" && tc.MIMEType == "image/png"
		}},
		{"audio", func(c mcp.Content) bool {
			tc, isAudio := c.(mcp.AudioContent)
			return isAudio && tc.Data == "YXVkaW8tYnl0ZXM=" && tc.MIMEType == "audio/wav"
		}},
		{"resource link", func(c mcp.Content) bool {
			tc, isLink := c.(mcp.ResourceLink)
			return isLink && tc.URI == "file:///tmp/report.txt" &&
				tc.Name == "report.txt" && tc.Description == "a report"
		}},
		{"embedded text resource", func(c mcp.Content) bool {
			tc, isResource := c.(mcp.EmbeddedResource)
			if !isResource {
				return false
			}
			rc, isText := tc.Resource.(mcp.TextResourceContents)
			return isText && rc.URI == "file:///tmp/notes.txt" && rc.Text == "notes body"
		}},
		{"embedded blob resource", func(c mcp.Content) bool {
			tc, isResource := c.(mcp.EmbeddedResource)
			if !isResource {
				return false
			}
			rc, isBlob := tc.Resource.(mcp.BlobResourceContents)
			return isBlob && rc.URI == "file:///tmp/blob.bin" && rc.Blob == "YmluYXJ5"
		}},
	}
	for i, check := range checks {
		if !check.ok(content[i]) {
			t.Errorf("content[%d] (%s) came back as %T: %+v", i, check.kind, content[i], content[i])
		}
	}
}

func TestUpstreamServer_RebuildsBinaryContentWithoutRaw(t *testing.T) {
	t.Parallel()

	mcpClient, ctx := startResultGateway(t, domain.ToolResult{
		Content: []domain.ToolContent{
			{Type: domain.ContentTypeImage, Data: "aW1nLWJ5dGVz", MIMEType: "image/png"},
			{Type: domain.ContentTypeAudio, Data: "YXVkaW8tYnl0ZXM=", MIMEType: "audio/wav"},
		},
	})
	content := renderContent(t, mcpClient, ctx)

	if len(content) != 2 {
		t.Fatalf("got %d content blocks, want 2: %+v", len(content), content)
	}

	image, ok := content[0].(mcp.ImageContent)
	if !ok {
		t.Fatalf("content[0] = %T, want mcp.ImageContent", content[0])
	}
	if image.Data != "aW1nLWJ5dGVz" || image.MIMEType != "image/png" {
		t.Errorf("image content = %+v, want the original data and MIME type", image)
	}

	audio, ok := content[1].(mcp.AudioContent)
	if !ok {
		t.Fatalf("content[1] = %T, want mcp.AudioContent", content[1])
	}
	if audio.Data != "YXVkaW8tYnl0ZXM=" || audio.MIMEType != "audio/wav" {
		t.Errorf("audio content = %+v, want the original data and MIME type", audio)
	}
}

func TestUpstreamServer_RendersPlainTextWithoutRaw(t *testing.T) {
	t.Parallel()

	mcpClient, ctx := startResultGateway(t, domain.ToolResult{
		Content: []domain.ToolContent{{Type: domain.ContentTypeText, Text: "plain answer"}},
	})
	content := renderContent(t, mcpClient, ctx)

	if len(content) != 1 {
		t.Fatalf("got %d content blocks, want 1: %+v", len(content), content)
	}
	text, ok := content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want mcp.TextContent", content[0])
	}
	if text.Text != "plain answer" {
		t.Errorf("text content = %q, want %q", text.Text, "plain answer")
	}
}

func TestUpstreamServer_SurfacesUnrepresentableContent(t *testing.T) {
	t.Parallel()

	mcpClient, ctx := startResultGateway(t, domain.ToolResult{
		Content: []domain.ToolContent{
			{Type: "video", Raw: []byte(`{"type":"video","data":"dmlkZW8="}`)},
		},
	})
	content := renderContent(t, mcpClient, ctx)

	if len(content) != 1 {
		t.Fatalf("got %d content blocks, want 1: %+v", len(content), content)
	}
	text, ok := content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want a visible text placeholder", content[0])
	}
	if !strings.Contains(text.Text, "video") {
		t.Errorf("placeholder %q does not name the unsupported content type", text.Text)
	}
}

func TestUpstreamServer_RemovesRecalledModuleTools(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	upstream := transport.NewUpstreamServer(reg)

	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_status", Description: "gateway status"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			return transport.ResultText("status: ok"), nil
		},
	)

	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "ephemeral",
		Transport: domain.TransportStdio,
		Command:   "/bin/ephemeral",
	})
	stub := &stubDownstreamClient{
		tools: []domain.Tool{
			{Name: "temp_tool", OriginModule: "ephemeral"},
		},
	}
	if err := reg.Register(mod, stub); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	mcpClient, err := client.NewSSEMCPClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := mcpClient.Start(ctx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	defer func() {
		_ = mcpClient.Close()
	}()

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "recall-client", Version: "1.0"},
		},
	}
	if _, err := mcpClient.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	listToolNames := func() []string {
		toolsRes, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			t.Fatalf("ListTools failed: %v", err)
		}
		names := make([]string, 0, len(toolsRes.Tools))
		for _, tool := range toolsRes.Tools {
			names = append(names, tool.Name)
		}
		return names
	}

	contains := func(names []string, want string) bool {
		for _, n := range names {
			if n == want {
				return true
			}
		}
		return false
	}

	before := listToolNames()
	if !contains(before, "ephemeral_temp_tool") {
		t.Fatalf("expected ephemeral_temp_tool before recall, got: %v", before)
	}

	if err := reg.Unregister("ephemeral"); err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	after := listToolNames()
	if contains(after, "ephemeral_temp_tool") {
		t.Fatalf("expected ephemeral_temp_tool to be removed after recall, got: %v", after)
	}
	if contains(after, "temp_tool") {
		t.Fatalf("expected temp_tool alias to be removed after recall, got: %v", after)
	}
	if !contains(after, "veronica_status") {
		t.Fatalf("expected custom veronica_status tool to survive recall, got: %v", after)
	}
}
