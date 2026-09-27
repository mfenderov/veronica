package meta_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

// realClientFactory returns real downstream clients so the test can observe an
// actual child process instead of a mock.
type realClientFactory struct{}

func (realClientFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return transport.NewDownstreamClient(ctx, cfg, nil)
}

// TestToggleModuleRegisterFailureStopsStartedClient pins the cleanup duty of the
// toggle-enable path: when registry.Register fails after a successful Start, the
// started client must be stopped, or its without-cancel child leaks.
func TestToggleModuleRegisterFailureStopsStartedClient(t *testing.T) {
	t.Parallel()

	stdioBin, pidFile := buildToolsListFailingStub(t)

	reg := registry.New()
	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "toggle-leak",
		Transport: domain.TransportStdio,
		Command:   stdioBin,
		Args:      []string{pidFile},
	})
	// Seed a discoverable, non-active module: ToggleModule only needs it to exist.
	reg.RegisterError(mod, errors.New("seeded for toggle test"))

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	handler := meta.NewHandler(reg, store, realClientFactory{})

	// The stub completes initialize but rejects tools/list, so Register fails
	// while the child is still running.
	_, err = handler.ToggleModule(t.Context(), "toggle-leak", true)
	if err == nil {
		t.Fatal("expected ToggleModule to fail when Register fails")
	}
	if !strings.Contains(err.Error(), "list tools") {
		t.Fatalf("expected a tools/list failure from Register, got: %v", err)
	}

	pid := readStubPID(t, pidFile)
	if !stubProcessGone(pid, 2*time.Second) {
		t.Fatalf("leaked child process %d after failed toggle-enable", pid)
	}
}

// buildToolsListFailingStub compiles a stub stdio server that publishes its PID,
// answers the initialize handshake, and rejects every other request (notably
// tools/list) while staying alive. It exits only when stdin closes.
func buildToolsListFailingStub(t *testing.T) (binPath, pidFile string) {
	t.Helper()

	tmpDir := t.TempDir()
	binPath = filepath.Join(tmpDir, "tools-list-failing-stub")
	pidFile = filepath.Join(tmpDir, "child.pid")

	srcCode := `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

func main() {
	if err := os.WriteFile(os.Args[1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		os.Exit(1)
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	out := bufio.NewWriter(os.Stdout)

	for in.Scan() {
		var msg struct {
			ID     json.RawMessage
			Method string
			Params struct {
				ProtocolVersion string
			}
		}
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil || msg.Method == "" || len(msg.ID) == 0 {
			continue
		}

		if msg.Method == "initialize" {
			fmt.Fprintf(out, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"protocolVersion\":%q,\"capabilities\":{},\"serverInfo\":{\"name\":\"stub\",\"version\":\"1.0.0\"}}}\n", msg.ID, msg.Params.ProtocolVersion)
		} else {
			fmt.Fprintf(out, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32601,\"message\":\"method not found\"}}\n", msg.ID)
		}
		out.Flush()
	}
}
`
	srcFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcFile, []byte(srcCode), 0o600); err != nil {
		t.Fatalf("failed to write stub src: %v", err)
	}

	cmd := exec.Command("go", "build", "-o", binPath, srcFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build stub binary: %v, out: %s", err, string(out))
	}
	return binPath, pidFile
}

// readStubPID polls until the stub has published its PID file and returns the PID.
func readStubPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stub never wrote its PID file %s", path)
	return 0
}

// stubProcessGone polls until pid no longer refers to a live process, up to timeout.
func stubProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		proc, err := os.FindProcess(pid)
		if err != nil || proc.Signal(syscall.Signal(0)) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
