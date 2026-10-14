package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFlagsDuplicateBusinessUnitNamesUseExactSelector(t *testing.T) {
	for _, selected := range []struct{ id, slug string }{{flagsTestBU, "test-clouds"}, {flagsTestBU2, "second-clouds"}} {
		t.Run(selected.slug, func(t *testing.T) {
			f := newFlagsFixture()
			f.units[1]["name"] = "Test Clouds"
			f.flag("root", "", flagsTestKey, true)
			f.flag("company", flagsTestCompany, flagsTestKey, true)
			f.flag("business_unit", flagsTestBU, flagsTestKey, false)
			f.flag("business_unit", flagsTestBU2, flagsTestKey, false)
			p := flagsTestProfile()
			p.BusinessUnit.Slug = selected.slug
			p.CompanyCascade = ""
			result, err := f.server(t).ApplyFlags(context.Background(), p)
			if err != nil || !result.Satisfied || result.BusinessUnitID != selected.id || len(result.Blockers) > 0 {
				t.Fatalf("distinct-slug BU rejected: %+v %v", result, err)
			}
			writes := f.writeLog()
			if len(writes) != 1 || writes[0].Scope != "business_unit" || writes[0].ID != selected.id {
				t.Fatalf("wrong BU selected: %+v", writes)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, id := range []string{flagsTestBU, flagsTestBU2} {
				if id != selected.id && f.rows[flagsFixtureRowKey("business_unit", id, flagsTestKey)]["value"] != false {
					t.Fatal("unselected same-name BU changed")
				}
			}
		})
	}
}

func TestFlagsPaginationAndCollectionBoundaries(t *testing.T) {
	for _, kind := range []string{"two_pages", "duplicate_id", "duplicate_selector", "foreign_unit", "foreign_account", "count", "missing_meta", "total_drift", "empty_page", "too_many", "combined_bound", "oversize", "read_error", "bad_flag", "duplicate_flag", "root_link"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, true)
			if kind == "two_pages" || kind == "total_drift" || kind == "empty_page" || kind == "combined_bound" {
				f.units = f.units[:0]
				for i := 0; i < 60; i++ {
					f.units = append(f.units, map[string]any{"id": fmt.Sprintf("42000000-0000-0000-0000-%012d", i+1), "company": flagsTestCompany, "name": fmt.Sprintf("Clouds %d", i), "slug": fmt.Sprintf("clouds-%d", i)})
				}
				f.units[0]["name"], f.units[0]["slug"] = "Test Clouds", "test-clouds"
				f.accounts = []map[string]any{}
				if kind == "combined_bound" {
					for i := 0; i < 41; i++ {
						f.accounts = append(f.accounts, map[string]any{"id": fmt.Sprintf("52000000-0000-0000-0000-%012d", i+1), "business_unit": f.units[0]["id"]})
					}
				}
			}
			switch kind {
			case "duplicate_id":
				f.units[1]["id"] = flagsTestBU
			case "duplicate_selector":
				f.units[1]["slug"] = "test-clouds"
			case "duplicate_flag":
				f.flag("company", flagsTestCompany, flagsTestKey, false)
				f.rows[flagsFixtureRowKey("company", flagsTestCompany, flagsTestKey)]["id"] = f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]["id"]
			case "root_link":
				f.flag("company", flagsTestCompany, flagsTestKey, false)
				f.rows[flagsFixtureRowKey("company", flagsTestCompany, flagsTestKey)]["root_feature_flag"] = uuid.New().String()
			case "bad_flag":
				delete(f.rows[flagsFixtureRowKey("root", "", flagsTestKey)], "is_secret")
			}
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v2/organization/business-units" {
					switch kind {
					case "foreign_unit":
						flagsFixtureList(w, r, []map[string]any{f.units[2]})
						return true
					case "count", "missing_meta", "too_many", "empty_page", "total_drift":
						if (kind == "total_drift" || kind == "empty_page") && r.URL.Query().Get("api_offset") == "0" {
							return false
						}
						meta := map[string]any{"count": 0, "total_count": 0, "offset": 0, "limit": 50}
						if kind == "count" {
							meta["count"] = 1
						}
						if kind == "missing_meta" {
							delete(meta, "total_count")
						}
						if kind == "too_many" {
							meta["total_count"] = 101
						}
						if kind == "empty_page" {
							meta["total_count"], meta["offset"] = 60, 50
						}
						if kind == "total_drift" {
							meta["total_count"], meta["offset"] = 61, 50
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": meta})
						return true
					case "oversize":
						_, _ = w.Write([]byte(strings.Repeat(" ", basicsMaxBody+1)))
						return true
					case "read_error":
						w.WriteHeader(403)
						_, _ = w.Write([]byte("PRIVATE_RESPONSE"))
						return true
					}
				}
				if kind == "foreign_account" && r.URL.Path == "/api/v2/cloud/cloud-accounts" {
					flagsFixtureList(w, r, []map[string]any{f.accounts[2]})
					return true
				}
				return false
			}
			r, err := f.server(t).PlanFlags(context.Background(), flagsTestProfile())
			if kind == "two_pages" {
				if err != nil || len(r.Blockers) > 0 || len(r.Actions) != 62 {
					t.Fatalf("pagination %+v %v", r, err)
				}
			} else if err == nil || len(r.Blockers) == 0 {
				t.Fatalf("boundary not rejected %+v %v", r, err)
			}
			if len(f.writeLog()) != 0 {
				t.Fatal("plan mutated")
			}
		})
	}
}

