package meta

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
)

// stubChildClient is a downstream client backed by a real child process, so restart
// tests can prove the old child actually dies after a swap instead of leaking as a
// stale PID.
type stubChildClient struct {
	tools  []domain.Tool
	broken bool

	mu  sync.Mutex
	cmd *exec.Cmd
}

func (c *stubChildClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.broken {
		// Stands in for a module binary that exits 1 immediately.
		return exec.Command("sh", "-c", "exit 1").Run()
	}
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		return err
	}
	c.cmd = cmd
	return nil
}

func (c *stubChildClient) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	_, _ = c.cmd.Process.Wait() // reap, so liveness probes see the PID as gone
	return nil
}

func (c *stubChildClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd == nil || c.cmd.Process == nil {
		return nil, errors.New("stub server is not running")
	}
	if err := c.cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return nil, fmt.Errorf("stub server is gone: %w", err)
	}
	return c.tools, nil
}

func (c *stubChildClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return domain.ToolResult{Content: []domain.ToolContent{{Type: "text", Text: "stub call ok"}}}, nil
}

func (c *stubChildClient) Status() domain.ModuleStatus { return domain.StatusActive }

func (c *stubChildClient) pid() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// stubFactory hands out a fresh stubChildClient per CreateClient call, like a real
// factory spawning a new child per start. Modules marked broken get a client whose
// Start fails, standing in for a module binary that exits 1 immediately.
type stubFactory struct {
	mu      sync.Mutex
	broken  map[string]bool
	created []*stubChildClient
}

func (f *stubFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return f.spawn(cfg), nil
}

func (f *stubFactory) spawn(cfg domain.ModuleConfig) *stubChildClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := &stubChildClient{
		tools:  []domain.Tool{{Name: cfg.Name + "_query", OriginModule: cfg.Name}},
		broken: f.broken[cfg.Name],
	}
	f.created = append(f.created, c)
	return c
}

func (f *stubFactory) markBroken(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.broken[name] = true
}

// last returns the most recently created client, i.e. the one a restart installed.
func (f *stubFactory) last() *stubChildClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.created[len(f.created)-1]
}

func (f *stubFactory) stopAll() {
	f.mu.Lock()
	created := append([]*stubChildClient(nil), f.created...)
	f.mu.Unlock()

	for _, c := range created {
		_ = c.Stop(context.Background())
	}
}

func newRestartFixture(t *testing.T) (*registry.Registry, *Handler, *stubFactory) {
	t.Helper()

	store, err := auth.NewFileStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := registry.New()
	factory := &stubFactory{broken: map[string]bool{}}
	t.Cleanup(factory.stopAll)
	return reg, NewHandler(reg, store, factory), factory
}

// mountStubModule starts a stub child for cfg and registers it, as if the module
// had been deployed earlier.
func mountStubModule(t *testing.T, reg *registry.Registry, factory *stubFactory, cfg domain.ModuleConfig) *stubChildClient {
	t.Helper()

	client := factory.spawn(cfg)
	if err := client.Start(t.Context()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := reg.Register(domain.NewModule(cfg), client); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	return client
}

// assertProcessAlive and assertProcessGone probe a PID with Signal(0), the same
// findProcess check used to confirm a child was retired.
func assertProcessAlive(t *testing.T, pid int, label string) {
	t.Helper()

	p, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("%s: FindProcess(%d) failed: %v", label, pid, err)
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("%s (pid %d) should still be running: %v", label, pid, err)
	}
}

func assertProcessGone(t *testing.T, pid int, label string) {
	t.Helper()

	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		t.Fatalf("%s (pid %d) is still running after the swap (stale PID)", label, pid)
	}
}

func TestRestartModuleClient_ReplacesServingPID(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newRestartFixture(t)
	old := mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/opt/mark42/bin/mark42",
	})
	oldPID := old.pid()
	assertProcessAlive(t, oldPID, "old stub server")

	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to be registered before restart")
	}

	if err := handler.restartModuleClient(t.Context(), mod); err != nil {
		t.Fatalf("restartModuleClient failed: %v", err)
	}

	assertProcessGone(t, oldPID, "old stub server")

	if mod.Status != domain.StatusActive {
		t.Fatalf("expected restarted module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
	if err := reg.ProbeModule(t.Context(), "mark42"); err != nil {
		t.Fatalf("expected the new client to be registered and serving: %v", err)
	}
	newPID := factory.last().pid()
	if newPID == oldPID {
		t.Fatal("expected a fresh child after the restart")
	}
	assertProcessAlive(t, newPID, "new stub server")
}

func TestRestartModuleClient_BrokenBinaryKeepsOld(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newRestartFixture(t)
	old := mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/opt/mark42/bin/mark42",
	})
	oldPID := old.pid()
	factory.markBroken("mark42") // the replacement binary exits 1 immediately

	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to be registered before restart")
	}

	if err := handler.restartModuleClient(t.Context(), mod); err == nil {
		t.Fatal("expected restart to fail for a broken binary")
	}

	if err := reg.ProbeModule(t.Context(), "mark42"); err != nil {
		t.Fatalf("expected the old client to stay registered and serving after a failed restart: %v", err)
	}
	assertProcessAlive(t, oldPID, "old stub server")
}

func TestRestartDaemon_SurfacesErrors(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newRestartFixture(t)
	healthy := mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "healthy",
		Transport: domain.TransportStdio,
		Command:   "/opt/healthy/bin/healthy",
	})
	healthyPID := healthy.pid()
	mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "broken",
		Transport: domain.TransportStdio,
		Command:   "/opt/broken/bin/broken",
	})
	factory.markBroken("broken") // the binary exits 1 on every restart

	res, err := handler.RestartDaemon(t.Context())
	if err != nil {
		t.Fatalf("RestartDaemon failed: %v", err)
	}
	if res.Success {
		t.Fatal("expected success = false when a module fails to reload")
	}
	if !strings.Contains(res.Message, "broken") {
		t.Fatalf("expected the failing module named in the message, got %q", res.Message)
	}

	assertProcessGone(t, healthyPID, "healthy stub server")
	if err := reg.ProbeModule(t.Context(), "healthy"); err != nil {
		t.Fatalf("expected the healthy module to survive the reload: %v", err)
	}
}
