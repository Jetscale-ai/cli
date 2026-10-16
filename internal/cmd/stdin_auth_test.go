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

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func stdinCommandTest(t *testing.T) string {
	t.Helper()
	dir := localCommandTest(t)
	buildinfo.Channel = "unstable"
	t.Setenv("JETSCALE_UNSTABLE_CONFIG_DIR", dir)
	t.Setenv("JETSCALE_UNSTABLE_API_URL", "")
	t.Setenv("JETSCALE_UNSTABLE_INSTANCE", "")
	t.Setenv("JETSCALE_UNSTABLE_ACCOUNT", "")
	value, set := os.LookupEnv("JETSCALE_UNSTABLE_TOKEN")
	_ = os.Unsetenv("JETSCALE_UNSTABLE_TOKEN")
	t.Cleanup(func() {
		if set {
			_ = os.Setenv("JETSCALE_UNSTABLE_TOKEN", value)
		} else {
			_ = os.Unsetenv("JETSCALE_UNSTABLE_TOKEN")
		}
	})
	return dir
}

func TestStdinAuthCanceledLoginDoesNotRunCommand(t *testing.T) {
	dir := stdinCommandTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			logins.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			cancel()
			<-r.Context().Done()
		default:
			t.Error("command made a request after cancellation")
		}
	}))
	defer server.Close()
	root := newRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetIn(strings.NewReader("synthetic-password\n"))
	root.SetArgs([]string{"--api-url", server.URL, "--login", "test@example.invalid", "--password-stdin", "accounts", "list"})
	start := time.Now()
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "outcome uncertain") || !strings.Contains(err.Error(), "no automatic retry") || time.Since(start) > time.Second {
		t.Fatalf("canceled login did not fail safely: %v", err)
	}
	if logins.Load() != 1 {
		t.Fatalf("canceled login attempts=%d", logins.Load())
	}
	assertStdinAuthNoFiles(t, dir)
}

