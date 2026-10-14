package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

func basicsNamedManifest(t *testing.T) BasicsManifest {
	t.Helper()
	// Decode the wrapper's wire shape, including a null optional UUID pin.
	data := `{
		"company":{"name":"Test Company","slug":"test-company","domain_emails":["example.test"]},
		"pricing_plan":{"internal_name":"ESSENTIAL_ANNUAL"},
		"license":{"pricing_plan_id":null,"name":"Unstable Jetscale Test License",
			"description":"Internal simulated customer license for three registered cloud accounts under the Essential Annual plan. No payment method, Stripe subscription or commercial commitment is being configured.",
			"billing_interval":"YEARLY"}
	}`
	var m BasicsManifest
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("named manifest rejected: %v", err)
	}
	return m
}

func newBasicsNamedFixture() *basicsFixture {
	f := newBasicsFixture()
	p := f.plans[0]
	p["internal_name"], p["display_name"] = "ESSENTIAL_ANNUAL", "Essential (Annual Plan)"
	p["default_billing_interval"], p["currency_code"] = "YEARLY", "CAD"
	p["price_per_month"], p["price_per_year"] = 199, 2388
	p["performance_fee_enabled"], p["performance_fee_rate_bps"] = true, 2500
	p["performance_fee_allowance_yearly_cents"], p["performance_fee_term_months"] = 500000, 12
	l := f.licenses[0]
	l["name"] = "Unstable Jetscale Test License"
	l["description"] = "Internal simulated customer license for three registered cloud accounts under the Essential Annual plan. No payment method, Stripe subscription or commercial commitment is being configured."
	l["currency_code"] = "CAD"
	l["price_per_month"], l["price_per_year"] = nil, nil
	l["performance_fee_allowance_yearly_cents"], l["performance_fee_term_months"] = nil, nil
	return f
}

func TestPrepareBasicsNamedSetupUsesRecreatedCatalogUUID(t *testing.T) {
	for _, planID := range []string{"10000000-0000-0000-0000-000000000002", "10000000-0000-0000-0000-000000000003"} {
		t.Run(planID, func(t *testing.T) {
			m := basicsNamedManifest(t)
			before, _ := json.Marshal(m)
			f := newBasicsNamedFixture()
			f.plans[0]["id"] = planID
			f.companies, f.licenses, f.units = nil, nil, nil
			client, closeServer := f.serve(t)
			defer closeServer()
			for run := range 2 {
				r, err := client.PrepareBasics(context.Background(), m, true)
				if err != nil || !r.Ready || r.PricingPlanID != planID || r.CompanyID != basicsCompanyID || r.LicenseID != basicsLicenseID || !r.Gates.License || len(f.writes) != 1 {
					t.Fatalf("run %d did not use current catalog: %+v err=%v writes=%v", run, r, err, f.writes)
				}
			}
			if len(f.setups) != 1 || f.writes[0] != "POST /api/v2/organization/action/setup-company" {
				t.Fatalf("expected exactly one atomic setup: %+v", f.writes)
			}
			body := f.setups[0].License
			if body.PricingPlanId != uuid.MustParse(planID) || body.Name == nil || *body.Name != "Unstable Jetscale Test License" || body.Description == nil || *body.Description != "Internal simulated customer license for three registered cloud accounts under the Essential Annual plan. No payment method, Stripe subscription or commercial commitment is being configured." || body.BillingInterval == nil || *body.BillingInterval != "YEARLY" {
				t.Fatalf("incorrect resolved setup metadata: %+v", body)
			}
			after, _ := json.Marshal(m)
			if string(before) != string(after) || m.License.PricingPlanId != uuid.Nil {
				t.Fatal("resolution changed the supplied manifest instead of only the setup request")
			}
		})
	}
}

