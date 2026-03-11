package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// --- Token Storage Tests ---

func TestTokenRoundTrip(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	entry := TokenEntry{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		ExpiresAt:    time.Now().Add(30 * time.Minute).Truncate(time.Second),
	}

	if err := SetToken("local", entry); err != nil {
		t.Fatal(err)
	}

	got, ok, err := GetToken("local")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected token to be found")
	}
	if got.AccessToken != entry.AccessToken {
		t.Errorf("access_token = %q, want %q", got.AccessToken, entry.AccessToken)
	}
	if got.RefreshToken != entry.RefreshToken {
		t.Errorf("refresh_token = %q, want %q", got.RefreshToken, entry.RefreshToken)
	}
}

func TestTokenNotFound(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	_, ok, err := GetToken("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected ok=false for missing token")
	}
}

func TestDeleteToken(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	if err := SetToken("staging", TokenEntry{AccessToken: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteToken("staging"); err != nil {
		t.Fatal(err)
	}

	_, ok, err := GetToken("staging")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected token to be deleted")
	}
}

func TestTokenExpired(t *testing.T) {
	expired := TokenEntry{ExpiresAt: time.Now().Add(-1 * time.Minute)}
	if !expired.Expired() {
		t.Error("expected token to be expired")
	}

	valid := TokenEntry{ExpiresAt: time.Now().Add(5 * time.Minute)}
	if valid.Expired() {
		t.Error("expected token to not be expired")
	}
}

func TestTokenFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("JETSCALE_CONFIG_DIR", tmp)

	if err := SetToken("test", TokenEntry{AccessToken: "secret"}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(tmp + "/tokens.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestResolveTokenEnvVarWins(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	t.Setenv("JETSCALE_TOKEN", "env-token-wins")

	if err := SetToken("production", TokenEntry{AccessToken: "stored-token"}); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveToken("production")
	if err != nil {
		t.Fatal(err)
	}
	if got != "env-token-wins" {
		t.Errorf("token = %q, want %q", got, "env-token-wins")
	}
}

func TestResolveTokenFallsBackToStored(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	t.Setenv("JETSCALE_TOKEN", "")

	if err := SetToken("production", TokenEntry{AccessToken: "stored-token"}); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveToken("production")
	if err != nil {
		t.Fatal(err)
	}
	if got != "stored-token" {
		t.Errorf("token = %q, want %q", got, "stored-token")
	}
}

func TestMultipleInstances(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())

	if err := SetToken("local", TokenEntry{AccessToken: "local-tok"}); err != nil {
		t.Fatal(err)
	}
	if err := SetToken("production", TokenEntry{AccessToken: "prod-tok"}); err != nil {
		t.Fatal(err)
	}

	local, ok, _ := GetToken("local")
	if !ok || local.AccessToken != "local-tok" {
		t.Error("local token mismatch")
	}
	prod, ok, _ := GetToken("production")
	if !ok || prod.AccessToken != "prod-tok" {
		t.Error("production token mismatch")
	}
}

// --- Auth Client Tests (against httptest server) ---

func tokenResponse(access, refresh string) map[string]any {
	return map[string]any{
		"data": map[string]any{
			"tokens": map[string]any{
				"access_token":  access,
				"refresh_token": refresh,
				"token_type":    "bearer",
				"expires_in":    1800,
			},
		},
	}
}

func TestSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/sign-in" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Login    string `json:"login"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Login != "test@example.com" || req.Password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse("at-123", "rt-456"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)

	tokens, err := client.SignIn("test@example.com", "secret")
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if tokens.AccessToken != "at-123" {
		t.Errorf("access_token = %q", tokens.AccessToken)
	}
	if tokens.RefreshToken != "rt-456" {
		t.Errorf("refresh_token = %q", tokens.RefreshToken)
	}

	_, err = client.SignIn("wrong@example.com", "nope")
	if err == nil {
		t.Error("expected error for bad credentials")
	}
}

func TestWhoami(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer valid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"user": map[string]any{
					"email":    "dev@jetscale.ai",
					"username": "admin",
				},
			},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)

	who, err := client.Whoami("valid-token")
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if who.Email != "dev@jetscale.ai" {
		t.Errorf("email = %q", who.Email)
	}
	if who.Username != "admin" {
		t.Errorf("username = %q", who.Username)
	}

	_, err = client.Whoami("bad-token")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestRefresh(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/token/refresh-token" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.RefreshToken != "valid-refresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokenResponse("new-at", "new-rt"))
	}))
	defer srv.Close()

	client := NewClient(srv.URL)

	tokens, err := client.Refresh("valid-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.AccessToken != "new-at" {
		t.Errorf("access_token = %q", tokens.AccessToken)
	}

	_, err = client.Refresh("expired-refresh")
	if err == nil {
		t.Error("expected error for expired refresh token")
	}
}

func TestSignOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/auth/sign-out" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	if err := client.SignOut("any-token"); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
}

func TestEnsureFreshTokenRefreshesExpired(t *testing.T) {
	t.Setenv("JETSCALE_CONFIG_DIR", t.TempDir())
	t.Setenv("JETSCALE_TOKEN", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/token/refresh-token" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tokenResponse("refreshed-at", "refreshed-rt"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	expired := TokenEntry{
		AccessToken:  "old-at",
		RefreshToken: "old-rt",
		ExpiresAt:    time.Now().Add(-5 * time.Minute),
	}
	if err := SetToken("local", expired); err != nil {
		t.Fatal(err)
	}

	token, err := EnsureFreshToken("local", srv.URL)
	if err != nil {
		t.Fatalf("EnsureFreshToken: %v", err)
	}
	if token != "refreshed-at" {
		t.Errorf("token = %q, want %q", token, "refreshed-at")
	}

	entry, ok, _ := GetToken("local")
	if !ok {
		t.Fatal("expected refreshed token to be stored")
	}
	if entry.AccessToken != "refreshed-at" {
		t.Errorf("stored token = %q", entry.AccessToken)
	}
}

func TestTokenEntryFromPayload(t *testing.T) {
	p := TokenPayload{
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresIn:    1800,
	}
	entry := TokenEntryFromPayload(p)
	if entry.AccessToken != "at" || entry.RefreshToken != "rt" {
		t.Error("token fields mismatch")
	}
	diff := time.Until(entry.ExpiresAt)
	if diff < 29*time.Minute || diff > 31*time.Minute {
		t.Errorf("expires_at diff = %v, want ~30m", diff)
	}
}