func TestStdinAuthPinnedTargetSurvivesConfigDrift(t *testing.T) {
	dir := stdinCommandTest(t)
	var logins, cleanups atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		case "/api/v2/auth/me":
			_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
		case "/api/v2/auth/sign-out":
			cleanups.Add(1)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	root := newRootCmd()
	fixture := &cobra.Command{Use: "fixture", PreRunE: func(cmd *cobra.Command, _ []string) error {
		cmd.SetContext(context.WithValue(cmd.Context(), sessionTargetKey{}, sessionTarget{instance: "fixture", origin: server.URL}))
		// A second resolution would fail or probe a different origin.
		flagAPIURL = "http://not-contacted.invalid"
		return nil
	}, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := commandSession(cmd, "fixture", server.URL)
		return err
	}}
	wrapSessionLifecycle(fixture)
	root.AddCommand(fixture)
	root.SetIn(strings.NewReader("synthetic-password\n"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--login", "test@example.invalid", "--password-stdin", "fixture"})
	if err := root.Execute(); err != nil || logins.Load() != 1 || cleanups.Load() != 1 {
		t.Fatalf("eager auth ignored pinned target: %v", err)
	}
	assertStdinAuthNoFiles(t, dir)
}

func TestCommandSessionRejectsCachedOriginMismatch(t *testing.T) {
	stdinCommandTest(t)
	t.Setenv(buildinfo.EnvVar("TOKEN"), "synthetic-token")
	s, err := auth.OpenSession("fixture", "https://EXAMPLE.invalid:443/", "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.HTTPClient.CloseIdleConnections()
	cmd := &cobra.Command{}
	cmd.SetContext(context.WithValue(context.Background(), sessionContextKey{}, &commandSessionState{session: s}))
	for _, origin := range []string{"https://example.invalid", "https://EXAMPLE.invalid:0443/"} {
		if got, err := commandSession(cmd, "alias", origin); err != nil || got != s {
			t.Fatalf("equivalent origin rejected: %v", err)
		}
	}
	for _, origin := range []string{"https://other.invalid", "https://example.invalid/path", "https://example.invalid?", "https://example.invalid:444"} {
		if _, err := commandSession(cmd, "fixture", origin); err == nil || !strings.Contains(err.Error(), "origin") {
			t.Fatal("cached session reused for a different/invalid origin")
		}
	}
}

func TestStdinAuthIdentityOutputOnlyMatchedField(t *testing.T) {
	for _, command := range []string{"whoami", "status"} {
		for _, matched := range []string{"email", "username"} {
			for _, format := range []string{"json", "yaml", "table"} {
				t.Run(command+"/"+matched+"/"+format, func(t *testing.T) {
					dir := stdinCommandTest(t)
					const password = "synthetic-password-not-output"
					login, counterpart := "test@example.invalid", "username"
					if matched == "username" {
						login, counterpart = "fixture", "email"
					}
					var meCalls, logins, cleanups atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/api/v2/auth/sign-in":
							logins.Add(1)
							http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
						case "/api/v2/auth/me":
							user := map[string]string{matched: login, counterpart: password}
							if meCalls.Add(1) > 2 {
								// A later response must not replace the verified snapshot.
								user[matched] = password
							}
							_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": user}})
						case "/api/v2/auth/sign-out":
							cleanups.Add(1)
						default:
							t.Error("unexpected request")
						}
					}))
					defer server.Close()
					root := newRootCmd()
					var stdout, stderr bytes.Buffer
					root.SetIn(strings.NewReader(password + "\n"))
					root.SetOut(&stdout)
					root.SetErr(&stderr)
					root.SetArgs([]string{"--api-url", server.URL, "--login", login, "--password-stdin", "--output", format, "auth", command})
					err := root.Execute()
					if err != nil {
						t.Fatalf("temporary identity command failed: %v", err)
					}
					if strings.Contains(stdout.String()+stderr.String(), password) {
						t.Fatal("unverified identity reflected password in output")
					}
					if meCalls.Load() != 2 || logins.Load() != 1 || cleanups.Load() != 1 {
						t.Fatal("identity snapshot re-fetched or session lifecycle changed")
					}
					if format == "table" {
						if !strings.Contains(stdout.String(), "as "+login) {
							t.Fatal("table omitted the verified identity")
						}
					} else {
						var row map[string]any
						if format == "json" {
							err = json.Unmarshal(stdout.Bytes(), &row)
						} else {
							err = yaml.Unmarshal(stdout.Bytes(), &row)
						}
						if err != nil || row[matched] != login {
							t.Fatal("structured output omitted the verified identity")
						}
						if _, present := row[counterpart]; present {
							t.Fatal("structured output included the unverified counterpart")
						}
						if command == "status" && row["authenticated"] != true {
							t.Fatal("verified session not reported authenticated")
						}
					}
					assertStdinAuthNoFiles(t, dir)
				})
			}
		}
	}
}

func TestStdinAuthWhoamiCancellation(t *testing.T) {
	for _, verification := range []int32{1, 2} {
		t.Run(fmt.Sprint(verification), func(t *testing.T) {
			dir := stdinCommandTest(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var meCalls, cleanups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
				case "/api/v2/auth/me":
					if meCalls.Add(1) == verification {
						cancel()
						<-r.Context().Done()
						return
					}
					_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
				case "/api/v2/auth/sign-out":
					cleanups.Add(1)
				default:
					t.Error("unexpected request")
				}
			}))
			defer server.Close()
			root := newRootCmd()
			var output bytes.Buffer
			root.SetIn(strings.NewReader("synthetic-password\n"))
			root.SetOut(&output)
			root.SetErr(&output)
			root.SetArgs([]string{"--api-url", server.URL, "--login", "test@example.invalid", "--password-stdin", "auth", "whoami"})
			start := time.Now()
			err := root.ExecuteContext(ctx)
			if !errors.Is(err, context.Canceled) || cleanups.Load() != 1 || time.Since(start) > time.Second || strings.Contains(output.String(), "test@example.invalid") {
				t.Fatalf("verification cancellation/cleanup failed: %v", err)
			}
			assertStdinAuthNoFiles(t, dir)
		})
	}
}

