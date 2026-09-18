package meta_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mfenderov/veronica/internal/meta"
)

func TestCommandPolicy(t *testing.T) {
	command := os.Args[0]
	resolved, err := exec.LookPath(command)
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}

	t.Run("empty policy allows command", func(t *testing.T) {
		var policy meta.CommandPolicy
		if err := policy.Validate(command); err != nil {
			t.Fatalf("empty policy rejected command: %v", err)
		}
	})

	t.Run("configured policy allows exact resolved path", func(t *testing.T) {
		policy, err := meta.NewCommandPolicy([]string{resolved})
		if err != nil {
			t.Fatalf("NewCommandPolicy failed: %v", err)
		}
		if err := policy.Validate(command); err != nil {
			t.Fatalf("configured policy rejected command: %v", err)
		}
	})

	t.Run("unlisted executable is rejected", func(t *testing.T) {
		unlisted, err := exec.LookPath("sh")
		if err != nil {
			t.Fatalf("resolve unlisted executable: %v", err)
		}
		policy, err := meta.NewCommandPolicy([]string{resolved})
		if err != nil {
			t.Fatalf("NewCommandPolicy failed: %v", err)
		}
		if err := policy.Validate(unlisted); err == nil {
			t.Fatal("expected unlisted executable to be rejected")
		}
	})

	t.Run("missing allowlist entry is a configuration error", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing-command")
		if _, err := meta.NewCommandPolicy([]string{missing}); err == nil {
			t.Fatal("expected missing allowlist entry to fail configuration")
		}
	})
}
