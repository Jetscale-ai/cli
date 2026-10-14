package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFlagsShowEffectiveChains(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(fmt.Sprint(root), func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, root)
			f.flag("company", flagsTestCompany, flagsTestKey, true)
			f.flag("business_unit", flagsTestBU, flagsTestKey, false)
			f.flag("cloud_account", flagsTestAccount, flagsTestKey, true)
			p := flagsTestProfile()
			p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}}
			r, err := f.server(t).ShowFlags(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.writeLog()) != 0 || r.Satisfied {
				t.Fatal("show mutated or incorrectly satisfied")
			}
			want := map[string][2]bool{"root": {root, root}, "company": {true, root}, "business_unit": {false, false}, "cloud_account": {true, false}}
			for _, s := range r.States {
				if s.EntityID != "" && s.EntityID != flagsTestCompany && s.EntityID != flagsTestBU && s.EntityID != flagsTestAccount {
					t.Fatalf("unrequested scope %+v", s)
				}
				if s.Stored == nil || s.Effective == nil || [2]bool{*s.Stored, *s.Effective} != want[s.Scope] {
					t.Fatalf("bad chain %+v", s)
				}
			}
		})
	}
}

func TestFlagsPlanCascadeAndApplyRerun(t *testing.T) {
	f := newFlagsFixture()
	f.flag("root", "", flagsTestKey, true)
	f.flag("business_unit", flagsTestBU2, flagsTestKey, false)
	f.flag("cloud_account", flagsTestAccount2, flagsTestKey, false)
	f.flag("company", flagsTestOtherCompany, flagsTestKey, false)
	f.flag("business_unit", flagsTestOtherBU, flagsTestKey, false)
	f.flag("cloud_account", flagsTestOtherAccount, flagsTestKey, false)
	p := flagsTestProfile()
	p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}}
	client := f.server(t)
	r, err := client.PlanFlags(context.Background(), p)
	if err != nil || len(r.Blockers) != 0 {
		t.Fatalf("plan %+v %v", r, err)
	}
	if len(f.writeLog()) != 0 {
		t.Fatal("plan mutated")
	}
	if len(r.Actions) != 6 {
		t.Fatalf("not full cascade: %+v", r.Actions)
	}
	cascade := 0
	for _, a := range r.Actions {
		if a.Operation == "cascade_enable" {
			cascade++
			if !a.Cascade || a.Status != "planned" {
				t.Fatal(a)
			}
		}
	}
	if cascade != 4 {
		t.Fatalf("expected four descendants: %d", cascade)
	}
	r2, err := client.PlanFlags(context.Background(), p)
	if err != nil || !reflect.DeepEqual(r, r2) {
		t.Fatal("non-deterministic plan")
	}
	r, err = client.ApplyFlags(context.Background(), p)
	if err != nil || !r.Satisfied || r.OutcomeUncertain {
		t.Fatalf("apply %+v %v", r, err)
	}
	if writes := f.writeLog(); len(writes) != 1 || writes[0].Scope != "company" {
		t.Fatalf("double-written cascade: %+v", writes)
	}
	for _, a := range r.Actions {
		if a.Status != "applied" && a.Status != "noop" {
			t.Fatal(a)
		}
	}
	r, err = client.ApplyFlags(context.Background(), p)
	if err != nil || !r.Satisfied || len(f.writeLog()) != 1 {
		t.Fatalf("rerun mutated %+v %v", r, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ref := range [][2]string{{"company", flagsTestOtherCompany}, {"business_unit", flagsTestOtherBU}, {"cloud_account", flagsTestOtherAccount}} {
		if f.rows[flagsFixtureRowKey(ref[0], ref[1], flagsTestKey)]["value"] != false {
			t.Fatal("other tenant changed")
		}
	}
	for _, read := range f.reads {
		if strings.Contains(read, flagsTestOtherCompany) || strings.Contains(read, flagsTestOtherBU) || strings.Contains(read, flagsTestOtherAccount) {
			t.Fatal("unrelated tenant read")
		}
	}
}

func TestFlagsAbsentRootBootstrap(t *testing.T) {
	f := newFlagsFixture()
	client := f.server(t)
	r, err := client.ApplyFlags(context.Background(), flagsTestProfile())
	if err != nil || !r.Satisfied {
		t.Fatalf("bootstrap %+v %v", r, err)
	}
	writes := f.writeLog()
	if len(writes) != 2 || writes[0].Scope != "root" || writes[0].Method != "POST" || writes[1].Scope != "company" || writes[1].Method != "POST" {
		t.Fatalf("incorrect bootstrap %+v", writes)
	}
	if !r.RootApplicationWide {
		t.Fatal("missing global warning")
	}
}

func TestFlagsEnabledParentRecoveryNoCascade(t *testing.T) {
	for _, childPresent := range []bool{false, true} {
		t.Run(fmt.Sprint(childPresent), func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, true)
			f.flag("company", flagsTestCompany, flagsTestKey, true)
			if childPresent {
				f.flag("business_unit", flagsTestBU, flagsTestKey, false)
			}
			p := flagsTestProfile()
			p.CompanyCascade = ""
			p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}}
			r, err := f.server(t).ApplyFlags(context.Background(), p)
			if err != nil || !r.Satisfied {
				t.Fatalf("recovery %+v %v", r, err)
			}
			writes := f.writeLog()
			if len(writes) != 2 || writes[0].Scope != "business_unit" || writes[1].Scope != "cloud_account" {
				t.Fatalf("recascaded parent %+v", writes)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.rows[flagsFixtureRowKey("business_unit", flagsTestBU2, flagsTestKey)] != nil || f.rows[flagsFixtureRowKey("cloud_account", flagsTestAccount2, flagsTestKey)] != nil {
				t.Fatal("unselected child changed")
			}
		})
	}
}

