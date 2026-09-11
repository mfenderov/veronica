package transport_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