func TestStdinAuthFailuresAndIdentity(t *testing.T) {
	for _, scenario := range []string{"auth-login", "username", "command-error", "bad-login", "bad-me", "mismatch", "identity-check-error", "logout-error", "same-origin-redirect", "cross-origin-redirect", "command-redirect", "logout-redirect"} {
		t.Run(scenario, func(t *testing.T) {
			dir := stdinCommandTest(t)
			const password, cookie = "synthetic-password-not-output", "synthetic-cookie-not-output"
			var loginCalls, logoutCalls, accountCalls, meCalls, foreignCalls atomic.Int32
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { foreignCalls.Add(1) }))
			defer foreign.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					loginCalls.Add(1)
					if scenario == "bad-login" {
						w.WriteHeader(401)
						_, _ = fmt.Fprint(w, password)
						return
					}
					if strings.HasSuffix(scenario, "origin-redirect") {
						target := "/" + password
						if scenario == "cross-origin-redirect" {
							target = foreign.URL + target
						}
						http.Redirect(w, r, target, http.StatusTemporaryRedirect)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie, Path: "/"})
					_, _ = fmt.Fprint(w, `{}`)
				case "/api/v2/auth/me":
					meCalls.Add(1)
					if scenario == "bad-me" || (scenario == "identity-check-error" && meCalls.Load() == 2) {
						w.WriteHeader(500)
						_, _ = fmt.Fprint(w, password+cookie)
						return
					}
					if scenario == "mismatch" {
						_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"someone-else@example.invalid","username":"someone-else"}}}`)
					} else {
						_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid","username":"fixture"}}}`)
					}
				case "/api/v2/cloud/cloud-accounts":
					accountCalls.Add(1)
					switch scenario {
					case "command-error":
						w.WriteHeader(500)
						_, _ = fmt.Fprint(w, password+cookie)
					case "command-redirect":
						http.Redirect(w, r, foreign.URL+"/"+cookie, http.StatusTemporaryRedirect)
					default:
						_, _ = fmt.Fprint(w, `{"data":[]}`)
					}
				case "/api/v2/auth/sign-out":
					logoutCalls.Add(1)
					switch scenario {
					case "logout-error":
						w.WriteHeader(500)
						_, _ = fmt.Fprint(w, password+cookie)
					case "logout-redirect":
						http.Redirect(w, r, foreign.URL+"/"+cookie, http.StatusTemporaryRedirect)
					default:
						_, _ = fmt.Fprint(w, `{}`)
					}
				default:
					t.Error("unexpected request or followed redirect")
				}
			}))
			defer server.Close()
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetIn(strings.NewReader(password + "\n"))
			login := "test@example.invalid"
			if scenario == "username" {
				login = "fixture"
			}
			command := []string{"accounts", "list"}
			if scenario == "auth-login" {
				command = []string{"auth", "login"}
			}
			root.SetArgs(append([]string{"--api-url", server.URL, "--login", login, "--password-stdin"}, command...))
			err := root.Execute()
			wantSuccess := scenario == "auth-login" || scenario == "username"
			if (err == nil) != wantSuccess {
				t.Fatalf("success=%t, error=%v", wantSuccess, err)
			}
			wantLogout := int32(1)
			if scenario == "bad-login" || strings.HasSuffix(scenario, "origin-redirect") {
				wantLogout = 0
			}
			if loginCalls.Load() != 1 || logoutCalls.Load() != wantLogout || foreignCalls.Load() != 0 {
				t.Fatal("wrong login/cleanup counts or foreign request")
			}
			if (scenario == "mismatch" || scenario == "bad-me" || scenario == "identity-check-error" || scenario == "auth-login") && accountCalls.Load() != 0 {
				t.Fatal("command ran before identity verification")
			}
			combined := stdout.String() + stderr.String() + fmt.Sprint(err)
			if strings.Contains(combined, password) || strings.Contains(combined, cookie) || strings.Contains(combined, "\x1b[") {
				t.Fatal("unsafe output")
			}
			if strings.HasPrefix(scenario, "logout-") && !strings.Contains(combined, "cleanup") {
				t.Fatal("cleanup failure hidden")
			}
			if scenario == "auth-login" && !strings.Contains(stdout.String(), "temporary session") {
				t.Fatal("login did not report temporary verification")
			}
			assertStdinAuthNoFiles(t, dir)
		})
	}
}

// Invalid flag combinations must not consume even one byte of the password.
type stdinUnreadable struct{ reads int }

func (r *stdinUnreadable) Read([]byte) (int, error) {
	r.reads++
	return 0, errors.New("synthetic-reader-secret")
}

