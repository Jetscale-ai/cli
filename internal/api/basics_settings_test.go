package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestPrepareBasicsSettingsCreatePatchThenNoop(t *testing.T) {
	for _, scope := range []string{"root", "company", "business_unit"} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", scope, existing), func(t *testing.T) {
				f := newBasicsFixture()
				if existing {
					f.setting(scope, "recommendation_temperature", "number", 0.1)
				}
				m := basicsTestManifest()
				settings := map[string]BasicsSetting{"recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
				switch scope {
				case "root":
					m.Settings.Root = settings
				case "company":
					m.Settings.Company = settings
				case "business_unit":
					m.Settings.BusinessUnit = settings
				}
				client, closeServer := f.serve(t)
				defer closeServer()
				plan, err := client.PrepareBasics(context.Background(), m, false)
				if err != nil {
					t.Fatal(err)
				}
				if len(f.writes) != 0 || plan.Actions[len(plan.Actions)-1].Status != "planned" {
					t.Fatalf("bad setting plan: %+v", plan)
				}
				for range 2 {
					r, applyErr := client.PrepareBasics(context.Background(), m, true)
					if applyErr != nil || !r.Gates.Configuration {
						t.Fatalf("setting apply: %+v %v", r, applyErr)
					}
					encoded, _ := json.Marshal(r)
					if strings.Contains(string(encoded), `"value"`) || strings.Contains(string(encoded), "0.2") {
						t.Fatal("setting values escaped result")
					}
				}
				if len(f.writes) != 1 {
					t.Fatalf("non-idempotent settings: %v", f.writes)
				}
				method := "POST"
				if existing {
					method = "PATCH"
				}
				if !strings.HasPrefix(f.writes[0], method+" ") {
					t.Fatalf("wrong mutation: %v", f.writes)
				}
			})
		}
	}
}

func TestPrepareBasicsRootSettingWithCompanyOverride(t *testing.T) {
	for _, existing := range []string{"missing", "different", "matching"} {
		t.Run(existing, func(t *testing.T) {
			f := newBasicsFixture()
			f.setting("company", "recommendation_temperature", "number", 0.9)
			if existing != "missing" {
				value := 0.1
				if existing == "matching" {
					value = 0.2
				}
				f.setting("root", "recommendation_temperature", "number", value)
			}
			m := basicsTestManifest()
			m.Settings.Root = map[string]BasicsSetting{"recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
			client, closeServer := f.serve(t)
			defer closeServer()
			for range 2 {
				r, err := client.PrepareBasics(context.Background(), m, true)
				if err != nil || !r.Gates.Configuration || r.OutcomeUncertain {
					t.Fatalf("caller override broke root verification: %+v %v", r, err)
				}
				encoded, _ := json.Marshal(r)
				if strings.Contains(string(encoded), `"value"`) || strings.Contains(string(encoded), "0.9") || strings.Contains(string(encoded), "0.2") {
					t.Fatal("chain values escaped result")
				}
			}
			wantWrites := 1
			if existing == "matching" {
				wantWrites = 0
			}
			if len(f.writes) != wantWrites || f.settings["company/recommendation_temperature"]["value"].(map[string]any)["value"] != 0.9 {
				t.Fatalf("root reconciliation modified company or repeated write: %v", f.writes)
			}
		})
	}
}

func TestPrepareBasicsRootSettingChainDriftBlocks(t *testing.T) {
	for _, kind := range []string{"missing chain", "missing root", "duplicate root", "missing ID", "wrong ID", "wrong source", "wrong value", "object value"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			f.setting("root", "recommendation_temperature", "number", 0.2)
			f.hook = func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v2/settings/effective/recommendation_temperature" {
					return false
				}
				root := map[string]any{"scope": "root", "value": 0.2, "source": "root", "setting_id": f.settings["root/recommendation_temperature"]["id"], "applies": true}
				chain := []map[string]any{root}
				switch kind {
				case "missing chain":
					chain = nil
				case "missing root":
					root["scope"] = "company"
				case "duplicate root":
					chain = append(chain, root)
				case "missing ID":
					delete(root, "setting_id")
				case "wrong ID":
					root["setting_id"] = basicsBUID
				case "wrong source":
					root["source"] = "unset"
				case "wrong value":
					root["value"] = 0.9
				case "object value":
					root["value"] = map[string]any{"secret": "synthetic-secret"}
				}
				basicsFixtureJSON(w, map[string]any{"key": "recommendation_temperature", "value_type": "number", "scope": "root", "value": 0.2, "scope_chain": chain})
				return true
			}
			m := basicsTestManifest()
			m.Settings.Root = map[string]BasicsSetting{"recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
			m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if len(f.writes) != 0 || (err == nil && r.Gates.Configuration) {
				t.Fatalf("invalid chain allowed writes: %+v %v %v", r, err, f.writes)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded)+fmt.Sprint(err), "synthetic-secret") {
				t.Fatal("invalid chain leaked value")
			}
		})
	}
}

