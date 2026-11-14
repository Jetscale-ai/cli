package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func commandCapacityLicense(seats any) map[string]any {
	return map[string]any{
		"id": testUnitID, "company": testCompanyID, "pricing_plan": testPlanID, "status": "ACTIVE", "max_seats": seats,
		"valid_from": "2020-01-01T00:00:00Z", "valid_until": nil, "billing_interval": "YEARLY", "currency_code": "CAD",
		"price_per_month": 199, "price_per_year": 2388, "performance_fee_enabled": true, "performance_fee_rate_bps": 2500,
		"performance_fee_allowance_yearly_cents": 500000, "performance_fee_term_months": 12, "stripe_subscription_id": "synthetic-stripe",
		"max_accounts": 3, "max_business_units": 1, "max_granted_tokens": nil, "description": "synthetic-secret",
	}
}

func (f *fakeBackend) withCapacityLicense(row map[string]any) {
	f.withCompany()
	f.on("GET /api/v2/license-and-billing/licenses", func(r *http.Request, _ []byte) (int, any) {
		if r.URL.Query().Get("company") != "eq "+testCompanyID {
			f.t.Error("unscoped licenses")
		}
		return list(row)
	})
	f.on("GET /api/v2/license-and-billing/licenses/"+testUnitID, func(*http.Request, []byte) (int, any) { return item(row) })
}

func TestLicenseCapacityCommandGrowThenNoop(t *testing.T) {
	f, server := newFakeBackend(t)
	row := commandCapacityLicense(1)
	f.withCapacityLicense(row)
	f.on("PATCH /api/v2/license-and-billing/licenses/"+testUnitID, func(_ *http.Request, body []byte) (int, any) {
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if len(payload) != 1 || payload["max_seats"] != float64(2) {
			t.Errorf("patch is not quota-only: %s", body)
		}
		row["max_seats"] = 2
		return item(row)
	})
	for _, status := range []string{"updated", "noop"} {
		stdout, stderr, code := run(t, server, "licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2", "--yes", "-o", "json")
		result := decode(t, stdout).(map[string]any)
		if code != 0 || result["status"] != status || stderr != "" || result["details"].(map[string]any)["max_seats"] != float64(2) || strings.Contains(stdout, "synthetic-") {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
	}
	if f.count("PATCH /api/v2/license-and-billing/licenses/"+testUnitID) != 1 {
		t.Fatal("rerun patched twice")
	}
}

func TestLicenseCapacityCommandPlansAndConfirmation(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table", "quiet"} {
		for _, confirmed := range []bool{true, false} {
			t.Run(format+"/"+map[bool]string{true: "dry-run", false: "unconfirmed"}[confirmed], func(t *testing.T) {
				f, server := newFakeBackend(t)
				f.withCapacityLicense(commandCapacityLicense(1))
				args := []string{"licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2"}
				if format == "quiet" {
					args = append(args, "-q")
				} else {
					args = append(args, "-o", format)
				}
				wantCode := ExitForbidden
				if confirmed {
					args = append(args, "--dry-run")
					wantCode = 0
				}
				stdout, stderr, code := run(t, server, args...)
				if code != wantCode || strings.Contains(stdout+stderr, "synthetic-") || strings.Contains(stdout, "Would create") {
					t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
				}
				if format == "json" {
					result := decode(t, stdout).(map[string]any)
					if result["status"] != "planned" || result["details"].(map[string]any)["operation"] != "grow" {
						t.Fatalf("wrong plan %v", result)
					}
				}
				if format == "table" && !strings.Contains(stdout, "Would grow") {
					t.Fatalf("bad table: %s", stdout)
				}
				if format == "quiet" && stdout != testUnitID+"\n" {
					t.Fatalf("bad quiet: %q", stdout)
				}
				if f.count("PATCH /api/v2/license-and-billing/licenses/"+testUnitID) != 0 {
					t.Fatal("unconfirmed plan wrote")
				}
			})
		}
	}
}