func TestStdinAuthFlagValidationBeforeRead(t *testing.T) {
	for _, flags := range [][]string{
		{"--login", "fixture"}, {"--password-stdin"}, {"--login=", "--password-stdin"},
		{"--login", "fixture", "--password-stdin=false"},
		{"--login", "fixture", "--password-stdin", "--credentials-file", "/not-read.env"},
		{"--login", "fixture", "--password-stdin", "--credentials-file="},
	} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			dir := stdinCommandTest(t)
			root := newRootCmd()
			input := &stdinUnreadable{}
			root.SetIn(input)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append(flags, "auth", "login"))
			if err := root.Execute(); err == nil || input.reads != 0 {
				t.Fatalf("flags not rejected before read: %v", err)
			}
			assertStdinAuthNoFiles(t, dir)
		})
	}
	for _, token := range []string{"", "synthetic-token"} {
		t.Run("token-"+token, func(t *testing.T) {
			stdinCommandTest(t)
			t.Setenv("JETSCALE_UNSTABLE_TOKEN", token)
			root := newRootCmd()
			input := &stdinUnreadable{}
			root.SetIn(input)
			root.SetArgs([]string{"--login", "fixture", "--password-stdin", "auth", "login"})
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "TOKEN") || input.reads != 0 {
				t.Fatal("token override not rejected before read")
			}
		})
	}
	stdinCommandTest(t)
	root := newRootCmd()
	input := &stdinUnreadable{}
	root.SetIn(input)
	root.SetArgs([]string{"--login", "fixture", "--password-stdin", "auth", "logout"})
	if err := root.Execute(); err == nil || input.reads != 0 {
		t.Fatal("logout consumed password")
	}
}

func TestStdinAuthPasswordInput(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		valid             bool
	}{
		{"lf", "fake-password\n", "fake-password", true},
		{"crlf", "fake-password\r\n", "fake-password", true},
		{"spaces", " \t fake-password \t\n", " \t fake-password \t", true},
		{"single-crlf", "fake-password\r\r\n", "fake-password\r", true},
		{"first-line", "fake-password\nignored\n", "fake-password", true},
		{"limit-lf", strings.Repeat("x", 4096) + "\n", strings.Repeat("x", 4096), true},
		{"limit-crlf", strings.Repeat("x", 4096) + "\r\n", strings.Repeat("x", 4096), true},
		{"oversize", strings.Repeat("x", 4097) + "\n", "", false},
		{"huge", strings.Repeat("x", 1<<20), "", false},
		{"empty", "\n", "", false}, {"empty-crlf", "\r\n", "", false},
		{"eof", "", "", false}, {"partial-eof", "synthetic-password", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readStdinPassword(context.Background(), strings.NewReader(tc.input), time.Second)
			defer clear(got)
			if (err == nil) != tc.valid || string(got) != tc.want {
				t.Fatal("wrong password framing or size behavior")
			}
		})
	}
	input := &stdinUnreadable{}
	if _, err := readStdinPassword(context.Background(), input, time.Second); err == nil || strings.Contains(err.Error(), "synthetic-reader-secret") {
		t.Fatal("input error leaked")
	}
}

func TestStdinAuthInputFailureDoesNotLogin(t *testing.T) {
	for _, input := range []string{"", "\n", "partial-synthetic-password", strings.Repeat("x", 4097) + "\n"} {
		dir := stdinCommandTest(t)
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
		root := newRootCmd()
		root.SetIn(strings.NewReader(input))
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{"--api-url", server.URL, "--login", "fixture", "--password-stdin", "auth", "login"})
		err := root.Execute()
		server.Close()
		if err == nil || requests.Load() != 0 {
			t.Fatal("invalid input attempted login")
		}
		assertStdinAuthNoFiles(t, dir)
	}
}

func TestStdinAuthCleanupDeadline(t *testing.T) {
	stdinCommandTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
			_, _ = fmt.Fprint(w, `{}`)
		case "/api/v2/auth/me":
			_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
		case "/api/v2/auth/sign-out":
			<-r.Context().Done()
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	root := newRootCmd()
	root.SetIn(strings.NewReader("synthetic-password\n"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--api-url", server.URL, "--login", "test@example.invalid", "--password-stdin", "auth", "login"})
	start := time.Now()
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "cleanup") || time.Since(start) > 7*time.Second {
		t.Fatalf("sign-out not bounded or failure hidden: %v", err)
	}
}

func TestStdinAuthInputTimeoutAndCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		read, write := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		start := time.Now()
		_, err := readStdinPassword(ctx, read, 20*time.Millisecond)
		cancel()
		_ = read.Close()
		_ = write.Close()
		if err == nil || time.Since(start) > time.Second {
			t.Fatal("blocked password input not bounded")
		}
	}
}

