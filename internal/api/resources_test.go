package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Jetscale-ai/cli/internal/auth"
)

func TestResourceWriteErrorClassification(t *testing.T) {
	for _, status := range []int{400, 403, 409, 429, 500, 501, 502, 503, 504, 599, 600} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			do := func(context.Context) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"detail":{"data":{"error":{"causes":["synthetic-secret"]}}}}`))}, nil
			}
			client := &Client{}
			_, err := client.write(context.Background(), do)
			if status >= 500 {
				if !errors.Is(err, ErrUncertain) {
					t.Fatalf("server error must be uncertain: %v", err)
				}
			} else {
				var rejected *APIError
				if !errors.As(err, &rejected) || rejected.Status != status {
					t.Fatalf("expected definitive rejection: %v", err)
				}
			}
			if calls != 1 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("unsafe error/retry: err=%v calls=%d", err, calls)
			}

			_, err = client.read(context.Background(), do)
			var rejected *APIError
			if !errors.As(err, &rejected) || rejected.Status != status || calls != 2 {
				t.Fatalf("read classification changed: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestResourceClonesShareHTTPRequestBudget(t *testing.T) {
	var calls, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/api/v2/auth/sign-in":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		case "/api/v2/auth/me":
			_, _ = io.WriteString(w, `{"data":{"user":{"email":"fixture@example.invalid"}}}`)
		case "/write":
			writes.Add(1)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer server.Close()
	session, err := auth.LoginSessionBoundedContext(context.Background(), server.URL, "fixture", "synthetic-password", "", 5)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewSessionClient(server.URL, session.HTTPClient).Resources()
	if err != nil {
		t.Fatal(err)
	}
	clone, err := client.Resources()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*Client{client, clone} {
		if _, err := c.Raw(context.Background(), "GET", "/read", nil); err != nil {
			t.Fatal(err)
		}
	}
	blocked := func(ctx context.Context) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/write", nil)
		return session.HTTPClient.Do(req)
	}
	if _, err := clone.write(context.Background(), blocked); !errors.Is(err, auth.ErrHTTPRequestBound) || errors.Is(err, ErrUncertain) {
		t.Fatalf("blocked write classified uncertain: %v", err)
	}
	if _, err := clone.read(context.Background(), blocked); !errors.Is(err, auth.ErrHTTPRequestBound) || errors.Is(err, ErrTransport) {
		t.Fatalf("blocked read classified transient: %v", err)
	}
	if _, err := client.Raw(context.Background(), "POST", "/write", nil); !errors.Is(err, auth.ErrHTTPRequestBound) || errors.Is(err, ErrUncertain) {
		t.Fatalf("blocked raw write classified uncertain: %v", err)
	}
	if err := session.SignOutContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 || writes.Load() != 0 {
		t.Fatalf("calls=%d writes=%d", calls.Load(), writes.Load())
	}
}

func TestResourceClonesConcurrentBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/api/v2/auth/sign-in" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "synthetic-cookie", Path: "/"})
		}
		_, _ = io.WriteString(w, `{"data":{"user":{"email":"fixture@example.invalid"}}}`)
	}))
	defer server.Close()
	session, err := auth.LoginSessionBoundedContext(context.Background(), server.URL, "fixture", "synthetic-password", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewSessionClient(server.URL, session.HTTPClient).Resources()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var successes atomic.Int32
	for range 20 {
		wg.Go(func() {
			clone, cloneErr := client.Resources()
			if cloneErr != nil {
				t.Error(cloneErr)
				return
			}
			_, err := clone.Raw(context.Background(), "GET", "/read", nil)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, auth.ErrHTTPRequestBound) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := session.SignOutContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 10 || successes.Load() != 7 {
		t.Fatalf("calls=%d successful reads=%d", calls.Load(), successes.Load())
	}
}

func TestCollectionPageContextDefaultAndNarrowing(t *testing.T) {
	for _, pages := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(pages), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				offset, _ := strconv.Atoi(r.URL.Query().Get("api_offset"))
				count := 100
				if offset != 0 {
					count = 1
				}
				items := make([]Record, count)
				for i := range items {
					items[i] = Record{"id": fmt.Sprint(offset + i)}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "meta": map[string]any{"total_count": 101}})
			}))
			defer server.Close()
			client, err := NewSessionClient(server.URL, server.Client()).Resources()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if pages != 0 {
				ctx, err = WithMaxPages(ctx, pages)
				if err != nil {
					t.Fatal(err)
				}
			}
			items, err := client.ListCompanies(ctx, nil)
			if pages == 1 {
				if !errors.Is(err, ErrPageBound) || len(items) != 0 || calls.Load() != 1 {
					t.Fatalf("partial answer: %v %d %d", err, len(items), calls.Load())
				}
			} else if err != nil || len(items) != 101 || calls.Load() != 2 {
				t.Fatalf("normal pagination changed: %v %d %d", err, len(items), calls.Load())
			}
			if pages == 0 && MaxPages(ctx) != 20 {
				t.Fatal("default page limit changed")
			}
		})
	}
}
