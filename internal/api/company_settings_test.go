package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	settingsCompanyID   = "11111111-1111-4111-8111-111111111111"
	settingsRowID       = "22222222-2222-4222-8222-222222222222"
	settingsOrgKey      = "WORKOS_SSO_ORG_ID_COMPANY_SETTING_KEY"
	settingsProviderKey = "WORKOS_SSO_PROVIDER_TYPE_COMPANY_SETTING_KEY"
	settingsRequiredKey = "WORKOS_SSO_REQUIRED_COMPANY_SETTING_KEY"
)

func reconciliationClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewSessionClient(server.URL, server.Client()).Resources()
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func settingFixture(key, kind string, value any) map[string]any {
	return map[string]any{"id": settingsRowID, "company": settingsCompanyID, "key": key, "value_type": kind, "value": map[string]any{"value": value}, "description": "synthetic-secret-not-for-output"}
}

func reconciliationJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func TestCompanySettingCreateThenNoop(t *testing.T) {
	var row map[string]any
	writes := 0
	client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/system/company-settings/by-key/" + settingsOrgKey:
			if r.URL.Query().Get("company_id") != settingsCompanyID {
				t.Error("read not scoped to company")
			}
			if row == nil {
				reconciliationJSON(w, 404, nil)
				return
			}
			reconciliationJSON(w, 200, row)
		case "POST /api/v2/system/company-settings":
			writes++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 4 || body["company"] != settingsCompanyID || body["key"] != settingsOrgKey || body["value_type"] != "string" {
				t.Errorf("wrong create: %v", body)
			}
			value, _ := body["value"].(map[string]any)
			if len(value) != 1 || value["value"] != "org_fixture" {
				t.Errorf("wrong nesting: %v", body)
			}
			row = settingFixture(settingsOrgKey, "string", "org_fixture")
			reconciliationJSON(w, 201, row)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	for _, want := range []string{StatusCreated, StatusNoop} {
		result, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, "org_fixture", false)
		if err != nil || result.Status != want || result.ID != settingsRowID {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		raw, _ := json.Marshal(result)
		if string(raw) == "" || result.Details["value"] != "org_fixture" {
			t.Fatalf("missing projected result: %s", raw)
		}
	}
	if writes != 1 {
		t.Fatalf("expected one create, got %d", writes)
	}
}

func TestCompanySettingDriftAndDryRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		row    map[string]any
		status string
		drift  string
	}{
		{"missing", nil, StatusPlanned, ""},
		{"value", settingFixture(settingsOrgKey, "string", "other_org"), StatusDrift, "value"},
		{"type", settingFixture(settingsOrgKey, "boolean", false), StatusDrift, "value_type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("unexpected write: %s", r.Method)
				}
				if tc.row == nil {
					reconciliationJSON(w, 404, nil)
				} else {
					reconciliationJSON(w, 200, tc.row)
				}
			})
			result, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, "org_fixture", true)
			if err != nil || result.Status != tc.status {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.drift != "" && (len(result.Drift) != 1 || result.Drift[0] != tc.drift) {
				t.Fatalf("drift=%v", result.Drift)
			}
			if tc.row == nil && result.Details["operation"] != "create" {
				t.Fatalf("missing create plan: %+v", result)
			}
			raw, _ := json.Marshal(result)
			if strings.Contains(string(raw), "synthetic-secret") {
				t.Fatalf("raw body leaked: %s", raw)
			}
		})
	}
}

func TestCompanySettingRequiredPolicyProjection(t *testing.T) {
	for _, tc := range []struct {
		kind     string
		value    any
		required bool
	}{
		{"boolean", true, true}, {"boolean", false, false}, {"string", " TRUE ", true}, {"string", "false", false},
	} {
		t.Run(tc.kind+"/"+fmtValue(tc.value), func(t *testing.T) {
			client := reconciliationClient(t, func(w http.ResponseWriter, _ *http.Request) {
				reconciliationJSON(w, 200, settingFixture(settingsRequiredKey, tc.kind, tc.value))
			})
			row, err := client.GetCompanySetting(context.Background(), settingsCompanyID, settingsRequiredKey)
			if err != nil || row["required"] != tc.required || row["value"] != tc.value {
				t.Fatalf("row=%+v err=%v", row, err)
			}
			if len(row) != 6 {
				t.Fatalf("unprojected row: %+v", row)
			}
		})
	}
}

