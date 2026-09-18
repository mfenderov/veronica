package meta

import (
	"errors"
	"fmt"
	"os/exec"
)

// CommandPolicy controls which executable paths may be used for stdio modules.
type CommandPolicy struct {
	allowed map[string]struct{}
}

// NewCommandPolicy resolves every configured command and builds an exact-path allowlist.
func NewCommandPolicy(commands []string) (CommandPolicy, error) {
	if len(commands) == 0 {
		return CommandPolicy{}, nil
	}

	allowed := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		resolved, err := exec.LookPath(command)
		if err != nil {
			return CommandPolicy{}, fmt.Errorf("invalid allowed command: %w", err)
		}
		allowed[resolved] = struct{}{}
	}

	return CommandPolicy{allowed: allowed}, nil
}

// Validate checks a command against the exact resolved-path allowlist.
func (p CommandPolicy) Validate(command string) error {
	if p.allowed == nil {
		return nil
	}

	resolved, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("command is not available: %w", err)
	}
	if _, ok := p.allowed[resolved]; !ok {
		return errors.New("command is not allowed")
	}
	return nil
}