func TestFlagsConsentRefusalAndMissingParents(t *testing.T) {
	for _, kind := range []string{"consent", "root", "company", "bu"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlagsFixture()
			p := flagsTestProfile()
			f.flag("root", "", flagsTestKey, true)
			switch kind {
			case "consent":
				p.CompanyCascade = ""
			case "root":
				delete(f.rows, flagsFixtureRowKey("root", "", flagsTestKey))
				p.Flags.Root = nil
			case "company":
				p.Flags.Company = nil
			case "bu":
				f.flag("company", flagsTestCompany, flagsTestKey, true)
				p.Flags.BusinessUnit = nil
				p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(flagsTestAccount), Flags: map[string]bool{flagsTestKey: true}}}
			}
			r, err := f.server(t).ApplyFlags(context.Background(), p)
			if err != nil || len(r.Blockers) == 0 || r.Satisfied || len(f.writeLog()) != 0 {
				t.Fatalf("unsafe enable %+v %v", r, err)
			}
		})
	}
}

func TestFlagsSelectedAccountMembership(t *testing.T) {
	for _, id := range []string{flagsTestOtherAccount, flagsTestAccount2, "51000000-0000-0000-0000-000000000099"} {
		t.Run(id, func(t *testing.T) {
			f := newFlagsFixture()
			p := flagsTestProfile()
			p.Flags.CloudAccounts = []FlagsAccountLayer{{ID: uuid.MustParse(id), Flags: map[string]bool{flagsTestKey: true}}}
			r, err := f.server(t).ApplyFlags(context.Background(), p)
			if err == nil || len(r.Blockers) == 0 || len(f.writeLog()) != 0 {
				t.Fatalf("membership not rejected %+v %v", r, err)
			}
		})
	}
}

