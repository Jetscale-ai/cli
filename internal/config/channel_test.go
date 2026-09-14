package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func useChannel(t *testing.T, channel string) {
	t.Helper()
	previous := buildinfo.Channel
	buildinfo.Channel = channel
	t.Cleanup(func() { buildinfo.Channel = previous })
	for _, suffix := range []string{"API_URL", "INSTANCE", "CONFIG_DIR", "ACCOUNT"} {
		t.Setenv(buildinfo.EnvVar(suffix), "")
	}
}

func TestNonLiveDefaultsIgnoreGenericEnvironment(t *testing.T) {
	for _, channel := range []string{"local", "unstable"} {
		t.Run(channel, func(t *testing.T) {
			useChannel(t, channel)
			t.Setenv("JETSCALE_API_URL", ProductionURL)
			t.Setenv("JETSCALE_INSTANCE", "production")
			t.Setenv("JETSCALE_ACCOUNT", "live-account")
			t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
			xdg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdg)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			name, url, err := Resolve(cfg, "", false, "")
			if err != nil || name != channel || url != buildinfo.DefaultURL() {
				t.Fatalf("resolved %q %q %v", name, url, err)
			}
			if ResolveAccount(cfg, name, "") != "" {
				t.Fatal("generic account leaked")
			}
			dir, err := Dir()
			if err != nil || dir != filepath.Join(xdg, "jetscale", channel) {
				t.Fatalf("Dir = %q, %v", dir, err)
			}
			cfg.DefaultInstance = ""
			name, url, err = Resolve(cfg, "", false, "")
			if err != nil || name != channel || url != buildinfo.DefaultURL() {
				t.Fatal("empty default did not use channel")
			}
			delete(cfg.Instances, channel)
			if _, _, err := Resolve(cfg, "", false, ""); err == nil {
				t.Fatal("missing channel must fail, not use production")
			}
		})
	}
}

func TestNonLiveOverridesAndCustomInstances(t *testing.T) {
	for _, channel := range []string{"local", "unstable"} {
		t.Run(channel, func(t *testing.T) {
			useChannel(t, channel)
			t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), t.TempDir())
			cfg := DefaultConfig()
			cfg.DefaultInstance = "custom"
			cfg.Instances["custom"] = Instance{APIURL: "https://custom.example.com", ActiveAccount: "stored"}
			if err := Save(cfg); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			name, url, err := Resolve(cfg, "", false, "")
			if err != nil || name != "custom" || url != "https://custom.example.com" {
				t.Fatalf("custom lost: %q %q %v", name, url, err)
			}
			t.Setenv(buildinfo.EnvVar("INSTANCE"), "local")
			name, url, err = Resolve(cfg, "", false, "")
			if err != nil || name != "local" || url != buildinfo.LocalURL {
				t.Fatal("channel instance override not used")
			}
			name, _, err = Resolve(cfg, "custom", false, "")
			if err != nil || name != "custom" {
				t.Fatal("instance flag must beat instance env")
			}
			t.Setenv(buildinfo.EnvVar("API_URL"), "https://override.example.com")
			name, url, err = Resolve(cfg, "custom", false, "")
			if err != nil || name != buildinfo.EnvVar("API_URL") || url != "https://override.example.com" {
				t.Fatal("channel API_URL not used")
			}
			_, url, err = Resolve(cfg, "", false, "https://flag.example.com")
			if err != nil || url != "https://flag.example.com" {
				t.Fatal("flag must win")
			}
			t.Setenv(buildinfo.EnvVar("ACCOUNT"), "channel-account")
			if ResolveAccount(cfg, "custom", "") != "channel-account" || ResolveAccount(cfg, "custom", "flag-account") != "flag-account" {
				t.Fatal("account override precedence")
			}
		})
	}
}

func TestChannelDirectoriesAndLiveCompatibility(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	for _, channel := range []string{"live", "local", "unstable"} {
		t.Run(channel, func(t *testing.T) {
			useChannel(t, channel)
			want := filepath.Join(xdg, "jetscale", channel)
			if channel == "live" {
				want = filepath.Join(home, DefaultConfigDir)
			}
			dir, err := Dir()
			if err != nil || dir != want {
				t.Fatalf("Dir = %q, %v; want %q", dir, err, want)
			}
			cfg := DefaultConfig()
			if err := Save(cfg); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(dir, ConfigFileName))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatal("config must be private")
			}
			t.Setenv("XDG_CONFIG_HOME", "")
			want = filepath.Join(home, DefaultConfigDir)
			if channel != "live" {
				want = filepath.Join(want, channel)
			}
			dir, err = Dir()
			if err != nil || dir != want {
				t.Fatalf("home fallback = %q, %v", dir, err)
			}
		})
	}
	useChannel(t, "live")
	cfg, err := Load()
	if err != nil || cfg.DefaultInstance != "production" {
		t.Fatal("live config was changed")
	}
}

func TestEmptyLoadedDefaultUsesChannel(t *testing.T) {
	useChannel(t, "local")
	t.Setenv("JETSCALE_LOCAL_CONFIG_DIR", t.TempDir())
	if err := Save(Config{}); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil || cfg.DefaultInstance != "local" || cfg.Instances["local"].APIURL != buildinfo.LocalURL {
		t.Fatalf("Load = %#v, %v", cfg, err)
	}
}

func TestInvalidChannelFailsBeforeOverrides(t *testing.T) {
	useChannel(t, "invalid")
	if _, err := Dir(); err == nil {
		t.Fatal("invalid channel directory accepted")
	}
	if _, _, err := Resolve(DefaultConfig(), "production", false, ProductionURL); err == nil {
		t.Fatal("invalid channel allowed endpoint resolution")
	}
}
