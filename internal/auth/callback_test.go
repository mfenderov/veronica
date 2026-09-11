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
		_ = cbServer.Stop(context.Background())
	}()

	addr := cbServer.Addr()
	if addr == "" {
		t.Fatal("expected non-empty addr")
	}

	// Trigger callback HTTP GET
	go func() {
		time.Sleep(50 * time.Millisecond)
		resp, err := http.Get("http://" + addr + "/oauth/callback?code=test-code-123&state=state-abc")
		if err == nil {
			_ = resp.Body.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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