func TestFlagsCancelledBeforeInspection(t *testing.T) {
	f := newFlagsFixture()
	client := f.server(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := client.ApplyFlags(ctx, flagsTestProfile())
	if err == nil || r.OutcomeUncertain || len(f.writeLog()) != 0 {
		t.Fatalf("cancelled %+v %v", r, err)
	}
}

func TestFlagsPartialCascadeReadRecovery(t *testing.T) {
	f := newFlagsFixture()
	f.partial = true
	f.flag("root", "", flagsTestKey, true)
	client := f.server(t)
	p := flagsTestProfile()
	p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}}
	r, err := client.ApplyFlags(context.Background(), p)
	if err == nil || !r.OutcomeUncertain || len(f.writeLog()) != 1 {
		t.Fatalf("partial %+v %v", r, err)
	}
	parentObserved := false
	for _, s := range r.States {
		if s.Scope == "company" && s.Stored != nil && *s.Stored {
			parentObserved = true
		}
	}
	if !parentObserved {
		t.Fatal("committed parent not reconciled")
	}
	p.CompanyCascade = ""
	r, err = client.ApplyFlags(context.Background(), p)
	if err != nil || !r.Satisfied {
		t.Fatalf("recovery %+v %v", r, err)
	}
	writes := f.writeLog()
	if len(writes) != 3 || writes[1].Scope != "business_unit" || writes[2].Scope != "cloud_account" {
		t.Fatalf("parent reissued %+v", writes)
	}
}

func TestFlagsSecretDriftRedactsOutput(t *testing.T) {
	for _, stage := range []string{"prewrite", "readback", "prewrite_malformed", "readback_malformed", "prewrite_child_duplicate", "readback_child_duplicate", "prewrite_visibility", "readback_visibility"} {
		t.Run(stage, func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, false)
			if strings.HasSuffix(stage, "_child_duplicate") {
				f.flag("business_unit", flagsTestBU, flagsTestKey, false)
			}
			reads := 0
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v2/auth/me" {
					reads++
					target := 2
					if strings.HasPrefix(stage, "readback") {
						target = 3
					}
					if reads == target {
						if strings.HasSuffix(stage, "_visibility") {
							f.superuser = false
						}
						row := f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]
						if strings.HasSuffix(stage, "_child_duplicate") {
							child := f.rows[flagsFixtureRowKey("business_unit", flagsTestBU, flagsTestKey)]
							child["id"] = row["id"]
							row = child
						}
						row["is_secret"] = true
						if strings.HasSuffix(stage, "_malformed") {
							delete(row, "value")
						}
					}
				}
				return false
			}
			r, err := f.server(t).ApplyFlags(context.Background(), flagsTestProfile())
			if err == nil {
				t.Fatal("secret drift not rejected")
			}
			body, _ := json.Marshal(r)
			if strings.Contains(string(body), flagsTestKey) {
				t.Fatalf("secret drift echoed label %s", body)
			}
		})
	}
}

