package auth

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	preparePrivateTestDir(t, dir)
	return dir
}

func isolateSessionStore(t *testing.T) {
	t.Helper()
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), privateTempDir(t))
	t.Setenv(buildinfo.EnvVar("TOKEN"), "")
	if err := os.Unsetenv(buildinfo.EnvVar("TOKEN")); err != nil {
		t.Fatal(err)
	}
}

func serverCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fakeWhoami = `{"data":{"user":{"email":"fixture@example.test","username":"fixture"}}}`

func TestSessionCookieLifecycle(t *testing.T) {
	isolateSessionStore(t)
	var meCalls atomic.Int32
	var signouts atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("cookie session sent Authorization")
		}
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			var body struct{ Login, Password string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Login != "fixture" || body.Password != "fake-password" || r.Method != "POST" {
				t.Error("wrong sign-in request")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "opaque-one", Path: "/api", Secure: true, HttpOnly: true, MaxAge: 300})
			_, _ = io.WriteString(w, `{"data":{"tokens":{"access_token":"must-not-be-used"}}}`)
		case "/api/v2/auth/me":
			want := "opaque-one"
			if meCalls.Add(1) > 1 {
				want = "opaque-three"
			}
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != want {
				t.Error("wrong session cookie")
			}
			if want == "opaque-one" {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "opaque-two", Path: "/api", Secure: true, HttpOnly: true, MaxAge: 300})
			}
			_, _ = io.WriteString(w, fakeWhoami)
		case "/api/rotate":
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "opaque-two" {
				t.Error("verification rotation not retained")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "opaque-three", Path: "/api", Secure: true, HttpOnly: true, MaxAge: 300})
		case "/api/v2/auth/sign-out":
			signouts.Add(1)
			if _, err := r.Cookie("session"); err != nil {
				t.Error("sign-out missing session")
			}
			http.SetCookie(w, &http.Cookie{Name: "session", Path: "/api", MaxAge: -1, Secure: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ca := serverCA(t, srv)
	s, err := LoginSession(srv.URL, "fixture", "fake-password", ca)
	if err != nil {
		t.Fatal(err)
	}
	if s.HTTPClient.Timeout != 30*time.Second {
		t.Fatal("missing request timeout")
	}
	defer s.HTTPClient.CloseIdleConnections()
	resp, err := s.HTTPClient.Get(srv.URL + "/api/rotate")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if err := s.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	store, err := LoadTokens()
	entry := store.Sessions[s.storeKey()]
	if err != nil || entry.AccessToken != "" || entry.RefreshToken != "" || entry.Origin != srv.URL || entry.Channel != buildinfo.Channel || len(entry.Cookies) != 1 {
		t.Fatal("incorrect saved session")
	}
	c := entry.Cookies[0]
	if c.Value != "opaque-three" || c.Path != "/api" || !c.Secure || !c.HTTPOnly || !c.HostOnly || c.Expires.IsZero() {
		t.Fatal("cookie metadata lost")
	}
	reopened, err := OpenSession("fixture", srv.URL+"/", ca)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.HTTPClient.CloseIdleConnections()
	who, err := reopened.Whoami()
	if err != nil || who.Username != "fixture" {
		t.Fatalf("whoami after reopen: %v", err)
	}
	if err := reopened.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	store, err = LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	entry = store.Sessions[s.storeKey()]
	if !entry.Cookies[0].Expires.Equal(c.Expires) {
		t.Fatal("reload extended Max-Age")
	}
	if err := reopened.SignOut(); err != nil {
		t.Fatal(err)
	}
	if signouts.Load() != 1 {
		t.Fatal("sign-out was not called")
	}
	if err := reopened.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", srv.URL, ca); err == nil {
		t.Fatal("signed-out session reopened")
	}
	p, _ := tokensPath()
	assertPrivateTokenFile(t, p)
}

