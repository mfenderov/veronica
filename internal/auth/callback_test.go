package auth_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/mfenderov/veronica/internal/auth"
)

func TestOAuthCallbackServer(t *testing.T) {
	t.Parallel()

	cbServer := auth.NewCallbackServer("127.0.0.1:0") // dynamic port for test
	err := cbServer.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = cbServer.Stop(t.Context())
	}()

	addr := cbServer.Addr()
	if addr == "" {
		t.Fatal("expected non-empty addr")
	}

	// Trigger callback HTTP GET using test context
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/oauth/callback?code=test-code-123&state=state-abc", http.NoBody)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	result, err := cbServer.WaitForCode(ctx)
	if err != nil {
		t.Fatalf("WaitForCode failed: %v", err)
	}

	if result.Code != "test-code-123" {
		t.Fatalf("expected code test-code-123, got %s", result.Code)
	}
	if result.State != "state-abc" {
		t.Fatalf("expected state state-abc, got %s", result.State)
	}
}

func TestOAuthCallbackServer_AlternativeCallbackPath(t *testing.T) {
	t.Parallel()

	cbServer := auth.NewCallbackServer("127.0.0.1:0")
	if err := cbServer.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = cbServer.Stop(t.Context())
	}()

	addr := cbServer.Addr()
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/callback?code=code-from-callback-path&state=state-xyz", http.NoBody)
		if err != nil {
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancel()

	result, err := cbServer.WaitForCode(ctx)
	if err != nil {
		t.Fatalf("WaitForCode failed: %v", err)
	}
	if result.Code != "code-from-callback-path" {
		t.Fatalf("expected code-from-callback-path, got %s", result.Code)
	}
}
