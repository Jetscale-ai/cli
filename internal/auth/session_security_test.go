package auth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func fixtureSession(t *testing.T, origin, value string) *Session {
	t.Helper()
	s, err := newSession(origin, "", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(origin)
	s.jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: value, Path: "/", HttpOnly: true}})
	return s
}

func TestSessionPlaintextRestrictedToLoopback(t *testing.T) {
	for _, origin := range []string{"http://example.test", "http://192.0.2.1", "http://0.0.0.0", "http://[::]", "http://localhost.example.test", "http://127.0.0.1.example.test", "http://127.1"} {
		for _, token := range []string{"", "fake-token"} {
			if _, err := newSession(origin, "", token); err == nil || !strings.Contains(err.Error(), "require HTTPS") {
				t.Errorf("plaintext origin accepted: %s", origin)
			}
		}
	}
	for _, origin := range []string{"http://127.0.0.1", "http://127.0.0.2:8080", "http://[::1]", "http://localhost", "http://LOCALHOST:8080", "https://example.test"} {
		if _, err := newSession(origin, "", "fake-token"); err != nil {
			t.Errorf("allowed origin rejected: %s: %v", origin, err)
		}
	}
}

func TestSessionDirectTransportAndLocalhostPin(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { proxyCalls.Add(1) }))
	defer proxy.Close()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-token" {
			t.Error("missing fake auth")
		}
		_, _ = io.WriteString(w, fakeWhoami)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	s, err := newSession("http://localhost:"+u.Port(), "", "fake-token")
	if err != nil {
		t.Fatal(err)
	}
	defer s.HTTPClient.CloseIdleConnections()
	transport := s.HTTPClient.Transport.(*originTransport)
	if transport.base.Proxy != nil {
		t.Fatal("ambient proxy configured")
	}
	if _, err := s.Whoami(); err != nil {
		t.Fatal(err)
	}
	// The localhost dialer must ignore even a non-loopback resolver destination.
	conn, err := transport.base.DialContext(context.Background(), "tcp", "unresolved.example.test:"+u.Port())
	if err != nil {
		t.Fatal(err)
	}
	if !conn.RemoteAddr().(*net.TCPAddr).IP.IsLoopback() {
		t.Error("localhost dial escaped loopback")
	}
	_ = conn.Close()
	cookieSession, err := newSession(srv.URL, "", "")
	if err != nil || cookieSession.HTTPClient.Transport.(*originTransport).base.Proxy != nil {
		t.Fatal("cookie transport uses proxy")
	}
	if proxyCalls.Load() != 0 {
		t.Fatal("credentials reached ambient proxy")
	}
}