func TestSessionOriginAndRedirectIsolation(t *testing.T) {
	isolateSessionStore(t)
	var leaked atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Add(1) }))
	defer target.Close()
	for _, code := range []int{301, 302, 303, 307, 308} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Location", target.URL+"/fake-password")
				w.WriteHeader(code)
			}))
			defer srv.Close()
			_, err := LoginSession(srv.URL, "fixture", "fake-password", "")
			if err == nil || strings.Contains(err.Error(), "fake-password") || requests.Load() != 1 {
				t.Fatalf("redirect not safely rejected: %v", err)
			}
		})
	}
	if leaked.Load() != 0 {
		t.Fatal("redirect leaked credentials")
	}
	s, err := newSession("https://example.test", "", "fake-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{target.URL, "http://example.test", "https://sub.example.test", "https://example.test:8080"} {
		if _, err := s.HTTPClient.Get(destination); err == nil {
			t.Fatal("cross-origin request allowed")
		}
	}
	if leaked.Load() != 0 {
		t.Fatal("direct cross-origin request escaped")
	}
	req, _ := http.NewRequest("GET", "https://example.test", nil)
	req.Host = "other.test"
	if _, err := s.HTTPClient.Do(req); err == nil {
		t.Fatal("Host override allowed")
	}
}

func TestSessionVerificationCleanupAndRedaction(t *testing.T) {
	isolateSessionStore(t)
	for _, scenario := range []struct {
		name    string
		status  int
		body    string
		cleanup int
	}{
		{"unauthorized", 401, "fake-password", 200},
		{"malformed", 200, `{"data": "fake-password"}`, 200},
		{"missing-user", 200, `{}`, 200},
		{"cleanup-failed", 500, `fake-password`, 500},
		{"cleanup-unauthorized", 401, `fake-password`, 401},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var cleanups atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "fake-password", Path: "/"})
				case "/api/v2/auth/me":
					w.WriteHeader(scenario.status)
					_, _ = io.WriteString(w, scenario.body)
				case "/api/v2/auth/sign-out":
					cleanups.Add(1)
					w.WriteHeader(scenario.cleanup)
					_, _ = io.WriteString(w, "fake-password")
				}
			}))
			defer srv.Close()
			_, err := LoginSession(srv.URL, "fixture", "fake-password", "")
			if err == nil || strings.Contains(err.Error(), "fake-password") || cleanups.Load() != 1 {
				t.Fatalf("unsafe verification failure: %v", err)
			}
			if scenario.cleanup >= 300 && (!strings.Contains(err.Error(), "cleanup sign-out failed") || strings.Contains(err.Error(), "cleanup sign-out completed")) {
				t.Fatal("cleanup failure hidden")
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = io.WriteString(w, "fake-password")
	}))
	defer srv.Close()
	_, err := LoginSession(srv.URL, "fixture", "fake-password", "")
	if err == nil || strings.Contains(err.Error(), "fake-password") {
		t.Fatal("login error body exposed")
	}
}

func TestSessionContextCancellation(t *testing.T) {
	isolateSessionStore(t)
	for _, operation := range []string{"sign-in", "me", "me-body", "sign-out"} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if operation == "me-body" {
					_, _ = io.WriteString(w, `{"data":`)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			var err error
			if operation == "sign-in" {
				_, err = LoginSessionContext(ctx, srv.URL, "fixture", "synthetic-secret", "")
			} else {
				s := fixtureSession(t, srv.URL, "synthetic-secret")
				defer s.HTTPClient.CloseIdleConnections()
				if strings.HasPrefix(operation, "me") {
					_, err = s.WhoamiContext(ctx)
				} else {
					err = s.SignOutContext(ctx)
					if len(s.jar.snapshot()) != 1 {
						t.Fatal("unconfirmed sign-out discarded local credentials")
					}
				}
			}
			if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 || time.Since(start) > time.Second || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), srv.URL) {
				t.Fatalf("context not honored or error not sanitized: %v", err)
			}
			if operation == "sign-in" && (!strings.Contains(err.Error(), "outcome uncertain") || !strings.Contains(err.Error(), "no automatic retry")) {
				t.Fatalf("ambiguous sign-in not reported: %v", err)
			}
		})
	}
}

func TestSessionAlreadyCanceledLoginDoesNotSend(t *testing.T) {
	isolateSessionStore(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LoginSessionContext(ctx, srv.URL, "fixture", "synthetic-secret", "")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "not attempted") || calls.Load() != 0 {
		t.Fatalf("already canceled login attempted: %v", err)
	}
}

func TestSessionCanceledVerificationDetachedCleanup(t *testing.T) {
	isolateSessionStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cleanups atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-secret", Path: "/"})
		case "/api/v2/auth/me":
			cancel()
			<-r.Context().Done()
		case "/api/v2/auth/sign-out":
			cleanups.Add(1)
			if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "synthetic-secret" {
				t.Error("cleanup missing accepted session")
			}
		}
	}))
	defer srv.Close()
	_, err := LoginSessionContext(ctx, srv.URL, "fixture", "synthetic-secret", "")
	if !errors.Is(err, context.Canceled) || cleanups.Load() != 1 || !strings.Contains(err.Error(), "cleanup sign-out completed") {
		t.Fatalf("canceled verification not cleaned up: %v", err)
	}
}