func TestFlagsAmbiguousCompanyRedactionKeepsActionBoundaries(t *testing.T) {
	f := newFlagsFixture()
	p := flagsTestProfile()
	p.Flags.Company["CLOUD.FOCUS"] = true
	f.flag("root", "", flagsTestKey, true)
	f.flag("root", "", "CLOUD.FOCUS", true)
	client := f.server(t)
	plan, err := client.PlanFlags(context.Background(), p)
	if err != nil || len(plan.Blockers) > 0 {
		t.Fatalf("plan rejected: %v", err)
	}
	f.mu.Lock()
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if len(f.writes) > 0 && r.URL.Path == "/api/v2/auth/me" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	}
	f.mu.Unlock()
	result, err := client.ApplyFlags(context.Background(), p)
	if err == nil || !result.OutcomeUncertain || len(f.writeLog()) != 1 || len(result.Actions) != len(plan.Actions) {
		t.Fatalf("ambiguous company not stopped: %+v %v", result, err)
	}
	for i, action := range result.Actions {
		before := plan.Actions[i]
		if before.Key == "CLOUD.FOCUS" && before.Operation == "cascade_enable" && action.Status != "blocked" {
			t.Fatalf("unattempted cascade marked uncertain: %+v", action)
		}
	}
	body, _ := json.Marshal(result)
	if strings.Contains(string(body), flagsTestKey) || strings.Contains(string(body), "CLOUD.FOCUS") {
		t.Fatal("unproved labels echoed")
	}
}

func TestFlagsValidationAndJSONContract(t *testing.T) {
	for _, kind := range []string{"unknown", "disable", "nil_id", "duplicate_account", "empty", "version", "selector", "bu_selector", "cascade"} {
		t.Run(kind, func(t *testing.T) {
			p := flagsTestProfile()
			switch kind {
			case "unknown":
				p.Flags.Root["SECRET.DO_NOT_ECHO"] = true
			case "disable":
				p.Flags.Company[flagsTestKey] = false
			case "nil_id":
				p.Flags.CloudAccounts = []FlagsAccountLayer{{Flags: map[string]bool{flagsTestKey: true}}}
			case "duplicate_account":
				a := FlagsAccountLayer{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}
				p.Flags.CloudAccounts = []FlagsAccountLayer{a, a}
			case "empty":
				p.Flags = FlagsLayers{}
			case "version":
				p.Version = 2
			case "selector":
				p.Company.Name = ""
			case "bu_selector":
				p.BusinessUnit = nil
			case "cascade":
				p.CompanyCascade = "selected_only"
			}
			f := newFlagsFixture()
			r, err := f.server(t).ApplyFlags(context.Background(), p)
			if err == nil || len(f.writeLog()) != 0 || len(f.reads) != 0 {
				t.Fatal("invalid profile used network")
			}
			body, _ := json.Marshal(r)
			if strings.Contains(string(body), "SECRET.DO_NOT_ECHO") || strings.Contains(err.Error(), "SECRET.DO_NOT_ECHO") {
				t.Fatal("unknown key echoed")
			}
		})
	}
	p := flagsTestProfile()
	for _, key := range []string{"CLOUD.FOCUS", "CLOUD.AZURE.FOCUS", "INTEGRATIONS.AABHRA"} {
		p.Flags.Root[key] = true
	}
	body, _ := json.Marshal(p)
	var roundtrip FlagsProfile
	if json.Unmarshal(body, &roundtrip) != nil || ValidateFlagsProfile(roundtrip) != nil || roundtrip.CompanyCascade != "all_descendants" {
		t.Fatal("profile JSON contract")
	}
	resultBody, _ := json.Marshal(flagsResult("show"))
	var result map[string]any
	_ = json.Unmarshal(resultBody, &result)
	for _, key := range []string{"mode", "company_id", "business_unit_id", "satisfied", "outcome_uncertain", "states", "actions", "blockers", "root_application_wide"} {
		if _, ok := result[key]; !ok {
			t.Fatalf("missing JSON field %s", key)
		}
	}
}
