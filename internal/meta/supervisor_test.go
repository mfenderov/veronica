package meta_test

import (
	"context"
	"errors"
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
