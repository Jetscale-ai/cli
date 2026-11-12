package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegenerateHelpDescribesInPlaceOverwriteOffline(t *testing.T) {
	for _, args := range [][]string{
		{"recommendations", "--help"},
		{"recommendations", "regenerate", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root := newRootCmd()
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("help must work offline: %v", err)
			}
			help := strings.Join(strings.Fields(stdout.String()), " ")
			for _, text := range []string{"in place", "same ID/version"} {
				if !strings.Contains(help, text) {
					t.Errorf("help missing %q: %s", text, help)
				}
			}
			for _, text := range []string{"Request a new recommendation version", "Request a new version"} {
				if strings.Contains(help, text) {
					t.Errorf("help must not promise a new version: %s", help)
				}
			}
			if len(args) == 3 {
				for _, text := range []string{"Capture", "next successful run", "eligible evidence sources", "--exclude-source", "do not retry"} {
					if !strings.Contains(help, text) {
						t.Errorf("regeneration help missing %q: %s", text, help)
					}
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("help emitted diagnostics: %q", stderr.String())
			}
		})
	}
}

func TestRegenerateCanExcludeFocusForOneRequest(t *testing.T) {
	f, server := newFakeBackend(t)
	const recommendationID = "3f2c0a6e-0000-4000-8000-000000000000"
	const endpoint = "POST /api/v2/cloud/custom-recommendations/" + recommendationID + "/action/regenerate"
	f.on(endpoint, func(_ *http.Request, body []byte) (int, any) {
		if got, want := string(body), `{"excluded_source_keys":["focus-cost-attribution"]}`; got != want {
			t.Errorf("regeneration must exclude only the requested source: got %s, want %s", got, want)
		}
		return item(map[string]any{"id": recommendationID, "status": "processing"})
	})

	stdout, stderr, code := run(t, server, "-o", "json", "recommendations", "regenerate", recommendationID,
		"--exclude-source", "focus-cost-attribution", "-y")
	if code != 0 || decode(t, stdout).(map[string]any)["id"] != recommendationID {
		t.Fatalf("exclude FOCUS regeneration failed: %d %s %s", code, stdout, stderr)
	}
	if f.count(endpoint) != 1 {
		t.Fatalf("regeneration must be submitted exactly once, got %d", f.count(endpoint))
	}
}

