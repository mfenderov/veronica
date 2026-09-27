package meta

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/registry"
	"github.com/mfenderov/veronica/internal/transport"
)

// stubChildClient is a downstream client backed by a real child process, so restart
// tests can prove the old child actually dies after a swap instead of leaking as a
// stale PID.
type stubChildClient struct {
	tools     []domain.Tool
	startFail bool // Start fails immediately, standing in for a binary that exits 1
	toolsFail bool // the child runs but ListTools fails: starts without ever serving

	mu  sync.Mutex
	cmd *exec.Cmd
}

func (c *stubChildClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.startFail {
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

	if c.toolsFail {
		return nil, errors.New("stub server never completed the handshake")
	}
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
// Start fails, standing in for a module binary that exits 1 immediately; modules
// marked toolsFail get a client that starts its child but never serves tools.
type stubFactory struct {
	mu        sync.Mutex
	broken    map[string]bool
	toolsFail map[string]bool
	created   []*stubChildClient
}

func (f *stubFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return f.spawn(cfg), nil
}

func (f *stubFactory) spawn(cfg domain.ModuleConfig) *stubChildClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := &stubChildClient{
		tools:     []domain.Tool{{Name: cfg.Name + "_query", OriginModule: cfg.Name}},
		startFail: f.broken[cfg.Name],
		toolsFail: f.toolsFail[cfg.Name],
	}
	f.created = append(f.created, c)
	return c
}

func (f *stubFactory) markBroken(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.broken[name] = true
}

func (f *stubFactory) markToolsFail(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.toolsFail[name] = true
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
	factory := &stubFactory{broken: map[string]bool{}, toolsFail: map[string]bool{}}
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

// transportFactory creates real downstream adapters, for tests that need the true
// spawn and handshake behavior of a module binary.
type transportFactory struct{}

func (transportFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	return transport.NewDownstreamClient(ctx, cfg, nil)
}

// writeStubBinary writes version bytes to a stub module binary on disk, standing in
// for a binary that is replaced between supervisor ticks.
func writeStubBinary(t *testing.T, path, version string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(version), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

// sleepChildren lists the sleep processes the test process is still parent to —
// running or zombie, since an unreaped child is a leak too. The tests that call
// this are serial, so only their own children can appear.
func sleepChildren() []int {
	me := os.Getpid()
	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue // exited while scanning
		}
		// stat is "pid (comm) state ppid ...", and comm may contain spaces and parens.
		firstParen := bytes.IndexByte(stat, '(')
		lastParen := bytes.LastIndexByte(stat, ')')
		if firstParen < 0 || lastParen < firstParen {
			continue
		}
		if !bytes.Equal(stat[firstParen+1:lastParen], []byte("sleep")) {
			continue
		}
		fields := strings.Fields(string(stat[lastParen+1:]))
		if len(fields) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid != me {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func TestFailedHotswapKeepsOldChild(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newRestartFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeStubBinary(t, bin, "v1")
	old := mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	oldPID := old.pid()
	sup := NewSupervisor(handler, SupervisorConfig{})

	sup.CheckOnce(t.Context()) // first tick only records the fingerprint

	writeStubBinary(t, bin, "v2") // the binary changed on disk
	factory.markBroken("mark42")  // ... and the replacement cannot start

	sup.CheckOnce(t.Context()) // the hotswap fails; the old child must survive it

	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to stay registered after a failed hotswap")
	}
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected module to stay active after failed hotswap, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
	if err := reg.ProbeModule(t.Context(), "mark42"); err != nil {
		t.Fatalf("expected the old client to keep serving after failed hotswap: %v", err)
	}
	assertProcessAlive(t, oldPID, "old stub server") // kept serving, not killed
}

func TestRestartModuleClient_StartFailureLeavesNoChild(t *testing.T) {
	// Serial on purpose: this test inspects children of the test process.
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("LookPath sleep failed: %v", err)
	}

	reg := registry.New()
	store, err := auth.NewFileStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	handler := NewHandler(reg, store, transportFactory{})
	mod := domain.NewModule(domain.ModuleConfig{
		Name:      "hang",
		Transport: domain.TransportStdio,
		Command:   sleepBin,
		Args:      []string{"30"},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	err = handler.restartModuleClient(ctx, mod)
	if err == nil {
		t.Fatal("expected restart to fail when the binary never completes the handshake")
	}
	if !strings.Contains(err.Error(), "failed to initialize") {
		t.Fatalf("expected the handshake to fail after the spawn, got: %v", err)
	}

	if kids := sleepChildren(); len(kids) > 0 {
		t.Fatalf("failed start leaked sleep child process(es) %v", kids)
	}
}

func TestRestartModuleClient_RegisterFailureStopsNewKeepsOld(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newRestartFixture(t)
	old := mountStubModule(t, reg, factory, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/opt/mark42/bin/mark42",
	})
	oldPID := old.pid()
	factory.markToolsFail("mark42") // the replacement starts but never serves tools

	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to be registered before restart")
	}

	if err := handler.restartModuleClient(t.Context(), mod); err == nil {
		t.Fatal("expected restart to fail when the new client cannot list tools")
	}

	assertProcessGone(t, factory.last().pid(), "new stub server")
	if err := reg.ProbeModule(t.Context(), "mark42"); err != nil {
		t.Fatalf("expected the old client to stay registered and serving: %v", err)
	}
	assertProcessAlive(t, oldPID, "old stub server")
}
