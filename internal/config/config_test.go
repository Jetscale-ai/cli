package config

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigShape(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DefaultInstance != "production" {
		t.Errorf("default_instance = %q, want %q", cfg.DefaultInstance, "production")
	}
	for _, name := range []string{"production", "staging", "local"} {
		if _, ok := cfg.Instances[name]; !ok {
			t.Errorf("missing builtin instance %q", name)
		}
	}
	if cfg.Instances["production"].APIURL != ProductionURL {
		t.Errorf("production URL = %q", cfg.Instances["production"].APIURL)
	}
	if cfg.Instances["local"].APIURL != "auto" {
		t.Errorf("local URL = %q, want %q", cfg.Instances["local"].APIURL, "auto")
	}
}

func TestResolveDefaultIsProduction(t *testing.T) {
	cfg := DefaultConfig()
	name, url, err := Resolve(cfg, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "production" {
		t.Errorf("name = %q, want %q", name, "production")
	}
	if url != ProductionURL {
		t.Errorf("url = %q, want %q", url, ProductionURL)
	}
}

func TestResolveAPIURLFlagWins(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("JETSCALE_API_URL", "http://env-should-lose")
	name, url, err := Resolve(cfg, "staging", false, "http://flag-wins:1234")
	if err != nil {
		t.Fatal(err)
	}
	if name != "--api-url" {
		t.Errorf("name = %q, want %q", name, "--api-url")
	}
	if url != "http://flag-wins:1234" {
		t.Errorf("url = %q", url)
	}
}

func TestResolveEnvVarBeatsInstance(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("JETSCALE_API_URL", "http://env-url:9999")
	name, url, err := Resolve(cfg, "staging", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "JETSCALE_API_URL" {
		t.Errorf("name = %q", name)
	}
	if url != "http://env-url:9999" {
		t.Errorf("url = %q", url)
	}
}

func TestResolveInstanceFlag(t *testing.T) {
	cfg := DefaultConfig()
	name, url, err := Resolve(cfg, "staging", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "staging" {
		t.Errorf("name = %q", name)
	}
	if url != "https://jetscale.staging.jetscale.ai" {
		t.Errorf("url = %q", url)
	}
}

func TestResolveLocalFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origPorts := localPorts
	localPorts = []string{srv.URL}
	defer func() { localPorts = origPorts }()

	cfg := DefaultConfig()
	name, url, err := Resolve(cfg, "", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "local" {
		t.Errorf("name = %q, want %q", name, "local")
	}
	if url != srv.URL {
		t.Errorf("url = %q, want %q", url, srv.URL)
	}
}

func TestResolveLocalFlagOverridesInstanceEnv(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origPorts := localPorts
	localPorts = []string{srv.URL}
	defer func() { localPorts = origPorts }()

	t.Setenv("JETSCALE_INSTANCE", "staging")
	cfg := DefaultConfig()
	name, _, err := Resolve(cfg, "", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "local" {
		t.Errorf("--local should win over JETSCALE_INSTANCE, got name=%q", name)
	}
}

func TestResolveInstanceEnvVar(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("JETSCALE_INSTANCE", "staging")
	name, url, err := Resolve(cfg, "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "staging" {
		t.Errorf("name = %q", name)
	}
	if url != "https://jetscale.staging.jetscale.ai" {
		t.Errorf("url = %q", url)
	}
}

func TestResolveUnknownInstance(t *testing.T) {
	cfg := DefaultConfig()
	_, _, err := Resolve(cfg, "nonexistent", false, "")
	if err == nil {
		t.Fatal("expected error for unknown instance")
	}
}

func TestResolveLocalFailsGracefully(t *testing.T) {
	origPorts := localPorts
	localPorts = []string{"http://127.0.0.1:19999"}
	defer func() { localPorts = origPorts }()

	cfg := DefaultConfig()
	_, _, err := Resolve(cfg, "", true, "")
	if err == nil {
		t.Fatal("expected error when no local backend running")
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JETSCALE_CONFIG_DIR", tmp)

	cfg := DefaultConfig()
	cfg.DefaultInstance = "staging"
	cfg.Instances["custom"] = Instance{APIURL: "https://custom.example.com"}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmp, ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.DefaultInstance != "staging" {
		t.Errorf("default_instance = %q, want %q", loaded.DefaultInstance, "staging")
	}
	if loaded.Instances["custom"].APIURL != "https://custom.example.com" {
		t.Errorf("custom instance not persisted")
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultInstance != "production" {
		t.Errorf("default_instance = %q, want %q", cfg.DefaultInstance, "production")
	}
}

func TestLoadMergesBuiltins(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JETSCALE_CONFIG_DIR", tmp)

	// Save a config with only a custom instance
	cfg := Config{
		DefaultInstance: "custom",
		Instances: map[string]Instance{
			"custom": {APIURL: "https://custom.example.com"},
		},
	}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Builtins should be merged in
	if _, ok := loaded.Instances["production"]; !ok {
		t.Error("builtin 'production' not merged into loaded config")
	}
	if _, ok := loaded.Instances["staging"]; !ok {
		t.Error("builtin 'staging' not merged into loaded config")
	}
	// Custom should still be there
	if loaded.Instances["custom"].APIURL != "https://custom.example.com" {
		t.Error("custom instance lost after load")
	}
}

func TestResolveAccountFlag(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Instances["local"] = Instance{APIURL: "auto", ActiveAccount: "stored-acct"}

	got := ResolveAccount(cfg, "local", "flag-acct")
	if got != "flag-acct" {
		t.Errorf("expected flag to win, got %q", got)
	}
}

func TestResolveAccountEnvVar(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Instances["local"] = Instance{APIURL: "auto", ActiveAccount: "stored-acct"}

	t.Setenv("JETSCALE_ACCOUNT", "env-acct")
	got := ResolveAccount(cfg, "local", "")
	if got != "env-acct" {
		t.Errorf("expected env to win, got %q", got)
	}
}

func TestResolveAccountStored(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Instances["local"] = Instance{APIURL: "auto", ActiveAccount: "stored-acct"}

	t.Setenv("JETSCALE_ACCOUNT", "")
	got := ResolveAccount(cfg, "local", "")
	if got != "stored-acct" {
		t.Errorf("expected stored, got %q", got)
	}
}

func TestResolveAccountEmpty(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("JETSCALE_ACCOUNT", "")
	got := ResolveAccount(cfg, "production", "")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestSetActiveAccount(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	// Save initial config
	cfg := DefaultConfig()
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	if err := SetActiveAccount("local", "my-acct"); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Instances["local"].ActiveAccount != "my-acct" {
		t.Errorf("active_account = %q, want %q", loaded.Instances["local"].ActiveAccount, "my-acct")
	}
}
