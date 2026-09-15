package meta

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

// SupervisorDefaults describes how often modules are checked and how long a single
// probe or restart may take before the supervisor gives up on it.
const (
	// DefaultSupervisorInterval is the time between module health checks.
	DefaultSupervisorInterval = 30 * time.Second
	// DefaultProbeTimeout bounds a single liveness probe.
	DefaultProbeTimeout = 5 * time.Second
	// DefaultRestartTimeout bounds a single module restart attempt.
	DefaultRestartTimeout = 30 * time.Second
)

// SupervisorConfig tunes the module supervision loop.
type SupervisorConfig struct {
	// Interval is the delay between full passes over the module catalog.
	Interval time.Duration
	// ProbeTimeout bounds each individual liveness probe.
	ProbeTimeout time.Duration
	// RestartTimeout bounds each individual restart attempt.
	RestartTimeout time.Duration
}

// DefaultSupervisorConfig returns the supervision settings used when a module opts in
// to auto_restart without further tuning.
func DefaultSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		Interval:       DefaultSupervisorInterval,
		ProbeTimeout:   DefaultProbeTimeout,
		RestartTimeout: DefaultRestartTimeout,
	}
}

// Supervisor keeps modules that opted in to auto_restart alive. It probes each of them
// over the live transport and restarts the ones that stopped responding, so a crashed
// downstream process recovers without an operator reloading the whole daemon.
type Supervisor struct {
	handler *Handler
	cfg     SupervisorConfig
}

// NewSupervisor builds a Supervisor, filling in defaults for any unset timing field.
func NewSupervisor(handler *Handler, cfg SupervisorConfig) *Supervisor {
	defaults := DefaultSupervisorConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = defaults.Interval
	}
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = defaults.ProbeTimeout
	}
	if cfg.RestartTimeout <= 0 {
		cfg.RestartTimeout = defaults.RestartTimeout
	}
	return &Supervisor{handler: handler, cfg: cfg}
}

// Config returns the effective supervision settings.
func (s *Supervisor) Config() SupervisorConfig {
	return s.cfg
}

// Run supervises modules until ctx is cancelled, checking on every interval tick.
func (s *Supervisor) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.CheckOnce(ctx)
		}
	}
}

// CheckOnce probes every auto-restart module once, restarting any that is unresponsive
// or still sitting in an error state from a failed start.
func (s *Supervisor) CheckOnce(ctx context.Context) {
	for _, mod := range s.handler.registry.ListModules() {
		if !mod.Config.AutoRestart {
			continue
		}

		switch mod.Status {
		case domain.StatusActive:
			if s.healthy(ctx, mod.Name) {
				continue
			}
		case domain.StatusError:
			// The module never got a client, so there is nothing to probe; retry it outright.
		default:
			// Inactive or mid-transition modules are not supervised.
			continue
		}

		s.restart(ctx, mod)
	}
}

func (s *Supervisor) healthy(ctx context.Context, name string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	return s.handler.registry.ProbeModule(probeCtx, name) == nil
}

func (s *Supervisor) restart(ctx context.Context, mod *domain.Module) {
	slog.Warn("supervisor restarting module",
		"module", mod.Name,
		"status", mod.Status,
		"error", mod.ErrorMessage,
	)

	restartCtx, cancel := context.WithTimeout(ctx, s.cfg.RestartTimeout)
	defer cancel()

	if err := s.handler.restartModuleClient(restartCtx, mod); err != nil {
		s.handler.registry.RegisterError(mod, fmt.Errorf("auto-restart failed: %w", err))
		slog.Error("supervisor failed to restart module", "module", mod.Name, "error", err)
		return
	}

	slog.Info("supervisor restarted module", "module", mod.Name)
}
