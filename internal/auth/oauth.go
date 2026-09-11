package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

var openBrowserFn = defaultOpenBrowser

func browserCommand(goos, targetURL string) (string, []string) {
	if goos == "darwin" {
		return "open", []string{targetURL}
	}
	if goos == "windows" {
		return "rundll32", []string{"url.dll,FileProtocolHandler", targetURL}
	}
	return "xdg-open", []string{targetURL}
}

func defaultOpenBrowser(targetURL string) error {
	bin, args := browserCommand(runtime.GOOS, targetURL)
	return exec.Command(bin, args...).Start()
}

// SetOpenBrowserFnForTesting overrides the browser launch function for tests and returns a restore cleanup func.
func SetOpenBrowserFnForTesting(fn func(string) error) func() {
	prev := openBrowserFn
	openBrowserFn = fn
	return func() {
		openBrowserFn = prev
	}
}

// OAuthManager implements domain.TokenProvider to manage token storage, renewal, and exchange.
type OAuthManager struct {
	store      domain.AuthStore
	httpClient *http.Client
}

// NewOAuthManager constructs an OAuthManager using the specified store and HTTP client.
func NewOAuthManager(store domain.AuthStore, client *http.Client) *OAuthManager {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &OAuthManager{
		store:      store,
		httpClient: client,
	}
}

// GetToken retrieves an authentication token for the given server name from the store.
func (m *OAuthManager) GetToken(ctx context.Context, serverName string) (*domain.AuthToken, error) {
	return m.store.GetToken(ctx, serverName)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// EnsureValidToken returns a valid authentication token, refreshing it automatically if close to expiry.
func (m *OAuthManager) EnsureValidToken(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	tok, err := m.store.GetToken(ctx, cfg.ServerName)
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, ErrTokenNotFound
	}

	if !tok.WillExpireSoon(time.Now(), 5*time.Minute) {
		return tok, nil
	}

	if tok.RefreshToken == "" {
		return tok, nil
	}

	refreshed, err := m.RefreshToken(ctx, cfg, tok.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token for %s: %w", cfg.ServerName, err)
	}

	return refreshed, nil
}

// RefreshToken requests a new access token from the token endpoint using a refresh token.
func (m *OAuthManager) RefreshToken(ctx context.Context, cfg domain.OAuthClientConfig, refreshToken string) (*domain.AuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)
	addClientCredentials(data, cfg)

	tokenResp, err := m.requestToken(ctx, cfg.TokenURL, data, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token for %s: %w", cfg.ServerName, err)
	}

	newToken := buildAuthToken(cfg.ServerName, tokenResp, refreshToken)
	if err := m.store.SaveToken(ctx, newToken); err != nil {
		return nil, fmt.Errorf("failed to persist refreshed token: %w", err)
	}

	return &newToken, nil
}

// ExchangeCode exchanges an OAuth authorization code for access and refresh tokens.
func (m *OAuthManager) ExchangeCode(ctx context.Context, cfg domain.OAuthClientConfig, code string, verifier ...string) (*domain.AuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", cfg.RedirectURL)
	if len(verifier) > 0 && verifier[0] != "" {
		data.Set("code_verifier", verifier[0])
	}
	addClientCredentials(data, cfg)

	tokenResp, err := m.requestToken(ctx, cfg.TokenURL, data, cfg)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed for %s: %w", cfg.ServerName, err)
	}

	newToken := buildAuthToken(cfg.ServerName, tokenResp, tokenResp.RefreshToken)
	if err := m.store.SaveToken(ctx, newToken); err != nil {
		return nil, fmt.Errorf("failed to persist token: %w", err)
	}

	return &newToken, nil
}

