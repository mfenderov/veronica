package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
)

type UpstreamServer struct {
	mu          sync.RWMutex
	mcpServer   *server.MCPServer
	sseServer   *server.SSEServer
	streamable  *server.StreamableHTTPServer
	registry    *registry.Registry
	customTools map[string]func(ctx context.Context, args any) (domain.ToolResult, error)
	httpServer  *http.Server
}

func NewUpstreamServer(reg *registry.Registry) *UpstreamServer {
	s := server.NewMCPServer("veronica", "1.0.0")
	sse := server.NewSSEServer(s)
	streamable := server.NewStreamableHTTPServer(s)

	u := &UpstreamServer{
		mcpServer:   s,
		sseServer:   sse,
		streamable:  streamable,
		registry:    reg,
		customTools: make(map[string]func(ctx context.Context, args any) (domain.ToolResult, error)),
	}

	reg.OnToolsChanged(func() {
		u.syncTools()
	})

	u.syncTools()
	return u
}

func (u *UpstreamServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Legacy SSE GET /sse
		if r.Method == http.MethodGet && r.URL.Path == "/sse" {
			u.sseServer.SSEHandler().ServeHTTP(w, r)
			return
		}
		// 2. Legacy SSE POST /message
		if r.URL.Path == "/message" {
			u.sseServer.MessageHandler().ServeHTTP(w, r)
			return
		}
		// 3. Modern Streamable HTTP (handles POST /sse, POST /mcp, GET /mcp, POST /, etc.)
		u.streamable.ServeHTTP(w, r)
	})
}

func (u *UpstreamServer) RegisterCustomTool(
	tool domain.Tool,
	handler func(ctx context.Context, args any) (domain.ToolResult, error),
) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.customTools[tool.Name] = handler
	u.syncToolsLocked()
}

func (u *UpstreamServer) syncTools() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.syncToolsLocked()
}

func (u *UpstreamServer) syncToolsLocked() {
	// Re-register custom tools
	for name, handler := range u.customTools {
		toolName := name
		h := handler
		u.mcpServer.AddTool(
			mcp.NewTool(toolName, mcp.WithDescription("Veronica custom tool")),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				res, err := h(ctx, req.Params.Arguments)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return toMCPResult(res), nil
			},
		)
	}

	// Register downstream aggregated tools from registry
	for _, dt := range u.registry.ListTools() {
		downstreamToolName := dt.Name
		u.mcpServer.AddTool(
			mcp.NewTool(downstreamToolName, mcp.WithDescription(dt.Description)),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				call := domain.ToolCall{
					ToolName:  downstreamToolName,
					Arguments: req.Params.Arguments,
				}
				res, err := u.registry.CallTool(ctx, call)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return toMCPResult(res), nil
			},
		)
	}

	u.mcpServer.SendNotificationToAllClients(mcp.MethodNotificationToolsListChanged, nil)
}

func toMCPResult(res domain.ToolResult) *mcp.CallToolResult {
	contents := make([]mcp.Content, 0, len(res.Content))
	for _, c := range res.Content {
		contents = append(contents, mcp.NewTextContent(c.Text))
	}
	return &mcp.CallToolResult{
		Content: contents,
		IsError: res.IsError,
	}
}

func (u *UpstreamServer) Start(addr string) error {
	u.mu.Lock()
	u.httpServer = &http.Server{
		Addr:    addr,
		Handler: u.Handler(),
	}
	srv := u.httpServer
	u.mu.Unlock()

	return srv.ListenAndServe()
}

func (u *UpstreamServer) ServeStdio() error {
	return server.ServeStdio(u.mcpServer)
}

func (u *UpstreamServer) Shutdown(ctx context.Context) error {
	u.mu.RLock()
	srv := u.httpServer
	u.mu.RUnlock()

	u.sseServer.CloseSessions()
	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

func SerializeJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func ResultText(text string) domain.ToolResult {
	return domain.ToolResult{
		Content: []domain.ToolContent{
			{Type: "text", Text: text},
		},
	}
}

func ResultJSON(v any) domain.ToolResult {
	return ResultText(SerializeJSON(v))
}

func ResultError(err error) domain.ToolResult {
	return domain.ToolResult{
		Content: []domain.ToolContent{
			{Type: "text", Text: fmt.Sprintf("Error: %v", err)},
		},
		IsError: true,
	}
}
