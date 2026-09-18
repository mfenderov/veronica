package auth

import (
	"errors"
	"os"
	"strings"
)

const GatewayTokenEnv = "VERONICA_GATEWAY_TOKEN"

var ErrGatewayTokenMissing = errors.New("gateway token is missing")

// LoadGatewayToken loads the shared gateway token from the environment or a file.
func LoadGatewayToken(path string) (string, error) {
	if token := strings.TrimSpace(os.Getenv(GatewayTokenEnv)); token != "" {
		return token, nil
	}
	if strings.TrimSpace(path) == "" {
		return "", ErrGatewayTokenMissing
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("failed to read gateway token file")
	}

	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", ErrGatewayTokenMissing
	}
	return token, nil
}
