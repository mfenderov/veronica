package meta

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mfenderov/veronica/internal/domain"
)

// shimCommands are resolved launchers, never watched: their own bytes do not
// change when the underlying package upgrades.
var shimCommands = map[string]bool{
	"npx": true, "bunx": true, "npm": true, "uvx": true,
	"pnpm": true, "yarn": true, "deno": true,
}

// resolveModuleBinary returns the absolute local binary for a stdio module.
// ok is false for non-stdio transports, shims, and unresolvable commands.
func resolveModuleBinary(cfg domain.ModuleConfig) (string, bool) {
	if cfg.Transport != domain.TransportStdio {
		return "", false
	}
	cmd := strings.TrimSpace(cfg.Command)
	if cmd == "" {
		return "", false
	}
	if shimCommands[filepath.Base(cmd)] {
		return "", false
	}
	abs := cmd
	if !filepath.IsAbs(abs) {
		found, err := exec.LookPath(abs)
		if err != nil {
			return "", false
		}
		abs = found
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", false
	}
	return abs, true
}

// hashFile returns the sha256 hex digest of a file's contents.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
