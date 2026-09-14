package auth

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/Jetscale-ai/cli/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	tokensFileName   = "tokens.yaml"
	tokensLockName   = ".tokens.lock"
	storeLockTimeout = 5 * time.Second
)

// TokenEntry holds credentials for a single instance.
type TokenEntry struct {
	AccessToken  string         `yaml:"access_token"`
	RefreshToken string         `yaml:"refresh_token"`
	ExpiresAt    time.Time      `yaml:"expires_at"`
	Origin       string         `yaml:"origin,omitempty"`
	Channel      string         `yaml:"channel,omitempty"`
	Cookies      []StoredCookie `yaml:"cookies,omitempty"`
}

// Expired returns true if the access token has expired (with 30s buffer).
func (t TokenEntry) Expired() bool {
	return time.Now().After(t.ExpiresAt.Add(-30 * time.Second))
}

// TokenStore is the on-disk token file structure.
type TokenStore struct {
	Instances map[string]TokenEntry `yaml:"instances"`
	Sessions  map[string]TokenEntry `yaml:"sessions,omitempty"`
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
		return TokenStore{}, errors.New("locate token store failed")
	}
	return loadTokens(p)
}

func loadTokens(p string) (TokenStore, error) {
	empty := TokenStore{Instances: make(map[string]TokenEntry), Sessions: make(map[string]TokenEntry)}
	if err := privateStoreDir(filepath.Dir(p)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, nil
		}
		return TokenStore{}, err
	}
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return TokenStore{}, errors.New("inspect token store failed")
	}
	if !info.Mode().IsRegular() {
		return TokenStore{}, errors.New("token store must be a private regular file (0600), not a symlink")
	}
	f, err := openStoreFile(p)
	if err != nil {
		return TokenStore{}, errors.New("open token store failed")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || privateStoreFile(f) != nil {
		return TokenStore{}, errors.New("token store changed or has unsafe permissions")
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return TokenStore{}, errors.New("read token store failed")
	}
	var store TokenStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return TokenStore{}, errors.New("parse token store: invalid format")
	}
	if store.Instances == nil {
		store.Instances = make(map[string]TokenEntry)
	}
	if store.Sessions == nil {
		store.Sessions = make(map[string]TokenEntry)
	}
	return store, nil
}

// SaveTokens replaces the whole store. Entry updates must use updateTokens so
// that their read and write share one interprocess lock.
func SaveTokens(store TokenStore) error {
	p, err := tokensPath()
	if err != nil {
		return errors.New("locate token store failed")
	}
	lock, err := lockTokenStore(p, storeLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	return saveTokens(p, store)
}

func updateTokens(update func(*TokenStore)) error {
	p, err := tokensPath()
	if err != nil {
		return errors.New("locate token store failed")
	}
	lock, err := lockTokenStore(p, storeLockTimeout)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	store, err := loadTokens(p)
	if err != nil {
		return err
	}
	update(&store)
	return saveTokens(p, store)
}

// The sidecar must never be removed or replaced: waiters must lock the same
// inode/file across token-file renames. Closing the handle (including process
// exit) releases the OS lock; there is no stale-lock deletion protocol.
func lockTokenStore(p string, timeout time.Duration) (*os.File, error) {
	dir := filepath.Dir(p)
	if err := createStoreDir(dir); err != nil {
		return nil, errors.New("create private config directory failed")
	}
	if err := privateStoreDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, tokensLockName)
	f, err := openStoreLockFile(path)
	if err != nil {
		return nil, errors.New("open token store lock failed")
	}
	fail := func(err error) (*os.File, error) {
		_ = f.Close()
		return nil, err
	}
	if err := privateStoreFile(f); err != nil {
		return fail(errors.New("token store lock has unsafe permissions or type"))
	}
	deadline := time.Now().Add(timeout)
	for {
		locked, err := tryStoreLock(f)
		if err != nil {
			return fail(errors.New("lock token store failed"))
		}
		if locked {
			info, err := os.Lstat(path)
			opened, statErr := f.Stat()
			if err != nil || statErr != nil || !info.Mode().IsRegular() || !os.SameFile(info, opened) || privateStoreFile(f) != nil || privateStoreDir(dir) != nil {
				return fail(errors.New("token store lock changed or has unsafe permissions"))
			}
			return f, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fail(errors.New("token store is busy: lock wait timed out; retry the command"))
		}
		time.Sleep(min(10*time.Millisecond, remaining))
	}
}

func saveTokens(p string, store TokenStore) error {
	if err := privateStoreDir(filepath.Dir(p)); err != nil {
		return err
	}
	if info, err := os.Lstat(p); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("refusing to replace a non-regular token store or symlink")
		}
		if err := validateStoreReplacement(p); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect token store failed")
	}
	data, err := yaml.Marshal(store)
	if err != nil {
		return errors.New("encode token store failed")
	}
	// Replace atomically: WriteFile alone preserves an existing permissive mode.
	f, err := createPrivateStoreTemp(filepath.Dir(p))
	if err != nil {
		return errors.New("create protected token store failed")
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return errors.New("write token store failed")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return errors.New("sync token store failed")
	}
	if err := f.Close(); err != nil {
		return errors.New("close token store failed")
	}
	if err := os.Rename(f.Name(), p); err != nil {
		return errors.New("replace token store failed")
	}
	return nil
}

// GetToken returns the legacy token for a named instance, or empty if not found.
// Origin-bound Session records deliberately use a separate namespace.
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
	return updateTokens(func(store *TokenStore) {
		store.Instances[instanceName] = entry
	})
}

// DeleteToken removes the token for a named instance.
func DeleteToken(instanceName string) error {
	return updateTokens(func(store *TokenStore) {
		delete(store.Instances, instanceName)
	})
}

// ResolveToken returns the bearer token to use for API calls.
// Priority: compiled channel's TOKEN env var > stored token for the instance.
func ResolveToken(instanceName string) (string, error) {
	if v := os.Getenv(buildinfo.EnvVar("TOKEN")); v != "" {
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
