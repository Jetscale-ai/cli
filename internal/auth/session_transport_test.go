package auth

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func TestSessionTransportSingleAuthMode(t *testing.T) {
	isolateSessionStore(t)
	for _, bearer := range []bool{false, true} {
		t.Run(map[bool]string{false: "cookie", true: "bearer"}[bearer], func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if bearer {
					if r.Header.Get("Authorization") != "Bearer fake-token" || r.Header.Get("Cookie") != "" {
						t.Error("bearer mode mixed credentials")
					}
				} else if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "session=fake-cookie" {
					t.Error("cookie mode mixed credentials")
				}
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "fake-server-cookie", Path: "/"})
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer srv.Close()
			token := ""
			if bearer {
				token = "fake-token"
			}
			s, err := newSession(srv.URL, "", token)
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(srv.URL)
			if !bearer {
				s.jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "fake-cookie", Path: "/"}})
			}
			req, _ := http.NewRequest("GET", srv.URL, nil)
			req.Header["authorization"] = []string{"Bearer fake-injected"}
			req.Header["cookie"] = []string{"injected=fake-injected"}
			resp, err := s.HTTPClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if calls.Load() != 1 {
				t.Fatal("authentication retried/fell back")
			}
			if bearer && s.HTTPClient.Jar != nil {
				t.Fatal("bearer accepted response cookies")
			}
		})
	}
}

func TestSessionRejectedRedirectPreservesDeletion(t *testing.T) {
	isolateSessionStore(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1})
		// Invalid Location must not leak through net/http's URL parser either.
		w.Header().Set("Location", "http://invalid.test/%fake-secret")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	s, err := newSession(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	s.jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "fake-cookie", Path: "/"}})
	if _, err := s.Whoami(); err == nil {
		t.Fatal("redirect accepted")
	}
	if len(s.jar.snapshot()) != 0 || requests.Load() != 1 {
		t.Fatal("redirect deletion lost or redirect followed")
	}
	if err := s.Save("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", srv.URL, ""); err == nil {
		t.Fatal("deleted cookie resurrected")
	}
}

func TestSessionExpiredCookiesDoNotReload(t *testing.T) {
	isolateSessionStore(t)
	entry := TokenEntry{Origin: "https://example.test", Channel: buildinfo.Channel, Cookies: []StoredCookie{{Name: "session", Value: "fake-cookie", Path: "/", Domain: "example.test", HostOnly: true, Secure: true, Expires: time.Now().Add(-time.Second)}}}
	if err := SetToken("fixture", entry); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession("fixture", entry.Origin, ""); err == nil {
		t.Fatal("expired cookie restored")
	}
}

func TestSessionSameOriginRedirectAndMissingUsableCookie(t *testing.T) {
	isolateSessionStore(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "/again")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	if _, err := LoginSession(srv.URL, "fixture", "fake-password", ""); err == nil || requests.Load() != 1 {
		t.Fatal("same-origin redirect followed")
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/sign-in" {
			http.SetCookie(w, &http.Cookie{Name: "unrelated", Value: "fake-cookie", Path: "/other"})
		}
		_, _ = io.WriteString(w, fakeWhoami)
	}))
	defer srv2.Close()
	if _, err := LoginSession(srv2.URL, "fixture", "fake-password", ""); err == nil {
		t.Fatal("unusable login cookie accepted")
	}
}
