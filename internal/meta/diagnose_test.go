package meta_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
	"github.com/mfenderov/veronica/internal/meta"
	"github.com/mfenderov/veronica/internal/registry"
)

// brokenStore fails every token lookup, standing in for an unreadable auth.json.
type brokenStore struct{ err error }

func (s brokenStore) GetToken(context.Context, string) (*domain.AuthToken, error) {
	return nil, s.err
}
func (s brokenStore) SaveToken(context.Context, domain.AuthToken) error { return nil }
func (s brokenStore) DeleteToken(context.Context, string) error         { return nil }
func (s brokenStore) ListTokens(context.Context) ([]domain.AuthToken, error) {
	return nil, s.err
}

func newDiagnoseStore(t *testing.T) *auth.FileAuthStore {
	t.Helper()

	store, err := auth.NewFileStore(t.TempDir() + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	return store
}

func findDiagnostic(diags []meta.Diagnostic, module, check string) (meta.Diagnostic, bool) {
	for _, d := range diags {
		if d.Module == module && d.Check == check {
			return d, true
		}
	}
	return meta.Diagnostic{}, false
}

func oauthModule(name string) domain.ModuleConfig {
	return domain.ModuleConfig{
		Name:      name,
		Transport: domain.TransportHTTP,
		URL:       "https://mcp.example.com/sse",
		OAuth: &domain.OAuthClientConfig{
			ServerName: name,
			AuthURL:    "https://auth.example.com/authorize",
			TokenURL:   "https://auth.example.com/token",
		},
	}
}

func stdioModule(name string) domain.ModuleConfig {
	return domain.ModuleConfig{Name: name, Transport: domain.TransportStdio, Command: "/usr/bin/true"}
}

func TestDiagnoseReportsMissingTokenForOAuthModule(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, time.Now())

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("missing token should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "no token") {
		t.Fatalf("expected message to explain the missing token, got %q", diag.Message)
	}
}

func TestDiagnoseReportsValidTokenAsHealthy(t *testing.T) {
	store := newDiagnoseStore(t)
	now := time.Now()
	if err := store.SaveToken(t.Context(), domain.AuthToken{
		ServerName:  "atlassian",
		AccessToken: "fresh",
		ExpiresAt:   now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}
	handler := meta.NewHandler(registry.New(), store, &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, now)

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticOK {
		t.Fatalf("valid token should be ok, got %q (%s)", diag.Severity, diag.Message)
	}
}

func TestDiagnoseWarnsAboutExpiredTokenThatCanBeRefreshed(t *testing.T) {
	store := newDiagnoseStore(t)
	now := time.Now()
	if err := store.SaveToken(t.Context(), domain.AuthToken{
		ServerName:   "atlassian",
		AccessToken:  "stale",
		RefreshToken: "refresh-me",
		ExpiresAt:    now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}
	handler := meta.NewHandler(registry.New(), store, &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, now)

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticWarning {
		t.Fatalf("refreshable expiry should be a warning, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "expired") {
		t.Fatalf("expected message to mention expiry, got %q", diag.Message)
	}
}

func TestDiagnoseErrorsOnExpiredTokenWithoutRefreshToken(t *testing.T) {
	store := newDiagnoseStore(t)
	now := time.Now()
	if err := store.SaveToken(t.Context(), domain.AuthToken{
		ServerName:  "atlassian",
		AccessToken: "stale",
		ExpiresAt:   now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}
	handler := meta.NewHandler(registry.New(), store, &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, now)

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("unrefreshable expiry should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "reauth") {
		t.Fatalf("expected message to ask for reauth, got %q", diag.Message)
	}
}

func TestDiagnoseErrorsWhenTokenStoreFails(t *testing.T) {
	handler := meta.NewHandler(registry.New(), brokenStore{err: errors.New("permission denied")}, &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, time.Now())

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("store failure should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "permission denied") {
		t.Fatalf("expected message to carry the store error, got %q", diag.Message)
	}
}

func TestDiagnoseReportsMissingStdioCommand(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{{
		Name:      "mark42",
		Transport: domain.TransportStdio,
	}}, time.Now())

	diag, ok := findDiagnostic(diags, "mark42", "transport")
	if !ok {
		t.Fatalf("expected a transport diagnostic for mark42, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("missing command should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "command") {
		t.Fatalf("expected message to mention the command, got %q", diag.Message)
	}
}

func TestDiagnoseReportsMissingHTTPURL(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{{
		Name:      "slack",
		Transport: domain.TransportHTTP,
	}}, time.Now())

	diag, ok := findDiagnostic(diags, "slack", "transport")
	if !ok {
		t.Fatalf("expected a transport diagnostic for slack, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("missing url should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "url") {
		t.Fatalf("expected message to mention the url, got %q", diag.Message)
	}
}

func TestDiagnoseReportsValidTargetAsHealthy(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("mark42")}, time.Now())

	diag, ok := findDiagnostic(diags, "mark42", "transport")
	if !ok {
		t.Fatalf("expected a transport diagnostic for mark42, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticOK {
		t.Fatalf("valid target should be ok, got %q (%s)", diag.Severity, diag.Message)
	}
}

func TestDiagnoseSkipsDisabledModule(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})
	disabled := oauthModule("honeycomb")
	disabled.Disabled = true

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{disabled}, time.Now())

	if len(diags) != 1 {
		t.Fatalf("disabled module should produce exactly one diagnostic, got %+v", diags)
	}
	if diags[0].Severity != meta.DiagnosticOK {
		t.Fatalf("disabled module should not be flagged, got %q", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "disabled") {
		t.Fatalf("expected message to mention the disabled state, got %q", diags[0].Message)
	}
	if _, ok := findDiagnostic(diags, "honeycomb", "auth"); ok {
		t.Fatal("disabled module should not be auth-checked")
	}
}

func TestDiagnoseReportsUnnamedModule(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("  ")}, time.Now())

	if len(diags) != 1 {
		t.Fatalf("unnamed module should produce exactly one diagnostic, got %+v", diags)
	}
	if diags[0].Severity != meta.DiagnosticError {
		t.Fatalf("unnamed module should be an error, got %q", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "name") {
		t.Fatalf("expected message to mention the missing name, got %q", diags[0].Message)
	}
}