func addClientCredentials(data url.Values, cfg domain.OAuthClientConfig) {
	if cfg.ClientID != "" {
		data.Set("client_id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}
}

func (m *OAuthManager) requestToken(ctx context.Context, tokenURL string, data url.Values, cfg domain.OAuthClientConfig) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cfg.ClientID != "" && cfg.ClientSecret != "" {
		req.SetBasicAuth(cfg.ClientID, cfg.ClientSecret)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}

	return &tokenResp, nil
}

func buildAuthToken(serverName string, resp *tokenResponse, fallbackRefresh string) domain.AuthToken {
	token := domain.AuthToken{
		ServerName:   serverName,
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
	}
	if token.RefreshToken == "" {
		token.RefreshToken = fallbackRefresh
	}
	if resp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(resp.ExpiresIn) * time.Second)
	}
	return token
}

// StartInteractiveFlow starts a local callback listener, launches the browser for user consent, and exchanges the code.
func (m *OAuthManager) StartInteractiveFlow(ctx context.Context, cfg domain.OAuthClientConfig) (*domain.AuthToken, error) {
	if cfg.AuthURL == "" {
		return nil, errors.New("auth_url is required for interactive OAuth flow")
	}

	code, verifier, err := m.runInteractiveConsent(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return m.ExchangeCode(ctx, cfg, code, verifier)
}

func (m *OAuthManager) runInteractiveConsent(ctx context.Context, cfg domain.OAuthClientConfig) (string, string, error) {
	cbServer, actualRedirect, err := m.startCallbackServer(cfg.RedirectURL)
	if err != nil {
		return "", "", err
	}
	defer func() {
		_ = cbServer.Stop(context.Background())
	}()

	cfg.RedirectURL = actualRedirect
	state := generateRandomState()
	verifier := generateCodeVerifier()
	challenge := generateCodeChallenge(verifier)

	if err := m.launchConsentBrowser(cfg, state, challenge); err != nil {
		return "", "", err
	}

	code, err := m.waitForValidatedCode(ctx, cbServer, state)
	if err != nil {
		return "", "", err
	}
	return code, verifier, nil
}

func (m *OAuthManager) startCallbackServer(redirectURL string) (*CallbackServer, string, error) {
	bindAddr := resolveCallbackBindAddr(redirectURL)
	cbServer := NewCallbackServer(bindAddr)
	if err := cbServer.Start(); err != nil {
		return nil, "", fmt.Errorf("failed to start callback server on %s: %w", bindAddr, err)
	}

	actualAddr := cbServer.Addr()
	if strings.Contains(redirectURL, ":0/") && actualAddr != "" {
		redirectURL = "http://" + actualAddr + "/oauth/callback"
	}
	return cbServer, redirectURL, nil
}

func (m *OAuthManager) launchConsentBrowser(cfg domain.OAuthClientConfig, state, codeChallenge string) error {
	authURL, err := buildAuthorizeURL(cfg, state, codeChallenge)
	if err != nil {
		return fmt.Errorf("failed to build auth url: %w", err)
	}
	if err := openBrowserFn(authURL); err != nil {
		return fmt.Errorf("failed to launch browser: %w", err)
	}
	return nil
}

func (m *OAuthManager) waitForValidatedCode(ctx context.Context, cb *CallbackServer, expectedState string) (string, error) {
	res, err := cb.WaitForCode(ctx)
	if err != nil {
		return "", fmt.Errorf("waiting for authorization code: %w", err)
	}
	if res.Error != "" {
		return "", fmt.Errorf("oauth provider error: %s", res.Error)
	}
	if res.State != "" && res.State != expectedState {
		return "", errors.New("mismatched OAuth state parameter")
	}
	return res.Code, nil
}

func resolveCallbackBindAddr(redirectURL string) string {
	if redirectURL != "" {
		if u, err := url.Parse(redirectURL); err == nil && u.Port() != "" {
			return ":" + u.Port()
		}
	}
	return ":9091"
}

func generateRandomState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func generateCodeVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func buildAuthorizeURL(cfg domain.OAuthClientConfig, state, codeChallenge string) (string, error) {
	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	if cfg.RedirectURL != "" {
		q.Set("redirect_uri", cfg.RedirectURL)
	}
	if state != "" {
		q.Set("state", state)
	}
	if codeChallenge != "" {
		q.Set("code_challenge", codeChallenge)
		q.Set("code_challenge_method", "S256")
	}
	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	for k, v := range cfg.AuthParams {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
