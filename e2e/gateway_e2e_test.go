package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/config"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

type testClientFactory struct {
	tokenProvider domain.TokenProvider
}

func (f *testClientFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return transport.NewDownstreamClient(ctx, cfg, f.tokenProvider)
}

func buildMockStdioBinary(t *testing.T) string {
	t.Helper()
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
		mcp.NewTool("echo", mcp.WithDescription("echo tool"), mcp.WithString("msg", mcp.Required())),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, _ := req.Params.Arguments.(map[string]any)
			m, _ := args["msg"].(string)
			return mcp.NewToolResultText("echo: " + m), nil
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
	return binPath
}

func setupMockRemoteMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	mcpSrv := server.NewMCPServer("mock-remote", "1.0.0")
	mcpSrv.AddTool(
		mcp.NewTool("remote_query", mcp.WithDescription("remote query tool")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("remote response: ok"), nil
		},
	)

	streamableSrv := server.NewStreamableHTTPServer(mcpSrv)
	authEnforcingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer valid-token" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		streamableSrv.ServeHTTP(w, r)
	})

	srv := httptest.NewServer(authEnforcingHandler)
	t.Cleanup(srv.Close)
	return srv
}

func setupMockOAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "valid-token",
			"refresh_token": "valid-refresh",
			"expires_in": 3600,
			"token_type": "Bearer"
		}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func registerGatewayMetaTools(u *transport.UpstreamServer, h *meta.Handler) {
	u.RegisterCustomTool(domain.Tool{Name: "veronica_status"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		st, err := h.Status(ctx)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(st), nil
	})
	u.RegisterCustomTool(domain.Tool{Name: "veronica_list_modules"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		list, err := h.ListModules(ctx)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(list), nil
	})
	u.RegisterCustomTool(domain.Tool{Name: "veronica_deploy_module"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		var p meta.DeployParams
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &p)
		res, err := h.DeployModule(ctx, p)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(res), nil
	})
	u.RegisterCustomTool(domain.Tool{Name: "veronica_recall_module"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		var p struct {
			Name string `json:"name"`
		}
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &p)
		res, err := h.RecallModule(ctx, p.Name)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(res), nil
	})
	u.RegisterCustomTool(domain.Tool{Name: "veronica_toggle_module"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		var p struct {
			Name   string `json:"name"`
			Enable bool   `json:"enable"`
		}
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &p)
		res, err := h.ToggleModule(ctx, p.Name, p.Enable)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(res), nil
	})
	u.RegisterCustomTool(domain.Tool{Name: "veronica_reauth_module"}, func(ctx context.Context, args any) (domain.ToolResult, error) {
		var p struct {
			Name string `json:"name"`
		}
		b, _ := json.Marshal(args)
		_ = json.Unmarshal(b, &p)
		res, err := h.ReauthModule(ctx, p.Name)
		if err != nil {
			return transport.ResultError(err), nil
		}
		return transport.ResultJSON(res), nil
	})
}

