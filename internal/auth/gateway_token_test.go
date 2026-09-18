package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mfenderov/veronica/internal/auth"
)

func TestLoadGatewayToken(t *testing.T) {
	const (
		envToken  = "environment-gateway-token"
		fileToken = "file-gateway-token"
	)

	t.Run("environment takes precedence over file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "gateway-token")
		if err := os.WriteFile(path, []byte(fileToken), 0o600); err != nil {
			t.Fatalf("write token file: %v", err)
		}
		t.Setenv(auth.GatewayTokenEnv, "  "+envToken+"\n")

		got, err := auth.LoadGatewayToken(path)
		if err != nil {
			t.Fatalf("LoadGatewayToken returned error: %v", err)
		}
		if got != envToken {
			t.Fatalf("LoadGatewayToken = %q, want %q", got, envToken)
		}
	})

	t.Run("falls back to trimmed file content", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "gateway-token")
		if err := os.WriteFile(path, []byte(" \n"+fileToken+"\t\n"), 0o600); err != nil {
			t.Fatalf("write token file: %v", err)
		}
		t.Setenv(auth.GatewayTokenEnv, " \t")

		got, err := auth.LoadGatewayToken(path)
		if err != nil {
			t.Fatalf("LoadGatewayToken returned error: %v", err)
		}
		if got != fileToken {
			t.Fatalf("LoadGatewayToken = %q, want %q", got, fileToken)
		}
	})

	t.Run("reports missing file without token contents", func(t *testing.T) {
		const secret = "missing-file-secret-token"
		t.Setenv(auth.GatewayTokenEnv, "")

		_, err := auth.LoadGatewayToken(filepath.Join(t.TempDir(), "gateway-token-"+secret))
		if err == nil {
			t.Fatal("LoadGatewayToken returned nil error for missing file")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error exposed token contents: %v", err)
		}
	})

	t.Run("rejects empty path with the missing sentinel", func(t *testing.T) {
		t.Setenv(auth.GatewayTokenEnv, " \t")

		_, err := auth.LoadGatewayToken("")
		if !errors.Is(err, auth.ErrGatewayTokenMissing) {
			t.Fatalf("LoadGatewayToken error = %v, want exact sentinel %v", err, auth.ErrGatewayTokenMissing)
		}
	})

	t.Run("rejects empty file with the missing sentinel", func(t *testing.T) {
		const secret = "empty-file-secret-token"
		path := filepath.Join(t.TempDir(), "gateway-token-"+secret)
		if err := os.WriteFile(path, []byte(" \t\n"), 0o600); err != nil {
			t.Fatalf("write token file: %v", err)
		}
		t.Setenv(auth.GatewayTokenEnv, "")

		_, err := auth.LoadGatewayToken(path)
		if !errors.Is(err, auth.ErrGatewayTokenMissing) {
			t.Fatalf("LoadGatewayToken error = %v, want exact sentinel %v", err, auth.ErrGatewayTokenMissing)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error exposed token contents: %v", err)
		}
	})
}
