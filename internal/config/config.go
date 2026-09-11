// Package config handles loading, persisting, and modifying the Veronica gateway configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mfenderov/veronica/internal/domain"
	"gopkg.in/yaml.v3"
)

// ServerConfig holds network listening and callback addresses for the gateway.
type ServerConfig struct {
	Addr              string `yaml:"addr"`
	OAuthCallbackAddr string `yaml:"oauth_callback_addr"`
}

// Config represents the complete persistent configuration for the Veronica gateway.
type Config struct {
	mu      sync.RWMutex
	Server  ServerConfig                   `yaml:"server"`
	Modules map[string]domain.ModuleConfig `yaml:"modules"`
}

// DefaultConfig returns a new Config with standard default values.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:              ":9090",
			OAuthCallbackAddr: "127.0.0.1:9091",
		},
		Modules: make(map[string]domain.ModuleConfig),
	}
}

// Load reads and parses a YAML configuration file from the given path, returning defaults if not found.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.Modules == nil {
		cfg.Modules = make(map[string]domain.ModuleConfig)
	}

	return cfg, nil
}

// Save writes the current configuration atomically to disk at the specified path.
func (c *Config) Save(path string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	tmpFile := path + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write tmp config: %w", err)
	}

	if err := os.Rename(tmpFile, path); err != nil {
		return fmt.Errorf("failed to commit config: %w", err)
	}

	return nil
}

// AddModule registers or updates a module configuration.
func (c *Config) AddModule(mod domain.ModuleConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Modules[mod.Name] = mod
}

// RemoveModule deletes a module configuration by name.
func (c *Config) RemoveModule(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Modules, name)
}

// GetModule retrieves a module configuration by name, returning false if not found.
func (c *Config) GetModule(name string) (domain.ModuleConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	mod, ok := c.Modules[name]
	return mod, ok
}
