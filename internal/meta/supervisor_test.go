package meta_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
)

// supervisedClient is a downstream client whose liveness can be flipped at will,
// so the supervisor's probes can be driven deterministically.
type supervisedClient struct {
	mu        sync.Mutex
	tools     []domain.Tool
	unhealthy bool
}

func (c *supervisedClient) Start(ctx context.Context) error { return nil }
func (c *supervisedClient) Stop(ctx context.Context) error  { return nil }

func (c *supervisedClient) ListTools(ctx context.Context) ([]domain.Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unhealthy {
		return nil, errors.New("write |1: broken pipe")
	}
	return c.tools, nil
}

func (c *supervisedClient) CallTool(ctx context.Context, call domain.ToolCall) (domain.ToolResult, error) {
	return domain.ToolResult{}, nil
}

func (c *supervisedClient) Status() domain.ModuleStatus { return domain.StatusActive }

func (c *supervisedClient) markUnhealthy() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unhealthy = true
}

// supervisedFactory hands out fresh healthy clients and counts how many times the
// supervisor asked for one, which is how restarts are observed.
type supervisedFactory struct {
	mu       sync.Mutex
	created  int
	createEr error
}

func (f *supervisedFactory) CreateClient(ctx context.Context, cfg domain.ModuleConfig) (domain.DownstreamClient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	if f.createEr != nil {
		return nil, f.createEr
	}
	return &supervisedClient{tools: []domain.Tool{{Name: cfg.Name + "_query", OriginModule: cfg.Name}}}, nil
}

func (f *supervisedFactory) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func newSupervisorFixture(t *testing.T) (*registry.Registry, *meta.Handler, *supervisedFactory) {
	t.Helper()

	store, err := auth.NewFileStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	reg := registry.New()
	factory := &supervisedFactory{}
	return reg, meta.NewHandler(reg, store, factory), factory
}

func mountSupervisedModule(t *testing.T, reg *registry.Registry, cfg domain.ModuleConfig) *supervisedClient {
	t.Helper()

	client := &supervisedClient{tools: []domain.Tool{{Name: cfg.Name + "_query", OriginModule: cfg.Name}}}
	if err := reg.Register(domain.NewModule(cfg), client); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	return client
}

func TestNewSupervisorAppliesDefaults(t *testing.T) {
	t.Parallel()

	_, handler, _ := newSupervisorFixture(t)

	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{})
	if sup.Config() != meta.DefaultSupervisorConfig() {
		t.Fatalf("expected default config, got: %+v", sup.Config())
	}
}

func TestSupervisorRestartsUnresponsiveAutoRestartModule(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	client := mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})
	client.markUnhealthy()

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	if factory.createCount() != 1 {
		t.Fatalf("expected exactly 1 restart, got %d", factory.createCount())
	}
	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to stay registered after restart")
	}
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected restarted module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
	if len(mod.Tools) != 1 {
		t.Fatalf("expected restarted module to rediscover its tools, got %d", len(mod.Tools))
	}
}

func TestSupervisorLeavesHealthyModuleAlone(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	if factory.createCount() != 0 {
		t.Fatalf("expected no restart for healthy module, got %d", factory.createCount())
	}
}

func TestSupervisorIgnoresModulesWithoutAutoRestart(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	client := mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/bin/mark42",
	})
	client.markUnhealthy()

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	if factory.createCount() != 0 {
		t.Fatalf("expected no restart without auto_restart, got %d", factory.createCount())
	}
}

func TestSupervisorIgnoresInactiveModule(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})
	if err := reg.Deactivate("mark42"); err != nil {
		t.Fatalf("Deactivate failed: %v", err)
	}

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	if factory.createCount() != 0 {
		t.Fatalf("expected deactivated module to stay stopped, got %d restarts", factory.createCount())
	}
}

func TestSupervisorRecoversModuleStuckInErrorState(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	failed := domain.NewModule(domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})
	reg.RegisterError(failed, errors.New("start mark42: no such file or directory"))

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	if factory.createCount() != 1 {
		t.Fatalf("expected errored module to be retried once, got %d", factory.createCount())
	}
	mod, _ := reg.GetModule("mark42")
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected recovered module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
}

