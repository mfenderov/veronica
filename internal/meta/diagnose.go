package meta

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

// DiagnosticSeverity classifies how urgently an operator needs to act on a diagnostic.
type DiagnosticSeverity string

const (
	// DiagnosticOK reports a check that passed.
	DiagnosticOK DiagnosticSeverity = "ok"
	// DiagnosticWarning reports something that still works but is likely to break soon.
	DiagnosticWarning DiagnosticSeverity = "warning"
	// DiagnosticError reports something broken that blocks a module from serving tools.
	DiagnosticError DiagnosticSeverity = "error"
)

// Diagnostic is a single finding about one aspect of one module's configuration or runtime state.
type Diagnostic struct {
	Module   string             `json:"module"`
	Check    string             `json:"check"`
	Severity DiagnosticSeverity `json:"severity"`
	Message  string             `json:"message"`
}

// Check names reported in Diagnostic.Check.
const (
	checkName      = "name"
	checkAuth      = "auth"
	checkTransport = "transport"
	checkRuntime   = "runtime"
)

// DiagnosticExpiryWindow is how far ahead of expiry a token is reported as expiring soon.
const DiagnosticExpiryWindow = 15 * time.Minute

// HasErrors reports whether any diagnostic is severe enough to fail a health gate.
func HasErrors(diags []Diagnostic) bool {
	for _, diag := range diags {
		if diag.Severity == DiagnosticError {
			return true
		}
	}
	return false
}

// Diagnose inspects module configuration and stored credentials for every configured module and,
// for modules that are already mounted, probes whether they still respond. It mutates no state,
// so it is safe to run against a live daemon or from a read-only command.
func (h *Handler) Diagnose(ctx context.Context, configs []domain.ModuleConfig, now time.Time) []Diagnostic {
	diags := make([]Diagnostic, 0, len(configs))

	for _, cfg := range configs {
		if err := cfg.Validate(); errors.Is(err, domain.ErrEmptyModuleName) {
			diags = append(diags, Diagnostic{
				Module:   cfg.Name,
				Check:    checkName,
				Severity: DiagnosticError,
				Message:  err.Error(),
			})
			continue
		}

		if cfg.Disabled {
			diags = append(diags, Diagnostic{
				Module:   cfg.Name,
				Check:    checkTransport,
				Severity: DiagnosticOK,
				Message:  fmt.Sprintf("module %s is disabled, skipping checks", cfg.Name),
			})
			continue
		}

		if cfg.OAuth != nil {
			diags = append(diags, h.diagnoseAuth(ctx, cfg, now))
		}

		diags = append(diags, diagnoseTransport(cfg))

		if runtimeDiag, ok := h.diagnoseRuntime(ctx, cfg.Name); ok {
			diags = append(diags, runtimeDiag)
		}
	}

	return diags
}

// diagnoseTransport checks that the module has enough configuration to reach its downstream server.
func diagnoseTransport(cfg domain.ModuleConfig) Diagnostic {
	diag := Diagnostic{Module: cfg.Name, Check: checkTransport}

	if err := cfg.Validate(); err != nil {
		diag.Severity = DiagnosticError
		diag.Message = err.Error()
		return diag
	}

	diag.Severity = DiagnosticOK
	diag.Message = fmt.Sprintf("%s target %s is configured", cfg.Transport, moduleTarget(cfg))
	return diag
}

// diagnoseAuth checks whether a usable OAuth token is on disk for an OAuth-enabled module.
func (h *Handler) diagnoseAuth(ctx context.Context, cfg domain.ModuleConfig, now time.Time) Diagnostic {
	serverName := cfg.OAuth.ServerName
	if serverName == "" {
		serverName = cfg.Name
	}

	diag := Diagnostic{Module: cfg.Name, Check: checkAuth, Severity: DiagnosticError}

	token, err := h.authStore.GetToken(ctx, serverName)
	if errors.Is(err, domain.ErrTokenNotFound) || (err == nil && token == nil) {
		diag.Message = fmt.Sprintf("no token for %s; run: veronica reauth %s", serverName, cfg.Name)
		return diag
	}
	if err != nil {
		diag.Message = fmt.Sprintf("token store unavailable: %v", err)
		return diag
	}

	return describeToken(diag, token, now)
}

// describeToken refines an auth diagnostic with the token's freshness.
func describeToken(diag Diagnostic, token *domain.AuthToken, now time.Time) Diagnostic {
	switch {
	case token.IsExpired(now) && token.RefreshToken == "":
		diag.Message = fmt.Sprintf("token for %s expired at %s and cannot be refreshed; reauthentication required",
			token.ServerName, token.ExpiresAt.Format(time.RFC3339))
	case token.IsExpired(now):
		diag.Severity = DiagnosticWarning
		diag.Message = fmt.Sprintf("token for %s expired at %s; it will be refreshed on next use",
			token.ServerName, token.ExpiresAt.Format(time.RFC3339))
	case token.WillExpireSoon(now, DiagnosticExpiryWindow):
		diag.Severity = DiagnosticWarning
		diag.Message = fmt.Sprintf("token for %s expires at %s; it will be refreshed on next use",
			token.ServerName, token.ExpiresAt.Format(time.RFC3339))
	default:
		diag.Severity = DiagnosticOK
		diag.Message = fmt.Sprintf("token for %s is valid until %s", token.ServerName, token.ExpiresAt.Format(time.RFC3339))
	}

	return diag
}

// diagnoseRuntime reports how a mounted module behaved, probing it only when it is expected to be up.
func (h *Handler) diagnoseRuntime(ctx context.Context, name string) (Diagnostic, bool) {
	mod, ok := h.registry.GetModule(name)
	if !ok {
		return Diagnostic{}, false
	}

	diag := Diagnostic{Module: name, Check: checkRuntime}

	if mod.Status == domain.StatusError {
		diag.Severity = DiagnosticError
		diag.Message = "module failed to start: " + mod.ErrorMessage
		return diag, true
	}

	if err := h.registry.ProbeModule(ctx, name); err != nil {
		diag.Severity = DiagnosticError
		diag.Message = err.Error()
		return diag, true
	}

	diag.Severity = DiagnosticOK
	diag.Message = fmt.Sprintf("%d tools reachable on %s", len(mod.Tools), moduleTarget(mod.Config))
	return diag, true
}

// SummaryRuntime turns a daemon's module summaries into runtime diagnostics, so a
// read-only client can report health without mounting the modules itself.
func SummarizeRuntime(summaries []domain.ModuleSummary) []Diagnostic {
	diags := make([]Diagnostic, 0, len(summaries))

	for _, summary := range summaries {
		diag := Diagnostic{Module: summary.Name, Check: checkRuntime}

		switch {
		case summary.Error != "" || summary.Status == domain.StatusError:
			diag.Severity = DiagnosticError
			diag.Message = "module failed to start: " + summary.Error
		case summary.Status == domain.StatusActive:
			diag.Severity = DiagnosticOK
			diag.Message = fmt.Sprintf("%d tools registered on %s", len(summary.Tools), summary.Target)
		default:
			diag.Severity = DiagnosticWarning
			diag.Message = fmt.Sprintf("module is %s", summary.Status)
		}

		diags = append(diags, diag)
	}

	return diags
}

// moduleTarget renders the endpoint a module talks to for human-readable diagnostics.
func moduleTarget(cfg domain.ModuleConfig) string {
	if cfg.Transport == domain.TransportStdio {
		return strings.TrimSpace(strings.Join(append([]string{cfg.Command}, cfg.Args...), " "))
	}
	return cfg.URL
}
