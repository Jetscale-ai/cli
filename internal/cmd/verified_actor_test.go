package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// verifiedActorServer answers /auth/me with one fixed user and records every request.
func verifiedActorServer(t *testing.T, user map[string]any) (*httptest.Server, func(string) int) {
	t.Helper()
	stdinCommandTest(t)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
			_, _ = io.WriteString(w, `{}`)
		case "/api/v2/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"user": user}})
		case "/api/v2/auth/sign-out":
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("refused command dispatched %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	count := func(request string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, seen := range requests {
			if seen == request {
				n++
			}
		}
		return n
	}
	return server, count
}

// The sign-in verification reply is the superuser proof: an ordinary user is
// refused before any resource request, without a second /auth/me.
func TestRequireSuperuserUsesSignInVerification(t *testing.T) {
	for name, user := range map[string]map[string]any{
		"ordinary": {"id": testUserID, "email": "fixture@example.invalid", "is_superuser": false},
		"missing":  {"id": testUserID, "email": "fixture@example.invalid"},
		"zero_id":  {"id": "00000000-0000-0000-0000-000000000000", "email": "fixture@example.invalid", "is_superuser": true},
	} {
		t.Run(name, func(t *testing.T) {
			server, count := verifiedActorServer(t, user)
			stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
				"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "4", "companies", "list", "--require-superuser")
			if code != ExitForbidden || !strings.Contains(stdout, "visibility_unproved") {
				t.Fatalf("unverified superuser must be refused: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if count("GET /api/v2/auth/me") != 1 || count("POST /api/v2/auth/sign-out") != 1 {
				t.Fatal("refusal re-read the actor or skipped cleanup")
			}
		})
	}
}

// Users commands also need an ACTIVE actor; the sign-in reply decides that too.
func TestUsersActorUsesSignInVerification(t *testing.T) {
	server, count := verifiedActorServer(t, map[string]any{
		"id": testUserID, "email": "fixture@example.invalid", "is_superuser": true, "status": "PENDING_ACTIVATION",
	})
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "4", "users", "get", usersTestEmail, "--max-pages=1")
	if code == 0 || !strings.Contains(stdout, "visibility_unproved") {
		t.Fatalf("inactive actor must be refused: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if count("GET /api/v2/auth/me") != 1 || count("GET /api/v2/auth/users") != 0 || count("POST /api/v2/auth/sign-out") != 1 {
		t.Fatal("users actor check re-read /auth/me, listed users or skipped cleanup")
	}
}