func TestPrepareBasicsNamedManifestBoundary(t *testing.T) {
	for _, name := range []string{"", "ESSENTIAL_MONTHLY", "essential_annual", "ESSENTIAL_ANNUAL ", " ESSENTIAL_ANNUAL", "ESSENTIAL_ANNUAL or id ne 0", "synthetic-unapproved-selector"} {
		t.Run(name, func(t *testing.T) {
			m := basicsNamedManifest(t)
			m.PricingPlan.InternalName = name
			f := newBasicsNamedFixture()
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if ValidateBasicsManifest(m) == nil || err != nil || len(r.Blockers) == 0 || len(f.reads)+len(f.writes) != 0 {
				t.Fatalf("unsupported selector escaped preflight: %+v err=%v reads=%v writes=%v", r, err, f.reads, f.writes)
			}
			if strings.Contains(fmt.Sprint(r), "synthetic-unapproved-selector") {
				t.Fatal("unapproved selector leaked into result")
			}
		})
	}
	for _, field := range []string{"name", "description", "billing_interval"} {
		for _, absent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/absent=%v", field, absent), func(t *testing.T) {
				m := basicsNamedManifest(t)
				wrong := "Other"
				monthly := generated.BillingInterval("MONTHLY")
				switch field {
				case "name":
					m.License.Name = &wrong
					if absent {
						m.License.Name = nil
					}
				case "description":
					m.License.Description = &wrong
					if absent {
						m.License.Description = nil
					}
				case "billing_interval":
					m.License.BillingInterval = &monthly
					if absent {
						m.License.BillingInterval = nil
					}
				}
				f := newBasicsNamedFixture()
				client, closeServer := f.serve(t)
				defer closeServer()
				r, err := client.PrepareBasics(context.Background(), m, true)
				if ValidateBasicsManifest(m) == nil || err != nil || len(r.Blockers) == 0 || len(f.reads)+len(f.writes) != 0 {
					t.Fatalf("unapproved metadata escaped preflight: %+v err=%v", r, err)
				}
			})
		}
	}
	m := basicsNamedManifest(t)
	if ValidateBasicsManifest(m) != nil {
		t.Fatal("supported unpinned manifest failed offline validation")
	}
	m.License.PricingPlanId = uuid.MustParse("10000000-0000-0000-0000-000000000001")
	if ValidateBasicsManifest(m) != nil {
		t.Fatal("supported pinned manifest failed offline validation")
	}
	data, _ := json.Marshal(m)
	data = []byte(strings.Replace(string(data), `"internal_name":"ESSENTIAL_ANNUAL"`, `"internal_name":"ESSENTIAL_ANNUAL","unexpected":true`, 1))
	if json.Unmarshal(data, &m) == nil {
		t.Fatal("unknown selector field was accepted")
	}
}

