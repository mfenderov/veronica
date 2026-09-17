package client_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/client"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

type mockFactory struct{}

type mockDownstream struct{}

func (m *mockDownstream) Start(ctx context.Context) error { return nil }
func (m *mockDownstream) Stop(ctx context.Context) error  { return nil }
func (m *mockDownstream) ListTools(ctx context.Context) ([]domain.Tool, error) {
	return []domain.Tool{{Name: "test_tool"}}, nil
}
func (m *mockDownstream) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return domain.ToolResult{}, nil
}
func (m *mockDownstream) Status() domain.ModuleStatus { return domain.StatusActive }

func (f *mockFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return &mockDownstream{}, nil
}

func TestRemotePodClient(t *testing.T) {
	t.Parallel()

	// 1. Setup local mock Veronica upstream server
	reg := registry.New()
	store, _ := auth.NewFileStore(t.TempDir() + "/auth.json")
	factory := &mockFactory{}
	metaHandler := meta.NewHandler(reg, store, factory)

	upstream := transport.NewUpstreamServer(reg)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_status"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			st, err := metaHandler.Status(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(st), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_list_modules"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			list, err := metaHandler.ListModules(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(list), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_deploy_module"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			res, err := metaHandler.DeployModule(ctx, meta.DeployParams{
				Name:      "test-mod",
				Transport: "stdio",
				Command:   "/bin/echo",
			})
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_recall_module"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			res, err := metaHandler.RecallModule(ctx, "test-mod")
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_toggle_module"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct {
				Name   string `json:"name"`
				Enable bool   `json:"enable"`
			}
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)
			res, err := metaHandler.ToggleModule(ctx, p.Name, p.Enable)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_reauth_module"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			var p struct {
				Name string `json:"name"`
			}
			b, _ := json.Marshal(args)
			_ = json.Unmarshal(b, &p)
			res, err := metaHandler.ReauthModule(ctx, p.Name)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_restart_daemon"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			res, err := metaHandler.RestartDaemon(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(res), nil
		},
	)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_traces"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			traces, err := metaHandler.RecentTraces(ctx, 10)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(traces), nil
		},
	)

	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	// 2. Connect RemotePodClient
	remote, err := client.NewRemotePodClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewRemotePodClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := remote.Connect(ctx); err != nil {
		t.Fatalf("remote.Connect failed: %v", err)
	}
	defer remote.Close()

	// 3. Test Status
	st, err := remote.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if st.ActiveModules != 0 {
		t.Fatalf("expected 0 active modules, got %d", st.ActiveModules)
	}

	// 4. Test DeployModule
	deployRes, err := remote.DeployModule(ctx, domain.DeployParams{
		Name:      "test-mod",
		Transport: "stdio",
		Command:   "/bin/echo",
	})
	if err != nil {
		t.Fatalf("DeployModule failed: %v", err)
	}
	if deployRes.Status != domain.StatusActive {
		t.Fatalf("expected active status, got %s", deployRes.Status)
	}

	// 5. Test ListModules
	list, err := remote.ListModules(ctx)
	if err != nil {
		t.Fatalf("ListModules failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "test-mod" {
		t.Fatalf("expected test-mod in list, got %+v", list)
	}

	// 6. Test RecallModule
	recallRes, err := remote.RecallModule(ctx, "test-mod")
	if err != nil {
		t.Fatalf("RecallModule failed: %v", err)
	}
	if !recallRes.Success {
		t.Fatal("expected recall success")
	}

	// 7. Test ToggleModule (enable & disable)
	_, _ = remote.DeployModule(ctx, domain.DeployParams{Name: "test-mod", Transport: "stdio", Command: "/bin/echo"})
	toggleRes, err := remote.ToggleModule(ctx, "test-mod", false)
	if err != nil {
		t.Fatalf("ToggleModule(disable) failed: %v", err)
	}
	if toggleRes.Enabled {
		t.Fatal("expected enabled=false")
	}

	toggleRes, err = remote.ToggleModule(ctx, "test-mod", true)
	if err != nil {
		t.Fatalf("ToggleModule(enable) failed: %v", err)
	}
	if !toggleRes.Enabled {
		t.Fatal("expected enabled=true")
	}

	// 9. Test ToggleModule (unknown)
	_, err = remote.ToggleModule(ctx, "unknown-mod", true)
	if err == nil {
		t.Fatal("expected error for unknown module")
	}

	// 10. Test ReauthModule
	_, _ = remote.DeployModule(ctx, domain.DeployParams{Name: "oauth-mod", Transport: "http", URL: "https://example.com"})
	_, err = remote.ReauthModule(ctx, "oauth-mod")
	if err == nil {
		t.Fatal("expected error reauthing module without oauth config")
	}

	// 11. Test RestartDaemon
	restartRes, err := remote.RestartDaemon(ctx)
	if err != nil {
		t.Fatalf("RestartDaemon failed: %v", err)
	}
	if !restartRes.Success {
		t.Fatal("expected restart success = true")
	}

	// 12. Test RecentTraces
	traces, err := remote.RecentTraces(ctx, 10)
	if err != nil {
		t.Fatalf("RecentTraces failed: %v", err)
	}
	_ = traces
}

func TestRemotePodClient_ConnectionSurvivesConnectContextCancel(t *testing.T) {
	t.Parallel()

	reg := registry.New()
	store, _ := auth.NewFileStore(t.TempDir() + "/auth.json")
	factory := &mockFactory{}
	metaHandler := meta.NewHandler(reg, store, factory)
	upstream := transport.NewUpstreamServer(reg)
	upstream.RegisterCustomTool(
		domain.Tool{Name: "veronica_status"},
		func(ctx context.Context, args any) (domain.ToolResult, error) {
			st, err := metaHandler.Status(ctx)
			if err != nil {
				return transport.ResultError(err), nil
			}
			return transport.ResultJSON(st), nil
		},
	)
	httpSrv := httptest.NewServer(upstream.Handler())
	defer httpSrv.Close()

	remote, err := client.NewRemotePodClient(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewRemotePodClient failed: %v", err)
	}
	defer remote.Close()

	connectCtx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	if err := remote.Connect(connectCtx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	cancel() // Cancel the connect context immediately!

	// Subsequent call with fresh context must still succeed
	callCtx, callCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer callCancel()

	_, err = remote.Status(callCtx)
	if err != nil {
		t.Fatalf("Status failed after connectCtx was canceled: %v", err)
	}
}
