package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mfenderov/veronica/internal/domain"
)

type FileAuthStore struct {
	mu       sync.RWMutex
	filePath string
	tokens   map[string]domain.AuthToken
}

var ErrTokenNotFound = errors.New("token not found")

func NewFileStore(filePath string) (*FileAuthStore, error) {
	store := &FileAuthStore{
		filePath: filePath,
		tokens:   make(map[string]domain.AuthToken),
	}

	if err := store.load(); err != nil {
		return nil, err
	}

	return store, nil
}

func (s *FileAuthStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read auth file: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	var loaded map[string]domain.AuthToken
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("failed to unmarshal auth data: %w", err)
	}

	s.tokens = loaded
	return nil
}

func (s *FileAuthStore) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create auth directory: %w", err)
	}

	data, err := json.MarshalIndent(s.tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auth data: %w", err)
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write tmp auth file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return fmt.Errorf("failed to commit auth file: %w", err)
	}

	return nil
}

func (s *FileAuthStore) GetToken(ctx context.Context, serverName string) (*domain.AuthToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tok, ok := s.tokens[serverName]
	if !ok {
		return nil, nil
	}
	return &tok, nil
}

func (s *FileAuthStore) SaveToken(ctx context.Context, token domain.AuthToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[token.ServerName] = token
	return s.saveLocked()
}

func (s *FileAuthStore) DeleteToken(ctx context.Context, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.tokens, serverName)
	return s.saveLocked()
}

func (s *FileAuthStore) ListTokens(ctx context.Context) ([]domain.AuthToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]domain.AuthToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		list = append(list, t)
	}
	return list, nil
}

type openCodeServerEntry struct {
	Tokens struct {
		AccessToken  string  `json:"accessToken"`
		RefreshToken string  `json:"refreshToken"`
		ExpiresAt    float64 `json:"expiresAt"`
	} `json:"tokens"`
}

func (s *FileAuthStore) ImportFromOpenCode(openCodePath string) error {
	data, err := os.ReadFile(openCodePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read opencode auth: %w", err)
	}

	var parsed map[string]openCodeServerEntry
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("failed to parse opencode auth: %w", err)
	}

	for serverName, entry := range parsed {
		if entry.Tokens.AccessToken == "" {
			continue
		}
		tok := domain.AuthToken{
			ServerName:   serverName,
			AccessToken:  entry.Tokens.AccessToken,
			RefreshToken: entry.Tokens.RefreshToken,
			TokenType:    "Bearer",
		}
		if entry.Tokens.ExpiresAt > 0 {
			tok.ExpiresAt = time.Unix(int64(entry.Tokens.ExpiresAt), 0)
		}
		if err := s.SaveToken(context.Background(), tok); err != nil {
			return err
		}
	}

	return nil
}
