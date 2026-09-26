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
// It also hotswaps any active or error-state module whose watched binary fingerprint changed on disk.
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
// or still sitting in an error state from a failed start. Modules whose watched binary
// fingerprint changed since the last tick are hotswapped first, including ones still in
// error from a hotswap that failed earlier.
func (s *Supervisor) CheckOnce(ctx context.Context) {
	for _, mod := range s.handler.registry.ListModules() {
		if s.hotswapIfNeeded(ctx, mod) {
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

		print, hashed := s.fingerprint(mod)
		if err := s.restart(ctx, mod); err != nil {
			// Health-triggered restarts report the failure on the module. The module
			// was already unresponsive or errored, so its old client is stopped too.
			slog.Error("supervisor failed to restart module", "module", mod.Name, "error", err)
			s.handler.registry.RegisterError(mod, fmt.Errorf("auto-restart failed: %w", err))
		} else if hashed && mod.Status == domain.StatusActive {
			// The module came up on the binary hashed just before the restart;
			// re-baseline the print so the same change is not hotswapped again.
			s.handler.recordWatch(mod.Name, print, false)
		}
	}
}

func (s *Supervisor) healthy(ctx context.Context, name string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()

	return s.handler.registry.ProbeModule(probeCtx, name) == nil
}

// hotswapIfNeeded restarts mod when its watched binary fingerprint changed since the
// last tick, reporting whether the module was already handled this tick. The first
// sighting only records the fingerprint, so nothing hotswaps on daemon startup. Modules
// in error are watched too, so a hotswap that failed earlier retries on the next tick
// even without auto_restart. A mismatch marks the module stale until a swap lands on the
// new binary; a failed swap keeps the old child serving and the previous fingerprint, so
// the module is never left dead and the change stays visible to the watch.
func (s *Supervisor) hotswapIfNeeded(ctx context.Context, mod *domain.Module) bool {
	if mod.Status != domain.StatusActive && mod.Status != domain.StatusError {
		return false
	}
	print, ok := s.fingerprint(mod)
	if !ok {
		return false
	}
	rec, seen := s.handler.watchState(mod.Name)
	if !seen {
		s.handler.recordWatch(mod.Name, print, false)
		return false
	}
	if rec.baseline == print {
		// The binary matches the running baseline again, so no swap is pending.
		s.handler.recordWatch(mod.Name, rec.baseline, false)
		return false
	}
	// Mismatch seen: the module stays stale until a swap lands on the new binary.
	s.handler.recordWatch(mod.Name, rec.baseline, true)
	if err := s.restart(ctx, mod); err != nil {
		// A failed swap must never kill a healthy module: the old child keeps
		// serving and the old baseline stays recorded, so the change remains
		// visible and the next tick retries (spec Goal 3).
		slog.Warn("hotswap failed; module keeps serving the old binary, retrying next tick",
			"module", mod.Name, "error", err)
		return true
	}
	if mod.Status == domain.StatusActive {
		// The swap landed on the new binary. Anything else keeps the old
		// baseline, so a failed swap stays visible to the next tick.
		s.handler.recordWatch(mod.Name, print, false)
	}
	return true
}

// fingerprint resolves and hashes the module binary for the hotswap watch. ok is false
// when watching does not apply (flag off, no watchable local binary) or when the file
// cannot be resolved or read. The config is read through the registry's locked snapshot,
// so the watch flag can be flipped live without racing this check.
func (s *Supervisor) fingerprint(mod *domain.Module) (string, bool) {
	cfg, ok := s.handler.registry.ConfigSnapshot(mod.Name)
	if !ok {
		return "", false
	}
	if !cfg.WatchBinaryEnabled() || !watchableBinary(cfg) {
		return "", false
	}
	path, ok := resolveModuleBinary(cfg)
	if !ok {
		slog.Warn("supervisor cannot resolve module binary; keeping last fingerprint",
			"module", mod.Name, "command", cfg.Command)
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

// restart stops and respawns mod's client, reporting failure without touching module
// state: restartModuleClient leaves the old client registered and serving on any
// failure, so each caller decides what a failed restart means for the module.
func (s *Supervisor) restart(ctx context.Context, mod *domain.Module) error {
	slog.Warn("supervisor restarting module",
		"module", mod.Name,
		"status", mod.Status,
		"error", mod.ErrorMessage,
	)

	restartCtx, cancel := context.WithTimeout(ctx, s.cfg.RestartTimeout)
	defer cancel()

	if err := s.handler.restartModuleClient(restartCtx, mod); err != nil {
		return err
	}

	slog.Info("supervisor restarted module", "module", mod.Name)
	return nil
}
