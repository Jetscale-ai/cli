package auth

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBoundedSessionCleanupSlotIsExclusiveAndSingleUse(t *testing.T) {
	isolateSessionStore(t)
	var requests, signouts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		case "/api/v2/auth/sign-out":
			signouts.Add(1)
		default:
			_, _ = io.WriteString(w, fakeWhoami)
		}
	}))
	defer srv.Close()
	s, err := LoginSessionBoundedContext(context.Background(), srv.URL, "fixture", "synthetic-password", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	// A direct signout request must not impersonate lifecycle cleanup.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v2/auth/sign-out", nil)
	if _, err := s.HTTPClient.Do(req); !errors.Is(err, ErrHTTPRequestBound) {
		t.Fatalf("raw request spent reserve: %v", err)
	}
	if err := s.SignOutContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SignOutContext(context.Background()); !errors.Is(err, ErrHTTPRequestBound) {
		t.Fatalf("cleanup slot reusable or error identity lost: %v", err)
	}
	if requests.Load() != 3 || signouts.Load() != 1 {
		t.Fatalf("requests=%d signouts=%d", requests.Load(), signouts.Load())
	}
}

func TestBoundedSessionAcquisitionFailureCleanup(t *testing.T) {
	for _, mode := range []string{"verification", "cancel-verification", "cancel-signin", "lost-signin", "cleanup-failure"} {
		t.Run(mode, func(t *testing.T) {
			isolateSessionStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requests, signouts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch r.URL.Path {
				case "/api/v2/auth/sign-in":
					_, _ = io.Copy(io.Discard, r.Body)
					if mode == "cancel-signin" {
						cancel()
						<-r.Context().Done()
						return
					}
					if mode == "lost-signin" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
				case "/api/v2/auth/me":
					if mode == "cancel-verification" {
						cancel()
						<-r.Context().Done()
						return
					}
					w.WriteHeader(503)
					_, _ = io.WriteString(w, "synthetic-password")
				case "/api/v2/auth/sign-out":
					signouts.Add(1)
					if mode == "cleanup-failure" {
						w.WriteHeader(503)
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()
			_, err := LoginSessionBoundedContext(ctx, srv.URL, "fixture", "synthetic-password", "", 3)
			if err == nil || strings.Contains(err.Error(), "synthetic-password") || signouts.Load() != 1 || requests.Load() > 3 {
				t.Fatalf("acquisition cleanup failed: err=%v requests=%d signouts=%d", err, requests.Load(), signouts.Load())
			}
			if (mode == "cancel-signin" || mode == "lost-signin") && !strings.Contains(err.Error(), "cleanup unverified") {
				t.Fatalf("missing cookie claimed invalidation: %v", err)
			}
		})
	}
}

func TestBoundedTransportUsesFreshHTTP1AndDoesNotReplayLostRead(t *testing.T) {
	isolateSessionStore(t)
	var lostReads atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 || !r.Close {
			t.Errorf("bounded request must use nonpersistent HTTP/1: %s close=%v", r.Proto, r.Close)
		}
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/", Secure: true})
		case "/api/v2/auth/me":
			_, _ = io.WriteString(w, fakeWhoami)
		case "/lost":
			lostReads.Add(1)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	s, err := LoginSessionBoundedContext(context.Background(), srv.URL, "fixture", "synthetic-password", serverCA(t, srv), 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.HTTPClient.Get(srv.URL + "/lost"); err == nil || lostReads.Load() != 1 {
		t.Fatalf("lost GET retransmitted: %v calls=%d", err, lostReads.Load())
	}
	if _, err := s.HTTPClient.Get(srv.URL + "/lost"); !errors.Is(err, ErrHTTPRequestBound) || lostReads.Load() != 1 {
		t.Fatalf("failed request refunded: %v", err)
	}
	if err := s.SignOutContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