func TestRegenerateSourceSelectionIsNotPersisted(t *testing.T) {
	f, server := newFakeBackend(t)
	const recommendationID = "3f2c0a6e-0000-4000-8000-000000000000"
	const endpoint = "POST /api/v2/cloud/custom-recommendations/" + recommendationID + "/action/regenerate"
	for _, tc := range []struct {
		name  string
		flags []string
		body  string
	}{
		{"without FOCUS", []string{"--exclude-source", "focus-cost-attribution"}, `{"excluded_source_keys":["focus-cost-attribution"]}`},
		{"default sources after exclusion", nil, `{}`},
		{"repeatable exclusions", []string{"--exclude-source", "focus-cost-attribution", "--exclude-source", "resource-inventory"}, `{"excluded_source_keys":["focus-cost-attribution","resource-inventory"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.on(endpoint, func(_ *http.Request, body []byte) (int, any) {
				if string(body) != tc.body {
					t.Errorf("source selection must match this request only: got %s, want %s", body, tc.body)
				}
				return item(map[string]any{"id": recommendationID, "version": 7})
			})
			args := append([]string{"-o", "json", "recommendations", "regenerate", recommendationID, "-y"}, tc.flags...)
			stdout, stderr, code := run(t, server, args...)
			if code != 0 {
				t.Fatalf("regeneration failed: %d %s %s", code, stdout, stderr)
			}
			rec := decode(t, stdout).(map[string]any)
			if rec["id"] != recommendationID || rec["version"] != float64(7) {
				t.Fatalf("regeneration must return the same recommendation ID and version: %s", stdout)
			}
		})
	}
	if f.count(endpoint) != 3 {
		t.Fatalf("each request must be submitted once, got %d", f.count(endpoint))
	}
}

func TestRegenerateExclusionStillRequiresConfirmation(t *testing.T) {
	f, server := newFakeBackend(t)
	stdout, _, code := run(t, server, "-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000",
		"--exclude-source", "focus-cost-attribution")
	if code != ExitForbidden || !strings.Contains(stdout, "confirmation_required") {
		t.Fatalf("excluding a source must not bypass confirmation: %d %s", code, stdout)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 0 {
		t.Fatalf("unconfirmed regeneration must not access the Backend: %v", f.requests)
	}
}

func TestRegenerateExclusionsHonorBackendErrorsWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   int
	}{
		{"unsupported source", http.StatusBadRequest, ExitFailure},
		{"superuser required", http.StatusForbidden, ExitForbidden},
		{"uncertain outcome", http.StatusBadGateway, ExitFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := newFakeBackend(t)
			const endpoint = "POST /api/v2/cloud/custom-recommendations/3f2c0a6e-0000-4000-8000-000000000000/action/regenerate"
			f.on(endpoint, func(*http.Request, []byte) (int, any) {
				return backendError(tc.status, "EXCLUSION_REJECTED")
			})
			stdout, _, code := run(t, server, "-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000",
				"--exclude-source", "focus-cost-attribution", "-y")
			if code != tc.code || decode(t, stdout).(map[string]any)["error"].(map[string]any)["transient"] != false {
				t.Fatalf("must preserve rejection or uncertainty without recommending retries: %d %s", code, stdout)
			}
			if f.count(endpoint) != 1 {
				t.Fatalf("a failed submission must not be retried or fall back to default sources: %d", f.count(endpoint))
			}
		})
	}
}

func TestRegenerateRejectsMalformedExclusionsBeforeAccess(t *testing.T) {
	tooMany := []string{}
	for i := 0; i < 33; i++ {
		tooMany = append(tooMany, "--exclude-source", strings.Repeat("x", i+1))
	}
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"empty", []string{"--exclude-source", ""}},
		{"blank", []string{"--exclude-source", "  "}},
		{"duplicate", []string{"--exclude-source", "focus-cost-attribution", "--exclude-source", "focus-cost-attribution"}},
		{"too many", tooMany},
		{"before stdin authentication", []string{"--exclude-source", "", "--login", "test@example.invalid", "--password-stdin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server := newFakeBackend(t)
			args := append([]string{"-o", "json", "recommendations", "regenerate", "3f2c0a6e-0000-4000-8000-000000000000", "-y"}, tc.flags...)
			stdout, stderr, code := run(t, server, args...)
			if code != ExitUsage {
				t.Fatalf("invalid exclusions must be a usage error: %d %s %s", code, stdout, stderr)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.requests) != 0 {
				t.Fatalf("invalid exclusions must not access the Backend: %v", f.requests)
			}
		})
	}
}

func TestRecommendationsListHelpIsOffline(t *testing.T) {
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"recommendations", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("help must work offline: %v", err)
	}
	help := stdout.String()
	for _, text := range []string{
		"--status",
		"--resource-type",
		"--limit",
		"--all-accounts",
		"DISCOVERED",
		"IN_PROGRESS",
		"COMPLETED",
		"DISCARDED",
		"POSTPONED",
		"STALE",
	} {
		if !strings.Contains(help, text) {
			t.Errorf("help missing %q:\n%s", text, help)
		}
	}
	if strings.Contains(help, "OPEN") {
		t.Errorf("help must not advertise an OPEN status alias:\n%s", help)
	}
	if stderr.Len() != 0 {
		t.Fatalf("help emitted diagnostics: %q", stderr.String())
	}
}

func TestRecommendationsListOmitsStatusWhenUnset(t *testing.T) {
	f, server, endpoint := recommendationListBackend(t, func(r *http.Request) (int, any) {
		for _, key := range []string{"status", "cloud_resource_type", "resource_type"} {
			if _, ok := r.URL.Query()[key]; ok {
				t.Errorf("omitted filters must not send %s: %s", key, r.URL.RawQuery)
			}
		}
		return list(map[string]any{"id": "rec-unfiltered", "title": "Resize", "status": "POSTPONED"})
	})
	stdout, stderr, code := run(t, server, "--company", "unstable-jetscale", "--account", testAccountID, "recommendations", "list")
	if code != 0 {
		t.Fatalf("unfiltered list failed: %d %s %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "POSTPONED") {
		t.Fatalf("omitted --status must not hide POSTPONED rows: %s", stdout)
	}
	assertAccountRecommendationList(t, f, endpoint)
}

func TestRecommendationsListPassesRepeatableFiltersOnAccountRoute(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flags        []string
		status       string
		resourceType string
	}{
		{
			name:   "status order",
			flags:  []string{"--status", "DISCOVERED", "--status", "IN_PROGRESS"},
			status: "DISCOVERED,IN_PROGRESS",
		},
		{
			name:   "status order reversed",
			flags:  []string{"--status", "IN_PROGRESS", "--status", "DISCOVERED"},
			status: "IN_PROGRESS,DISCOVERED",
		},
		{
			name:         "resource type",
			flags:        []string{"--resource-type", "AWS::EC2::Instance", "--resource-type", "AWS::S3::Bucket"},
			resourceType: "AWS::EC2::Instance,AWS::S3::Bucket",
		},
		{
			name:         "both filters",
			flags:        []string{"--status", "STALE", "--resource-type", "AWS::EC2::Instance"},
			status:       "STALE",
			resourceType: "AWS::EC2::Instance",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server, endpoint := recommendationListBackend(t, func(r *http.Request) (int, any) {
				if r.URL.Path != "/api/v2/cloud/cloud-accounts/"+testAccountID+"/custom-recommendations/latest" {
					t.Errorf("path: %s", r.URL.Path)
				}
				assertQuery(t, r, "status", tc.status)
				assertQuery(t, r, "cloud_resource_type", tc.resourceType)
				if _, ok := r.URL.Query()["resource_type"]; ok {
					t.Errorf("must not send resource_type: %s", r.URL.RawQuery)
				}
				return list(map[string]any{"id": "rec-filtered", "title": "Resize", "status": "DISCOVERED"})
			})
			args := append([]string{"--company", "unstable-jetscale", "--account", testAccountID}, tc.flags...)
			args = append(args, "recommendations", "list")
			stdout, stderr, code := run(t, server, args...)
			if code != 0 {
				t.Fatalf("filtered list failed: %d %s %s", code, stdout, stderr)
			}
			assertAccountRecommendationList(t, f, endpoint)
		})
	}
}

func TestRecommendationsListJSONIncludesFreshnessFields(t *testing.T) {
	const (
		freshID   = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		legacyID  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		versionID = "77777777-7777-4777-8777-777777777777"
		staleAt   = "2026-04-01T00:00:00Z"
	)
	_, server, _ := recommendationListBackend(t, func(*http.Request) (int, any) {
		return list(
			map[string]any{
				"id": freshID, "title": "Resize", "status": "DISCOVERED",
				"freshness_status": "CURRENT", "stale_at": staleAt, "resource_version_id": versionID,
			},
			map[string]any{
				"id": legacyID, "title": "Legacy", "status": "DISCOVERED",
				"freshness_status": nil, "stale_at": nil, "resource_version_id": nil,
			},
		)
	})
	stdout, stderr, code := run(t, server, "-o", "json", "--company", "unstable-jetscale", "--account", testAccountID, "recommendations", "list")
	if code != 0 {
		t.Fatalf("json list failed: %d %s %s", code, stdout, stderr)
	}
	rows := decode(t, stdout).([]any)
	if len(rows) != 2 {
		t.Fatalf("expected both rows: %s", stdout)
	}
	fresh := rows[0].(map[string]any)
	for key, want := range map[string]any{
		"freshness_status":    "CURRENT",
		"stale_at":            staleAt,
		"resource_version_id": versionID,
		"cloud_account_id":    testAccountID,
	} {
		if fresh[key] != want {
			t.Errorf("%s: got %#v, want %#v", key, fresh[key], want)
		}
	}
	legacy := rows[1].(map[string]any)
	for _, key := range []string{"freshness_status", "stale_at", "resource_version_id"} {
		value, ok := legacy[key]
		if !ok || value != nil {
			t.Errorf("legacy %s must be present and null: %#v", key, legacy[key])
		}
	}
	table, stderr, code := run(t, server, "--company", "unstable-jetscale", "--account", testAccountID, "recommendations", "list")
	if code != 0 {
		t.Fatalf("table list failed: %d %s %s", code, table, stderr)
	}
	for _, text := range []string{"TITLE", "STATUS", "ACCOUNT", "ID"} {
		if !strings.Contains(table, text) {
			t.Errorf("table missing %s: %s", text, table)
		}
	}
	for _, text := range []string{"FRESHNESS", "CURRENT", staleAt, versionID} {
		if strings.Contains(table, text) {
			t.Errorf("freshness stays out of the table, found %q: %s", text, table)
		}
	}
}

func TestRecommendationsListLimitCapsRows(t *testing.T) {
	_, server, _ := recommendationListBackend(t, func(*http.Request) (int, any) {
		return list(
			map[string]any{"id": "rec-1", "title": "One", "status": "DISCOVERED"},
			map[string]any{"id": "rec-2", "title": "Two", "status": "DISCOVERED"},
			map[string]any{"id": "rec-3", "title": "Three", "status": "DISCOVERED"},
		)
	})
	stdout, stderr, code := run(t, server, "-o", "json", "--company", "unstable-jetscale", "--account", testAccountID, "--limit", "2", "recommendations", "list")
	if code != 0 {
		t.Fatalf("limited list failed: %d %s %s", code, stdout, stderr)
	}
	rows := decode(t, stdout).([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"] != "rec-1" || rows[1].(map[string]any)["id"] != "rec-2" {
		t.Fatalf("--limit must keep the first rows only: %s", stdout)
	}
}

func recommendationListBackend(t *testing.T, respond func(*http.Request) (int, any)) (*fakeBackend, *httptest.Server, string) {
	t.Helper()
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withAccounts(map[string]any{
		"id":                  testAccountID,
		"name":                "Unstable",
		"provider_account_id": "402289717651",
	})
	endpoint := "GET /api/v2/cloud/cloud-accounts/" + testAccountID + "/custom-recommendations/latest"
	f.on(endpoint, func(r *http.Request, _ []byte) (int, any) {
		return respond(r)
	})
	return f, server, endpoint
}

func assertQuery(t *testing.T, r *http.Request, key, want string) {
	t.Helper()
	values, ok := r.URL.Query()[key]
	if want == "" {
		if ok {
			t.Errorf("omitted %s must be absent: %s", key, r.URL.RawQuery)
		}
		return
	}
	if !ok || len(values) != 1 || values[0] != want {
		t.Errorf("%s: got %q from %s, want %q", key, values, r.URL.RawQuery, want)
	}
}

func assertAccountRecommendationList(t *testing.T, f *fakeBackend, endpoint string) {
	t.Helper()
	if f.count(endpoint) != 1 {
		t.Fatalf("account list must be called once, got %d", f.count(endpoint))
	}
	if f.count("GET /api/v2/cloud/custom-recommendations/latest") != 0 {
		t.Fatal("list must stay on the per-account route")
	}
}
