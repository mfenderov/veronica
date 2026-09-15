package auth_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
)

func newScopeMockServer(t *testing.T, scope string) *httptest.Server {
	t.Helper()
	body := `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600,"token_type":"Bearer"`
	if scope != "" {
		body += `,"scope":"` + scope + `"`
	}
	body += `}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestOAuthManagerRefresh_AdoptsProviderScopes(t *testing.T) {
	t.Parallel()

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	srv := newScopeMockServer(t, "read write")
	defer srv.Close()

	mgr := auth.NewOAuthManager(store, http.DefaultClient)
	ctx := t.Context()
	_ = store.SaveToken(ctx, domain.AuthToken{
		ServerName:   "slack",
		AccessToken:  "old",
		RefreshToken: "rt-1",
		ExpiresAt:    time.Now().Add(time.Minute),
	})

	cfg := domain.OAuthClientConfig{
		ServerName:   "slack",
		ClientID:     "id",
		ClientSecret: "secret",
		TokenURL:     srv.URL,
	}
	tok, err := mgr.RefreshToken(ctx, cfg, "rt-1")
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if !slices.Equal(tok.Scopes, []string{"read", "write"}) {
		t.Fatalf("Scopes = %v, want [read write]", tok.Scopes)
	}
}

func TestOAuthManagerRefresh_KeepsConfiguredScopesWhenProviderOmitsScope(t *testing.T) {
	t.Parallel()

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	srv := newScopeMockServer(t, "")
	defer srv.Close()

	mgr := auth.NewOAuthManager(store, http.DefaultClient)
	ctx := t.Context()
	_ = store.SaveToken(ctx, domain.AuthToken{
		ServerName:   "slack",
		AccessToken:  "old",
		RefreshToken: "rt-1",
		ExpiresAt:    time.Now().Add(time.Minute),
	})

	cfg := domain.OAuthClientConfig{
		ServerName:   "slack",
		ClientID:     "id",
		ClientSecret: "secret",
		TokenURL:     srv.URL,
		Scopes:       []string{"chat:write"},
	}
	tok, err := mgr.RefreshToken(ctx, cfg, "rt-1")
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}
	if !slices.Equal(tok.Scopes, []string{"chat:write"}) {
		t.Fatalf("Scopes = %v, want [chat:write]", tok.Scopes)
	}
}

func TestOAuthManagerExchangeCode_AdoptsProviderScopes(t *testing.T) {
	t.Parallel()

	store, err := auth.NewFileStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	srv := newScopeMockServer(t, "channels:read users:read")
	defer srv.Close()

	mgr := auth.NewOAuthManager(store, http.DefaultClient)
	cfg := domain.OAuthClientConfig{
		ServerName:  "slack",
		ClientID:    "id",
		RedirectURL: "http://localhost:9091/oauth/callback",
		TokenURL:    srv.URL,
	}
	tok, err := mgr.ExchangeCode(t.Context(), cfg, "auth-code-xyz")
	if err != nil {
		t.Fatalf("ExchangeCode failed: %v", err)
	}
	if !slices.Equal(tok.Scopes, []string{"channels:read", "users:read"}) {
		t.Fatalf("Scopes = %v, want [channels:read users:read]", tok.Scopes)
	}
}
