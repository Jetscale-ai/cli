package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

func basicsTestManifest() BasicsManifest {
	slug := "test-company"
	return BasicsManifest{
		Company:      generated.CreateCompanyRequest{Name: "Test Company", Slug: &slug, DomainEmails: []string{"example.test"}},
		License:      generated.SetupRootCompanyLicensePayload{PricingPlanId: uuid.MustParse("10000000-0000-0000-0000-000000000001")},
		BusinessUnit: &BasicsBusinessUnit{Name: "Test Clouds", Slug: "test-clouds"},
	}
}

const (
	basicsCompanyID  = "20000000-0000-0000-0000-000000000001"
	basicsLicenseID  = "30000000-0000-0000-0000-000000000001"
	basicsBUID       = "40000000-0000-0000-0000-000000000001"
	basicsAgentID    = "50000000-0000-0000-0000-000000000001"
	basicsRootFlagID = "60000000-0000-0000-0000-000000000001"
)

type basicsFixture struct {
	companies []map[string]any
	plans     []map[string]any
	licenses  []map[string]any
	units     []map[string]any
	accounts  []map[string]any
	flags     map[string]map[string]any
	settings  map[string]map[string]any
	writes    []string
	reads     []string
	hook      func(http.ResponseWriter, *http.Request) bool
	setups    []generated.SetupRootCompanyRequest
}

func newBasicsFixture() *basicsFixture {
	return &basicsFixture{
		companies: []map[string]any{{"id": basicsCompanyID, "name": "Test Company", "slug": "test-company", "domain_emails": []string{"example.test"}, "is_root": false, "status": "ACTIVE"}},
		plans:     []map[string]any{{"id": basicsTestManifest().License.PricingPlanId.String(), "is_active": true, "max_accounts": 3, "max_business_units": 1, "max_seats": 1, "max_granted_tokens": 100, "performance_fee_enabled": false, "performance_fee_rate_bps": 0, "price_per_month": 0, "price_per_year": 0}},
		licenses:  []map[string]any{{"id": basicsLicenseID, "company": basicsCompanyID, "pricing_plan": basicsTestManifest().License.PricingPlanId.String(), "status": "ACTIVE", "max_accounts": 3, "max_business_units": 1, "max_seats": 1, "max_granted_tokens": 100, "available_tokens": 100, "valid_from": "2020-01-01T00:00:00Z", "valid_until": nil, "billing_interval": "YEARLY", "performance_fee_enabled": nil, "performance_fee_rate_bps": nil, "price_per_month": 0, "price_per_year": 0}},
		units:     []map[string]any{{"id": basicsBUID, "company": basicsCompanyID, "name": "Test Clouds", "slug": "test-clouds", "managed_agent": basicsAgentID}},
		accounts:  []map[string]any{}, flags: map[string]map[string]any{}, settings: map[string]map[string]any{},
	}
}

func (f *basicsFixture) flag(scope, key string, enabled bool) {
	row := map[string]any{"id": basicsRootFlagID, "value": enabled, "is_secret": false, "deprecated_at": nil}
	if scope == "root" {
		row["key"] = key
	} else {
		row["feature_flag_key"] = key
		row["root_feature_flag"] = basicsRootFlagID
		if scope == "company" {
			row["id"] = "60000000-0000-0000-0000-000000000002"
			row["company"] = basicsCompanyID
		} else {
			row["id"] = "60000000-0000-0000-0000-000000000003"
			row["business_unit"] = basicsBUID
		}
	}
	f.flags[scope+"/"+key] = row
}

func (f *basicsFixture) setting(scope, key, valueType string, value any) {
	row := map[string]any{"id": "70000000-0000-0000-0000-000000000001", "key": key, "value_type": valueType, "value": map[string]any{"value": value}}
	if scope == "company" {
		row["company"] = basicsCompanyID
	}
	if scope == "business_unit" {
		row["business_unit"] = basicsBUID
	}
	f.settings[scope+"/"+key] = row
}

func basicsFixtureJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *basicsFixture) serve(t *testing.T) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if r.Method == "GET" {
			f.reads = append(f.reads, path)
		} else {
			f.writes = append(f.writes, r.Method+" "+path)
		}
		if f.hook != nil && f.hook(w, r) {
			return
		}
		if path == "/api/v2/auth/me" && r.Method == http.MethodGet {
			basicsFixtureJSON(w, map[string]any{"user": map[string]any{"id": basicsAgentID, "is_superuser": true}})
			return
		}
		if path == "/api/v2/organization/action/setup-company" && r.Method == "POST" {
			var body generated.SetupRootCompanyRequest
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if decoder.Decode(&body) != nil || body.Company.Slug == nil || *body.Company.Slug != "test-company" || len(f.plans) != 1 || body.License.PricingPlanId.String() != f.plans[0]["id"] {
				t.Error("incorrect atomic setup body")
			}
			f.setups = append(f.setups, body)
			f.companies = newBasicsFixture().companies
			f.licenses = newBasicsFixture().licenses
			license := f.licenses[0]
			license["pricing_plan"] = body.License.PricingPlanId.String()
			license["name"], license["description"] = body.License.Name, body.License.Description
			for _, field := range []string{"max_accounts", "max_business_units", "max_seats", "max_granted_tokens", "price_per_month", "price_per_year", "currency_code"} {
				license[field] = f.plans[0][field]
			}
			license["available_tokens"] = f.plans[0]["max_granted_tokens"]
			if body.License.BillingInterval != nil {
				license["billing_interval"] = *body.License.BillingInterval
			}
			if body.License.ValidFrom != nil {
				license["valid_from"] = body.License.ValidFrom
			}
			license["valid_until"] = body.License.ValidUntil
			// Atomic Backend setup leaves nullable fee overrides unset to inherit the plan.
			license["performance_fee_allowance_yearly_cents"] = nil
			license["performance_fee_term_months"] = nil
			basicsFixtureJSON(w, f.companies[0])
			return
		}
		var data []map[string]any
		switch path {
		case "/api/v2/organization/companies":
			if r.URL.Query().Get("slug") != "eq test-company" {
				t.Error("unscoped company query")
			}
			data = f.companies
		case "/api/v2/license-and-billing/pricing-plans":
			q := r.URL.Query()
			if q.Get("internal_name") != "" {
				if q.Get("internal_name") != "eq ESSENTIAL_ANNUAL" || q.Get("id") != "" {
					t.Error("unscoped named plan query")
				}
			} else if q.Get("id") != "eq "+basicsTestManifest().License.PricingPlanId.String() {
				t.Error("unscoped plan query")
			}
			data = f.plans
		case "/api/v2/license-and-billing/licenses":
			if r.URL.Query().Get("company") != "eq "+basicsCompanyID {
				t.Error("unscoped license query")
			}
			data = f.licenses
		case "/api/v2/organization/business-units":
			if r.URL.Query().Get("company") != "eq "+basicsCompanyID {
				t.Error("unscoped BU query")
			}
			data = []map[string]any{}
			for _, unit := range f.units {
				if r.URL.Query().Get("slug") == "" || r.URL.Query().Get("slug") == "eq "+fmt.Sprint(unit["slug"]) {
					data = append(data, unit)
				}
			}
		case "/api/v2/cloud/cloud-accounts":
			if r.URL.Query().Get("company_id") != basicsCompanyID {
				t.Error("account topology must use explicit company_id")
			}
			data = f.accounts
		default:
			if strings.HasPrefix(path, "/api/v2/system/feature-flags/") {
				parts := strings.Split(strings.TrimPrefix(path, "/api/v2/system/feature-flags/"), "/")
				scope, key := parts[0], parts[len(parts)-1]
				if scope == "business-unit" {
					scope = "business_unit"
				}
				if scope == "company" && r.URL.Query().Get("company_id") != basicsCompanyID {
					t.Error("wrong flag company")
				}
				if scope == "business_unit" && r.URL.Query().Get("business_unit_id") != basicsBUID {
					t.Error("wrong flag BU")
				}
				row := f.flags[scope+"/"+key]
				if r.Method == "PATCH" {
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if len(body) != 1 || body["value"] != true {
						t.Error("flag metadata was modified")
					}
					if row == nil {
						f.flag(scope, key, true)
						row = f.flags[scope+"/"+key]
					} else {
						row["value"] = true
					}
					if scope == "company" {
						// Backend upserts the BU child and preserves existing secret metadata.
						child := f.flags["business_unit/"+key]
						if child == nil {
							f.flag("business_unit", key, true)
							f.flags["business_unit/"+key]["is_secret"] = row["is_secret"]
						} else {
							child["value"] = true
						}
					}
				}
				if row == nil {
					w.WriteHeader(404)
					return
				}
				basicsFixtureJSON(w, row)
				return
			}
			if strings.HasPrefix(path, "/api/v2/system/") && strings.Contains(path, "-settings") {
				parts := strings.Split(strings.TrimPrefix(path, "/api/v2/system/"), "/")
				scope := strings.TrimSuffix(parts[0], "-settings")
				if scope == "business-unit" {
					scope = "business_unit"
				}
				if r.Method == "GET" {
					if scope == "company" && r.URL.Query().Get("company_id") != basicsCompanyID {
						t.Error("wrong setting company")
					}
					if scope == "business_unit" && r.URL.Query().Get("business_unit_id") != basicsBUID {
						t.Error("wrong setting BU")
					}
					row := f.settings[scope+"/"+parts[len(parts)-1]]
					if row == nil {
						w.WriteHeader(404)
						return
					}
					basicsFixtureJSON(w, row)
					return
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				key, _ := body["key"].(string)
				if r.Method == "PATCH" {
					for k, row := range f.settings {
						if strings.HasPrefix(k, scope+"/") && row["id"] == parts[len(parts)-1] {
							key = fmt.Sprint(row["key"])
						}
					}
				}
				if key == "" {
					t.Error("wrong setting mutation ID")
					w.WriteHeader(404)
					return
				}
				if r.Method == "POST" && scope == "company" && body["company"] != basicsCompanyID {
					t.Error("wrong create company")
				}
				if r.Method == "POST" && scope == "business_unit" && body["business_unit"] != basicsBUID {
					t.Error("wrong create BU")
				}
				value, _ := body["value"].(map[string]any)
				f.setting(scope, key, fmt.Sprint(body["value_type"]), value["value"])
				basicsFixtureJSON(w, f.settings[scope+"/"+key])
				return
			}
			if strings.HasPrefix(path, "/api/v2/settings/effective/") {
				key := strings.TrimPrefix(path, "/api/v2/settings/effective/")
				value := any(true)
				valueType := "boolean"
				if key == "recommendation_temperature" {
					value = 0.1
					valueType = "number"
				}
				chain := []map[string]any{{"scope": "user", "value": nil, "source": "unset", "setting_id": nil, "applies": false}}
				// An omitted company_id resolves to the caller's company, not root-only.
				for _, candidate := range []string{"business_unit", "company", "root"} {
					if candidate == "business_unit" && r.URL.Query().Get("business_unit_id") == "" {
						continue
					}
					entry := map[string]any{"scope": candidate, "value": nil, "source": "unset", "setting_id": nil, "applies": false}
					if row := f.settings[candidate+"/"+key]; row != nil {
						entry["setting_id"] = row["id"]
						if row["value_type"] == valueType {
							entry["value"] = row["value"].(map[string]any)["value"]
							entry["source"] = candidate
						}
					}
					chain = append(chain, entry)
				}
				selected := map[string]any{"scope": "system", "value": value, "source": "default", "setting_id": nil, "applies": false}
				chain = append(chain, selected)
				for _, entry := range chain {
					if entry["value"] != nil {
						selected = entry
						break
					}
				}
				selected["applies"] = true
				basicsFixtureJSON(w, map[string]any{"key": key, "value_type": valueType, "value": selected["value"], "scope": selected["scope"], "source": selected["source"], "scope_chain": chain})
				return
			}
			t.Errorf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(404)
			return
		}
		if r.Method != "GET" {
			t.Errorf("unexpected list mutation: %s %s", r.Method, path)
			w.WriteHeader(405)
			return
		}
		q := r.URL.Query()
		if q.Get("api_limit") != "100" || q.Get("api_offset") != "0" || q.Get("api_fields") == "" || q.Get("api_sort") != "id" || q.Get("api_include") != "" {
			t.Error("unbounded/overbroad collection query")
		}
		if data == nil {
			data = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"count": len(data), "total_count": len(data), "offset": 0, "limit": 100}, "data": data})
	}))
	return NewSessionClient(server.URL, server.Client()), server.Close
}

