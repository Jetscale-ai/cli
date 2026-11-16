package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Jetscale-ai/cli/internal/api"
)

func TestCompaniesIDsBoundedProjection(t *testing.T) {
	f, server := boundedTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/api/v2/organization/companies" ||
			len(q) != 4 || q.Get("api_fields") != "id" || q.Get("api_limit") != "100" || q.Get("api_offset") != "0" || q.Get("api_sort") != "id" {
			t.Errorf("unexpected company ID request: %s %s", r.Method, r.URL)
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"`+testUnitID+`","contact_email":"foreign-metadata@example.invalid"},{"id":"`+testCompanyID+`","name":"foreign-metadata"}],"meta":{"total_count":2}}`)
	})
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "6", "companies", "ids", "--require-superuser", "--max-pages=1")
	var got []api.Record
	want := []api.Record{{"id": testCompanyID}, {"id": testUnitID}}
	if code != 0 || json.Unmarshal([]byte(stdout), &got) != nil || !reflect.DeepEqual(got, want) ||
		!strings.Contains(stderr, "Temporary session signed out; no credentials saved.") || strings.Contains(stderr, "foreign-metadata") {
		t.Fatalf("company IDs must be sorted ID-only JSON: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	f.mu.Lock()
	requests := len(f.requests)
	f.mu.Unlock()
	if requests != 6 || f.count("GET /api/v2/organization/companies") != 1 || f.count("POST /api/v2/auth/sign-out") != 1 {
		t.Fatalf("expected six-request lifecycle with cleanup, got %d requests", requests)
	}
}

func TestCompaniesIDsInvalidCollectionsPrintNoPartialData(t *testing.T) {
	row := `{"id":"` + testCompanyID + `"}`
	for _, test := range []struct {
		name string
		body string
	}{
		{"missing_total", `{"data":[` + row + `],"meta":{}}`},
		{"incomplete", `{"data":[` + row + `],"meta":{"total_count":2}}`},
		{"duplicate", `{"data":[` + row + `,` + row + `],"meta":{"total_count":2}}`},
		{"invalid_id", `{"data":[{"id":"foreign-metadata"}],"meta":{"total_count":1}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, server := boundedTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, test.body)
			})
			stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
				"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "6", "companies", "ids", "--require-superuser", "--max-pages=1")
			var failure map[string]any
			if code != ExitFailure || json.Unmarshal([]byte(stdout), &failure) != nil || failure["error"] == nil ||
				strings.Contains(stdout, testCompanyID) || strings.Contains(stdout+stderr, "foreign-metadata") ||
				f.count("GET /api/v2/organization/companies") != 1 || f.count("POST /api/v2/auth/sign-out") != 1 {
				t.Fatalf("invalid collection leaked data or skipped cleanup: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestCompaniesIDsExhaustionReservesCleanup(t *testing.T) {
	f, server := boundedTestServer(t, func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("exhausted command dispatched resource read: %s", r.URL)
	})
	stdout, stderr, code := executeBounded(t, context.Background(), server.URL, strings.NewReader("synthetic-password\n"),
		"--login", "fixture@example.invalid", "--password-stdin", "--max-http-requests", "5", "companies", "ids", "--require-superuser", "--max-pages=1")
	if code != ExitFailure || !strings.Contains(stdout, `"reason": "http_request_bound"`) ||
		f.count("GET /api/v2/organization/companies") != 0 || f.count("POST /api/v2/auth/sign-out") != 1 {
		t.Fatalf("request ceiling or reserved cleanup changed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestCompaniesIDsEmptyJSON(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(_ *http.Request, _ []byte) (int, any) {
		return list()
	})
	stdout, stderr, code := run(t, server, "companies", "ids", "--max-pages=1", "-o", "json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" || stderr != "" {
		t.Fatalf("empty IDs must be an array: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}