func TestLicenseCapacityCommandUnlimitedNeedsNoConfirmation(t *testing.T) {
	f, server := newFakeBackend(t)
	f.withCapacityLicense(commandCapacityLicense(nil))
	stdout, stderr, code := run(t, server, "licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2", "-o", "json")
	result := decode(t, stdout).(map[string]any)
	if code != 0 || stderr != "" || result["status"] != "noop" || result["details"].(map[string]any)["unlimited"] != true {
		t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
	}
}

func TestLicenseCapacityCommandPreflightAndHelp(t *testing.T) {
	for _, extra := range [][]string{nil, {"--min-seats", "0"}, {"--min-seats", "-1"}, {"--min-seats", "1.5"}, {"--min-seats", "2147483648"}, {"--min-seats", "2", "--dry-run", "--yes"}} {
		f, server := newFakeBackend(t)
		stdout, stderr, code := run(t, server, append([]string{"licenses", "ensure-capacity", "-o", "json"}, extra...)...)
		if code != ExitUsage || len(f.requests) != 0 {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
	}
	f, server := newFakeBackend(t)
	stdout, stderr, code := run(t, server, "licenses", "ensure-capacity", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "--min-seats") || len(f.requests) != 0 {
		t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
	}
}

func TestLicenseCapacityPreflightDoesNotReadLoginStdin(t *testing.T) {
	_, server := newFakeBackend(t)
	root := newRootCmd()
	input := &flagsCountingInput{}
	root.SetIn(input)
	root.SetArgs([]string{"--api-url", server.URL, "licenses", "ensure-capacity", "--login", "fixture@example.invalid", "--password-stdin", "--min-seats", "0"})
	if err := root.Execute(); err == nil || input.reads != 0 {
		t.Fatalf("err=%v reads=%d", err, input.reads)
	}
}

func TestLicenseCapacityCommandPersistedThenServerError(t *testing.T) {
	for _, status := range []int{500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f, server := newFakeBackend(t)
			row := commandCapacityLicense(1)
			f.withCapacityLicense(row)
			f.on("PATCH /api/v2/license-and-billing/licenses/"+testUnitID, func(*http.Request, []byte) (int, any) {
				row["max_seats"] = 2
				return backendError(status, "INTERNAL_SERVER_ERROR")
			})
			stdout, stderr, code := run(t, server, "licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2", "--yes", "-o", "json")
			errBody := decode(t, stdout).(map[string]any)["error"].(map[string]any)
			if code != ExitFailure || errBody["reason"] != "outcome_uncertain" || errBody["transient"] != false || f.count("PATCH /api/v2/license-and-billing/licenses/"+testUnitID) != 1 || strings.Contains(stdout+stderr, "internal detail") {
				t.Fatalf("unsafe mutation outcome: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			stdout, stderr, code = run(t, server, "licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2", "--dry-run", "-o", "json")
			result := decode(t, stdout).(map[string]any)
			if code != 0 || result["status"] != "noop" || result["details"].(map[string]any)["max_seats"] != float64(2) || f.count("PATCH /api/v2/license-and-billing/licenses/"+testUnitID) != 1 {
				t.Fatalf("persisted capacity not readable without retry: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestLicenseCapacityCommandMissingMetadataAndUncertain(t *testing.T) {
	for _, mode := range []string{"missing-metadata", "uncertain"} {
		f, server := newFakeBackend(t)
		row := commandCapacityLicense(1)
		if mode == "missing-metadata" {
			delete(row, "max_seats")
		}
		f.withCapacityLicense(row)
		if mode == "uncertain" {
			f.on("PATCH /api/v2/license-and-billing/licenses/"+testUnitID, func(*http.Request, []byte) (int, any) { return 504, map[string]any{"secret": "synthetic-secret"} })
		}
		stdout, stderr, code := run(t, server, "licenses", "ensure-capacity", "--company", "unstable-jetscale", "--min-seats", "2", "--yes", "-o", "json")
		result := decode(t, stdout).(map[string]any)["error"].(map[string]any)
		want := "invalid_response"
		if mode == "uncertain" {
			want = "outcome_uncertain"
		}
		if code != ExitFailure || result["reason"] != want || strings.Contains(stdout+stderr, "synthetic-") {
			t.Fatalf("code=%d out=%s err=%s", code, stdout, stderr)
		}
	}
}