func TestPrepareBasicsSettingsDriftBlocks(t *testing.T) {
	for _, kind := range []string{"object", "extra metadata", "wrong scope", "wrong type", "effective mismatch", "concurrent value"} {
		t.Run(kind, func(t *testing.T) {
			f := newBasicsFixture()
			f.setting("company", "recommendation_temperature", "number", 0.2)
			row := f.settings["company/recommendation_temperature"]
			switch kind {
			case "object":
				row["value"] = map[string]any{"value": map[string]any{"secret": "synthetic-secret"}}
			case "extra metadata":
				row["value"] = map[string]any{"value": 0.2, "secret": "synthetic-secret"}
			case "wrong scope":
				row["company"] = basicsBUID
			case "wrong type":
				row["value_type"] = "string"
			case "effective mismatch":
				f.hook = func(w http.ResponseWriter, r *http.Request) bool {
					if strings.Contains(r.URL.Path, "/settings/effective/") {
						basicsFixtureJSON(w, map[string]any{"key": "recommendation_temperature", "value_type": "number", "value": 0.9, "scope": "system"})
						return true
					}
					return false
				}
			case "concurrent value":
				row["value"] = map[string]any{"value": 0.1}
				reads := 0
				f.hook = func(_ http.ResponseWriter, r *http.Request) bool {
					if strings.Contains(r.URL.Path, "company-settings/by-key") {
						reads++
						if reads > 1 {
							row["value"] = map[string]any{"value": 0.3}
						}
					}
					return false
				}
			}
			m := basicsTestManifest()
			m.Settings.Company = map[string]BasicsSetting{"recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
			client, closeServer := f.serve(t)
			defer closeServer()
			r, err := client.PrepareBasics(context.Background(), m, true)
			if len(f.writes) != 0 || (err == nil && r.Gates.Configuration) {
				t.Fatalf("drift written: %+v %v writes=%v", r, err, f.writes)
			}
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded)+fmt.Sprint(err), "synthetic-secret") {
				t.Fatal("response secret escaped")
			}
		})
	}
}

func TestPrepareBasicsSettingReadbackFailureIsUncertain(t *testing.T) {
	f := newBasicsFixture()
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if len(f.writes) > 0 && r.Method == "GET" && strings.Contains(r.URL.Path, "company-settings/by-key") {
			w.WriteHeader(503)
			return true
		}
		return false
	}
	m := basicsTestManifest()
	m.Settings.Company = map[string]BasicsSetting{"enable_caching": {ValueType: "boolean", Value: json.RawMessage(`true`)}, "recommendation_temperature": {ValueType: "number", Value: json.RawMessage(`0.2`)}}
	client, closeServer := f.serve(t)
	defer closeServer()
	r, err := client.PrepareBasics(context.Background(), m, true)
	if err == nil || !r.OutcomeUncertain || len(f.writes) != 1 {
		t.Fatalf("continued after readback failure: %+v %v %v", r, err, f.writes)
	}
}