func TestSupervisorRecordsRestartFailureOnModule(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	factory.createEr = errors.New("no such file or directory")
	client := mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})
	client.markUnhealthy()

	meta.NewSupervisor(handler, meta.SupervisorConfig{}).CheckOnce(t.Context())

	mod, _ := reg.GetModule("mark42")
	if mod.Status != domain.StatusError {
		t.Fatalf("expected module to be marked errored, got %s", mod.Status)
	}
	if !strings.Contains(mod.ErrorMessage, "auto-restart failed") {
		t.Fatalf("expected error to explain the failed restart, got %q", mod.ErrorMessage)
	}
}

func TestSupervisorRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	_, handler, _ := newSupervisorFixture(t)
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Hour})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean shutdown, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestSupervisorRunProbesModulesOnInterval(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	client := mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     "/bin/mark42",
		AutoRestart: true,
	})
	client.markUnhealthy()

	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{
		Interval:     5 * time.Millisecond,
		ProbeTimeout: time.Second,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- sup.Run(ctx) }()

	deadline := time.After(5 * time.Second)
	for factory.createCount() == 0 {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatal("supervisor did not restart the unresponsive module")
		case <-time.After(2 * time.Millisecond):
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

// TestSupervisor_ReportsWatchState checks the watch state module summaries expose:
// a baseline fingerprint after the first sighting, stale while a swap is pending, and
// a fresh baseline once the swap lands.
func TestSupervisor_ReportsWatchState(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mod")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     bin,
		AutoRestart: true,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{})

	summary := func() domain.ModuleSummary {
		t.Helper()
		mods, err := handler.ListModules(t.Context())
		if err != nil {
			t.Fatalf("ListModules failed: %v", err)
		}
		if len(mods) != 1 {
			t.Fatalf("expected 1 module, got %d", len(mods))
		}
		return mods[0]
	}

	sup.CheckOnce(t.Context()) // first sighting only records the baseline
	first := summary()
	if first.Fingerprint == "" {
		t.Fatal("expected baseline fingerprint after first tick")
	}
	if first.WatchStale {
		t.Fatal("expected clean watch state after first tick")
	}

	// A changed binary whose swap fails stays stale on the old baseline.
	factory.createEr = errors.New("spawn failed")
	writeBinary(t, bin, "v2")
	sup.CheckOnce(t.Context())
	pending := summary()
	if !pending.WatchStale {
		t.Fatal("expected stale watch state while the swap is pending")
	}
	if pending.Fingerprint != first.Fingerprint {
		t.Fatalf("expected failed swap to keep baseline %q, got %q", first.Fingerprint, pending.Fingerprint)
	}

	// The next tick lands the swap: fresh baseline, no longer stale.
	factory.createEr = nil
	sup.CheckOnce(t.Context())
	swapped := summary()
	if swapped.WatchStale {
		t.Fatal("expected clean watch state after the swap landed")
	}
	if swapped.Fingerprint == "" || swapped.Fingerprint == first.Fingerprint {
		t.Fatalf("expected new baseline after swap, got %q", swapped.Fingerprint)
	}
}

// writeBinary writes version bytes to path, standing in for a module binary
// that gets replaced between supervisor ticks.
func writeBinary(t *testing.T, path, version string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(version), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
}

func TestSupervisor_RestartsOnFingerprintChange(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context()) // first tick only records the fingerprint
	if factory.createCount() != 0 {
		t.Fatalf("expected first tick to record the fingerprint without restarting, got %d restarts", factory.createCount())
	}

	writeBinary(t, bin, "v2") // replace the binary with new bytes

	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected exactly one restart after fingerprint change, got %d", factory.createCount())
	}
	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to stay registered after hotswap")
	}
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected hotswapped module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
}

func TestSupervisor_NoRestartOnSameContent(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context())

	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(bin, later, later); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 0 {
		t.Fatalf("expected no restart for same content with new mtime, got %d", factory.createCount())
	}
}

func TestSupervisor_DeletedBinaryKeepsModule(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context())

	if err := os.Remove(bin); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 0 {
		t.Fatalf("expected no restart while the binary is missing, got %d", factory.createCount())
	}
	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to stay registered while the binary is missing")
	}
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected module to stay active while the binary is missing, got %s (%s)", mod.Status, mod.ErrorMessage)
	}

	// The old fingerprint is kept, so a binary that comes back changed still hotswaps.
	writeBinary(t, bin, "v2")

	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected one restart after the changed binary reappeared, got %d", factory.createCount())
	}
}