func TestDiagnoseReportsUnresponsiveMountedModule(t *testing.T) {
	handler, client := mountedDiagnoseModule(t, stdioModule("mark42"))
	client.markUnhealthy()

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("mark42")}, time.Now())

	diag, ok := findDiagnostic(diags, "mark42", "runtime")
	if !ok {
		t.Fatalf("expected a runtime diagnostic for mark42, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("unresponsive module should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "unresponsive") {
		t.Fatalf("expected message to report unresponsiveness, got %q", diag.Message)
	}
}

func TestDiagnoseReportsMountedModuleFailureReason(t *testing.T) {
	reg := registry.New()
	mod := domain.NewModule(stdioModule("mark42"))
	reg.RegisterError(mod, errors.New("exec: mark42-server: executable file not found"))

	diags := meta.NewHandler(reg, newDiagnoseStore(t), &supervisedFactory{}).
		Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("mark42")}, time.Now())

	diag, ok := findDiagnostic(diags, "mark42", "runtime")
	if !ok {
		t.Fatalf("expected a runtime diagnostic for mark42, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticError {
		t.Fatalf("errored module should be an error, got %q", diag.Severity)
	}
	if !strings.Contains(diag.Message, "executable file not found") {
		t.Fatalf("expected the last error to be surfaced, got %q", diag.Message)
	}
}

func TestDiagnoseReportsRespondingMountedModule(t *testing.T) {
	handler, _ := mountedDiagnoseModule(t, stdioModule("mark42"))

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("mark42")}, time.Now())

	diag, ok := findDiagnostic(diags, "mark42", "runtime")
	if !ok {
		t.Fatalf("expected a runtime diagnostic for mark42, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticOK {
		t.Fatalf("responding module should be ok, got %q (%s)", diag.Severity, diag.Message)
	}
	if !strings.Contains(diag.Message, "1 tool") {
		t.Fatalf("expected the tool count to be reported, got %q", diag.Message)
	}
}

func TestDiagnoseOmitsRuntimeCheckForUnmountedModule(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{stdioModule("mark42")}, time.Now())

	if _, ok := findDiagnostic(diags, "mark42", "runtime"); ok {
		t.Fatalf("unmounted module should not be runtime-checked, got %+v", diags)
	}
}

