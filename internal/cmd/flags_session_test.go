package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"gopkg.in/yaml.v3"
)

// Exercise the actual session lifecycle and generated-client transport. Every
// server and credential here is synthetic; the fixture rejects other endpoints.
func TestFlagsCommandSessionLifecycle(t *testing.T) {
	const companyID = "21000000-0000-0000-0000-000000000001"
	const rootID = "31000000-0000-0000-0000-000000000001"
	const login = "synthetic@example.invalid"
	const password = "synthetic-password-not-output"
	const cookie = "synthetic-cookie-not-output"
	for _, mode := range []string{"show", "plan", "apply"} {
		for _, scenario := range []string{"success", "missing", "read-failure", "write-uncertain", "cleanup-failure"} {
			if scenario == "write-uncertain" && mode != "apply" {
				continue
			}
			for _, format := range []string{"json", "yaml", "table"} {
				t.Run(mode+"/"+scenario+"/"+format, func(t *testing.T) {
					dir := flagsCommandTest(t, "unstable")
					if err := os.Unsetenv(buildinfo.EnvVar("TOKEN")); err != nil {
						t.Fatal(err)
					}
					var logins, cleanups, reads, writes atomic.Int32
					enabled, exists := false, scenario != "missing"
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						if r.URL.Path == "/api/v2/auth/sign-in" {
							logins.Add(1)
							var payload struct{ Login, Password string }
							if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Login != login || payload.Password != password {
								t.Error("wrong synthetic login payload")
							}
							http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie, Path: "/", HttpOnly: true})
							_, _ = fmt.Fprint(w, `{}`)
							return
						}
						c, err := r.Cookie("session")
						if err != nil || c.Value != cookie || r.Header.Get("Authorization") != "" {
							t.Error("flags API/cleanup did not use the ordinary cookie session")
						}
						switch r.URL.Path {
						case "/api/v2/auth/me":
							_, _ = fmt.Fprintf(w, `{"data":{"user":{"id":"71000000-0000-0000-0000-000000000001","email":%q,"is_superuser":true}}}`, login)
						case "/api/v2/auth/sign-out":
							cleanups.Add(1)
							if scenario == "cleanup-failure" {
								w.WriteHeader(http.StatusInternalServerError)
								_, _ = fmt.Fprint(w, password)
								return
							}
							_, _ = fmt.Fprint(w, `{}`)
						case "/api/v2/organization/companies":
							reads.Add(1)
							if r.Method != http.MethodGet || r.URL.Query().Get("slug") != "eq test-company" {
								t.Error("company provisioning or unscoped selector")
							}
							_, _ = fmt.Fprintf(w, `{"data":[{"id":%q,"name":"Test Company","slug":"test-company"}],"meta":{"count":1,"total_count":1,"offset":0,"limit":50}}`, companyID)
						case "/api/v2/system/feature-flags/root/by-key/AGENTS.EXPLORER":
							reads.Add(1)
							if r.Method != http.MethodGet {
								t.Error("observation endpoint attempted mutation")
							}
							if scenario == "read-failure" {
								w.WriteHeader(http.StatusInternalServerError)
								_, _ = fmt.Fprint(w, password+cookie)
								return
							}
							if !exists {
								w.WriteHeader(http.StatusNotFound)
								return
							}
							_, _ = fmt.Fprintf(w, `{"data":{"id":%q,"key":"AGENTS.EXPLORER","value":%t,"is_secret":false,"deprecated_at":null,"description":"Synthetic metadata"}}`, rootID, enabled)
						case "/api/v2/system/feature-flags/root/action/set/AGENTS.EXPLORER", "/api/v2/system/feature-flags/root":
							writes.Add(1)
							if mode != "apply" {
								t.Error("show/plan attempted a flag write")
							}
							var payload map[string]any
							if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["value"] != true {
								t.Error("invalid enable-only flag write")
							}
							if exists && (r.Method != http.MethodPatch || len(payload) != 1) {
								t.Error("existing flag should use only the generated set-value action")
							}
							if !exists && (r.Method != http.MethodPost || payload["key"] != "AGENTS.EXPLORER" || payload["is_secret"] != false) {
								t.Error("missing flag row should use supported generated create")
							}
							enabled, exists = true, true
							if scenario == "write-uncertain" {
								w.WriteHeader(http.StatusInternalServerError)
								_, _ = fmt.Fprint(w, cookie)
								return
							}
							_, _ = fmt.Fprint(w, `{"data":{}}`)
						default:
							t.Errorf("unexpected flags command endpoint: %s %s", r.Method, r.URL.Path)
							w.WriteHeader(http.StatusInternalServerError)
						}
					}))
					defer server.Close()
					root := newRootCmd()
					var stdout, stderr bytes.Buffer
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					root.SetIn(strings.NewReader(password + "\n"))
					root.SetArgs([]string{"flags", mode, "--profile", flagsProfileFile(t, flagsProfileFixture), "--api-url", server.URL, "--login", login, "--password-stdin", "--output", format})
					err := root.Execute()
					wantFailure := scenario == "read-failure" || scenario == "write-uncertain" || scenario == "cleanup-failure"
					if (err != nil) != wantFailure {
						t.Fatalf("wanted failure=%t got %v; stdout=%s", wantFailure, err, stdout.String())
					}
					if logins.Load() != 1 || cleanups.Load() != 1 || reads.Load() == 0 {
						t.Fatalf("session/read lifecycle: sign-in=%d sign-out=%d reads=%d", logins.Load(), cleanups.Load(), reads.Load())
					}
					wantWrites := int32(0)
					if mode == "apply" && scenario != "read-failure" {
						wantWrites = 1
					}
					if writes.Load() != wantWrites {
						t.Fatalf("explicit writes/no retry: got %d want %d", writes.Load(), wantWrites)
					}
					combined := stdout.String() + stderr.String() + fmt.Sprint(err)
					for _, forbidden := range []string{password, cookie, "\x1b", "\r"} {
						if strings.Contains(combined, forbidden) {
							t.Fatalf("unsafe output contains %q", forbidden)
						}
					}
					if !strings.Contains(stderr.String(), "Signing in for this command only") || strings.Contains(stdout.String(), "Signing in") || strings.Contains(stdout.String(), "signed out") {
						t.Fatal("auth diagnostics contaminated stdout or missing stderr")
					}
					if format != "table" {
						var result api.FlagsResult
						if format == "json" {
							if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
								t.Fatalf("JSON stdout invalid: %v", err)
							}
						} else {
							var decoded map[string]any
							if err := yaml.Unmarshal(stdout.Bytes(), &decoded); err != nil {
								t.Fatalf("YAML stdout invalid: %v", err)
							}
							body, _ := json.Marshal(decoded)
							if err := json.Unmarshal(body, &result); err != nil {
								t.Fatal(err)
							}
						}
						if result.Mode != mode || result.OutcomeUncertain != (scenario == "write-uncertain") {
							t.Fatalf("wrong mode/uncertainty result: %+v", result)
						}
						if scenario == "success" || scenario == "missing" || scenario == "cleanup-failure" {
							if result.Satisfied != (mode == "apply") || len(result.Actions) != 1 || result.RootApplicationWide != true {
								t.Fatalf("wrong observable flag result: %+v", result)
							}
							status := "planned"
							if mode == "apply" {
								status = "applied"
							}
							if result.Actions[0].Status != status {
								t.Fatalf("status=%s want=%s", result.Actions[0].Status, status)
							}
						}
					}
					leaf, _, findErr := root.Find([]string{"flags", mode})
					if findErr != nil {
						t.Fatal(findErr)
					}
					deadline, ok := leaf.Context().Deadline()
					if !ok || time.Until(deadline) > 3*time.Minute || !errors.Is(leaf.Context().Err(), context.Canceled) {
						t.Fatal("command deadline not bounded/released after lifecycle")
					}
					entries, fileErr := os.ReadDir(dir)
					if fileErr != nil || len(entries) != 0 {
						t.Fatalf("temporary session persisted files: %v %v", entries, fileErr)
					}
				})
			}
		}
	}
}

func TestFlagsCanceledBeforeAuthentication(t *testing.T) {
	flagsCommandTest(t, "unstable")
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"flags", "apply", "--profile", flagsProfileFile(t, flagsProfileFixture)})
	if err := root.ExecuteContext(parent); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command reached session acquisition: %v", err)
	}
}
