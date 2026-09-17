package domain_test

import (
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

func TestModuleConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     domain.ModuleConfig
		wantErr bool
	}{
		{
			name: "valid stdio module",
			cfg: domain.ModuleConfig{
				Name:      "mark42",
				Transport: domain.TransportStdio,
				Command:   "/opt/homebrew/bin/mark42-server",
			},
			wantErr: false,
		},
		{
			name: "valid sse module",
			cfg: domain.ModuleConfig{
				Name:      "atlassian",
				Transport: domain.TransportSSE,
				URL:       "https://mcp.atlassian.com/v1/mcp",
			},
			wantErr: false,
		},
		{
			name: "empty name fails",
			cfg: domain.ModuleConfig{
				Name:      "",
				Transport: domain.TransportStdio,
				Command:   "some-command",
			},
			wantErr: true,
		},
		{
			name: "invalid transport fails",
			cfg: domain.ModuleConfig{
				Name:      "test",
				Transport: "ftp",
				Command:   "some-command",
			},
			wantErr: true,
		},
		{
			name: "stdio without command fails",
			cfg: domain.ModuleConfig{
				Name:      "test",
				Transport: domain.TransportStdio,
				Command:   "",
			},
			wantErr: true,
		},
		{
			name: "sse without url fails",
			cfg: domain.ModuleConfig{
				Name:      "test",
				Transport: domain.TransportSSE,
				URL:       "",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestModuleLifecycle(t *testing.T) {
	t.Parallel()

	cfg := domain.ModuleConfig{
		Name:      "mark42",
		Transport: domain.TransportStdio,
		Command:   "/opt/homebrew/bin/mark42-server",
	}

	mod := domain.NewModule(cfg)
	if mod.Status != domain.StatusInactive {
		t.Fatalf("NewModule status = %v, want %v", mod.Status, domain.StatusInactive)
	}

	mod.MarkStarting()
	if mod.Status != domain.StatusStarting {
		t.Fatalf("MarkStarting status = %v, want %v", mod.Status, domain.StatusStarting)
	}

	mod.MarkActive([]domain.Tool{
		{Name: "search_nodes", OriginModule: "mark42"},
	})
	if mod.Status != domain.StatusActive {
		t.Fatalf("MarkActive status = %v, want %v", mod.Status, domain.StatusActive)
	}
	if len(mod.Tools) != 1 {
		t.Fatalf("MarkActive tools count = %d, want 1", len(mod.Tools))
	}

	mod.MarkError("failed to connect")
	if mod.Status != domain.StatusError {
		t.Fatalf("MarkError status = %v, want %v", mod.Status, domain.StatusError)
	}
	if mod.ErrorMessage != "failed to connect" {
		t.Fatalf("MarkError message = %q, want 'failed to connect'", mod.ErrorMessage)
	}
}

func TestAuthTokenExpiration(t *testing.T) {
	t.Parallel()

	now := time.Now()

	expiredToken := domain.AuthToken{
		ServerName:  "slack",
		AccessToken: "old-token",
		ExpiresAt:   now.Add(-10 * time.Minute),
	}
	if !expiredToken.IsExpired(now) {
		t.Fatal("expected token to be expired")
	}

	soonExpiringToken := domain.AuthToken{
		ServerName:  "slack",
		AccessToken: "expiring-token",
		ExpiresAt:   now.Add(2 * time.Minute),
	}
	if !soonExpiringToken.WillExpireSoon(now, 5*time.Minute) {
		t.Fatal("expected token to be expiring soon within 5 minutes")
	}

	validToken := domain.AuthToken{
		ServerName:  "slack",
		AccessToken: "valid-token",
		ExpiresAt:   now.Add(1 * time.Hour),
	}
	if validToken.IsExpired(now) {
		t.Fatal("expected token to be valid")
	}
	if validToken.WillExpireSoon(now, 5*time.Minute) {
		t.Fatal("expected token to not be expiring soon")
	}
}
