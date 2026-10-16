package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func localCommandTest(t *testing.T) string {
	t.Helper()
	old := buildinfo.Channel
	buildinfo.Channel = "local"
	t.Cleanup(func() { buildinfo.Channel = old })
	token, hadToken := os.LookupEnv("JETSCALE_LOCAL_TOKEN")
	_ = os.Unsetenv("JETSCALE_LOCAL_TOKEN")
	t.Cleanup(func() {
		if hadToken {
			_ = os.Setenv("JETSCALE_LOCAL_TOKEN", token)
		} else {
			_ = os.Unsetenv("JETSCALE_LOCAL_TOKEN")
		}
	})
	dir := t.TempDir()
	t.Setenv("JETSCALE_LOCAL_CONFIG_DIR", dir)
	t.Setenv("JETSCALE_LOCAL_API_URL", "")
	return dir
}

func TestLocalCredentialSessionLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "accounts-error", "logout-error", "bad-login", "bad-me", "auth-login", "status-error"} {
		t.Run(scenario, func(t *testing.T) {
			configDir := localCommandTest(t)
			creds := filepath.Join(t.TempDir(), ".env")
			const password = "fake-password-not-output"
			const cookie = "fake-opaque-cookie-not-output"
			if err := os.WriteFile(creds, []byte("JETSCALE_DEFAULT_ADMIN_EMAIL=test@example.invalid\nJETSCALE_DEFAULT_ADMIN_PASSWORD='"+password+"'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			loginCalls, accountCalls, logoutCalls := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v2/auth/sign-in" {
					loginCalls++
					var body struct {
						Login    string
						Password string
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Login != "test@example.invalid" || body.Password != password {
						t.Error("wrong fake credentials")
					}
					if scenario == "bad-login" {
						w.WriteHeader(401)
						_, _ = fmt.Fprint(w, password)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie, Path: "/", HttpOnly: true})
					_, _ = fmt.Fprint(w, `{}`)
					return
				}
				c, err := r.Cookie("session")
				if err != nil || c.Value != cookie {
					t.Error("missing session cookie")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("cookie session sent Authorization")
				}
				switch r.URL.Path {
				case "/api/v2/auth/me":
					if scenario == "bad-me" || scenario == "status-error" {
						w.WriteHeader(401)
						return
					}
					_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid","company":null}}}`)
				case "/api/v2/cloud/cloud-accounts":
					accountCalls++
					if scenario == "accounts-error" {
						w.WriteHeader(500)
						_, _ = fmt.Fprint(w, cookie)
						return
					}
					_, _ = fmt.Fprint(w, `{"data":[{"id":"fake-account","name":"Local account","cloud_provider_type":"aws"}]}`)
				case "/api/v2/auth/sign-out":
					logoutCalls++
					if scenario == "logout-error" {
						w.WriteHeader(500)
						_, _ = fmt.Fprint(w, cookie)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1})
					_, _ = fmt.Fprint(w, `{}`)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			command := []string{"accounts", "list"}
			if scenario == "auth-login" {
				command = []string{"auth", "login"}
			}
			if scenario == "status-error" {
				command = []string{"auth", "status"}
			}
			if scenario == "bad-me" {
				command = []string{"auth", "whoami"}
			}
			args := []string{"--api-url", server.URL, "--credentials-file", creds, "-o", "json"}
			root.SetArgs(append(args, command...))
			err := root.Execute()
			wantSuccess := scenario == "success" || scenario == "auth-login"
			if (err == nil) != wantSuccess {
				t.Fatalf("success=%t, error=%v", wantSuccess, err)
			}
			if loginCalls != 1 {
				t.Errorf("login attempts=%d", loginCalls)
			}
			wantLogout := 1
			if scenario == "bad-login" {
				wantLogout = 0
			}
			if logoutCalls != wantLogout {
				t.Errorf("logout attempts=%d, want %d", logoutCalls, wantLogout)
			}
			if scenario == "success" {
				var rows []struct {
					Name string `json:"name"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
					t.Fatalf("stdout not JSON: %v", err)
				}
				if len(rows) != 1 || rows[0].Name != "Local account" || accountCalls != 1 {
					t.Error("account listing failed")
				}
			}
			if scenario == "logout-error" && !strings.Contains(err.Error(), "cleanup") {
				t.Error("cleanup failure not surfaced")
			}
			combined := stdout.String() + stderr.String() + fmt.Sprint(err)
			if strings.Contains(combined, password) || strings.Contains(combined, cookie) {
				t.Error("secret leaked")
			}
			if strings.Contains(combined, "\x1b[") {
				t.Error("unexpected ANSI output")
			}
			entries, err := os.ReadDir(configDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("temporary session wrote config or credentials")
			}
		})
	}
}

func TestLocalCredentialTargetGuard(t *testing.T) {
	localCommandTest(t)
	for _, target := range []string{"https://unstable.jetscale.ai", "https://console.jetscale.ai", "http://jetscale.internal", "https://jetscale.internal.evil.invalid", "https://user:password@jetscale.internal", "https://jetscale.internal/path"} {
		if validateLocalCredentialTarget(target) == nil {
			t.Error("unsafe target accepted")
		}
	}
	for _, target := range []string{"https://jetscale.internal", "https://jetscale.internal/", "http://127.0.0.1:1234", "http://[::1]:1234"} {
		if err := validateLocalCredentialTarget(target); err != nil {
			t.Fatal(err)
		}
	}
	buildinfo.Channel = "live"
	if validateLocalCredentialTarget("https://jetscale.internal") == nil {
		t.Error("live build accepted local credentials file")
	}
}

func TestLocalCredentialParser(t *testing.T) {
	for _, value := range []string{"abc#def", "'abc#def' # comment", "\"abc#def\" # comment"} {
		got, err := parseCredentialValue(value)
		if err != nil || got != "abc#def" {
			t.Error("credential parsing failed")
		}
	}
	for _, value := range []string{"'unterminated", "\"secret\" trailing"} {
		if _, err := parseCredentialValue(value); err == nil {
			t.Error("malformed value accepted")
		}
	}
	path := filepath.Join(t.TempDir(), ".env")
	for _, contents := range []string{"JETSCALE_DEFAULT_ADMIN_EMAIL=test\nJETSCALE_DEFAULT_ADMIN_PASSWORD=\n", "JETSCALE_DEFAULT_ADMIN_EMAIL=test\nJETSCALE_DEFAULT_ADMIN_EMAIL=again\nJETSCALE_DEFAULT_ADMIN_PASSWORD=fake\n"} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readLocalCredentials(path); err == nil {
			t.Error("invalid credentials accepted")
		}
	}
}

func TestCredentialFileHelpDoesNotLogin(t *testing.T) {
	localCommandTest(t)
	root := newRootCmd()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"--credentials-file", "/not-read.env", "auth", "login", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "credentials-file") {
		t.Error("missing help")
	}
}
