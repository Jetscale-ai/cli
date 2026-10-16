package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
)

const (
	testCompanyID = "11111111-1111-4111-8111-111111111111"
	testUnitID    = "22222222-2222-4222-8222-222222222222"
	testOtherUnit = "33333333-3333-4333-8333-333333333333"
	testAccountID = "44444444-4444-4444-8444-444444444444"
	testPlanID    = "55555555-5555-4555-8555-555555555555"
	testUserID    = "66666666-6666-4666-8666-666666666666"
)

// fakeBackend answers by "METHOD path" and records every request.
type fakeBackend struct {
	t        *testing.T
	mu       sync.Mutex
	routes   map[string]func(*http.Request, []byte) (int, any)
	requests []string
}

func newFakeBackend(t *testing.T) (*fakeBackend, *httptest.Server) {
	t.Helper()
	t.Setenv(buildinfo.EnvVar("CONFIG_DIR"), t.TempDir())
	t.Setenv(buildinfo.EnvVar("TOKEN"), "offline-test-token")
	f := &fakeBackend{t: t, routes: map[string]func(*http.Request, []byte) (int, any){}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, key)
		handler := f.routes[key]
		f.mu.Unlock()
		if handler == nil {
			t.Errorf("unexpected request: %s", key)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		status, payload := handler(r, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)
	return f, server
}

func (f *fakeBackend) on(key string, handler func(*http.Request, []byte) (int, any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = handler
}

func (f *fakeBackend) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == key {
			n++
		}
	}
	return n
}

func list(items ...map[string]any) (int, any) {
	if items == nil {
		items = []map[string]any{}
	}
	return http.StatusOK, map[string]any{"data": items, "meta": map[string]any{"total_count": len(items), "count": len(items)}}
}

func item(v map[string]any) (int, any) { return http.StatusOK, map[string]any{"data": v} }

func backendError(status int, name string) (int, any) {
	return status, map[string]any{"detail": map[string]any{
		"meta": map[string]any{"security": map[string]any{"required_permission": "SUPERUSER_REQUIRED", "granted": false}},
		"data": map[string]any{"error": map[string]any{"api_error_name": name, "causes": []string{"internal detail that must not leak"}}},
	}}
}

var unstableCompany = map[string]any{"id": testCompanyID, "name": "Unstable Jetscale", "slug": "unstable-jetscale", "domain_emails": []string{"jetscale.ai"}, "is_root": false}

func (f *fakeBackend) withCompany() {
	f.on("GET /api/v2/organization/companies", func(r *http.Request, _ []byte) (int, any) {
		if q := r.URL.Query().Get("slug"); q != "" && q != "eq unstable-jetscale" {
			return list()
		}
		if q := r.URL.Query().Get("name"); q != "" && q != "eq Unstable Jetscale" {
			return list()
		}
		return list(unstableCompany)
	})
}

func (f *fakeBackend) withUnit() {
	f.on("GET /api/v2/organization/business-units", func(*http.Request, []byte) (int, any) {
		return list(map[string]any{"id": testUnitID, "name": "Unstable Jetscale Clouds", "slug": "unstable-jetscale-clouds", "company": testCompanyID})
	})
}

func (f *fakeBackend) withAccounts(accounts ...map[string]any) {
	f.on("GET /api/v2/cloud/cloud-accounts", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("company_id") != testCompanyID {
			f.t.Errorf("cloud accounts must be company-scoped: %s", r.URL.RawQuery)
		}
		return list(accounts...)
	})
}

// run executes the CLI like main does and returns stdout, stderr and the exit code.
func run(t *testing.T, server *httptest.Server, args ...string) (string, string, int) {
	t.Helper()
	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--api-url", server.URL}, args...))
	cmd, err := root.ExecuteC()
	code := finish(root, cmd, err, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func decode(t *testing.T, raw string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, raw)
	}
	return v
}

func privateCredentialFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResourceUsageErrorsExitTwoWithJSONError(t *testing.T) {
	_, server := newFakeBackend(t)
	stdout, _, code := run(t, server, "-o", "json", "companies", "list", "--no-such-flag")
	if code != ExitUsage {
		t.Fatalf("unknown flag must exit %d, got %d", ExitUsage, code)
	}
	if body := decode(t, stdout).(map[string]any)["error"].(map[string]any); body["reason"] != "usage" {
		t.Fatalf("unexpected error body: %v", body)
	}
}

func TestCompanyShowMissingExitsThree(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) { return list() })
	stdout, _, code := run(t, server, "-o", "json", "companies", "get", "absent-co")
	if code != ExitNotFound || decode(t, stdout).(map[string]any)["error"].(map[string]any)["reason"] != "not_found" {
		t.Fatalf("missing company must exit %d: %d %s", ExitNotFound, code, stdout)
	}
}

func TestEmptyListsAreExplicit(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) { return list() })
	stdout, _, code := run(t, server, "-o", "json", "companies", "list")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("empty JSON list must be [], got %d %q", code, stdout)
	}
	if stdout, _, _ = run(t, server, "companies", "list"); !strings.Contains(stdout, "No companies found.") {
		t.Fatalf("empty table must say so, got %q", stdout)
	}
}

func TestCompanyCreateIsNoopWhenMatchingAndDriftWhenNot(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	stdout, _, code := run(t, server, "-o", "json", "companies", "create", "--name", "Unstable Jetscale", "--slug", "unstable-jetscale", "--domain-email", "jetscale.ai")
	if code != 0 || decode(t, stdout).(map[string]any)["status"] != "noop" {
		t.Fatalf("matching company must be noop: %d %s", code, stdout)
	}
	stdout, _, code = run(t, server, "-o", "json", "companies", "create", "--name", "Renamed", "--slug", "unstable-jetscale", "--domain-email", "jetscale.ai")
	result := decode(t, stdout).(map[string]any)
	if code != ExitDrift || result["status"] != "drift" || result["drift"].([]any)[0] != "name" {
		t.Fatalf("differing company must report drift and exit %d: %d %s", ExitDrift, code, stdout)
	}
	if f.count("POST /api/v2/organization/companies")+f.count("POST /api/v2/organization/action/setup-company") != 0 {
		t.Fatal("an existing company must never be written")
	}
}

