package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

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

	tokenResp, err := m.requestToken(ctx, cfg.TokenURL, data)
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
func (m *OAuthManager) ExchangeCode(ctx context.Context, cfg domain.OAuthClientConfig, code string) (*domain.AuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", cfg.RedirectURL)
	addClientCredentials(data, cfg)

	tokenResp, err := m.requestToken(ctx, cfg.TokenURL, data)
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

func (m *OAuthManager) requestToken(ctx context.Context, tokenURL string, data url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

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
