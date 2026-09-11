package auth_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
	"github.com/mfenderov/veronica/internal/domain"
)

func TestFileAuthStore(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "auth.json")

	store, err := auth.NewFileStore(filePath)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	ctx := t.Context()

	// Initial empty
	tok, err := store.GetToken(ctx, "slack")
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}
	if tok != nil {
		t.Fatalf("expected nil token, got %+v", tok)
	}

	// Save token
	testToken := domain.AuthToken{
		ServerName:   "slack",
		AccessToken:  "xoxp-12345",
		RefreshToken: "xoxr-67890",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Truncate(time.Second),
	}
	err = store.SaveToken(ctx, testToken)
	if err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	// Get token
	got, err := store.GetToken(ctx, "slack")
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}
	if got == nil || got.AccessToken != testToken.AccessToken {
		t.Fatalf("GetToken = %+v, want %+v", got, testToken)
	}

	// Verify file persistence by loading a fresh store from same file
	store2, err := auth.NewFileStore(filePath)
	if err != nil {
		t.Fatalf("NewFileStore reload failed: %v", err)
	}
	got2, err := store2.GetToken(ctx, "slack")
	if err != nil {
		t.Fatalf("GetToken from store2 failed: %v", err)
	}
	if got2 == nil || got2.AccessToken != testToken.AccessToken {
		t.Fatalf("persisted token mismatch: got %+v, want %+v", got2, testToken)
	}

	// Delete token
	err = store.DeleteToken(ctx, "slack")
	if err != nil {
		t.Fatalf("DeleteToken failed: %v", err)
	}
	gotDeleted, err := store.GetToken(ctx, "slack")
	if err != nil {
		t.Fatalf("GetToken after delete failed: %v", err)
	}
	if gotDeleted != nil {
		t.Fatalf("expected nil token after delete, got %+v", gotDeleted)
	}
}

func TestOAuthManagerRefresh(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "auth.json")
	store, err := auth.NewFileStore(filePath)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	// Mock OAuth token refresh server
	var refreshCount int
	mockAuthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "refreshed-token",
			"refresh_token": "new-refresh-token",
			"expires_in": 3600,
			"token_type": "Bearer"
		}`))
	}))
	defer mockAuthServer.Close()

	oauthMgr := auth.NewOAuthManager(store, http.DefaultClient)

	ctx := t.Context()
	expiringToken := domain.AuthToken{
		ServerName:   "slack",
		AccessToken:  "old-token",
		RefreshToken: "refresh-token-1",
		ExpiresAt:    time.Now().Add(1 * time.Minute), // expiring within 5 min
	}
	_ = store.SaveToken(ctx, expiringToken)

	cfg := domain.OAuthClientConfig{
		ServerName:   "slack",
		ClientID:     "client-123",
		ClientSecret: "secret-456",
		TokenURL:     mockAuthServer.URL,
	}

	token, err := oauthMgr.EnsureValidToken(ctx, cfg)
	if err != nil {
		t.Fatalf("EnsureValidToken failed: %v", err)
	}

	if token.AccessToken != "refreshed-token" {
		t.Fatalf("expected refreshed-token, got %s", token.AccessToken)
	}
	if refreshCount != 1 {
		t.Fatalf("expected 1 refresh call, got %d", refreshCount)
	}
}

func TestOAuthManagerExchangeCode(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "auth.json")
	store, err := auth.NewFileStore(filePath)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	mockAuthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "exchanged-code-token",
			"refresh_token": "exchanged-refresh-token",
			"expires_in": 7200,
			"token_type": "Bearer"
		}`))
	}))
	defer mockAuthServer.Close()

	oauthMgr := auth.NewOAuthManager(store, http.DefaultClient)

	cfg := domain.OAuthClientConfig{
		ServerName:  "slack",
		ClientID:    "client-123",
		RedirectURL: "http://localhost:9091/oauth/callback",
		TokenURL:    mockAuthServer.URL,
	}

	token, err := oauthMgr.ExchangeCode(t.Context(), cfg, "auth-code-xyz")
	if err != nil {
		t.Fatalf("ExchangeCode failed: %v", err)
	}

	if token.AccessToken != "exchanged-code-token" {
		t.Fatalf("expected exchanged-code-token, got %s", token.AccessToken)
	}
	if token.RefreshToken != "exchanged-refresh-token" {
		t.Fatalf("expected exchanged-refresh-token, got %s", token.RefreshToken)
	}
}

func TestFileAuthStoreMigrationFromOpencode(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	opencodeAuthPath := filepath.Join(tmpDir, "mcp-auth.json")
	veronicaAuthPath := filepath.Join(tmpDir, "veronica-auth.json")

	// Sample opencode mcp-auth.json shape (like in ~/.local/share/opencode/mcp-auth.json)
	opencodeJSON := `{
		"slack": {
			"tokens": {
				"accessToken": "xoxp-opencode-sample",
				"refreshToken": "xoxr-sample",
				"expiresAt": 1893456000
			}
		},
		"atlassian": {
			"tokens": {
				"accessToken": "atlassian-token-sample",
				"refreshToken": "atlassian-refresh-sample",
				"expiresAt": 1893456000
			}
		}
	}`
	err := os.WriteFile(opencodeAuthPath, []byte(opencodeJSON), 0o600)
	if err != nil {
		t.Fatalf("failed to write sample opencode auth: %v", err)
	}

	store, err := auth.NewFileStore(veronicaAuthPath)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	err = store.ImportFromOpenCode(opencodeAuthPath)
	if err != nil {
		t.Fatalf("ImportFromOpenCode failed: %v", err)
	}

	tok, err := store.GetToken(t.Context(), "slack")
	if err != nil {
		t.Fatalf("GetToken failed: %v", err)
	}
	if tok == nil || tok.AccessToken != "xoxp-opencode-sample" {
		t.Fatalf("failed to import opencode slack token: got %+v", tok)
	}

	atlassianTok, err := store.GetToken(t.Context(), "atlassian")
	if err != nil {
		t.Fatalf("GetToken atlassian failed: %v", err)
	}
	if atlassianTok == nil || atlassianTok.AccessToken != "atlassian-token-sample" {
		t.Fatalf("failed to import opencode atlassian token: got %+v", atlassianTok)
	}
}
