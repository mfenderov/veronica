package meta_test

import (
	"context"
	"testing"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
)

type mockClientFactory struct{}

type mockRunningClient struct {
	tools []domain.Tool
}

func (m *mockRunningClient) Start(ctx context.Context) error { return nil }
func (m *mockRunningClient) Stop(ctx context.Context) error  { return nil }
func (m *mockRunningClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	return m.tools, nil
}
func (m *mockRunningClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return domain.ToolResult{
		Content: []domain.ToolContent{{Type: "text", Text: "mock call ok"}},
	}, nil
}
func (m *mockRunningClient) Status() domain.ModuleStatus { return domain.StatusActive }

func (f *mockClientFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return &mockRunningClient{
		tools: []domain.Tool{
			{Name: cfg.Name + "_query", OriginModule: cfg.Name},
		},
	}, nil
}

func TestMetaToolsLifecycle(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	store, err := auth.NewFileStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	factory := &mockClientFactory{}
	handler := meta.NewHandler(reg, store, factory)

	ctx := context.Background()

	// 1. Initial list modules should be empty
	res, err := handler.ListModules(ctx)
	if err != nil {
		t.Fatalf("ListModules failed: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 modules, got %d", len(res))
	}

	// 2. Deploy a new module
	deployParams := meta.DeployParams{
		Name:      "postgres-cart",
		Transport: "stdio",
		Command:   "/usr/local/bin/pg-mcp",
	}

	deployRes, err := handler.DeployModule(ctx, deployParams)
	if err != nil {
		t.Fatalf("DeployModule failed: %v", err)
	}
	if deployRes.Status != domain.StatusActive {
		t.Fatalf("expected status active, got %s", deployRes.Status)
	}
	if len(deployRes.Tools) != 1 || deployRes.Tools[0] != "postgres-cart_query" {
		t.Fatalf("unexpected tools: %+v", deployRes.Tools)
	}

	// 3. Verify tool is callable through registry
	toolRes, err := reg.CallTool(ctx, domain.ToolCall{ToolName: "postgres-cart_query"})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}
	if len(toolRes.Content) == 0 || toolRes.Content[0].Text != "mock call ok" {
		t.Fatalf("unexpected tool result: %+v", toolRes)
	}

	// 4. Check status
	status, err := handler.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.ActiveModules != 1 || status.TotalTools != 1 {
		t.Fatalf("unexpected status: %+v", status)
	}

	// 5. Recall module
	recallRes, err := handler.RecallModule(ctx, "postgres-cart")
	if err != nil {
		t.Fatalf("RecallModule failed: %v", err)
	}
	if !recallRes.Success {
		t.Fatal("expected recall success")
	}

	// 6. Verify tools list is now empty
	if len(reg.ListTools()) != 0 {
		t.Fatalf("expected 0 tools after recall, got %d", len(reg.ListTools()))
	}
}

func TestMetaToolsToggle(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	store, _ := auth.NewFileStore(t.TempDir() + "/auth.json")
	factory := &mockClientFactory{}
	handler := meta.NewHandler(reg, store, factory)

	ctx := context.Background()

	// Deploy module
	_, err := handler.DeployModule(ctx, meta.DeployParams{
		Name:      "redis-mod",
		Transport: "stdio",
		Command:   "/bin/redis-mcp",
	})
	if err != nil {
		t.Fatalf("DeployModule failed: %v", err)
	}

	// Toggle disable
	toggleRes, err := handler.ToggleModule(ctx, "redis-mod", false)
	if err != nil {
		t.Fatalf("ToggleModule(false) failed: %v", err)
	}
	if toggleRes.Enabled {
		t.Fatal("expected enabled = false")
	}

	// Toggle enable
	toggleRes2, err := handler.ToggleModule(ctx, "redis-mod", true)
	if err != nil {
		t.Fatalf("ToggleModule(true) failed: %v", err)
	}
	if !toggleRes2.Enabled {
		t.Fatal("expected enabled = true")
	}

	// Toggle non-existent module
	_, err = handler.ToggleModule(ctx, "unknown-mod", true)
	if err == nil {
		t.Fatal("expected error toggling unknown module")
	}
}

func TestMetaToolsReauth(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	store, _ := auth.NewFileStore(t.TempDir() + "/auth.json")
	factory := &mockClientFactory{}
	handler := meta.NewHandler(reg, store, factory)

	ctx := context.Background()

	// 1. Reauth non-existent module -> ErrModuleNotFound
	_, err := handler.ReauthModule(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error reauthing nonexistent module")
	}

	// 2. Deploy non-OAuth module -> error
	_, _ = handler.DeployModule(ctx, meta.DeployParams{
		Name:      "stdio-mod",
		Transport: "stdio",
		Command:   "/bin/echo",
	})
	_, err = handler.ReauthModule(ctx, "stdio-mod")
	if err == nil {
		t.Fatal("expected error for non-OAuth module")
	}

	// 3. Register OAuth module and token in store
	oauthMod := domain.NewModule(domain.ModuleConfig{
		Name:      "oauth-service",
		Transport: domain.TransportHTTP,
		URL:       "https://example.com/mcp",
		OAuth: &domain.OAuthClientConfig{
			ServerName: "oauth-service",
		},
	})
	_ = reg.Register(oauthMod, &mockRunningClient{})
	_ = store.SaveToken(ctx, domain.AuthToken{
		ServerName:  "oauth-service",
		AccessToken: "mock-tok",
	})

	res, err := handler.ReauthModule(ctx, "oauth-service")
	if err != nil {
		t.Fatalf("ReauthModule failed: %v", err)
	}
	if !res.Success {
		t.Fatal("expected success = true")
	}
}
