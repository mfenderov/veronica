package transport_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		_ = downstream.Stop(context.WithoutCancel(t.Context()))
	})

	warmCtx, cancelWarm := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelWarm()
	if _, err := downstream.CallTool(warmCtx, domain.ToolCall{ToolName: "echo"}); err != nil {
		t.Fatalf("warm-up CallTool failed: %v", err)
	}

	cancelStart()

	// The kill lands promptly in the buggy case; the fixed child serves
	// forever, so a failing probe here means the regression is present.
	assert.Never(t, func() bool {
		callCtx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		_, err := downstream.ListTools(callCtx)
		return err != nil
	}, 2*time.Second, 100*time.Millisecond, "child died after start context cancel")

	probeCtx, cancelProbe := context.WithTimeout(t.Context(), 5*time.Second)
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

// TestDownstreamStdioFailedInitializeDoesNotLeakChild pins the cleanup duty of
// a failed Start. The spawn context is never cancelled, so a child spawned for
// a handshake that fails must be torn down by Start itself: deploy (tools.go
// Start error path) and toggle do not Stop a client whose Start failed.
func TestDownstreamStdioFailedInitializeDoesNotLeakChild(t *testing.T) {
	t.Parallel()

	// Build a stub that spawns, publishes its PID, and never answers
	// initialize: the handshake fails when the caller's context expires.
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "mute-stdio-mcp")
	pidFile := filepath.Join(tmpDir, "child.pid")

	srcCode := `package main

import (
	"io"
	"os"
	"strconv"
)

func main() {
	if err := os.WriteFile(os.Args[1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(1)
	}
	// Serve no MCP at all. Exit only when stdin closes, like a stdio MCP server.
	_, _ = io.Copy(io.Discard, os.Stdin)
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
		Name:      "leak-test",
		Transport: domain.TransportStdio,
		Command:   binPath,
		Args:      []string{pidFile},
	}

	downstream, err := transport.NewDownstreamClient(t.Context(), cfg, nil)
	if err != nil {
		t.Fatalf("NewDownstreamClient failed: %v", err)
	}
	t.Cleanup(func() {
		// Never leave a child behind on a failing run.
		_ = downstream.Stop(context.WithoutCancel(t.Context()))
	})

	// A bounded context, like a tool call's: the handshake times out because
	// the stub never answers initialize.
	startCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := downstream.Start(startCtx); err == nil {
		t.Fatal("Start succeeded against a stub that never answers initialize")
	}

	pid := readChildPID(t, pidFile)
	if !waitForChildExit(pid, 2*time.Second) {
		t.Fatalf("leaked child process %d still running after failed Start", pid)
	}
}

// readChildPID waits until the stub has published its PID file and returns the PID.
func readChildPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		p, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || p <= 0 {
			return false
		}
		pid = p
		return true
	}, 5*time.Second, 20*time.Millisecond, "stub never wrote its PID file %s", path)
	return pid
}

// waitForChildExit waits until pid no longer refers to a live process, up to timeout.
func waitForChildExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		proc, err := os.FindProcess(pid)
		if err != nil || proc.Signal(syscall.Signal(0)) != nil {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
	}
	return false
}
