package registry_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
)

type mockClient struct {
	tools  []domain.Tool
	called atomic.Int32
	status domain.ModuleStatus
}

func (m *mockClient) Start(ctx context.Context) error {
	m.status = domain.StatusActive
	return nil
}

func (m *mockClient) Stop(ctx context.Context) error {
	m.status = domain.StatusInactive
	return nil
}

func (m *mockClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	return m.tools, nil
}

func (m *mockClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	m.called.Add(1)
	for _, t := range m.tools {
		if t.Name == call.ToolName {
			return domain.ToolResult{
				Content: []domain.ToolContent{
					{Type: "text", Text: "result from " + t.OriginModule},
				},
			}, nil
		}
	}
	return domain.ToolResult{}, domain.ErrToolNotFound
}

func (m *mockClient) Status() domain.ModuleStatus {
	return m.status
}

func TestRegistryRegisterAndList(t *testing.T) {
	t.Parallel()

	reg := registry.New()

	mod1 := domain.NewModule(domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/bin/mark42",
	})
	client1 := &mockClient{
		tools: []domain.Tool{
			{Name: "search_nodes", OriginModule: "mark42"},
			{Name: "create_entity", OriginModule: "mark42"},
		},
		status: domain.StatusActive,
	}

	var changedCalls atomic.Int32
	reg.OnToolsChanged(func() {
		changedCalls.Add(1)
	})

	err := reg.Register(mod1, client1)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if changedCalls.Load() != 1 {
		t.Fatalf("expected 1 tools_changed notification, got %d", changedCalls.Load())
	}

	// Verify module list
	modules := reg.ListModules()
	if len(modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(modules))
	}

	// Verify aggregated tools list (2 namespaced tools + 2 compatibility aliases)
	tools := reg.ListTools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
	if tools[0].Name != "mark42_search_nodes" {
		t.Fatalf("expected namespaced tool name mark42_search_nodes, got %s", tools[0].Name)
	}

	// Verify namespaced tool routing
	res, err := reg.CallTool(context.Background(), domain.ToolCall{
		ToolName: "mark42_search_nodes",
	})
	if err != nil {
		t.Fatalf("CallTool with namespaced name failed: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "result from mark42" {
		t.Fatalf("unexpected result content: %+v", res)
	}

	// Verify fallback tool routing
	res2, err := reg.CallTool(context.Background(), domain.ToolCall{
		ToolName: "search_nodes",
	})
	if err != nil {
		t.Fatalf("CallTool with fallback name failed: %v", err)
	}
	if len(res2.Content) == 0 || res2.Content[0].Text != "result from mark42" {
		t.Fatalf("unexpected result content: %+v", res2)
	}

	// Verify unknown tool
	_, err = reg.CallTool(context.Background(), domain.ToolCall{
		ToolName: "unknown_tool",
	})
	if !errors.Is(err, domain.ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}

	// Unregister
	err = reg.Unregister("mark42")
	if err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}

	if changedCalls.Load() != 2 {
		t.Fatalf("expected 2 tools_changed notifications, got %d", changedCalls.Load())
	}

	if len(reg.ListTools()) != 0 {
		t.Fatalf("expected 0 tools after unregister, got %d", len(reg.ListTools()))
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()

	reg := registry.New()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mod := domain.NewModule(domain.ModuleConfig{
				Name:      "mod",
				Transport: domain.TransportStdio,
				Command:   "cmd",
			})
			client := &mockClient{
				tools: []domain.Tool{
					{Name: "tool", OriginModule: "mod"},
				},
				status: domain.StatusActive,
			}
			_ = reg.Register(mod, client)
			_ = reg.ListModules()
			_ = reg.ListTools()
			_, _ = reg.CallTool(context.Background(), domain.ToolCall{ToolName: "tool"})
			_ = reg.Unregister("mod")
		}()
	}

	wg.Wait()
}

func TestRegistryDeactivate(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "test-mod",
		Transport: domain.TransportStdio,
		Command:   "/bin/echo",
	})
	client := &mockClient{
		tools:  []domain.Tool{{Name: "test_tool", OriginModule: "test-mod"}},
		status: domain.StatusActive,
	}

	if err := reg.Register(mod, client); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	if len(reg.ListTools()) != 2 {
		t.Fatalf("expected 2 tools before deactivate (namespaced + fallback), got %d", len(reg.ListTools()))
	}

	// Deactivate existing
	if err := reg.Deactivate("test-mod"); err != nil {
		t.Fatalf("deactivate failed: %v", err)
	}

	if len(reg.ListTools()) != 0 {
		t.Fatalf("expected 0 tools after deactivate, got %d", len(reg.ListTools()))
	}

	// Deactivate non-existent
	if err := reg.Deactivate("nonexistent"); !errors.Is(err, domain.ErrModuleNotFound) {
		t.Fatalf("expected ErrModuleNotFound, got %v", err)
	}
}

func TestRegistry_ListModulesDeterministicOrder(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	names := []string{"zeta", "alpha", "gamma", "beta", "delta"}
	for _, n := range names {
		mod := domain.NewModule(domain.ModuleConfig{
			Name:      n,
			Transport: domain.TransportStdio,
			Command:   "/bin/echo",
		})
		_ = reg.Register(mod, &mockClient{status: domain.StatusActive})
	}

	expectedOrder := []string{"alpha", "beta", "delta", "gamma", "zeta"}

	for iter := 0; iter < 10; iter++ {
		list := reg.ListModules()
		if len(list) != len(expectedOrder) {
			t.Fatalf("expected %d modules, got %d", len(expectedOrder), len(list))
		}
		for i, mod := range list {
			if mod.Name != expectedOrder[i] {
				t.Fatalf("iteration %d: expected module %s at index %d, got %s", iter, expectedOrder[i], i, mod.Name)
			}
		}
	}
}
