// Package domain defines the core business models, interfaces, and service contracts for Veronica.
package domain

import (
	"errors"
	"time"
)

// ErrTokenNotFound is returned by AuthStore implementations when no token is stored for a server.
var ErrTokenNotFound = errors.New("token not found")

// AuthToken represents an OAuth authentication token for an MCP server.
type AuthToken struct {
	ServerName   string    `json:"server_name"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// IsExpired reports whether the authentication token has expired relative to the given time.
func (t AuthToken) IsExpired(now time.Time) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return now.After(t.ExpiresAt)
}

// WillExpireSoon reports whether the token will expire within the specified duration window from now.
func (t AuthToken) WillExpireSoon(now time.Time, window time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(window).After(t.ExpiresAt)
}

// OAuthClientConfig holds client-side configuration parameters for OAuth 2.0 authentication flows.
type OAuthClientConfig struct {
	ServerName   string            `json:"server_name" yaml:"server_name"`
	ClientID     string            `json:"client_id" yaml:"client_id"`
	ClientSecret string            `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	AuthURL      string            `json:"auth_url" yaml:"auth_url"`
	TokenURL     string            `json:"token_url" yaml:"token_url"`
	RedirectURL  string            `json:"redirect_url" yaml:"redirect_url"`
	Scopes       []string          `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	AuthParams   map[string]string `json:"auth_params,omitempty" yaml:"auth_params,omitempty"`
}