func TestPrepareBasicsNamedPin(t *testing.T) {
	for _, match := range []bool{false, true} {
		t.Run(fmt.Sprint(match), func(t *testing.T) {
			m := basicsNamedManifest(t)
			m.License.PricingPlanId = uuid.MustParse("10000000-0000-0000-0000-000000000001")
			if !match {
				m.License.PricingPlanId = uuid.MustParse("10000000-0000-0000-0000-000000000002")
			}
			f := newBasicsNamedFixture()
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if err != nil || r.Ready != match || r.Gates.PricingPlan != match || len(f.writes) != 0 || (!match && len(r.Blockers) == 0) {
				t.Fatalf("incorrect pin gate: %+v err=%v writes=%v", r, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsNamedPlanConflicts(t *testing.T) {
	cases := map[string]func(*basicsFixture){
		"missing": func(f *basicsFixture) { f.plans = nil },
		"duplicate": func(f *basicsFixture) {
			other := map[string]any{}
			for k, v := range f.plans[0] {
				other[k] = v
			}
			other["id"] = "10000000-0000-0000-0000-000000000002"
			f.plans = append(f.plans, other)
		},
		"zero UUID":          func(f *basicsFixture) { f.plans[0]["id"] = uuid.Nil.String() },
		"wrong name":         func(f *basicsFixture) { f.plans[0]["internal_name"] = "OTHER" },
		"wrong display name": func(f *basicsFixture) { f.plans[0]["display_name"] = "Essential (Monthly Plan)" },
		"monthly interval":   func(f *basicsFixture) { f.plans[0]["default_billing_interval"] = "MONTHLY" },
		"wrong currency":     func(f *basicsFixture) { f.plans[0]["currency_code"] = "USD" },
		"inactive":           func(f *basicsFixture) { f.plans[0]["is_active"] = false },
		"disabled fees":      func(f *basicsFixture) { f.plans[0]["performance_fee_enabled"] = false },
		"unknown fees":       func(f *basicsFixture) { f.plans[0]["performance_fee_enabled"] = nil },
	}
	for _, field := range []string{"internal_name", "display_name", "default_billing_interval", "currency_code", "is_active", "performance_fee_enabled", "price_per_month", "price_per_year", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months", "max_accounts", "max_business_units", "max_seats", "max_granted_tokens"} {
		cases["missing "+field] = func(f *basicsFixture) { delete(f.plans[0], field) }
	}
	for _, field := range []string{"price_per_month", "price_per_year", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months"} {
		cases["changed "+field] = func(f *basicsFixture) { f.plans[0][field] = 1 }
		cases["null "+field] = func(f *basicsFixture) { f.plans[0][field] = nil }
	}
	for field, value := range map[string]int{"max_accounts": 2, "max_business_units": 0, "max_seats": 0, "max_granted_tokens": 0} {
		cases["low "+field] = func(f *basicsFixture) { f.plans[0][field] = value }
	}
	for name, change := range cases {
		for _, missingCompany := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing_company=%v", name, missingCompany), func(t *testing.T) {
				m := basicsNamedManifest(t)
				f := newBasicsNamedFixture()
				if missingCompany {
					f.companies, f.licenses = nil, nil
				}
				change(f)
				client, closeServer := f.serve(t)
				defer closeServer()
				r, err := client.PrepareBasics(context.Background(), m, true)
				if r.Ready || r.Gates.PricingPlan || len(f.writes) != 0 || (err == nil && len(r.Blockers) == 0) {
					t.Fatalf("unsafe plan accepted: %+v err=%v writes=%v", r, err, f.writes)
				}
			})
		}
	}
}

func TestPrepareBasicsNamedLicenseTerms(t *testing.T) {
	for field, exact := range map[string]any{"price_per_month": 199, "price_per_year": 2388, "performance_fee_enabled": true, "performance_fee_rate_bps": 2500, "performance_fee_allowance_yearly_cents": 500000, "performance_fee_term_months": 12} {
		for _, kind := range []string{"inherit", "exact", "drift", "missing"} {
			t.Run(field+"/"+kind, func(t *testing.T) {
				m := basicsNamedManifest(t)
				m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
				f := newBasicsNamedFixture()
				switch kind {
				case "inherit":
					f.licenses[0][field] = nil
				case "exact":
					f.licenses[0][field] = exact
				case "drift":
					f.licenses[0][field] = 0
					if field == "performance_fee_enabled" {
						f.licenses[0][field] = false
					}
				case "missing":
					delete(f.licenses[0], field)
				}
				client, closeServer := f.serve(t)
				defer closeServer()
				r, err := client.PrepareBasics(context.Background(), m, true)
				wantSafe := kind == "inherit" || kind == "exact"
				if err != nil || r.Ready != wantSafe || r.Gates.License != wantSafe || r.Gates.Configuration != wantSafe || (len(f.writes) == 1) != wantSafe || (!wantSafe && (len(f.writes) != 0 || len(r.Blockers) == 0)) {
					t.Fatalf("incorrect license terms gate: %+v err=%v writes=%v", r, err, f.writes)
				}
			})
		}
	}
}

func TestPrepareBasicsNamedLicenseConflicts(t *testing.T) {
	cases := map[string]func(*basicsFixture){
		"existing company without license": func(f *basicsFixture) { f.licenses = nil },
		"duplicate license": func(f *basicsFixture) {
			other := map[string]any{}
			for k, v := range f.licenses[0] {
				other[k] = v
			}
			other["id"] = "30000000-0000-0000-0000-000000000002"
			f.licenses = append(f.licenses, other)
		},
		"wrong currency":    func(f *basicsFixture) { f.licenses[0]["currency_code"] = "USD" },
		"null currency":     func(f *basicsFixture) { f.licenses[0]["currency_code"] = nil },
		"name drift":        func(f *basicsFixture) { f.licenses[0]["name"] = "Other" },
		"description drift": func(f *basicsFixture) { f.licenses[0]["description"] = "Other" },
		"monthly":           func(f *basicsFixture) { f.licenses[0]["billing_interval"] = "MONTHLY" },
		"wrong plan":        func(f *basicsFixture) { f.licenses[0]["pricing_plan"] = basicsBUID },
		"foreign company":   func(f *basicsFixture) { f.licenses[0]["company"] = basicsBUID },
		"inactive":          func(f *basicsFixture) { f.licenses[0]["status"] = "INACTIVE" },
		"future":            func(f *basicsFixture) { f.licenses[0]["valid_from"] = time.Now().Add(time.Hour).Format(time.RFC3339) },
		"expired":           func(f *basicsFixture) { f.licenses[0]["valid_until"] = "2020-01-01T00:00:00Z" },
	}
	for _, field := range []string{"currency_code", "name", "description", "valid_from", "billing_interval", "max_accounts", "max_business_units", "max_seats", "max_granted_tokens", "available_tokens"} {
		cases["missing "+field] = func(f *basicsFixture) { delete(f.licenses[0], field) }
	}
	for field, value := range map[string]int{"max_accounts": 2, "max_business_units": 0, "max_seats": 0, "max_granted_tokens": 0, "available_tokens": 0} {
		cases["low "+field] = func(f *basicsFixture) { f.licenses[0][field] = value }
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := basicsNamedManifest(t)
			m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
			f := newBasicsNamedFixture()
			change(f)
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if err != nil || r.Ready || r.Gates.License || len(f.writes) != 0 || len(r.Blockers) == 0 {
				t.Fatalf("unsafe license accepted: %+v err=%v writes=%v", r, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsNamedUnlimitedQuotas(t *testing.T) {
	m := basicsNamedManifest(t)
	f := newBasicsNamedFixture()
	for _, field := range []string{"max_accounts", "max_business_units", "max_seats", "max_granted_tokens"} {
		f.plans[0][field], f.licenses[0][field] = nil, nil
	}
	f.licenses[0]["available_tokens"] = nil
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), m, false)
	if err != nil || !r.Ready || len(f.writes) != 0 {
		t.Fatalf("explicit unlimited quotas rejected: %+v err=%v", r, err)
	}
}

func TestPrepareBasicsNamedRecheckBlocksDrift(t *testing.T) {
	for _, kind := range []string{"UUID changes", "quota changes", "fees change", "plan disappears", "duplicate plan", "company appears"} {
		t.Run(kind, func(t *testing.T) {
			m := basicsNamedManifest(t)
			f := newBasicsNamedFixture()
			f.companies, f.licenses = nil, nil
			planReads, companyReads := 0, 0
			f.hook = func(_ http.ResponseWriter, req *http.Request) bool {
				if req.URL.Path == "/api/v2/organization/companies" {
					companyReads++
					if kind == "company appears" && companyReads == 2 {
						f.companies = newBasicsFixture().companies
					}
				}
				if req.URL.Path == "/api/v2/license-and-billing/pricing-plans" {
					planReads++
					if planReads == 2 {
						switch kind {
						case "UUID changes":
							f.plans[0]["id"] = "10000000-0000-0000-0000-000000000002"
						case "quota changes":
							f.plans[0]["max_accounts"] = 4
						case "fees change":
							f.plans[0]["performance_fee_rate_bps"] = 2000
						case "plan disappears":
							f.plans = nil
						case "duplicate plan":
							f.plans = append(f.plans, map[string]any{"id": "10000000-0000-0000-0000-000000000002"})
						}
					}
				}
				return false
			}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if err == nil || r.Ready || r.OutcomeUncertain || len(f.writes) != 0 || len(r.Actions) != 1 || r.Actions[0].Reason != "prerequisite_drift" {
				t.Fatalf("recheck failed to stop setup: %+v err=%v writes=%v", r, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsNamedReadbackDriftIsUncertain(t *testing.T) {
	m := basicsNamedManifest(t)
	f := newBasicsNamedFixture()
	f.companies, f.licenses = nil, nil
	f.hook = func(_ http.ResponseWriter, req *http.Request) bool {
		if req.URL.Path == "/api/v2/license-and-billing/licenses" && len(f.writes) > 0 {
			f.licenses[0]["performance_fee_enabled"] = false
		}
		return false
	}
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), m, true)
	if err == nil || !r.OutcomeUncertain || r.Ready || len(f.writes) != 1 || len(f.setups) != 1 {
		t.Fatalf("readback drift did not stop without retry: %+v err=%v writes=%v", r, err, f.writes)
	}
}

func TestPrepareBasicsNamedLicenseReuse(t *testing.T) {
	m := basicsNamedManifest(t)
	f := newBasicsNamedFixture()
	client, closeServer := f.serve(t)
	defer closeServer()
	for range 2 {
		r, err := client.PrepareBasics(context.Background(), m, true)
		if err != nil || !r.Ready || !r.Gates.PricingPlan || !r.Gates.License || r.PricingPlanID != "10000000-0000-0000-0000-000000000001" || r.LicenseID != basicsLicenseID || len(f.writes) != 0 {
			t.Fatalf("named license was not reused: %+v err=%v writes=%v", r, err, f.writes)
		}
	}
}
