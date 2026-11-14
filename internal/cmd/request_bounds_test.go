package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

func executeBounded(t *testing.T, ctx context.Context, origin string, input io.Reader, args ...string) (string, string, int) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(input)
	root.SetArgs(append([]string{"--api-url", origin, "-o", "json"}, args...))
	cmd, err := root.ExecuteContextC(ctx)
	code := finish(root, cmd, err, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestBoundedRootRequestCost(t *testing.T) {
	dir := stdinCommandTest(t)
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
			_, _ = io.WriteString(w, `{"data":{"user":{"id":"`+testUserID+`","email":"fixture@example.invalid","is_superuser":true}}}`)
		case "/api/v2/auth/sign-out":
			_, _ = io.WriteString(w, `{}`)
		case "/api/v2/organization/companies":
			_, _ = io.WriteString(w, `{"data":[],"meta":{"total_count":0}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "6", "companies", "list", "--require-superuser")
	want := []string{"POST /api/v2/auth/sign-in", "GET /api/v2/auth/me", "GET /api/v2/auth/me", "GET /api/v2/auth/me", "GET /api/v2/organization/companies", "POST /api/v2/auth/sign-out"}
	mu.Lock()
	defer mu.Unlock()
	if code != 0 || strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("code=%d stdout=%s stderr=%s requests=%v", code, stdout, stderr, requests)
	}
	assertStdinAuthNoFiles(t, dir)
}

func boundedTestServer(t *testing.T, handler http.HandlerFunc) (*fakeBackend, *httptest.Server) {
	t.Helper()
	stdinCommandTest(t)
	f := &fakeBackend{t: t}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
			_, _ = io.WriteString(w, `{}`)
		case "/api/v2/auth/me":
			_, _ = io.WriteString(w, `{"data":{"user":{"id":"`+testUserID+`","email":"fixture@example.invalid","status":"ACTIVE","is_superuser":true}}}`)
		case "/api/v2/auth/sign-out":
			_, _ = io.WriteString(w, `{}`)
		default:
			handler(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return f, server
}

func TestBoundedRootExhaustionNeverWritesAndReservesCleanup(t *testing.T) {
	for _, limit := range []int{2, 3, 4, 5, 7, 8, 9, 10} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var created bool
			f, server := boundedTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST":
					created = true
					_ = json.NewEncoder(w).Encode(map[string]any{"data": commandSetting(api.CompanySettingWorkOSOrg, "string", "org_fixture")})
				case r.URL.Path == "/api/v2/organization/companies":
					_, body := list(unstableCompany)
					_ = json.NewEncoder(w).Encode(body)
				case created:
					_ = json.NewEncoder(w).Encode(map[string]any{"data": commandSetting(api.CompanySettingWorkOSOrg, "string", "org_fixture")})
				default:
					w.WriteHeader(404)
					_, _ = io.WriteString(w, `{}`)
				}
			})
			stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
				"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", fmt.Sprint(limit),
				"company-settings", "ensure", api.CompanySettingWorkOSOrg, "--company", "unstable-jetscale", "--value", "org_fixture", "--yes", "--require-superuser")
			if f.count("POST /api/v2/auth/sign-out") != 1 {
				t.Fatalf("cleanup not reserved: stdout=%s stderr=%s", stdout, stderr)
			}
			f.mu.Lock()
			count := len(f.requests)
			f.mu.Unlock()
			if count > limit {
				t.Fatalf("%d requests exceed %d", count, limit)
			}
			if limit < 9 {
				if code != ExitFailure || f.count("POST /api/v2/system/company-settings") != 0 || !strings.Contains(stdout, `"reason": "http_request_bound"`) || !strings.Contains(stdout, `"transient": false`) {
					t.Fatalf("exhausted command wrote or failed unsafely: code=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
			} else if limit == 9 {
				if code != ExitFailure || f.count("POST /api/v2/system/company-settings") != 1 || !strings.Contains(stdout, `"reason": "outcome_uncertain"`) {
					t.Fatalf("readback exhaustion must remain uncertain after write: %d %s %s", code, stdout, stderr)
				}
			} else if code != 0 || f.count("POST /api/v2/system/company-settings") != 1 {
				t.Fatalf("exact successful bound: %d %s %s", code, stdout, stderr)
			}
		})
	}
}

func TestBoundedUsersActualRootLifecycleAndClone(t *testing.T) {
	for _, limit := range []int{6, 7} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f, server := boundedTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/auth/users" || r.URL.Query().Get("email") != "eq "+usersTestEmail {
					t.Errorf("unsafe user lookup: %s", r.URL)
				}
				_, payload := list(usersCommandRecord(true, "ACTIVE"))
				_ = json.NewEncoder(w).Encode(payload)
			})
			stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
				"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", fmt.Sprint(limit), "users", "get", usersTestEmail, "--require-superuser", "--max-pages=1")
			if f.count("POST /api/v2/auth/sign-in") != 1 || f.count("POST /api/v2/auth/sign-out") != 1 || f.count("GET /api/v2/auth/me") != 4 {
				t.Fatalf("root lifecycle missing: %d %s %s", code, stdout, stderr)
			}
			if limit == 6 {
				if code != ExitFailure || f.count("GET /api/v2/auth/users") != 0 || !strings.Contains(stdout, "http_request_bound") {
					t.Fatalf("clone bypassed budget: %d %s %s", code, stdout, stderr)
				}
			} else if code != 0 || f.count("GET /api/v2/auth/users") != 1 || strings.Contains(stdout, "synthetic-secret") {
				t.Fatalf("user get: %d %s %s", code, stdout, stderr)
			}
		})
	}
}

func TestResourceBoundsActualRootHelp(t *testing.T) {
	stdinCommandTest(t)
	for _, resource := range resourceGroups {
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetIn(&stdinUnreadable{})
		root.SetArgs([]string{resource, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "--max-http-requests") || !strings.Contains(out.String(), "--max-pages") || !strings.Contains(out.String(), "20") {
			t.Fatalf("bounds absent from help for %s: %s", resource, out.String())
		}
	}
	root := newRootCmd()
	cmd, _, err := root.Find([]string{"users", "get"})
	if err != nil || !strings.Contains(cmd.Example, "jetscale-unstable users get") {
		t.Fatalf("new root command skipped help adaptation: %v %s", err, cmd.Example)
	}
}

func TestResourcePageBoundFailsRatherThanTruncates(t *testing.T) {
	for _, resource := range []string{"companies", "api"} {
		for _, short := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-short=%v", resource, short), func(t *testing.T) {
				f, server := newFakeBackend(t)
				f.on("GET /api/v2/organization/companies", func(r *http.Request, _ []byte) (int, any) {
					count := 100
					if short {
						count = 1
					}
					items := make([]map[string]any, count)
					for i := range items {
						items[i] = map[string]any{"id": fmt.Sprint(i)}
					}
					return 200, map[string]any{"data": items, "meta": map[string]any{"total_count": 101}}
				})
				args := []string{resource, "list", "--max-pages=1", "-o", "json"}
				if resource == "api" {
					args = []string{"api", "organization/companies", "--paginate", "--max-pages=1", "-o", "json"}
				}
				stdout, stderr, code := run(t, server, args...)
				if code != ExitFailure || f.count("GET /api/v2/organization/companies") != 1 || !strings.Contains(stdout, `"error"`) || strings.Contains(stdout, `"id"`) {
					t.Fatalf("incomplete result returned: %d %s %s", code, stdout, stderr)
				}
			})
		}
	}
}

func TestResourcePageBoundRejectsInconsistentTotals(t *testing.T) {
	for _, resource := range []string{"companies", "api"} {
		for _, total := range []int{-1, 0} {
			t.Run(fmt.Sprintf("%s-total=%d", resource, total), func(t *testing.T) {
				f, server := newFakeBackend(t)
				f.on("GET /api/v2/organization/companies", func(_ *http.Request, _ []byte) (int, any) {
					return 200, map[string]any{"data": []map[string]any{{"id": "fixture-row"}}, "meta": map[string]any{"total_count": total}}
				})
				args := []string{resource, "list", "--max-pages=1", "-o", "json"}
				if resource == "api" {
					args = []string{"api", "organization/companies", "--paginate", "--max-pages=1", "-o", "json"}
				}
				stdout, stderr, code := run(t, server, args...)
				if code != ExitFailure || f.count("GET /api/v2/organization/companies") != 1 || !strings.Contains(stdout, `"reason": "invalid_response"`) || strings.Contains(stdout, "fixture-row") {
					t.Fatalf("inconsistent total returned data: %d %s %s", code, stdout, stderr)
				}
			})
		}
	}
}

func TestResourcePageBoundValidationBeforeLogin(t *testing.T) {
	for _, pages := range []string{"0", "-1", "21"} {
		stdinCommandTest(t)
		input := &stdinUnreadable{}
		_, stderr, code := executeBounded(t, context.Background(), "http://127.0.0.1:1", input,
			"--login", "fixture", "--password-stdin", "companies", "list", "--max-pages", pages)
		if code != ExitUsage || input.reads != 0 {
			t.Fatalf("bad pages consumed password: %d %s", code, stderr)
		}
	}
}

func TestBoundedRootValidationBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"--max-http-requests", "0", "companies", "list"},
		{"--max-http-requests", "-1", "companies", "list"},
		{"--max-http-requests", "1", "companies", "list"},
		{"--max-http-requests", "6", "companies", "list"},
		{"--max-http-requests", "6", "--login", "fixture", "companies", "list"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "--credentials-file=/not-read", "companies", "list"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "version"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "auth", "login"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "config", "show"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "system", "info"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "prepare", "basics"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "flags", "show"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "companies", "list", "--timeout=0"},
		{"--max-http-requests", "6", "--login", "fixture", "--password-stdin", "api", "auth/me", "--input=-"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdinCommandTest(t)
			input := &stdinUnreadable{}
			_, stderr, code := executeBounded(t, context.Background(), "http://127.0.0.1:1", input, args...)
			if code != ExitUsage || input.reads != 0 {
				t.Fatalf("validation consumed credentials: code=%d reads=%d stderr=%s", code, input.reads, stderr)
			}
		})
	}
}

func TestBoundedRootRejectsChannelTokenBeforeCredentials(t *testing.T) {
	stdinCommandTest(t)
	t.Setenv(buildinfo.EnvVar("TOKEN"), "synthetic-token")
	input := &stdinUnreadable{}
	stdout, stderr, code := executeBounded(t, context.Background(), "http://127.0.0.1:1", input,
		"--login", "fixture", "--password-stdin", "--max-http-requests=6", "companies", "list")
	if code != ExitUsage || input.reads != 0 || strings.Contains(stdout+stderr, "synthetic-token") {
		t.Fatalf("token combination consumed credentials: %d %s %s", code, stdout, stderr)
	}
}

func TestBoundedRootAcquisitionExhaustionKeepsCleanupFailure(t *testing.T) {
	stdinCommandTest(t)
	var signins, signouts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			signins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		case "/api/v2/auth/sign-out":
			signouts.Add(1)
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "synthetic-secret")
		default:
			t.Errorf("exhausted acquisition sent another request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture", "--password-stdin", "--max-http-requests=2", "companies", "list")
	if code != ExitFailure || signins.Load() != 1 || signouts.Load() != 1 || !strings.Contains(stdout, `"reason": "http_request_bound"`) || !strings.Contains(stderr, "cleanup unverified") || strings.Contains(stdout+stderr, "synthetic-secret") || strings.Contains(stdout+stderr, "synthetic-password") {
		t.Fatalf("lost cleanup failure or retried: %d %s %s signins=%d signouts=%d", code, stdout, stderr, signins.Load(), signouts.Load())
	}
}

func TestBoundedRootDeadlineIncludesAcquisition(t *testing.T) {
	stdinCommandTest(t)
	var signins, verifications, signouts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			signins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		case "/api/v2/auth/me":
			verifications.Add(1)
			<-r.Context().Done()
		case "/api/v2/auth/sign-out":
			signouts.Add(1)
		default:
			t.Errorf("command ran after acquisition deadline: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	start := time.Now()
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture", "--password-stdin", "--max-http-requests=3", "companies", "list", "--timeout=100ms")
	if code == 0 || signins.Load() != 1 || verifications.Load() != 1 || signouts.Load() != 1 || time.Since(start) > time.Second || strings.Contains(stdout+stderr, "synthetic-password") {
		t.Fatalf("acquisition ignored deadline/cleanup: %d %s %s signins=%d me=%d signouts=%d", code, stdout, stderr, signins.Load(), verifications.Load(), signouts.Load())
	}
}

func TestUncappedRootKeepsNormalStdinLifecycle(t *testing.T) {
	f, server := boundedTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[],"meta":{"total_count":0}}`)
	})
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture@example.invalid", "--password-stdin", "companies", "list", "--require-superuser")
	if code != 0 || f.count("POST /api/v2/auth/sign-in") != 1 || f.count("GET /api/v2/auth/me") != 3 || f.count("GET /api/v2/organization/companies") != 1 || f.count("POST /api/v2/auth/sign-out") != 1 {
		t.Fatalf("omitted cap changed lifecycle: %d %s %s", code, stdout, stderr)
	}
}

func TestBoundedRootFailureAndCancellationKeepCleanup(t *testing.T) {
	for _, mode := range []string{"failure", "cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f, server := boundedTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "failure" {
					w.WriteHeader(503)
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if mode == "cancel" {
					cancel()
				}
				<-r.Context().Done()
			})
			args := []string{"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "5", "companies", "list"}
			if mode == "deadline" {
				args = append(args, "--timeout=100ms")
			}
			start := time.Now()
			_, stderr, code := executeBounded(t, ctx, server.URL, strings.NewReader("synthetic-password\n"), args...)
			if code == 0 || f.count("GET /api/v2/organization/companies") != 1 || f.count("POST /api/v2/auth/sign-out") != 1 || time.Since(start) > time.Second {
				t.Fatalf("failed/canceled cleanup: %d %s", code, stderr)
			}
		})
	}
}