func TestCompanyCreateWithLicenseUsesAtomicSetup(t *testing.T) {
	f, server := newFakeBackend(t)
	created := false
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) {
		if created {
			return list(unstableCompany)
		}
		return list()
	})
	f.on("GET /api/v2/license-and-billing/pricing-plans", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("internal_name") != "eq ESSENTIAL_ANNUAL" {
			return list()
		}
		return list(map[string]any{"id": testPlanID, "internal_name": "ESSENTIAL_ANNUAL", "is_active": true})
	})
	f.on("POST /api/v2/organization/action/setup-company", func(_ *http.Request, body []byte) (int, any) {
		var payload struct {
			Company map[string]any `json:"company"`
			License map[string]any `json:"license"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.License["pricing_plan_id"] != testPlanID || payload.Company["slug"] != "unstable-jetscale" || payload.License["billing_interval"] != "YEARLY" {
			t.Errorf("unexpected setup payload: %s", body)
		}
		created = true
		return item(map[string]any{"id": testCompanyID})
	})
	stdout, _, code := run(t, server, "-o", "json", "companies", "create", "--name", "Unstable Jetscale", "--slug", "unstable-jetscale", "--domain-email", "jetscale.ai", "--license-plan", "ESSENTIAL_ANNUAL", "--license-name", "Test License", "--billing-interval", "yearly")
	result := decode(t, stdout).(map[string]any)
	if code != 0 || result["status"] != "created" || result["id"] != testCompanyID {
		t.Fatalf("absent company must be created atomically: %d %s", code, stdout)
	}
}

func TestBusinessUnitCreateDryRunNeverWrites(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.on("GET /api/v2/organization/business-units", func(*http.Request, []byte) (int, any) { return list() })
	stdout, _, code := run(t, server, "-o", "json", "business-units", "create", "--company", "unstable-jetscale", "--name", "Unstable Jetscale Clouds", "--slug", "unstable-jetscale-clouds", "--dry-run")
	if code != 0 || decode(t, stdout).(map[string]any)["status"] != "planned" {
		t.Fatalf("dry run must plan: %d %s", code, stdout)
	}
	if f.count("POST /api/v2/organization/business-units") != 0 {
		t.Fatal("dry run wrote a business unit")
	}
}

func TestFeatureFlagSetNeedsConfirmationForCascadingScopes(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	value := false
	f.on("GET /api/v2/system/feature-flags/company/by-key/CLOUD.FOCUS", func(*http.Request, []byte) (int, any) {
		return item(map[string]any{"id": "77777777-7777-4777-8777-777777777777", "key": "CLOUD.FOCUS", "value": value, "is_secret": false})
	})
	f.on("PATCH /api/v2/system/feature-flags/company/action/set/CLOUD.FOCUS", func(r *http.Request, body []byte) (int, any) {
		if r.URL.Query().Get("company_id") != testCompanyID || strings.TrimSpace(string(body)) != `{"value":true}` {
			t.Errorf("unexpected set request: %s %s", r.URL.RawQuery, body)
		}
		value = true
		return item(map[string]any{"key": "CLOUD.FOCUS", "value": true})
	})
	const patch = "PATCH /api/v2/system/feature-flags/company/action/set/CLOUD.FOCUS"
	stdout, _, code := run(t, server, "-o", "json", "feature-flags", "set", "CLOUD.FOCUS=true", "--company", "unstable-jetscale")
	if code != ExitForbidden || f.count(patch) != 0 {
		t.Fatalf("unconfirmed company write must plan and exit %d without writing: %d %s", ExitForbidden, code, stdout)
	}
	if results := decode(t, stdout).([]any); results[0].(map[string]any)["status"] != "planned" {
		t.Fatalf("plan must be printed: %s", stdout)
	}
	stdout, _, code = run(t, server, "-o", "json", "feature-flags", "set", "CLOUD.FOCUS=true", "--company", "unstable-jetscale", "--yes")
	if code != 0 || decode(t, stdout).([]any)[0].(map[string]any)["status"] != "updated" {
		t.Fatalf("confirmed write must update and verify: %d %s", code, stdout)
	}
	stdout, _, code = run(t, server, "-o", "json", "feature-flags", "set", "CLOUD.FOCUS=true", "--company", "unstable-jetscale", "--yes")
	if code != 0 || decode(t, stdout).([]any)[0].(map[string]any)["status"] != "noop" || f.count(patch) != 1 {
		t.Fatalf("satisfied flag must be noop: %d %s", code, stdout)
	}
}

func TestFeatureFlagSetCreatesAbsentRootRow(t *testing.T) {
	f, server := newFakeBackend(t)
	exists := false
	f.on("GET /api/v2/system/feature-flags/root/by-key/AGENTS.EXPLORER", func(*http.Request, []byte) (int, any) {
		if !exists {
			return backendError(http.StatusNotFound, "NOT_FOUND")
		}
		return item(map[string]any{"id": "88888888-8888-4888-8888-888888888888", "key": "AGENTS.EXPLORER", "value": true})
	})
	f.on("POST /api/v2/system/feature-flags/root", func(_ *http.Request, body []byte) (int, any) {
		if !strings.Contains(string(body), `"key":"AGENTS.EXPLORER"`) {
			t.Errorf("create must send the key: %s", body)
		}
		exists = true
		return item(map[string]any{"key": "AGENTS.EXPLORER", "value": true})
	})
	stdout, _, code := run(t, server, "-o", "json", "feature-flags", "set", "AGENTS.EXPLORER=true", "--root", "--yes")
	if code != 0 || decode(t, stdout).([]any)[0].(map[string]any)["status"] != "created" {
		t.Fatalf("absent root row must be created: %d %s", code, stdout)
	}
}

func TestRequireSuperuserRefusesOrdinaryUsers(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/auth/me", func(*http.Request, []byte) (int, any) {
		return item(map[string]any{"user": map[string]any{"id": testUserID, "is_superuser": false}})
	})
	stdout, _, code := run(t, server, "-o", "json", "companies", "list", "--require-superuser")
	if code != ExitForbidden || !strings.Contains(stdout, "visibility_unproved") {
		t.Fatalf("non-superuser must be refused: %d %s", code, stdout)
	}
	if f.count("GET /api/v2/organization/companies") != 0 {
		t.Fatal("refused command still read companies")
	}
}

func TestBackendErrorsCarryNameNotCauses(t *testing.T) {
	f, server := newFakeBackend(t)
	f.on("GET /api/v2/organization/companies", func(*http.Request, []byte) (int, any) {
		return backendError(http.StatusForbidden, "GENERIC_PERMISSION_DENIED")
	})
	stdout, stderr, code := run(t, server, "-o", "json", "companies", "list")
	if code != ExitForbidden || !strings.Contains(stdout, "GENERIC_PERMISSION_DENIED") || !strings.Contains(stdout, "SUPERUSER_REQUIRED") {
		t.Fatalf("forbidden must exit %d with the error name and permission: %d %s", ExitForbidden, code, stdout)
	}
	if strings.Contains(stdout+stderr, "internal detail") {
		t.Fatal("backend causes leaked into output")
	}
}

func TestCloudAccountLinkCreatesAndNeverEchoesSecrets(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withUnit()
	linked := []map[string]any{}
	f.on("GET /api/v2/cloud/cloud-accounts", func(*http.Request, []byte) (int, any) { return list(linked...) })
	f.on("POST /api/v2/cloud/link-cloud-account", func(_ *http.Request, body []byte) (int, any) {
		var payload struct {
			Account     map[string]any `json:"cloud_account"`
			Integration struct {
				Data map[string]any `json:"integration_data"`
			} `json:"cloud_account_integration"`
		}
		_ = json.Unmarshal(body, &payload)
		if payload.Integration.Data["CLIENT_AWS_ROLE_ARN"] != "arn:aws:iam::402289717651:role/JetscaleUnstable" || payload.Integration.Data["CLIENT_AWS_ROLE_EXTERNAL_ID"] != "s3cr3t-external" || payload.Account["provider_account_id"] != "402289717651" || payload.Account["business_unit_id"] != testUnitID {
			t.Errorf("unexpected link payload: %s", body)
		}
		linked = append(linked, map[string]any{"id": testAccountID, "name": "Unstable AWS", "cloud_provider_type": "AWS", "provider_account_id": "402289717651", "business_unit": testUnitID})
		return item(map[string]any{"id": testAccountID})
	})
	credential := privateCredentialFile(t, `{"external_id":"s3cr3t-external"}`)
	link := []string{"-o", "json", "accounts", "link", "--company", "unstable-jetscale", "--business-unit", "unstable-jetscale-clouds", "--name", "Unstable AWS", "--provider", "aws", "--role-arn", "arn:aws:iam::402289717651:role/JetscaleUnstable"}
	stdout, stderr, code := run(t, server, append(link, "--credential-file", credential)...)
	if code != 0 || decode(t, stdout).(map[string]any)["status"] != "created" {
		t.Fatalf("absent account must be linked: %d %s %s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "s3cr3t") {
		t.Fatal("secret echoed in output")
	}
	stdout, _, code = run(t, server, link...)
	result := decode(t, stdout).(map[string]any)
	if code != 0 || result["status"] != "noop" || result["details"].(map[string]any)["credentials_compared"] != false {
		t.Fatalf("linked account must be noop with credentials not compared: %d %s", code, stdout)
	}
	if f.count("POST /api/v2/cloud/link-cloud-account") != 1 {
		t.Fatal("an existing link must never be resent")
	}
}

func TestCloudAccountLinkedElsewhereIsConflict(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withUnit()
	f.withAccounts(map[string]any{"id": testAccountID, "name": "Unstable GCP", "cloud_provider_type": "GCP", "provider_account_id": "jetscale-ai", "business_unit": testOtherUnit})
	stdout, _, code := run(t, server, "-o", "json", "accounts", "link", "--company", "unstable-jetscale", "--business-unit", "unstable-jetscale-clouds", "--name", "Unstable GCP", "--provider", "gcp", "--project-id", "jetscale-ai", "--workload-identity-provider", "projects/1/locations/global/workloadIdentityPools/p/providers/q", "--service-account-email", "sa@jetscale-ai.iam.gserviceaccount.com")
	if code != ExitConflict || !strings.Contains(stdout, "linked_to_another_business_unit") {
		t.Fatalf("account in another business unit must conflict: %d %s", code, stdout)
	}
}

func TestCredentialFileRejectsPublicFilesAndUnknownKeys(t *testing.T) {
	_, server := newFakeBackend(t)
	public := filepath.Join(t.TempDir(), "public.json")
	if err := os.WriteFile(public, []byte(`{"external_id":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"accounts", "link", "--company", "c", "--business-unit", "b", "--name", "n", "--provider", "aws", "--role-arn", "arn:aws:iam::402289717651:role/R"}
	if _, _, code := run(t, server, append(args, "--credential-file", public)...); code != ExitUsage {
		t.Fatalf("group/world-readable credential file must be refused, got %d", code)
	}
	unknown := privateCredentialFile(t, `{"client_secret":"x"}`)
	if _, _, code := run(t, server, append(args, "--credential-file", unknown)...); code != ExitUsage {
		t.Fatalf("keys foreign to the provider must be refused, got %d", code)
	}
}

func TestCloudAccountsWaitEndsOnReadyAndFailsOnFailed(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withAccounts(map[string]any{"id": testAccountID, "name": "Unstable AWS", "cloud_provider_type": "AWS", "provider_account_id": "402289717651", "business_unit": testUnitID})
	f.on("GET /api/v2/cloud/cloud-accounts/"+testAccountID+"/freshness-status", func(*http.Request, []byte) (int, any) {
		return item(map[string]any{"domains": []map[string]any{{"domain": "resources", "state": "ready"}, {"domain": "cost_focus", "state": "failed"}}})
	})
	stdout, _, code := run(t, server, "-o", "json", "accounts", "wait", "402289717651", "--company", "unstable-jetscale", "--for", "resources")
	if code != 0 || decode(t, stdout).([]any)[0].(map[string]any)["state"] != "ready" {
		t.Fatalf("ready domain must end the wait: %d %s", code, stdout)
	}
	stdout, _, code = run(t, server, "-o", "json", "accounts", "wait", "402289717651", "--company", "unstable-jetscale", "--for", "cost_focus")
	if code != ExitFailure || len(decode(t, stdout).([]any)) != 1 {
		t.Fatalf("failed domain must exit %d with states printed: %d %s", ExitFailure, code, stdout)
	}
}

func TestCostSourceCreateReportsDriftWithoutWriting(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCompany()
	f.withAccounts(map[string]any{"id": testAccountID, "name": "Unstable AWS", "cloud_provider_type": "AWS", "provider_account_id": "402289717651", "business_unit": testUnitID})
	f.on("GET /api/v2/cloud/cost-data-sources", func(*http.Request, []byte) (int, any) {
		return list(map[string]any{"id": "99999999-9999-4999-8999-999999999999", "name": "Unstable AWS FOCUS", "source_kind": "focus_s3", "provider_scope_id": "081373342681",
			"coverage":        []map[string]any{{"provider_account_id": "402289717651"}},
			"provider_config": map[string]any{"bucket": "old-bucket", "prefix": "focus/", "external_id": "never-printed"}})
	})
	stdout, stderr, code := run(t, server, "-o", "json", "cost-sources", "create", "--company", "unstable-jetscale", "--provider", "aws", "--name", "Unstable AWS FOCUS", "--provider-scope-id", "081373342681", "--cover", "402289717651", "--bucket", "new-bucket", "--prefix", "focus/")
	result := decode(t, stdout).(map[string]any)
	if code != ExitDrift || result["drift"].([]any)[0] != "provider_config.bucket" {
		t.Fatalf("differing source must drift: %d %s", code, stdout)
	}
	if strings.Contains(stdout+stderr, "never-printed") {
		t.Fatal("provider_config secret leaked")
	}
	if f.count("POST /api/v2/cloud/cost-data-sources") != 0 {
		t.Fatal("drifted source must not be written")
	}
}
