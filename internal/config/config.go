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

type ServerConfig struct {
	Addr              string `yaml:"addr"`
	OAuthCallbackAddr string `yaml:"oauth_callback_addr"`
}

type Config struct {
	mu      sync.RWMutex
	Server  ServerConfig                   `yaml:"server"`
	Modules map[string]domain.ModuleConfig `yaml:"modules"`
}

func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Addr:              ":9090",
			OAuthCallbackAddr: "127.0.0.1:9091",
		},
		Modules: make(map[string]domain.ModuleConfig),
	}
}

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

func (c *Config) AddModule(mod domain.ModuleConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Modules[mod.Name] = mod
}

func (c *Config) RemoveModule(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Modules, name)
}

func (c *Config) GetModule(name string) (domain.ModuleConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	mod, ok := c.Modules[name]
	return mod, ok
}