func TestSupervisor_WatchDisabledSkips(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	off := false
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     bin,
		WatchBinary: &off,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context())

	writeBinary(t, bin, "v2")

	sup.CheckOnce(t.Context())
	if factory.createCount() != 0 {
		t.Fatalf("expected no restart with watch_binary disabled, got %d", factory.createCount())
	}
}

func TestSupervisor_ShimNeverWatched(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "npx",
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context())
	sup.CheckOnce(t.Context())

	if factory.createCount() != 0 {
		t.Fatalf("expected shim command to never restart, got %d", factory.createCount())
	}
	mod, ok := reg.GetModule("mark42")
	if !ok {
		t.Fatal("expected module to stay registered")
	}
	if mod.Status != domain.StatusActive || mod.ErrorMessage != "" {
		t.Fatalf("expected shim module to stay active without errors, got %s (%s)", mod.Status, mod.ErrorMessage)
	}
}

func TestSupervisor_TwoUpgradesOneTickRestartsOnce(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context())

	// Two upgrades land inside a single tick window.
	for _, version := range []string{"v2", "v3"} {
		writeBinary(t, bin, version)
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected exactly one restart for two upgrades in one tick, got %d", factory.createCount())
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected the restart to land on the latest fingerprint, got %d restarts", factory.createCount())
	}
}

func TestSupervisor_FailedHotswapRetriesNextTick(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   bin,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context()) // record v1

	factory.createEr = errors.New("spawn failed")
	writeBinary(t, bin, "v2")
	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected one hotswap attempt, got %d", factory.createCount())
	}
	mod, _ := reg.GetModule("mark42")
	if mod.Status != domain.StatusError {
		t.Fatalf("expected module in error after failed hotswap, got %s", mod.Status)
	}

	// The print was rolled back to v1, so the next tick retries even without auto_restart.
	factory.createEr = nil
	sup.CheckOnce(t.Context())
	if factory.createCount() != 2 {
		t.Fatalf("expected the hotswap to retry once the factory works, got %d", factory.createCount())
	}
	mod, _ = reg.GetModule("mark42")
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected retried module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 2 {
		t.Fatalf("expected no further restart after the retry, got %d", factory.createCount())
	}
}

func TestSupervisor_ErrorRecoveryOnNewBinaryRestartsOnce(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	client := mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     bin,
		AutoRestart: true,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context()) // record v1

	// The module stops responding and its restart fails, leaving it in error.
	factory.createEr = errors.New("spawn failed")
	client.markUnhealthy()
	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected one failed restart attempt, got %d", factory.createCount())
	}

	// The binary changes to v2 while the module sits in error. Recovery must
	// restart exactly once and must not hotswap again for the same change.
	writeBinary(t, bin, "v2")
	factory.createEr = nil
	sup.CheckOnce(t.Context())
	if factory.createCount() != 2 {
		t.Fatalf("expected exactly one recovery restart, got %d", factory.createCount())
	}
	mod, _ := reg.GetModule("mark42")
	if mod.Status != domain.StatusActive {
		t.Fatalf("expected recovered module to be active, got %s (%s)", mod.Status, mod.ErrorMessage)
	}

	sup.CheckOnce(t.Context())
	if factory.createCount() != 2 {
		t.Fatalf("expected no second restart for the same binary change, got %d", factory.createCount())
	}
}

func TestSupervisor_HotswapAtMostOneRestartPerTick(t *testing.T) {
	t.Parallel()

	reg, handler, factory := newSupervisorFixture(t)
	bin := filepath.Join(t.TempDir(), "mark42")
	writeBinary(t, bin, "v1")
	mountSupervisedModule(t, reg, domain.ModuleConfig{
		Name:        "mark42",
		Transport:   domain.TransportStdio,
		Command:     bin,
		AutoRestart: true,
	})
	sup := meta.NewSupervisor(handler, meta.SupervisorConfig{Interval: time.Millisecond})

	sup.CheckOnce(t.Context()) // record v1

	// A failing hotswap must not fall through to a second restart attempt in
	// the same tick, even for an auto_restart module.
	factory.createEr = errors.New("spawn failed")
	writeBinary(t, bin, "v2")
	sup.CheckOnce(t.Context())
	if factory.createCount() != 1 {
		t.Fatalf("expected exactly one restart attempt per tick, got %d", factory.createCount())
	}
}
