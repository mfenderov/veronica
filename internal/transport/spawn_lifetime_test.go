package transport_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/transport"
)

// TestDownstreamStdioSpawnSurvivesContextCancel pins the downstream child's
// lifetime to the gateway rather than to the request context that triggered the
// spawn. Deploy, toggle, and reauth run in bounded tool-call contexts, and the
// supervisor restart path uses a 30s timeout; mcp-go's stdio transport spawns
// via exec.CommandContext, so tying the child to that context kills the module
// as soon as the call returns.
func TestDownstreamStdioSpawnSurvivesContextCancel(t *testing.T) {
	t.Parallel()

	// Build a mock stdio server binary the same way downstream_test.go does.
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
			return mcp.NewToolResultText("still serving"), nil
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
		Name:      "spawn-lifetime",
		Transport: domain.TransportStdio,
		Command:   binPath,
	}

	downstream, err := transport.NewDownstreamClient(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}

	// The start context models a bounded tool-call context whose cancellation
	// must not take the spawned child with it.
	startCtx, cancelStart := context.WithCancel(t.Context())
	if err := downstream.Start(startCtx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = downstream.Stop(context.Background())
	})

	warmCtx, cancelWarm := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWarm()
	if _, err := downstream.CallTool(warmCtx, domain.ToolCall{ToolName: "echo"}); err != nil {
		t.Fatalf("warm-up CallTool failed: %v", err)
	}

	cancelStart()

	// Give the exec.CommandContext cancellation path time to kill a
	// context-bound child so a regression fails deterministically instead of
	// racing the probe.
	time.Sleep(500 * time.Millisecond)

	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelProbe()

	if _, err := downstream.ListTools(probeCtx); err != nil {
		t.Fatalf("child stopped serving after start context cancel: ListTools failed: %v", err)
	}

	res, err := downstream.CallTool(probeCtx, domain.ToolCall{ToolName: "echo"})
	if err != nil {
		t.Fatalf("child stopped serving after start context cancel: CallTool failed: %v", err)
	}
	if len(res.Content) == 0 || res.Content[0].Text != "still serving" {
		t.Fatalf("unexpected result from surviving child: %+v", res)
	}
}
