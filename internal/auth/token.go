package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Jetscale-ai/cli/internal/config"
	"gopkg.in/yaml.v3"
)

const tokensFileName = "tokens.yaml"

// TokenEntry holds credentials for a single instance.
type TokenEntry struct {
	AccessToken  string    `yaml:"access_token"`
	RefreshToken string    `yaml:"refresh_token"`
	ExpiresAt    time.Time `yaml:"expires_at"`
}

// Expired returns true if the access token has expired (with 30s buffer).
func (t TokenEntry) Expired() bool {
	return time.Now().After(t.ExpiresAt.Add(-30 * time.Second))
}

// TokenStore is the on-disk token file structure.
type TokenStore struct {
	Instances map[string]TokenEntry `yaml:"instances"`
}

func tokensPath() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, tokensFileName), nil
}

func LoadTokens() (TokenStore, error) {
	p, err := tokensPath()
	if err != nil {
		return TokenStore{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TokenStore{Instances: make(map[string]TokenEntry)}, nil
		}
		return TokenStore{}, fmt.Errorf("read tokens: %w", err)
	}
	var store TokenStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return TokenStore{}, fmt.Errorf("parse tokens %s: %w", p, err)
	}
	if store.Instances == nil {
		store.Instances = make(map[string]TokenEntry)
	}
	return store, nil
}

func SaveTokens(store TokenStore) error {
	p, err := tokensPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(store)
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}
	return os.WriteFile(p, data, 0o600)
}

// GetToken returns the token for a named instance, or empty if not found.
func GetToken(instanceName string) (TokenEntry, bool, error) {
	store, err := LoadTokens()
	if err != nil {
		return TokenEntry{}, false, err
	}
	entry, ok := store.Instances[instanceName]
	return entry, ok, nil
}

// SetToken persists a token for a named instance.
func SetToken(instanceName string, entry TokenEntry) error {
	store, err := LoadTokens()
	if err != nil {
		return err
	}
	store.Instances[instanceName] = entry
	return SaveTokens(store)
}

// DeleteToken removes the token for a named instance.
func DeleteToken(instanceName string) error {
	store, err := LoadTokens()
	if err != nil {
		return err
	}
	delete(store.Instances, instanceName)
	return SaveTokens(store)
}

// ResolveToken returns the bearer token to use for API calls.
// Priority: JETSCALE_TOKEN env var > stored token for the instance.
func ResolveToken(instanceName string) (string, error) {
	if v := os.Getenv("JETSCALE_TOKEN"); v != "" {
		return v, nil
	}
	entry, ok, err := GetToken(instanceName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return entry.AccessToken, nil
}