func TestSessionFailedLoginCleanupGrace(t *testing.T) {
	isolateSessionStore(t)
	for _, failedSignIn := range []bool{false, true} {
		t.Run(fmt.Sprint(failedSignIn), func(t *testing.T) {
			var cleanups atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-secret", Path: "/"})
					if failedSignIn {
						w.WriteHeader(500)
					}
				case "/api/v2/auth/me":
					w.WriteHeader(401)
				case "/api/v2/auth/sign-out":
					cleanups.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					<-r.Context().Done()
				}
			}))
			defer srv.Close()
			start := time.Now()
			_, err := LoginSessionContext(context.Background(), srv.URL, "fixture", "synthetic-secret", "")
			elapsed := time.Since(start)
			if err == nil || cleanups.Load() != 1 || elapsed < 4*time.Second || elapsed > 7*time.Second || !strings.Contains(err.Error(), "cleanup sign-out failed") || !strings.Contains(err.Error(), "cleanup unverified") {
				t.Fatalf("cleanup grace not bounded/explicit: %v (%v)", err, elapsed)
			}
		})
	}
}

func TestSessionFailedSignInCookieCleanup(t *testing.T) {
	isolateSessionStore(t)
	for _, cleanupStatus := range []int{http.StatusNoContent, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(cleanupStatus), func(t *testing.T) {
			var logins, cleanups, verifications atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					logins.Add(1)
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-secret", Path: "/"})
					w.WriteHeader(http.StatusInternalServerError)
				case "/api/v2/auth/sign-out":
					cleanups.Add(1)
					if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "synthetic-secret" {
						t.Error("cleanup did not use the issued cookie")
					}
					w.WriteHeader(cleanupStatus)
				default:
					verifications.Add(1)
				}
				_, _ = io.WriteString(w, "synthetic-secret")
			}))
			defer srv.Close()
			s, err := LoginSession(srv.URL, "fixture", "synthetic-secret", "")
			if s != nil || err == nil || !strings.Contains(err.Error(), "sign-in outcome uncertain (HTTP 500)") ||
				strings.Contains(err.Error(), "synthetic-secret") || logins.Load() != 1 || cleanups.Load() != 1 || verifications.Load() != 0 {
				t.Fatalf("failed login cookie state mishandled: %v", err)
			}
			want := "cleanup sign-out completed"
			if cleanupStatus != http.StatusNoContent {
				want = "cleanup sign-out failed"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("cleanup result missing: %v", err)
			}
		})
	}
	path, _ := tokensPath()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed login persisted credentials")
	}
}

