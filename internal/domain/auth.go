package domain

import (
	"time"
)

type AuthToken struct {
	ServerName   string    `json:"server_name"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

func (t AuthToken) IsExpired(now time.Time) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return now.After(t.ExpiresAt)
}

func (t AuthToken) WillExpireSoon(now time.Time, window time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return now.Add(window).After(t.ExpiresAt)
}

type OAuthClientConfig struct {
	ServerName   string   `json:"server_name" yaml:"server_name"`
	ClientID     string   `json:"client_id" yaml:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty" yaml:"client_secret,omitempty"`
	AuthURL      string   `json:"auth_url" yaml:"auth_url"`
	TokenURL     string   `json:"token_url" yaml:"token_url"`
	RedirectURL  string   `json:"redirect_url" yaml:"redirect_url"`
	Scopes       []string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}