func TestPrepareBasicsExistingRepeatedNoop(t *testing.T) {
	f := newBasicsFixture()
	m := basicsTestManifest()
	key := "AGENTS.EXPLORER"
	for _, scope := range []string{"root", "company", "business_unit"} {
		f.flag(scope, key, true)
	}
	m.Flags = BasicsFlags{Root: map[string]bool{key: true}, Company: map[string]bool{key: true}, BusinessUnit: map[string]bool{key: true}}
	m.Settings.Company = map[string]BasicsSetting{"recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
	f.setting("company", "recommendation_temperature", "number", 0.2)
	client, closeServer := f.serve(t)
	defer closeServer()
	for range 2 {
		r, err := client.PrepareBasics(context.Background(), m, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.writes) != 0 || r.Ready || !r.Gates.Configuration || !r.Gates.ManagedAgentLocal || r.Gates.ManagedAgentExternal || r.ManagedAgentID != basicsAgentID {
			t.Fatalf("incorrect noop/gates: %+v writes=%v", r, f.writes)
		}
		for _, a := range r.Actions {
			if a.Status != "noop" {
				t.Fatalf("not noop: %+v", a)
			}
		}
		if len(r.Blockers) != 1 || r.Blockers[0].Reason != "external_state_unknown" {
			t.Fatalf("external agent proof falsely asserted: %+v", r)
		}
	}
}

func TestPrepareBasicsRequiresFullVisibility(t *testing.T) {
	for name, user := range map[string]map[string]any{
		"restricted actor":  {"id": basicsAgentID, "is_superuser": false},
		"missing privilege": {"id": basicsAgentID},
		"missing identity":  {"is_superuser": true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBasicsFixture()
			f.flag("root", "AGENTS.EXPLORER", false)
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v2/auth/me" {
					return false
				}
				basicsFixtureJSON(w, map[string]any{"user": user})
				return true
			}
			m := basicsTestManifest()
			m.Flags.Root = map[string]bool{"AGENTS.EXPLORER": true}
			client, closeServer := f.serve(t)
			defer closeServer()
			result, err := client.PrepareBasics(context.Background(), m, true)
			if err != nil || len(f.writes) != 0 || result.Ready || result.Gates.Configuration || len(result.Blockers) != 1 || result.Blockers[0].Reason != "visibility_unproved" {
				t.Fatalf("unproved visibility allowed basics effects: %+v err=%v writes=%v", result, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsCreatesCompanyOnceAcrossRerun(t *testing.T) {
	f := newBasicsFixture()
	f.companies = nil
	f.licenses = nil
	f.units = nil
	client, closeServer := f.serve(t)
	defer closeServer()
	for range 2 {
		r, err := client.PrepareBasics(context.Background(), basicsTestManifest(), true)
		if err != nil {
			t.Fatal(err)
		}
		if r.CompanyID != basicsCompanyID || r.LicenseID != basicsLicenseID || !r.Gates.License || r.Ready {
			t.Fatalf("bad setup result: %+v", r)
		}
	}
	if len(f.writes) != 1 || f.writes[0] != "POST /api/v2/organization/action/setup-company" {
		t.Fatalf("non-idempotent setup: %v", f.writes)
	}
}

func TestPrepareBasicsLicensePrices(t *testing.T) {
	for _, field := range []string{"price_per_month", "price_per_year"} {
		for _, kind := range []string{"zero", "null inherits free plan", "paid license", "missing license price", "null with paid plan", "null with missing plan price"} {
			t.Run(field+"/"+kind, func(t *testing.T) {
				f := newBasicsFixture()
				switch kind {
				case "null inherits free plan":
					f.licenses[0][field] = nil
				case "paid license":
					f.licenses[0][field] = 10
				case "missing license price":
					delete(f.licenses[0], field)
				case "null with paid plan":
					f.licenses[0][field] = nil
					f.plans[0][field] = 10
				case "null with missing plan price":
					f.licenses[0][field] = nil
					delete(f.plans[0], field)
				}
				m := basicsTestManifest()
				m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
				client, closeServer := f.serve(t)
				defer closeServer()
				r, err := client.PrepareBasics(context.Background(), m, true)
				if err != nil {
					t.Fatal(err)
				}
				wantSafe := kind == "zero" || kind == "null inherits free plan"
				if r.Gates.Configuration != wantSafe || (len(f.writes) == 1) != wantSafe || (!wantSafe && len(f.writes) != 0) {
					t.Fatalf("incorrect license price gate: %+v writes=%v", r, f.writes)
				}
				if !wantSafe && len(r.Blockers) == 0 {
					t.Fatal("unsafe price missing blocker")
				}
			})
		}
	}
}

func TestPrepareBasicsConflictsBlockAllWrites(t *testing.T) {
	cases := map[string]func(*basicsFixture){
		"duplicate company": func(f *basicsFixture) {
			f.companies = append(f.companies, map[string]any{"id": "20000000-0000-0000-0000-000000000002"})
		},
		"company name drift": func(f *basicsFixture) { f.companies[0]["name"] = "Other" },
		"company slug drift": func(f *basicsFixture) { f.companies[0]["slug"] = "other" },
		"root company":       func(f *basicsFixture) { f.companies[0]["is_root"] = true },
		"missing license":    func(f *basicsFixture) { f.licenses = nil },
		"multiple licenses": func(f *basicsFixture) {
			f.licenses = append(f.licenses, map[string]any{"id": "30000000-0000-0000-0000-000000000002"})
		},
		"future license":   func(f *basicsFixture) { f.licenses[0]["valid_from"] = time.Now().Add(time.Hour).Format(time.RFC3339) },
		"expired license":  func(f *basicsFixture) { f.licenses[0]["valid_until"] = "2020-01-01T00:00:00Z" },
		"inactive license": func(f *basicsFixture) { f.licenses[0]["status"] = "INACTIVE" },
		"foreign license":  func(f *basicsFixture) { f.licenses[0]["company"] = basicsBUID },
		"other plan":       func(f *basicsFixture) { f.licenses[0]["pricing_plan"] = basicsBUID },
		"account quota":    func(f *basicsFixture) { f.plans[0]["max_accounts"] = 2 },
		"unknown quota":    func(f *basicsFixture) { delete(f.plans[0], "max_accounts") },
		"fee enabled":      func(f *basicsFixture) { f.plans[0]["performance_fee_enabled"] = true },
		"paid plan":        func(f *basicsFixture) { f.plans[0]["price_per_year"] = 10 },
		"missing BU":       func(f *basicsFixture) { f.units = nil },
		"foreign BU":       func(f *basicsFixture) { f.units[0]["company"] = basicsBUID },
		"missing agent":    func(f *basicsFixture) { f.units[0]["managed_agent"] = nil },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newBasicsFixture()
			change(f)
			m := basicsTestManifest()
			m.Flags.Root = map[string]bool{"AGENTS.EXPLORER": true}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.writes) != 0 || len(r.Blockers) == 0 || r.Ready {
				t.Fatalf("unsafe conflict: %+v writes=%v", r, f.writes)
			}
		})
	}
}

func TestPrepareBasicsCompanyFlagCascadeScope(t *testing.T) {
	for _, scope := range []string{"single empty BU", "other BU", "accounts", "already true"} {
		t.Run(scope, func(t *testing.T) {
			f := newBasicsFixture()
			key := "AGENTS.EXPLORER"
			f.flag("root", key, true)
			f.flag("company", key, false)
			if scope == "other BU" {
				f.units = append(f.units, map[string]any{"id": "40000000-0000-0000-0000-000000000002", "company": basicsCompanyID, "slug": "other", "name": "Other", "managed_agent": nil})
			}
			if scope == "accounts" || scope == "already true" {
				f.accounts = []map[string]any{{"id": "80000000-0000-0000-0000-000000000001", "business_unit": basicsBUID}}
			}
			if scope == "already true" {
				f.flags["company/"+key]["value"] = true
				f.flag("business_unit", key, true)
			}
			m := basicsTestManifest()
			m.Flags.Company = map[string]bool{key: true}
			client, closeServer := f.serve(t)
			defer closeServer()
			for range 2 {
				r, err := client.PrepareBasics(context.Background(), m, true)
				if err != nil {
					t.Fatal(err)
				}
				if scope == "single empty BU" && !r.Gates.Configuration {
					t.Fatalf("safe enable blocked: %+v", r)
				}
			}
			wantWrites := 0
			if scope == "single empty BU" {
				wantWrites = 1
			}
			if len(f.writes) != wantWrites {
				t.Fatalf("cascade writes %v", f.writes)
			}
		})
	}
}

func TestPrepareBasicsCompanyCascadeChildDriftBlocks(t *testing.T) {
	for _, kind := range []string{"secret child", "wrong root", "wrong BU", "enabled parent disabled child", "enabled parent missing child", "child changes before write"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			key := "AGENTS.EXPLORER"
			f.flag("root", key, true)
			f.flag("company", key, false)
			f.flag("business_unit", key, false)
			child := f.flags["business_unit/"+key]
			switch kind {
			case "secret child":
				child["is_secret"] = true
			case "wrong root":
				child["root_feature_flag"] = basicsBUID
			case "wrong BU":
				child["business_unit"] = basicsCompanyID
			case "enabled parent disabled child":
				f.flags["company/"+key]["value"] = true
			case "enabled parent missing child":
				f.flags["company/"+key]["value"] = true
				delete(f.flags, "business_unit/"+key)
			case "child changes before write":
				reads := 0
				f.hook = func(_ http.ResponseWriter, r *http.Request) bool {
					if r.Method == "GET" && strings.Contains(r.URL.Path, "/feature-flags/business-unit/") {
						reads++
						if reads == 2 {
							child["id"] = basicsBUID
						}
					}
					return false
				}
			}
			m := basicsTestManifest()
			m.Flags.Company = map[string]bool{key: true}
			m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if len(f.writes) != 0 || (err == nil && r.Gates.Configuration) {
				t.Fatalf("cascade child drift allowed writes: %+v %v %v", r, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsCompanyCascadeReadback(t *testing.T) {
	for _, kind := range []string{"missing child created", "disabled child enabled", "secret readback", "replaced child", "child read fails", "parent committed cascade failed"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			key := "AGENTS.EXPLORER"
			f.flag("root", key, true)
			f.flag("company", key, false)
			if kind != "missing child created" {
				f.flag("business_unit", key, false)
			}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if kind == "parent committed cascade failed" && r.Method == "PATCH" {
					f.flags["company/"+key]["value"] = true
					w.WriteHeader(500)
					return true
				}
				if len(f.writes) > 0 && r.Method == "GET" && strings.Contains(r.URL.Path, "/feature-flags/business-unit/") {
					switch kind {
					case "secret readback":
						f.flags["business_unit/"+key]["is_secret"] = true
					case "replaced child":
						f.flags["business_unit/"+key]["id"] = basicsBUID
					case "child read fails":
						w.WriteHeader(503)
						return true
					}
				}
				return false
			}
			m := basicsTestManifest()
			m.Flags.Company = map[string]bool{key: true}
			m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if kind == "missing child created" || kind == "disabled child enabled" {
				if err != nil || !r.Gates.Configuration || len(f.writes) != 2 || f.flags["business_unit/"+key]["value"] != true {
					t.Fatalf("cascade not verified: %+v %v %v", r, err, f.writes)
				}
				r, err = client.PrepareBasics(context.Background(), m, true)
				if err != nil || !r.Gates.Configuration || len(f.writes) != 2 {
					t.Fatalf("cascade rerun wrote again: %+v %v %v", r, err, f.writes)
				}
				return
			}
			if err == nil || !r.OutcomeUncertain || len(f.writes) != 1 {
				t.Fatalf("unverified cascade continued: %+v %v %v", r, err, f.writes)
			}
			if kind == "parent committed cascade failed" {
				r, err = client.PrepareBasics(context.Background(), m, true)
				if len(f.writes) != 1 || r.Gates.Configuration || (err == nil && len(r.Blockers) == 0) {
					t.Fatalf("partial cascade was replayed or accepted as noop: %+v %v %v", r, err, f.writes)
				}
			}
		})
	}
}

func TestPrepareBasicsFailureStopsLaterMutationAndSanitizes(t *testing.T) {
	f := newBasicsFixture()
	f.flag("root", "AGENTS.EXPLORER", false)
	f.flag("root", "CLOUD.FOCUS", false)
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "PATCH" {
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"secret":"synthetic-secret-never-echo"}`))
			return true
		}
		return false
	}
	m := basicsTestManifest()
	m.Flags.Root = map[string]bool{"CLOUD.FOCUS": true, "AGENTS.EXPLORER": true}
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), m, true)
	if err == nil || !r.OutcomeUncertain || len(f.writes) != 1 {
		t.Fatalf("did not stop: %+v %v %v", r, err, f.writes)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded)+err.Error(), "synthetic-secret") {
		t.Fatal("secret escaped")
	}
	if !strings.HasSuffix(f.writes[0], "AGENTS.EXPLORER") {
		t.Fatalf("nondeterministic action order: %v", f.writes)
	}
}

func TestPrepareBasicsRejectsUnsupportedManifestBeforeRequests(t *testing.T) {
	for _, mutation := range []func(*BasicsManifest){
		func(m *BasicsManifest) { m.Flags.Root = map[string]bool{"AGENTS.EXPLORER": false} },
		func(m *BasicsManifest) { m.Flags.Root = map[string]bool{"secret-synthetic-key": true} },
		func(m *BasicsManifest) {
			m.Settings.Root = map[string]BasicsSetting{"agent_internal_aws_external_id": {ValueType: "string", Value: json.RawMessage(`"synthetic-secret"`)}}
		},
		func(m *BasicsManifest) {
			m.Settings.Root = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`false`)}}
		},
	} {
		f := newBasicsFixture()
		m := basicsTestManifest()
		mutation(&m)
		client, closeServer := f.serve(t)
		r, err := client.PrepareBasics(context.Background(), m, true)
		closeServer()
		if err != nil || len(f.reads) != 0 || len(f.writes) != 0 || len(r.Blockers) == 0 {
			t.Fatalf("invalid manifest executed: %+v %v", r, err)
		}
		encoded, _ := json.Marshal(r)
		if strings.Contains(string(encoded), "synthetic") {
			t.Fatal("manifest leaked")
		}
	}
	data, _ := json.Marshal(basicsTestManifest())
	var m BasicsManifest
	if json.Unmarshal(data, &m) != nil {
		t.Fatal("manifest roundtrip failed")
	}
	data = []byte(strings.Replace(string(data), `"business_unit":{`, `"business_unit":{"id":"synthetic-secret",`, 1))
	if err := json.Unmarshal(data, &m); err == nil || strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("unknown field not safely rejected: %v", err)
	}
}

func TestPrepareBasicsBoundedAndSanitizedReads(t *testing.T) {
	for _, failure := range []string{"oversized", "malformed", "missing meta", "too many matches", "unauthorized", "partial page", "duplicate ID"} {
		t.Run(failure, func(t *testing.T) {
			f := newBasicsFixture()
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v2/organization/companies" {
					return false
				}
				w.Header().Set("Content-Type", "application/json")
				switch failure {
				case "oversized":
					_, _ = w.Write([]byte(`{"secret":"` + strings.Repeat("x", basicsMaxBody) + `"}`))
				case "malformed":
					_, _ = w.Write([]byte(`synthetic-secret`))
				case "missing meta":
					basicsFixtureJSON(w, f.companies)
				case "unauthorized":
					w.WriteHeader(401)
					_, _ = w.Write([]byte(`synthetic-secret`))
				default:
					data := f.companies
					total := 2001
					if failure == "partial page" {
						total = 2
					}
					if failure == "duplicate ID" {
						data = append(data, data[0])
						total = 2
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"count": len(data), "offset": 0, "limit": 100, "total_count": total}, "data": data})
				}
				return true
			}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), basicsTestManifest(), true)
			if err == nil || len(f.writes) != 0 || len(f.reads) != 2 || f.reads[0] != "/api/v2/auth/me" || strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("unsafe response handling: %+v %v", r, err)
			}
		})
	}
}

func TestPrepareBasicsCompanyRecheckAndUncertainSetup(t *testing.T) {
	for _, kind := range []string{"slug appears", "plan changes", "POST rejected", "readback fails"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			f.companies = nil
			f.licenses = nil
			f.units = nil
			companyReads, planReads := 0, 0
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v2/organization/companies" {
					companyReads++
					if kind == "slug appears" && companyReads == 2 {
						f.companies = newBasicsFixture().companies
					}
					if kind == "readback fails" && companyReads == 3 {
						w.WriteHeader(500)
						return true
					}
				}
				if r.URL.Path == "/api/v2/license-and-billing/pricing-plans" {
					planReads++
					if kind == "plan changes" && planReads == 2 {
						f.plans[0]["max_accounts"] = 4
					}
				}
				if r.Method == "POST" && kind == "POST rejected" {
					w.WriteHeader(409)
					_, _ = w.Write([]byte(`synthetic-secret`))
					return true
				}
				return false
			}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), basicsTestManifest(), true)
			wantWrites := 0
			if kind == "POST rejected" || kind == "readback fails" {
				wantWrites = 1
			}
			if err == nil || len(f.writes) != wantWrites || r.OutcomeUncertain != (wantWrites == 1) || strings.Contains(fmt.Sprint(err), "synthetic") {
				t.Fatalf("unsafe setup continuation: %+v %v %v", r, err, f.writes)
			}
		})
	}
}

func TestPrepareBasicsRootSecretDeprecatedMissingBlocked(t *testing.T) {
	for _, kind := range []string{"secret", "deprecated", "missing deprecation metadata", "missing", "wrong key"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			key := "AGENTS.EXPLORER"
			f.flag("root", key, false)
			switch kind {
			case "secret":
				f.flags["root/"+key]["is_secret"] = true
			case "deprecated":
				f.flags["root/"+key]["deprecated_at"] = "2020-01-01T00:00:00Z"
			case "missing deprecation metadata":
				delete(f.flags["root/"+key], "deprecated_at")
			case "missing":
				delete(f.flags, "root/"+key)
			case "wrong key":
				f.flags["root/"+key]["key"] = "OTHER"
			}
			m := basicsTestManifest()
			m.Flags.Root = map[string]bool{key: true}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if len(f.writes) != 0 || (err == nil && r.Gates.Configuration) {
				t.Fatalf("unsafe root mutation: %+v %v", r, err)
			}
		})
	}
}

func TestPrepareBasicsCanceledContext(t *testing.T) {
	f := newBasicsFixture()
	client, closeServer := f.serve(t)
	defer closeServer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.PrepareBasics(ctx, basicsTestManifest(), true)
	if err == nil || len(f.reads) != 0 || len(f.writes) != 0 {
		t.Fatal("canceled context ignored")
	}
}

func TestPrepareBasicsBoundedPaginationCollectsDuplicates(t *testing.T) {
	f := newBasicsFixture()
	pages := 0
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v2/organization/companies" {
			return false
		}
		offset := pages * 100
		pages++
		if r.URL.Query().Get("api_offset") != fmt.Sprint(offset) {
			t.Error("incorrect pagination offset")
		}
		count := 100
		if offset == 100 {
			count = 1
		}
		data := make([]map[string]any, 0, count)
		for i := 0; i < count; i++ {
			data = append(data, map[string]any{"id": fmt.Sprintf("20000000-0000-0000-0000-%012d", offset+i+1), "name": "Test Company", "slug": "test-company", "domain_emails": []string{"example.test"}, "is_root": false, "status": "ACTIVE"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"count": count, "offset": offset, "limit": 100, "total_count": 101}, "data": data})
		return true
	}
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), basicsTestManifest(), true)
	if err != nil || pages != 2 || len(f.writes) != 0 || len(r.Blockers) != 1 || r.Blockers[0].Reason != "multiple_matches" {
		t.Fatalf("unsafe pagination: %+v %v pages=%d", r, err, pages)
	}
}

func TestPrepareBasicsExplicitUnlimitedQuotaAndNoBU(t *testing.T) {
	f := newBasicsFixture()
	f.plans[0]["max_accounts"] = nil
	f.licenses[0]["max_accounts"] = nil
	m := basicsTestManifest()
	m.BusinessUnit = nil
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), m, false)
	if err != nil || !r.Ready || len(f.writes) != 0 {
		t.Fatalf("unlimited quota should be valid: %+v %v", r, err)
	}
}

func TestPrepareBasicsPlanNeverWrites(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
			t.Error("plan attempted mutation")
		}
		var data []map[string]any
		switch r.URL.Path {
		case "/api/v2/auth/me":
			basicsFixtureJSON(w, map[string]any{"user": map[string]any{"id": basicsAgentID, "is_superuser": true}})
			return
		case "/api/v2/organization/companies":
			if r.URL.Query().Get("slug") != "eq test-company" || r.URL.Query().Get("api_limit") != "100" || r.URL.Query().Get("api_sort") != "id" || r.URL.Query().Get("api_fields") == "" {
				t.Errorf("missing bounded exact query: %s", r.URL.RawQuery)
			}
			data = []map[string]any{}
		case "/api/v2/license-and-billing/pricing-plans":
			data = []map[string]any{{"id": basicsTestManifest().License.PricingPlanId.String(), "is_active": true, "max_accounts": 3, "max_business_units": 1, "max_seats": 1, "max_granted_tokens": 100, "performance_fee_enabled": false, "performance_fee_rate_bps": 0, "price_per_month": 0, "price_per_year": 0}}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"count": len(data), "total_count": len(data), "offset": 0, "limit": 100}, "data": data})
	}))
	defer server.Close()

	result, err := NewSessionClient(server.URL, server.Client()).PrepareBasics(context.Background(), basicsTestManifest(), false)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 0 || result.Ready || len(result.Actions) == 0 {
		t.Fatalf("incorrect plan: %+v; writes=%d", result, writes)
	}
	if result.Actions[0].Status != "planned" {
		t.Fatalf("company should be planned: %+v", result.Actions)
	}
}
