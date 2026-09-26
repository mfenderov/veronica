package meta

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
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
// It also hotswaps any active module whose watched binary fingerprint changed on disk.
type Supervisor struct {
	handler *Handler
	cfg     SupervisorConfig
	// prints holds the last-seen binary fingerprint per module name, so a changed
	// binary triggers exactly one hotswap restart per change.
	prints map[string]string
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
	return &Supervisor{handler: handler, cfg: cfg, prints: make(map[string]string)}
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
// or still sitting in an error state from a failed start. Active modules whose watched
// binary fingerprint changed since the last tick are hotswapped first.
func (s *Supervisor) CheckOnce(ctx context.Context) {
	for _, mod := range s.handler.registry.ListModules() {
		if mod.Status == domain.StatusActive && s.binaryChanged(mod) {
			s.restart(ctx, mod)
			continue
		}

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

// binaryChanged reports whether mod's binary contents differ from the fingerprint
// recorded on an earlier tick. The first sighting only records the fingerprint, so
// nothing hotswaps on daemon startup. Modules that cannot be watched keep their
// last fingerprint and never trigger a restart.
func (s *Supervisor) binaryChanged(mod *domain.Module) bool {
	if !mod.Config.WatchBinaryEnabled() {
		return false
	}
	print, ok := s.fingerprint(mod)
	if !ok {
		return false
	}
	old, seen := s.prints[mod.Name]
	s.prints[mod.Name] = print
	return seen && old != print
}

// fingerprint resolves and hashes the module binary. ok is false when the module
// has no watchable local binary or when the file cannot be resolved or read.
func (s *Supervisor) fingerprint(mod *domain.Module) (string, bool) {
	if !watchableBinary(mod.Config) {
		return "", false
	}
	path, ok := resolveModuleBinary(mod.Config)
	if !ok {
		slog.Warn("supervisor cannot resolve module binary; keeping last fingerprint",
			"module", mod.Name, "command", mod.Config.Command)
		return "", false
	}
	print, err := hashFile(path)
	if err != nil {
		slog.Warn("supervisor cannot hash module binary; keeping last fingerprint",
			"module", mod.Name, "path", path, "error", err)
		return "", false
	}
	return print, true
}

// watchableBinary reports whether the module is configured with a local binary
// worth watching: stdio through a real command, not a shim launcher (npx, bunx, ...)
// or a remote endpoint. Those are never watched and never warn.
func watchableBinary(cfg domain.ModuleConfig) bool {
	cmd := strings.TrimSpace(cfg.Command)
	if cfg.Transport != domain.TransportStdio || cmd == "" {
		return false
	}
	return !shimCommands[filepath.Base(cmd)]
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