func TestSessionCAAndURLFailures(t *testing.T) {
	isolateSessionStore(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, fakeWhoami) }))
	defer srv.Close()
	s, err := newSession(srv.URL, "", "fake-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Whoami(); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
	badCA := filepath.Join(t.TempDir(), "fake-password.pem")
	if _, err := newSession(srv.URL, badCA, ""); err == nil || strings.Contains(err.Error(), "fake-password") {
		t.Fatal("missing CA path leaked or accepted")
	}
	if err := os.WriteFile(badCA, []byte("fake-password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSession(srv.URL, badCA, ""); err == nil || strings.Contains(err.Error(), "fake-password") {
		t.Fatal("invalid CA leaked or accepted")
	}
	for _, base := range []string{"https://user:fake-password@example.test", "https://example.test?fake-password", "https://example.test/path", "https://example.test/#fake-password", "ftp://example.test", ":invalid", "https://example.test:99999"} {
		if _, err := newSession(base, "", ""); err == nil || strings.Contains(err.Error(), "fake-password") {
			t.Fatal("invalid URL leaked or accepted")
		}
	}
	s, err = newSession("https://EXAMPLE.test:0443/", "", "")
	if err != nil || s.origin != "https://example.test" {
		t.Fatal("origin normalization failed")
	}
}

func TestSessionEnvironmentAndBinding(t *testing.T) {
	isolateSessionStore(t)
	origin := "https://example.test"
	entry := TokenEntry{Origin: origin, Channel: buildinfo.Channel, Cookies: []StoredCookie{{Name: "session", Value: "fake-cookie", Domain: "example.test", HostOnly: true, Path: "/"}}}
	if err := SetToken("fixture", entry); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "bad token", "fake\r\npassword"} {
		t.Setenv(buildinfo.EnvVar("TOKEN"), token)
		if _, err := OpenSession("fixture", origin, ""); err == nil {
			t.Fatal("invalid explicit token fell back")
		}
	}
	t.Setenv(buildinfo.EnvVar("TOKEN"), "fake-bearer")
	s, err := OpenSession("fixture", origin, "")
	if err != nil || s.HTTPClient.Jar != nil {
		t.Fatal("environment token not bearer-only")
	}
	if err := s.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	saved, _, _ := GetToken("fixture")
	if saved.AccessToken != "" || len(saved.Cookies) != 1 {
		t.Fatal("environment Save overwrote cookies")
	}
	if err := os.Unsetenv(buildinfo.EnvVar("TOKEN")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", "https://other.test", ""); err == nil {
		t.Fatal("origin mismatch accepted")
	}
	entry.Channel = "other"
	if err := SetToken("fixture", entry); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", origin, ""); err == nil {
		t.Fatal("channel mismatch accepted")
	}
	if err := SetToken("fixture", TokenEntry{AccessToken: "fake-legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", origin, ""); err == nil {
		t.Fatal("unbound legacy token accepted")
	}
	if err := SetToken("fixture", TokenEntry{AccessToken: "fake-legacy", Origin: origin, Channel: buildinfo.Channel, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", origin, ""); err != nil {
		t.Fatalf("bound bearer not supported: %v", err)
	}
}

func TestSessionJarMetadataAndDeletions(t *testing.T) {
	j := newSessionJar("https://api.example.test")
	u, _ := url.Parse("https://api.example.test/api/v2/auth/sign-in")
	j.SetCookies(u, []*http.Cookie{
		{Name: "default", Value: "fake-default"},
		{Name: "domain", Value: "fake-domain", Domain: ".example.test", Path: "/", Secure: true},
		{Name: "bad-domain", Value: "fake-bad", Domain: "attacker.test", Path: "/"},
		{Name: "expires", Value: "fake-expiry", Path: "/", Expires: time.Now().Add(-time.Hour)},
		{Name: "max-age", Value: "fake-age", Path: "/", MaxAge: 60, Expires: time.Now().Add(-time.Hour)},
	})
	cookies := j.snapshot()
	if len(cookies) != 3 || cookies[0].Path != "/api/v2/auth" || !cookies[0].HostOnly || cookies[1].HostOnly {
		t.Fatal("cookie metadata/acceptance incorrect")
	}
	restored := newSessionJar(j.origin)
	if err := restored.restore(cookies); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://sub.api.example.test/", "http://api.example.test/", "https://api.example.test:444/"} {
		other, _ := url.Parse(address)
		if len(restored.Cookies(other)) != 0 {
			t.Fatal("jar leaked across origin")
		}
	}
	root, _ := url.Parse(j.origin + "/")
	if len(restored.Cookies(root)) != 2 {
		t.Fatal("default-path cookie leaked to root")
	}
	for _, name := range []string{"domain", "max-age"} {
		c := &http.Cookie{Name: name, Path: "/", MaxAge: -1}
		if name == "domain" {
			c.Domain = ".example.test"
		}
		restored.SetCookies(u, []*http.Cookie{c})
	}
	if len(restored.snapshot()) != 1 {
		t.Fatal("deleted cookies persisted")
	}
	j = newSessionJar("https://api.example.test")
	if err := j.restore(restored.snapshot()); err != nil {
		t.Fatal(err)
	}
	if len(j.Cookies(root)) != 0 {
		t.Fatal("deletion resurrected after reload")
	}
}

func TestTokenStoreRedactionAndPermissionsRepair(t *testing.T) {
	isolateSessionStore(t)
	p, _ := tokensPath()
	if err := os.WriteFile(p, []byte("instances: fake-password"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokens(); err == nil || strings.Contains(err.Error(), "fake-password") {
		t.Fatal("store parser leaked credential")
	}
	if runtime.GOOS == "windows" {
		// Windows rejects permissive ACLs rather than repairing them; its
		// separate ACL tests cover this. Keep parser redaction above active.
		return
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveTokens(TokenStore{Instances: map[string]TokenEntry{}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("existing file permissions not repaired")
	}
}