func TestDiagnoseWarnsAboutTokenExpiringSoon(t *testing.T) {
	store := newDiagnoseStore(t)
	now := time.Now()
	if err := store.SaveToken(t.Context(), domain.AuthToken{
		ServerName:  "atlassian",
		AccessToken: "aging",
		ExpiresAt:   now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}
	handler := meta.NewHandler(registry.New(), store, &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), []domain.ModuleConfig{oauthModule("atlassian")}, now)

	diag, ok := findDiagnostic(diags, "atlassian", "auth")
	if !ok {
		t.Fatalf("expected an auth diagnostic for atlassian, got %+v", diags)
	}
	if diag.Severity != meta.DiagnosticWarning {
		t.Fatalf("soon-to-expire token should be a warning, got %q (%s)", diag.Severity, diag.Message)
	}
}

func TestDiagnoseWithNoModulesReturnsNoDiagnostics(t *testing.T) {
	handler := meta.NewHandler(registry.New(), newDiagnoseStore(t), &supervisedFactory{})

	diags := handler.Diagnose(t.Context(), nil, time.Now())

	if len(diags) != 0 {
		t.Fatalf("no modules should mean no diagnostics, got %+v", diags)
	}
}

func TestSummarizeRuntimeMapsDaemonStatuses(t *testing.T) {
	summaries := []domain.ModuleSummary{
		{Name: "mark42", Status: domain.StatusActive, Tools: []string{"remember", "recall", "forget"}},
		{Name: "atlassian", Status: domain.StatusError, Error: "401 unauthorized"},
	}

	diags := meta.SummarizeRuntime(summaries)

	if len(diags) != 2 {
		t.Fatalf("expected one diagnostic per module, got %+v", diags)
	}
	healthy, ok := findDiagnostic(diags, "mark42", "runtime")
	if !ok || healthy.Severity != meta.DiagnosticOK {
		t.Fatalf("active module should be ok, got %+v", healthy)
	}
	if !strings.Contains(healthy.Message, "3 tools") {
		t.Fatalf("expected tool count in message, got %q", healthy.Message)
	}
	failed, ok := findDiagnostic(diags, "atlassian", "runtime")
	if !ok || failed.Severity != meta.DiagnosticError {
		t.Fatalf("errored module should be an error, got %+v", failed)
	}
	if !strings.Contains(failed.Message, "401 unauthorized") {
		t.Fatalf("expected daemon error in message, got %q", failed.Message)
	}
}

func TestSummarizeRuntimeWarnsAboutNonRunningModule(t *testing.T) {
	diags := meta.SummarizeRuntime([]domain.ModuleSummary{{Name: "context7", Status: domain.StatusInactive}})

	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic, got %+v", diags)
	}
	if diags[0].Severity != meta.DiagnosticWarning {
		t.Fatalf("non-running module should be a warning, got %q", diags[0].Severity)
	}
	if meta.HasErrors(diags) {
		t.Fatal("a non-running module should not fail doctor")
	}
}

func TestSummarizeRuntimeWithNoSummariesReturnsNoDiagnostics(t *testing.T) {
	if diags := meta.SummarizeRuntime(nil); len(diags) != 0 {
		t.Fatalf("no summaries should mean no diagnostics, got %+v", diags)
	}
}

func TestHasErrorsDetectsErrorSeverity(t *testing.T) {
	if meta.HasErrors(nil) {
		t.Fatal("no diagnostics should mean no errors")
	}
	ok := []meta.Diagnostic{{Severity: meta.DiagnosticOK}, {Severity: meta.DiagnosticWarning}}
	if meta.HasErrors(ok) {
		t.Fatal("ok/warning diagnostics should not report errors")
	}
	bad := append(ok, meta.Diagnostic{Severity: meta.DiagnosticError})
	if !meta.HasErrors(bad) {
		t.Fatal("an error diagnostic should be detected")
	}
}

func mountedDiagnoseModule(t *testing.T, cfg domain.ModuleConfig) (*meta.Handler, *supervisedClient) {
	t.Helper()

	reg := registry.New()
	client := &supervisedClient{tools: []domain.Tool{{Name: cfg.Name + "_query", OriginModule: cfg.Name}}}
	if err := reg.Register(domain.NewModule(cfg), client); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	return meta.NewHandler(reg, newDiagnoseStore(t), &supervisedFactory{}), client
}