func TestSessionStorageIndependentOfAliasesAndChannels(t *testing.T) {
	isolateSessionStore(t)
	previous := buildinfo.Channel
	t.Cleanup(func() { buildinfo.Channel = previous })
	sharedDir := privateTempDir(t)
	for _, key := range []string{"JETSCALE_CONFIG_DIR", "JETSCALE_LOCAL_CONFIG_DIR", "JETSCALE_UNSTABLE_CONFIG_DIR"} {
		t.Setenv(key, sharedDir)
	}
	for _, key := range []string{"JETSCALE_TOKEN", "JETSCALE_LOCAL_TOKEN", "JETSCALE_UNSTABLE_TOKEN"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	for _, channel := range []string{"live", "local", "unstable"} {
		buildinfo.Channel = channel
		for _, origin := range []string{"https://one.example.test", "https://two.example.test"} {
			s := fixtureSession(t, origin, "fake-"+channel)
			if err := s.Save("same-alias"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, channel := range []string{"live", "local", "unstable"} {
		buildinfo.Channel = channel
		for _, origin := range []string{"https://one.example.test", "https://two.example.test"} {
			s, err := OpenSession("different-alias", origin+"/", "")
			if err != nil {
				t.Fatal(err)
			}
			cookies := s.jar.snapshot()
			if len(cookies) != 1 || cookies[0].Value != "fake-"+channel {
				t.Fatal("session overwritten across origin or channel")
			}
		}
	}
	store, err := LoadTokens()
	if err != nil || len(store.Sessions) != 6 || len(store.Instances) != 0 {
		t.Fatal("unexpected canonical session namespace")
	}
}

func TestSessionBoundLegacyMigrationAndNoResurrection(t *testing.T) {
	isolateSessionStore(t)
	s := fixtureSession(t, "https://example.test", "fake-cookie")
	entry := TokenEntry{Origin: s.origin, Channel: s.channel, Cookies: s.jar.snapshot()}
	for _, alias := range []string{"first", "second"} {
		if err := SetToken(alias, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetToken("unbound", TokenEntry{AccessToken: "fake-legacy"}); err != nil {
		t.Fatal(err)
	}
	opened, err := OpenSession("first", s.origin, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Save("renamed"); err != nil {
		t.Fatal(err)
	}
	store, err := LoadTokens()
	if err != nil || len(store.Instances) != 1 || store.Instances["unbound"].AccessToken != "fake-legacy" {
		t.Fatal("migration lost unrelated legacy entry or kept stale aliases")
	}
	if _, err := OpenSession("unknown-alias", s.origin, ""); err != nil {
		t.Fatal(err)
	}
	opened.jar.clear()
	if err := opened.Save("renamed"); err != nil {
		t.Fatal(err)
	}
	// Simulate an old writer reintroducing stale alias state after sign-out.
	if err := SetToken("first", entry); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("first", s.origin, ""); err == nil {
		t.Fatal("empty canonical session fell back to legacy credentials")
	}
	store, err = LoadTokens()
	if err != nil {
		t.Fatal(err)
	}
	entry.Origin = "https://wrong.example.test"
	store.Sessions[s.storeKey()] = entry
	if err := SaveTokens(store); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("first", s.origin, ""); err == nil {
		t.Fatal("mismatched canonical binding fell back to valid legacy entry")
	}
}

func TestSessionSignOutRequiresConfirmedSuccess(t *testing.T) {
	for _, deleteCookie := range []bool{false, true} {
		t.Run(map[bool]string{false: "retain", true: "server-deletion"}[deleteCookie], func(t *testing.T) {
			isolateSessionStore(t)
			var status atomic.Int32
			status.Store(http.StatusUnauthorized)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/v2/auth/sign-out" {
					t.Error("wrong sign-out request")
				}
				if deleteCookie {
					http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1})
				}
				w.WriteHeader(int(status.Load()))
				if status.Load() != http.StatusNoContent {
					_, _ = io.WriteString(w, "fake-secret-body")
				}
			}))
			defer srv.Close()
			s := fixtureSession(t, srv.URL, "fake-cookie")
			err := s.SignOut()
			if err == nil || !strings.Contains(err.Error(), "cleanup unverified (HTTP 401)") || strings.Contains(err.Error(), "fake-secret") {
				t.Fatalf("401 invalidation misreported: %v", err)
			}
			if err := s.Save("fixture"); err != nil {
				t.Fatal(err)
			}
			_, err = OpenSession("any-alias", srv.URL, "")
			if (err != nil) != deleteCookie {
				t.Fatal("401 response cookie deletion/retention was not preserved")
			}
			status.Store(http.StatusNoContent)
			if err := s.SignOut(); err != nil {
				t.Fatalf("confirmed local sign-out failed: %v", err)
			}
			if len(s.jar.snapshot()) != 0 {
				t.Fatal("successful sign-out retained cookies")
			}
			if err := s.Save("fixture"); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenSession("fixture", srv.URL, ""); err == nil {
				t.Fatal("successful sign-out not persisted")
			}
		})
	}
}

func TestLoginConnectionLossReportsUncertainStateWithoutRetry(t *testing.T) {
	isolateSessionStore(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		// Simulate creation of a server session followed by loss of the response.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	_, err := LoginSession(srv.URL, "fake-login", "fake-password", "")
	if err == nil || !strings.Contains(err.Error(), "sign-in outcome uncertain") || !strings.Contains(err.Error(), "cleanup unverified") || strings.Contains(err.Error(), "fake-password") {
		t.Fatalf("lost response incorrectly reported: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("uncertain sign-in was retried")
	}
	store, err := LoadTokens()
	if err != nil || len(store.Sessions) != 0 || len(store.Instances) != 0 {
		t.Fatal("uncertain login persisted credentials")
	}
}

func TestSessionStoreRejectsUnsafeFilesAndDirectories(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "directory", "directory-permissions", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			if runtime.GOOS == "windows" && (kind == "permissions" || kind == "directory-permissions") {
				t.Skip("POSIX chmod assertion; Windows ACL rejection is covered by token_storage_windows_test.go")
			}
			isolateSessionStore(t)
			p, _ := tokensPath()
			switch kind {
			case "permissions":
				if err := os.WriteFile(p, []byte("instances: {}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, 0o644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "fake-secret-store")
				if err := os.WriteFile(target, []byte("instances: {}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, p); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
						t.Skip("Windows symlink privilege unavailable; enable Developer Mode for this coverage")
					}
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
			case "directory-permissions":
				if err := os.Chmod(filepath.Dir(p), 0o777); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				link := filepath.Join(t.TempDir(), "config-link")
				if err := os.Symlink(filepath.Dir(p), link); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
						t.Skip("Windows symlink privilege unavailable; enable Developer Mode for this coverage")
					}
					t.Fatal(err)
				}
				t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), link)
			}
			if _, err := LoadTokens(); err == nil || strings.Contains(err.Error(), "fake-secret-store") {
				t.Fatal("unsafe store accepted or path leaked")
			}
			if kind != "permissions" {
				if err := SaveTokens(TokenStore{}); err == nil {
					t.Fatal("unsafe store write accepted")
				}
			}
		})
	}
}

func TestSessionCookieSerializationPreservesAttributes(t *testing.T) {
	isolateSessionStore(t)
	s := fixtureSession(t, "https://api.example.test", "fake-host-cookie")
	u, _ := url.Parse(s.origin)
	s.jar.SetCookies(u, []*http.Cookie{{Name: "domain", Value: "fake quoted value", Quoted: true, Domain: ".example.test", Path: "/api", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}})
	before := s.jar.snapshot()
	if err := s.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenSession("other-alias", s.origin, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, restored.jar.snapshot()) {
		t.Fatal("cookie attributes changed through disk serialization")
	}
	s.jar.SetCookies(u, []*http.Cookie{{Name: "partitioned", Value: "fake-partitioned", Path: "/", Secure: true, Partitioned: true}})
	if len(s.jar.snapshot()) != 2 {
		t.Fatal("unsupported partitioned cookie lost its isolation semantics")
	}
	for _, malformed := range []StoredCookie{
		{Name: "bad", Value: "fake-cookie", Path: "/", Domain: "", HostOnly: false},
		{Name: "bad", Value: "fake-cookie", Path: "/", Domain: "other.example.test", HostOnly: true},
		{Name: "bad", Value: "fake-cookie", Path: "/", Domain: "api.example.test", HostOnly: true, Secure: true, Partitioned: true},
	} {
		if err := newSessionJar(s.origin).restore([]StoredCookie{malformed}); err == nil {
			t.Fatal("malformed or unsupported persisted cookie accepted")
		}
	}
}
