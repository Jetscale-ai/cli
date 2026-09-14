package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func TestTokenEnvironmentByChannel(t *testing.T) {
	for _, channel := range []struct {
		name string
		key  string
	}{
		{"live", "JETSCALE_TOKEN"},
		{"local", "JETSCALE_LOCAL_TOKEN"},
		{"unstable", "JETSCALE_UNSTABLE_TOKEN"},
	} {
		for _, path := range []struct {
			name    string
			resolve func(string) (string, error)
		}{
			{"ResolveToken", ResolveToken},
			{"EnsureFreshToken", func(instance string) (string, error) {
				return EnsureFreshToken(instance, ":invalid-url")
			}},
		} {
			for _, scenario := range []struct {
				name    string
				env     string
				unset   bool
				stored  bool
				expired bool
				want    string
			}{
				{name: "environment only", env: "selected-token", want: "selected-token"},
				{name: "environment wins", env: "selected-token", stored: true, want: "selected-token"},
				{name: "environment skips refresh", env: "selected-token", stored: true, expired: true, want: "selected-token"},
				{name: "empty falls back", stored: true, want: "stored-token"},
				{name: "unset falls back", unset: true, stored: true, want: "stored-token"},
				{name: "empty without stored token"},
				{name: "unset without stored token", unset: true},
			} {
				t.Run(channel.name+"/"+path.name+"/"+scenario.name, func(t *testing.T) {
					previous := buildinfo.Channel
					buildinfo.Channel = channel.name
					t.Cleanup(func() { buildinfo.Channel = previous })
					// Isolate all stores, including the live store that non-live must ignore.
					for _, key := range []string{"JETSCALE_CONFIG_DIR", "JETSCALE_LOCAL_CONFIG_DIR", "JETSCALE_UNSTABLE_CONFIG_DIR"} {
						t.Setenv(key, privateTempDir(t))
					}
					for _, key := range []string{"JETSCALE_TOKEN", "JETSCALE_LOCAL_TOKEN", "JETSCALE_UNSTABLE_TOKEN"} {
						t.Setenv(key, "other-channel-token")
					}
					t.Setenv(channel.key, scenario.env)
					if scenario.unset {
						if err := os.Unsetenv(channel.key); err != nil {
							t.Fatal(err)
						}
					}
					entry := TokenEntry{AccessToken: "stored-token", ExpiresAt: time.Now().Add(time.Hour)}
					if scenario.expired {
						entry.ExpiresAt = time.Now().Add(-time.Hour)
						entry.RefreshToken = "stored-refresh"
					}
					// Use a non-default instance: token namespace follows the build, not the target.
					if scenario.stored {
						if err := SetToken("custom-instance", entry); err != nil {
							t.Fatal(err)
						}
					}
					got, err := path.resolve("custom-instance")
					if err != nil {
						t.Fatal(err)
					}
					if got != scenario.want {
						t.Fatal("token did not match the expected channel/precedence")
					}
					saved, ok, err := GetToken("custom-instance")
					if err != nil || ok != scenario.stored {
						t.Fatalf("unexpected token store state: present=%v, error=%v", ok, err)
					}
					if ok && (saved.AccessToken != entry.AccessToken || saved.RefreshToken != entry.RefreshToken || !saved.ExpiresAt.Equal(entry.ExpiresAt)) {
						t.Fatal("resolving an environment or fresh stored token modified the store")
					}
				})
			}
		}
	}
}

func TestEnsureFreshTokenChannelRefresh(t *testing.T) {
	for _, channel := range []string{"live", "local", "unstable"} {
		t.Run(channel, func(t *testing.T) {
			previous := buildinfo.Channel
			buildinfo.Channel = channel
			t.Cleanup(func() { buildinfo.Channel = previous })
			t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), privateTempDir(t))
			for _, key := range []string{"JETSCALE_TOKEN", "JETSCALE_LOCAL_TOKEN", "JETSCALE_UNSTABLE_TOKEN"} {
				t.Setenv(key, "other-channel-token")
			}
			t.Setenv(buildinfo.EnvVar("TOKEN"), "")

			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/api/v2/auth/token/refresh-token" {
					t.Error("unexpected refresh request")
					http.NotFound(w, r)
					return
				}
				var body struct {
					RefreshToken string `json:"refresh_token"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken != "stored-refresh" {
					t.Error("refresh did not use the stored refresh token")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(tokenResponse("refreshed-access", "refreshed-refresh"))
			}))
			defer srv.Close()
			if err := SetToken("custom-instance", TokenEntry{
				AccessToken: "expired-access", RefreshToken: "stored-refresh", ExpiresAt: time.Now().Add(-time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			got, err := EnsureFreshToken("custom-instance", srv.URL)
			if err != nil {
				t.Fatal(err)
			}
			if got != "refreshed-access" || calls.Load() != 1 {
				t.Fatal("expected exactly one refresh, ignoring other channels' environment tokens")
			}
			saved, ok, err := GetToken("custom-instance")
			if err != nil || !ok || saved.AccessToken != got || saved.RefreshToken != "refreshed-refresh" || saved.Expired() {
				t.Fatal("refreshed token pair was not persisted")
			}
		})
	}
}
