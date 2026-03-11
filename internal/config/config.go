package config

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigDir = ".config/jetscale"
	ConfigFileName   = "config.yaml"

	ProductionURL = "https://console.jetscale.ai"
)

// Instance is a named API target.
type Instance struct {
	APIURL        string `yaml:"api_url"`
	ActiveAccount string `yaml:"active_account,omitempty"`
}

// Config is the on-disk config file structure.
type Config struct {
	DefaultInstance string              `yaml:"default_instance"`
	Instances       map[string]Instance `yaml:"instances"`
}

// BuiltinInstances are always available, even without a config file.
var BuiltinInstances = map[string]Instance{
	"production": {APIURL: ProductionURL},
	"staging":    {APIURL: "https://jetscale.staging.jetscale.ai"},
	"local":      {APIURL: "auto"},
}

// localPorts are probed in order when an instance has api_url: auto.
var localPorts = []string{
	"http://localhost:8000",
	"http://localhost:8010",
}

func DefaultConfig() Config {
	instances := make(map[string]Instance, len(BuiltinInstances))
	for k, v := range BuiltinInstances {
		instances[k] = v
	}
	return Config{
		DefaultInstance: "production",
		Instances:       instances,
	}
}

// Resolve determines the API URL to use. Resolution order:
//
//  1. apiURLFlag   (--api-url flag, exact URL)
//  2. JETSCALE_API_URL env var
//  3. instanceFlag (-i/--instance flag or --local, named lookup)
//  4. JETSCALE_INSTANCE env var
//  5. cfg.DefaultInstance
//  6. "production" hardcoded fallback
func Resolve(cfg Config, instanceFlag string, localFlag bool, apiURLFlag string) (resolvedName string, resolvedURL string, err error) {
	// 1. --api-url flag (exact URL, highest priority)
	if apiURLFlag != "" {
		return "--api-url", apiURLFlag, nil
	}

	// 2. JETSCALE_API_URL env var
	if v := os.Getenv("JETSCALE_API_URL"); v != "" {
		return "JETSCALE_API_URL", v, nil
	}

	// 3. --local flag (sugar for -i local)
	if localFlag {
		instanceFlag = "local"
	}

	// 4. -i / --instance flag
	// 5. JETSCALE_INSTANCE env var
	// 6. config default / hardcoded fallback
	name := instanceFlag
	if name == "" {
		name = os.Getenv("JETSCALE_INSTANCE")
	}
	if name == "" {
		name = cfg.DefaultInstance
	}
	if name == "" {
		name = "production"
	}

	inst, ok := cfg.Instances[name]
	if !ok {
		return "", "", fmt.Errorf("instance %q not found (available: %v)", name, instanceNames(cfg))
	}

	url := inst.APIURL
	if url == "auto" {
		url, err = probeLocal()
		if err != nil {
			return name, "", err
		}
	}

	return name, url, nil
}

func probeLocal() (string, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}

	for _, base := range localPorts {
		resp, err := client.Get(base + "/api/v2/system/live")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return base, nil
			}
		}
	}

	return "", fmt.Errorf(
		"no local backend found (tried %v); "+
			"start one with: cd ../stack && tilt up (full stack on :8000) "+
			"or cd ../backend && just tilt-setup (backend-only on :8010), "+
			"or drop the --local / -i local flag to hit production",
		localPorts,
	)
}

// --- File I/O ---

func Dir() (string, error) {
	if v := os.Getenv("JETSCALE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, DefaultConfigDir), nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConfigFileName), nil
}

func Load() (Config, error) {
	p, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig(), nil
		}
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", p, err)
	}
	// Merge builtins so they're always available even if config omits them
	if cfg.Instances == nil {
		cfg.Instances = make(map[string]Instance)
	}
	for k, v := range BuiltinInstances {
		if _, exists := cfg.Instances[k]; !exists {
			cfg.Instances[k] = v
		}
	}
	return cfg, nil
}

func Save(cfg Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(p, data, 0o600)
}

func instanceNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Instances))
	for k := range cfg.Instances {
		names = append(names, k)
	}
	return names
}

// ResolveAccount determines the cloud account name to use.
//
//  1. accountFlag (--account flag)
//  2. JETSCALE_ACCOUNT env var
//  3. cfg.Instances[instanceName].ActiveAccount (sticky)
//  4. empty string (caller decides: auto-select or error)
func ResolveAccount(cfg Config, instanceName string, accountFlag string) string {
	if accountFlag != "" {
		return accountFlag
	}
	if v := os.Getenv("JETSCALE_ACCOUNT"); v != "" {
		return v
	}
	if inst, ok := cfg.Instances[instanceName]; ok {
		return inst.ActiveAccount
	}
	return ""
}

// SetActiveAccount persists the active account for an instance.
func SetActiveAccount(instanceName, accountName string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	inst, ok := cfg.Instances[instanceName]
	if !ok {
		return fmt.Errorf("instance %q not found", instanceName)
	}
	inst.ActiveAccount = accountName
	cfg.Instances[instanceName] = inst
	return Save(cfg)
}