func TestFlagsUnsafeRowsAndVisibility(t *testing.T) {
	for _, kind := range []string{"root_secret", "root_deprecated", "child_secret", "visibility", "missing_visibility"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, true)
			switch kind {
			case "root_secret":
				f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]["is_secret"] = true
			case "root_deprecated":
				f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]["deprecated_at"] = "2026-01-01T00:00:00Z"
			case "child_secret":
				f.flag("business_unit", flagsTestBU, flagsTestKey, false)
				f.rows[flagsFixtureRowKey("business_unit", flagsTestBU, flagsTestKey)]["is_secret"] = true
			case "visibility":
				f.superuser = false
			case "missing_visibility":
				f.superuser = nil
			}
			r, err := f.server(t).ApplyFlags(context.Background(), flagsTestProfile())
			if len(r.Blockers) == 0 || r.Satisfied || len(f.writeLog()) != 0 {
				t.Fatalf("unsafe rows permitted %+v %v", r, err)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), flagsTestKey) && strings.Contains(kind, "secret") {
				t.Fatalf("secret label echoed %s", encoded)
			}
		})
	}
}

func TestFlagsAmbiguousWriteStops(t *testing.T) {
	for _, kind := range []string{"rejected", "timeout", "partial", "readback_error"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlagsFixture()
			p := flagsTestProfile()
			p.Flags.Root["CLOUD.FOCUS"] = true
			seenWrite := false
			f.partial = kind == "partial"
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != "GET" && !seenWrite && kind != "partial" {
					seenWrite = true
					if kind == "readback_error" {
						return false
					}
					if kind == "timeout" {
						select {
						case <-r.Context().Done():
						case <-time.After(200 * time.Millisecond):
						}
						return true
					}
					f.flag("root", "", flagsTestKey, true)
					w.WriteHeader(500)
					_, _ = w.Write([]byte("DO_NOT_ECHO_SECRET"))
					return true
				}
				if kind == "readback_error" && seenWrite && strings.Contains(r.URL.Path, "by-key") {
					w.WriteHeader(503)
					return true
				}
				return false
			}
			ctx := context.Background()
			cancel := func() {}
			if kind == "timeout" {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			}
			defer cancel()
			r, err := f.server(t).ApplyFlags(ctx, p)
			if err == nil || !r.OutcomeUncertain || r.Satisfied {
				t.Fatalf("ambiguous success %+v %v", r, err)
			}
			uncertain := 0
			for _, a := range r.Actions {
				if a.Status == "outcome_uncertain" {
					uncertain++
				}
				if a.Status == "planned" {
					t.Fatal("later action not stopped")
				}
			}
			wantUncertain := 1
			if kind == "partial" {
				wantUncertain = 5
			}
			if uncertain != wantUncertain {
				t.Fatalf("uncertain count %d", uncertain)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), "DO_NOT_ECHO_SECRET") || strings.Contains(err.Error(), "DO_NOT_ECHO_SECRET") {
				t.Fatal("response leaked")
			}
			if kind == "partial" {
				if len(f.writeLog()) != 3 {
					t.Fatalf("partial writes %+v", f.writeLog())
				}
			} else if len(f.writeLog()) > 1 {
				t.Fatal("later mutation occurred")
			}
		})
	}
}

func TestFlagsPrewriteDrift(t *testing.T) {
	for _, kind := range []string{"topology", "flag", "metadata", "selector"} {
		t.Run(kind, func(t *testing.T) {
			f := newFlagsFixture()
			f.flag("root", "", flagsTestKey, false)
			meReads := 0
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path == "/api/v2/auth/me" {
					meReads++
					if meReads == 2 {
						switch kind {
						case "topology":
							f.units[0]["company"] = flagsTestOtherCompany
						case "flag":
							f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]["value"] = true
						case "metadata":
							f.rows[flagsFixtureRowKey("root", "", flagsTestKey)]["description"] = "drifted"
						case "selector":
							f.companies[0]["name"] = "Changed"
						}
					}
				}
				return false
			}
			r, err := f.server(t).ApplyFlags(context.Background(), flagsTestProfile())
			if err == nil || r.OutcomeUncertain || len(f.writeLog()) != 0 || len(r.Blockers) == 0 {
				t.Fatalf("drift mutated %+v %v", r, err)
			}
		})
	}
}