func TestUltimateGatewayE2E(t *testing.T) {
	stdioBin := buildMockStdioBinary(t)
	remoteMCPSrv := setupMockRemoteMCPServer(t)
	oauthSrv := setupMockOAuthServer(t)

	tmpDir := t.TempDir()
	authStore, err := auth.NewFileStore(filepath.Join(tmpDir, "auth.json"))
	if err != nil {
		t.Fatalf("failed to init auth store: %v", err)
	}

	// Prime auth store with an invalid token for remote-mod
	_ = authStore.SaveToken(t.Context(), domain.AuthToken{
		ServerName:  "remote-mod",
		AccessToken: "expired-token",
		ExpiresAt:   time.Now().Add(-1 * time.Hour),
	})

	reg := registry.New()
	oauthMgr := auth.NewOAuthManager(authStore, http.DefaultClient)
	factory := &testClientFactory{tokenProvider: oauthMgr}
	metaHandler := meta.NewHandler(reg, authStore, factory)
	metaHandler.SetTokenProvider(oauthMgr)

	cfg := config.DefaultConfig()
	cfg.AddModule(domain.ModuleConfig{
		Name:      "stdio-mod",
		Transport: domain.TransportStdio,
		Command:   stdioBin,
	})
	cfg.AddModule(domain.ModuleConfig{
		Name:      "remote-mod",
		Transport: domain.TransportHTTP,
		URL:       remoteMCPSrv.URL,
		OAuth: &domain.OAuthClientConfig{
			ServerName:  "remote-mod",
			ClientID:    "test-client-id",
			AuthURL:     oauthSrv.URL + "/authorize",
			TokenURL:    oauthSrv.URL + "/token",
			RedirectURL: "http://127.0.0.1:0/oauth/callback",
		},
	})

	// Mount stdio-mod (must succeed and become active)
	stdioCli, err := factory.CreateClient(t.Context(), cfg.Modules["stdio-mod"])
	if err != nil || stdioCli.Start(t.Context()) != nil {
		t.Fatalf("failed to start stdio mod: %v", err)
	}
	_ = reg.Register(domain.NewModule(cfg.Modules["stdio-mod"]), stdioCli)

	// Mount remote-mod (fails due to invalid token -> must register in StatusError)
	remoteCli, _ := factory.CreateClient(t.Context(), cfg.Modules["remote-mod"])
	if err := remoteCli.Start(t.Context()); err != nil {
		reg.RegisterError(domain.NewModule(cfg.Modules["remote-mod"]), err)
	}

	upstream := transport.NewUpstreamServer(reg)
	registerGatewayMetaTools(upstream, metaHandler)

	gatewaySrv := httptest.NewServer(upstream.Handler())
	t.Cleanup(gatewaySrv.Close)

	// Intercept browser launch and simulate user approving consent in browser
	restoreBrowser := auth.SetOpenBrowserFnForTesting(func(targetURL string) error {
		u, err := url.Parse(targetURL)
		if err != nil {
			return err
		}
		redirectURI := u.Query().Get("redirect_uri")
		state := u.Query().Get("state")

		go func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirectURI+"?code=valid-code-123&state="+state, http.NoBody)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	})
	t.Cleanup(restoreBrowser)

	// Connect external MCP client to Veronica gateway
	mcpClient, err := mcpclient.NewSSEMCPClient(gatewaySrv.URL + "/sse")
	if err != nil {
		t.Fatalf("NewSSEMCPClient failed: %v", err)
	}

	initCtx, initCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer initCancel()

	if err := mcpClient.Start(initCtx); err != nil {
		t.Fatalf("client.Start failed: %v", err)
	}
	t.Cleanup(func() { _ = mcpClient.Close() })

	_, err = mcpClient.Initialize(initCtx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "e2e-tester", Version: "1.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("mcpClient.Initialize failed: %v", err)
	}

	// 1. Verify Initial Tools & Status
	toolsRes, err := mcpClient.ListTools(initCtx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	foundStdioEcho := false
	for _, tool := range toolsRes.Tools {
		if tool.Name == "stdio-mod_echo" {
			foundStdioEcho = true
		}
	}
	if !foundStdioEcho {
		t.Fatal("expected stdio-mod_echo in initial tools list")
	}

	// 2. Call stdio-mod tool through gateway
	echoRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "stdio-mod_echo",
			Arguments: map[string]any{"msg": "ultimate-test"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool stdio-mod_echo failed: %v", err)
	}
	if len(echoRes.Content) == 0 {
		t.Fatal("expected non-empty echo content")
	}

	// 3. Trigger Reauth for remote-mod
	reauthRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "veronica_reauth_module",
			Arguments: map[string]any{"name": "remote-mod"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool veronica_reauth_module failed: %v", err)
	}
	if reauthRes.IsError {
		t.Fatalf("veronica_reauth_module returned error: %+v", reauthRes)
	}

	// 4. Verify remote-mod is now active and its tool is callable
	remoteQueryRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "remote-mod_remote_query",
			Arguments: map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("CallTool remote-mod_remote_query failed: %v", err)
	}
	if remoteQueryRes.IsError {
		t.Fatalf("remote_query returned error: %+v", remoteQueryRes)
	}

	// 5. Dynamic Module Lifecycle (Deploy -> Toggle -> Recall)
	deployRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "veronica_deploy_module",
			Arguments: map[string]any{
				"name":      "dynamic-stdio",
				"transport": "stdio",
				"command":   stdioBin,
			},
		},
	})
	if err != nil || deployRes.IsError {
		t.Fatalf("deploy dynamic-stdio failed: %v, res=%+v", err, deployRes)
	}

	toggleRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "veronica_toggle_module",
			Arguments: map[string]any{
				"name":   "dynamic-stdio",
				"enable": false,
			},
		},
	})
	if err != nil || toggleRes.IsError {
		t.Fatalf("toggle disable dynamic-stdio failed: %v, res=%+v", err, toggleRes)
	}

	recallRes, err := mcpClient.CallTool(initCtx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "veronica_recall_module",
			Arguments: map[string]any{
				"name": "dynamic-stdio",
			},
		},
	})
	if err != nil || recallRes.IsError {
		t.Fatalf("recall dynamic-stdio failed: %v, res=%+v", err, recallRes)
	}
}