func TestStdinAuthHelpOffline(t *testing.T) {
	for _, command := range [][]string{{"--help"}, {"auth", "login", "--help"}, {"accounts", "list", "--help"}} {
		dir := stdinCommandTest(t)
		root := newRootCmd()
		input := &stdinUnreadable{}
		var output bytes.Buffer
		root.SetIn(input)
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(append([]string{"--login", "fixture", "--password-stdin", "--api-url", "https://not-contacted.invalid"}, command...))
		if err := root.Execute(); err != nil || input.reads != 0 || !strings.Contains(output.String(), "--password-stdin") {
			t.Fatal("help not offline")
		}
		assertStdinAuthNoFiles(t, dir)
	}
}

func TestStdinAuthCommandCancellation(t *testing.T) {
	stdinCommandTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logouts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
			_, _ = fmt.Fprint(w, `{}`)
		case "/api/v2/auth/me":
			_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid"}}}`)
		case "/api/v2/auth/sign-out":
			logouts.Add(1)
			_, _ = fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	root := newRootCmd()
	fixture := &cobra.Command{Use: "fixture", RunE: func(cmd *cobra.Command, _ []string) error { cancel(); return cmd.Context().Err() }}
	wrapSessionLifecycle(fixture)
	root.AddCommand(fixture)
	root.SetIn(strings.NewReader("synthetic-password\n"))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"--api-url", server.URL, "--login", "test@example.invalid", "--password-stdin", "fixture"})
	if err := root.ExecuteContext(ctx); !errors.Is(err, context.Canceled) || logouts.Load() != 1 {
		t.Fatalf("cancellation cleanup failed: %v", err)
	}
}

func assertStdinAuthNoFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary authentication wrote files")
	}
}

func TestStdinAuthTemporaryAccountListing(t *testing.T) {
	dir := stdinCommandTest(t)
	const password = "  synthetic-password\t "
	const cookie = "synthetic-cookie-not-output"
	loginCalls, logoutCalls, accountCalls := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v2/auth/sign-in" {
			loginCalls++
			var body struct{ Login, Password string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Login != "test@example.invalid" || body.Password != password {
				t.Error("incorrect synthetic login payload")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: cookie, Path: "/", HttpOnly: true})
			_, _ = fmt.Fprint(w, `{}`)
			return
		}
		c, err := r.Cookie("session")
		if err != nil || c.Value != cookie || r.Header.Get("Authorization") != "" {
			t.Error("request did not use the temporary cookie session")
		}
		switch r.URL.Path {
		case "/api/v2/auth/me":
			_, _ = fmt.Fprint(w, `{"data":{"user":{"email":"test@example.invalid","username":"fixture"}}}`)
		case "/api/v2/cloud/cloud-accounts":
			accountCalls++
			_, _ = fmt.Fprint(w, `{"data":[{"id":"fixture-account","name":"Fixture account","cloud_provider_type":"aws"}]}`)
		case "/api/v2/auth/sign-out":
			logoutCalls++
			_, _ = fmt.Fprint(w, `{}`)
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(password + "\r\n"))
	root.SetArgs([]string{"--api-url", server.URL, "--login", "test@example.invalid", "--password-stdin", "-o", "json", "accounts", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(stdout.Bytes(), &rows) != nil || len(rows) != 1 || rows[0].Name != "Fixture account" {
		t.Fatal("account results were not preserved as JSON")
	}
	if loginCalls != 1 || logoutCalls != 1 || accountCalls != 1 {
		t.Fatalf("login/logout/accounts = %d/%d/%d", loginCalls, logoutCalls, accountCalls)
	}
	if strings.Contains(stdout.String()+stderr.String(), password) || strings.Contains(stdout.String()+stderr.String(), cookie) {
		t.Error("synthetic secret appeared in output")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary authentication wrote files")
	}
}