func TestCompanySettingExactStringsAndDefinitiveRejection(t *testing.T) {
	client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("drift must not write")
		}
		reconciliationJSON(w, 200, settingFixture(settingsProviderKey, "string", "GoogleOAuth"))
	})
	for _, value := range []string{"googleoauth", " GoogleOAuth "} {
		result, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsProviderKey, value, false)
		if err != nil || result.Status != StatusDrift {
			t.Fatalf("not exact: %+v %v", result, err)
		}
	}
	writes := 0
	client = reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			reconciliationJSON(w, 404, nil)
			return
		}
		writes++
		reconciliationJSON(w, 403, map[string]any{"secret": "synthetic-secret"})
	})
	_, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, "org_fixture", false)
	var rejected *APIError
	if !errors.As(err, &rejected) || rejected.Status != 403 || writes != 1 || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("err=%v writes=%d", err, writes)
	}
}

func fmtValue(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestCompanySettingRejectsMalformedOrForeignRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"foreign-company", func(r map[string]any) { r["company"] = settingsRowID }},
		{"wrong-key", func(r map[string]any) { r["key"] = settingsProviderKey }},
		{"invalid-id", func(r map[string]any) { r["id"] = "synthetic-secret" }},
		{"missing-value", func(r map[string]any) { delete(r, "value") }},
		{"extra-nesting", func(r map[string]any) {
			r["value"] = map[string]any{"value": map[string]any{"secret": "synthetic-secret"}}
		}},
		{"extra-field", func(r map[string]any) {
			r["value"] = map[string]any{"value": "org_fixture", "secret": "synthetic-secret"}
		}},
		{"scalar", func(r map[string]any) { r["value"] = "synthetic-secret" }},
		{"unknown-type", func(r map[string]any) { r["value_type"] = "secret" }},
		{"mismatched-type", func(r map[string]any) { r["value_type"] = "boolean" }},
		{"control-character", func(r map[string]any) { r["value"] = map[string]any{"value": "org\u001b[31m"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := settingFixture(settingsOrgKey, "string", "org_fixture")
			tc.change(row)
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("invalid row triggered write")
				}
				reconciliationJSON(w, 200, row)
			})
			result, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, "org_fixture", false)
			if !errors.Is(err, ErrInvalidResponse) || result.Details != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
	client := reconciliationClient(t, func(w http.ResponseWriter, _ *http.Request) {
		reconciliationJSON(w, 200, settingFixture(settingsRequiredKey, "string", "unknown-policy"))
	})
	if _, err := client.GetCompanySetting(context.Background(), settingsCompanyID, settingsRequiredKey); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("unknown policy: %v", err)
	}
}

func TestCompanySettingRejectsInputsWithoutRequests(t *testing.T) {
	client := reconciliationClient(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid input sent a request") })
	for _, key := range []string{"SECRET_KEY", "unknown", settingsRequiredKey} {
		if _, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, key, "org_fixture", false); err == nil || strings.Contains(err.Error(), key) {
			t.Fatalf("unsafe key error: %v", err)
		}
	}
	for _, value := range []string{"", " ", strings.Repeat("x", 1025), "value\n"} {
		if _, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, value, false); err == nil {
			t.Fatal("accepted invalid value")
		}
	}
	if _, err := client.GetCompanySetting(context.Background(), settingsCompanyID, "SECRET_KEY"); err == nil {
		t.Fatal("accepted secret key")
	}
	if _, err := client.GetCompanySetting(context.Background(), "not-a-uuid", settingsOrgKey); err == nil {
		t.Fatal("accepted invalid company")
	}
}

func TestCompanySettingUncertainWriteStops(t *testing.T) {
	for _, mode := range []string{"gateway", "invalid-create", "missing-readback", "different-readback"} {
		t.Run(mode, func(t *testing.T) {
			writes := 0
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes++
					if mode == "gateway" {
						reconciliationJSON(w, 504, nil)
						return
					}
					if mode == "invalid-create" {
						reconciliationJSON(w, 201, map[string]any{"secret": "synthetic-secret"})
						return
					}
					reconciliationJSON(w, 201, settingFixture(settingsOrgKey, "string", "org_fixture"))
					return
				}
				if writes == 0 || mode == "missing-readback" {
					reconciliationJSON(w, 404, nil)
					return
				}
				row := settingFixture(settingsOrgKey, "string", "org_fixture")
				row["id"] = settingsCompanyID
				reconciliationJSON(w, 200, row)
			})
			_, err := client.EnsureCompanySetting(context.Background(), settingsCompanyID, settingsOrgKey, "org_fixture", false)
			if !errors.Is(err, ErrUncertain) || writes != 1 {
				t.Fatalf("err=%v writes=%d", err, writes)
			}
		})
	}
}
