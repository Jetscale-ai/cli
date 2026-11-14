package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const capacityLicenseID = "33333333-3333-4333-8333-333333333333"
const capacityPlanID = "44444444-4444-4444-8444-444444444444"

// SHA-256 of the fixture's sorted-key compact JSON, with id/company and all
// preserved fields (including nulls), but without max_seats.
const capacityTermsSHA256 = "9056be3ded6b7396aeef5399adb82242c793d05ff669f9039c977718be3e407b"

func capacityFixture(seats any) map[string]any {
	return map[string]any{
		"id": capacityLicenseID, "company": settingsCompanyID, "pricing_plan": capacityPlanID,
		"status": "ACTIVE", "max_seats": seats, "valid_from": "2020-01-01T00:00:00Z", "valid_until": nil,
		"billing_interval": "YEARLY", "currency_code": "CAD", "price_per_month": 199, "price_per_year": 2388,
		"performance_fee_enabled": true, "performance_fee_rate_bps": 2500,
		"performance_fee_allowance_yearly_cents": 500000, "performance_fee_term_months": 12,
		"stripe_subscription_id": "synthetic-stripe-not-for-output",
		"max_accounts":           3, "max_business_units": 1, "max_granted_tokens": nil,
		"description": "synthetic-secret-not-for-output",
	}
}

func capacityList(w http.ResponseWriter, rows ...map[string]any) {
	if rows == nil {
		rows = []map[string]any{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "meta": map[string]any{"total_count": len(rows), "count": len(rows)}})
}

func TestLicenseCapacityGrowThenNoop(t *testing.T) {
	row := capacityFixture(1)
	writes := 0
	client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v2/license-and-billing/licenses":
			if r.URL.Query().Get("company") != "eq "+settingsCompanyID {
				t.Error("unscoped license list")
			}
			capacityList(w, row)
		case "GET /api/v2/license-and-billing/licenses/" + capacityLicenseID:
			reconciliationJSON(w, 200, row)
		case "PATCH /api/v2/license-and-billing/licenses/" + capacityLicenseID:
			writes++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			if len(payload) != 1 || payload["max_seats"] != float64(2) {
				t.Errorf("not quota-only: %v", payload)
			}
			row["max_seats"] = 2
			reconciliationJSON(w, 200, row)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	for _, want := range []string{StatusPlanned, StatusUpdated, StatusNoop} {
		result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, want == StatusPlanned)
		if err != nil || result.Status != want || result.ID != capacityLicenseID || result.Details["max_seats"] != 2 || result.Details["terms_sha256"] != capacityTermsSHA256 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		raw, _ := json.Marshal(result)
		if strings.Contains(string(raw), "synthetic-") || strings.Contains(string(raw), "price_") {
			t.Fatalf("commercial/raw output: %s", raw)
		}
	}
	if writes != 1 {
		t.Fatalf("expected one patch, got %d", writes)
	}
}

func TestLicenseCapacityTermsFingerprint(t *testing.T) {
	type termsCase struct {
		name      string
		field     string
		value     any
		unchanged bool
	}
	cases := []termsCase{
		{"stripe-link", "stripe_subscription_id", "synthetic-other-subscription", false},
		{"license-id", "id", capacityPlanID, false},
		{"company-id", "company", capacityPlanID, false},
		{"name-null", "name", nil, false},
		{"seat-growth", "max_seats", 5, true},
		{"unlimited-seats", "max_seats", nil, true},
		{"audit-timestamp", "updated_at", "2026-11-01T00:00:00Z", true},
		{"expanded-company", "company", map[string]any{"id": settingsCompanyID, "secret": "synthetic-secret"}, true},
		{"expanded-plan", "pricing_plan", map[string]any{"id": capacityPlanID, "max_seats": 1}, true},
	}
	for _, field := range []string{"currency_code", "price_per_month", "price_per_year", "performance_fee_enabled", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months", "stripe_subscription_id", "max_accounts", "max_business_units", "description"} {
		cases = append(cases, termsCase{name: field + "-null", field: field})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := capacityFixture(1)
			row[tc.field] = tc.value
			companyID := settingsCompanyID
			if company, ok := row["company"].(string); ok {
				companyID = company
			}
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("snapshot wrote")
				}
				if r.URL.Path == "/api/v2/license-and-billing/licenses" {
					capacityList(w, row)
				} else {
					reconciliationJSON(w, 200, row)
				}
			})
			result, err := client.EnsureLicenseCapacity(context.Background(), companyID, 2, true)
			if err != nil {
				t.Fatal(err)
			}
			digest, ok := result.Details["terms_sha256"].(string)
			if !ok || len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" || (digest == capacityTermsSHA256) != tc.unchanged {
				t.Fatalf("unexpected fingerprint: %+v", result)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "synthetic-") || strings.Contains(string(raw), "stripe_subscription_id") || strings.Contains(string(raw), "price_") || strings.Contains(string(raw), "currency_code") || strings.Contains(string(raw), "performance_fee") {
				t.Fatalf("commercial/raw output: %s", raw)
			}
		})
	}
}

func TestLicenseCapacityNullCurrency(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seats  any
		status string
		writes int
	}{
		{"adequate", 5, StatusNoop, 0},
		{"unlimited", nil, StatusNoop, 0},
		{"grow", 1, StatusUpdated, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := capacityFixture(tc.seats)
			row["currency_code"] = nil
			writes := 0
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v2/license-and-billing/licenses":
					capacityList(w, row)
				case "GET /api/v2/license-and-billing/licenses/" + capacityLicenseID:
					reconciliationJSON(w, 200, row)
				case "PATCH /api/v2/license-and-billing/licenses/" + capacityLicenseID:
					writes++
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if len(payload) != 1 || payload["max_seats"] != float64(2) {
						t.Errorf("not quota-only: %v", payload)
					}
					row["max_seats"] = 2
					reconciliationJSON(w, 200, row)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			for _, want := range []string{tc.status, StatusNoop} {
				result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, false)
				if err != nil || result.Status != want || result.ID != capacityLicenseID || result.Details["unlimited"] != (tc.seats == nil) {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				if currency, present := row["currency_code"]; !present || currency != nil || writes != tc.writes {
					t.Fatalf("currency was defaulted or write repeated: row=%v writes=%d", row, writes)
				}
			}
		})
	}
}

func TestLicenseCapacityPlansAndUnlimited(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seats  any
		status string
	}{
		{"grow", 1, StatusPlanned}, {"zero", 0, StatusPlanned}, {"adequate", 2, StatusNoop}, {"larger", 5, StatusNoop}, {"unlimited", nil, StatusNoop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := capacityFixture(tc.seats)
			row["company"] = map[string]any{"id": settingsCompanyID, "secret": "synthetic-secret"}
			row["pricing_plan"] = map[string]any{"id": capacityPlanID, "max_seats": 1}
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("dry-run/noop wrote")
				}
				if r.URL.Path == "/api/v2/license-and-billing/licenses" {
					capacityList(w, row)
				} else {
					reconciliationJSON(w, 200, row)
				}
			})
			result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, true)
			if err != nil || result.Status != tc.status || result.Details["unlimited"] != (tc.seats == nil) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if tc.status == StatusPlanned && (result.Details["operation"] != "grow" || result.Details["max_seats"] != 2) {
				t.Fatalf("wrong plan: %+v", result)
			}
		})
	}
}

func TestLicenseCapacityBlocksInvalidMetadata(t *testing.T) {
	cases := map[string]func(map[string]any){
		"foreign-company":  func(r map[string]any) { r["company"] = capacityPlanID },
		"invalid-id":       func(r map[string]any) { r["id"] = "secret-invalid-id" },
		"inactive":         func(r map[string]any) { r["status"] = "INACTIVE" },
		"unknown-status":   func(r map[string]any) { r["status"] = "unknown" },
		"future":           func(r map[string]any) { r["valid_from"] = time.Now().Add(time.Hour).Format(time.RFC3339) },
		"expired":          func(r map[string]any) { r["valid_until"] = "2020-01-01T00:00:00Z" },
		"bad-date":         func(r map[string]any) { r["valid_from"] = "secret-date" },
		"bad-plan":         func(r map[string]any) { r["pricing_plan"] = nil },
		"bad-interval":     func(r map[string]any) { r["billing_interval"] = "unknown" },
		"bad-currency":     func(r map[string]any) { r["currency_code"] = map[string]any{"secret": "secret"} },
		"number-currency":  func(r map[string]any) { r["currency_code"] = 123 },
		"empty-currency":   func(r map[string]any) { r["currency_code"] = "" },
		"lower-currency":   func(r map[string]any) { r["currency_code"] = "cad" },
		"fractional-seats": func(r map[string]any) { r["max_seats"] = 1.5 },
		"string-seats":     func(r map[string]any) { r["max_seats"] = "1" },
		"negative-seats":   func(r map[string]any) { r["max_seats"] = -1 },
		"overflow-seats":   func(r map[string]any) { r["max_seats"] = 2147483648.0 },
		"bad-price":        func(r map[string]any) { r["price_per_year"] = "secret-price" },
		"bad-fee":          func(r map[string]any) { r["performance_fee_enabled"] = "true" },
	}
	for _, field := range []string{"max_seats", "company", "status", "pricing_plan", "valid_from", "valid_until", "billing_interval", "currency_code", "price_per_month", "price_per_year", "performance_fee_enabled", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months", "stripe_subscription_id", "max_accounts", "max_business_units", "max_granted_tokens"} {
		cases["missing-"+field] = func(r map[string]any) { delete(r, field) }
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			row := capacityFixture(1)
			change(row)
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("invalid metadata caused write")
				}
				if r.URL.Path == "/api/v2/license-and-billing/licenses" {
					capacityList(w, row)
				} else {
					reconciliationJSON(w, 200, row)
				}
			})
			result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, false)
			if err == nil || result.Details != nil || strings.Contains(err.Error(), "secret-") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestLicenseCapacityRequiresSingleActiveLicense(t *testing.T) {
	for _, mode := range []string{"none", "multiple", "historic"} {
		t.Run(mode, func(t *testing.T) {
			row := capacityFixture(2)
			other := capacityFixture(1)
			other["id"] = capacityPlanID
			if mode == "historic" {
				other["status"] = "EXPIRED"
			}
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unexpected write")
				}
				if r.URL.Path == "/api/v2/license-and-billing/licenses" {
					if mode == "none" {
						capacityList(w)
					} else {
						capacityList(w, row, other)
					}
				} else {
					reconciliationJSON(w, 200, row)
				}
			})
			result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, false)
			if mode == "historic" {
				if err != nil || result.Status != StatusNoop {
					t.Fatalf("result=%+v err=%v", result, err)
				}
				return
			}
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("expected selection conflict: %v", err)
			}
		})
	}
}

func TestLicenseCapacityPrewriteAndReadbackDrift(t *testing.T) {
	for _, mode := range []string{"prewrite-quota", "prewrite-terms", "prewrite-currency", "gateway", "response-terms", "response-currency", "readback-terms", "readback-currency", "readback-quota", "readback-failure", "readback-selection"} {
		t.Run(mode, func(t *testing.T) {
			row := capacityFixture(1)
			if strings.HasSuffix(mode, "-currency") {
				row["currency_code"] = nil
			}
			gets, writes := 0, 0
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "PATCH":
					writes++
					if mode == "gateway" {
						reconciliationJSON(w, 502, nil)
						return
					}
					row["max_seats"] = 2
					if mode == "response-terms" {
						row["price_per_year"] = 9999
					}
					if mode == "response-currency" {
						row["currency_code"] = "CAD"
					}
					reconciliationJSON(w, 200, row)
					if mode == "readback-terms" {
						row["performance_fee_rate_bps"] = 5000
					}
					if mode == "readback-currency" {
						row["currency_code"] = "CAD"
					}
					if mode == "readback-quota" {
						row["max_seats"] = 1
					}
				case r.URL.Path == "/api/v2/license-and-billing/licenses":
					if writes > 0 && mode == "readback-selection" {
						capacityList(w)
						return
					}
					capacityList(w, row)
				default:
					gets++
					if gets == 2 && mode == "prewrite-quota" {
						row["max_seats"] = 4
					}
					if gets == 2 && mode == "prewrite-terms" {
						row["pricing_plan"] = settingsRowID
					}
					if gets == 2 && mode == "prewrite-currency" {
						row["currency_code"] = "CAD"
					}
					if writes > 0 && mode == "readback-failure" {
						reconciliationJSON(w, 503, nil)
						return
					}
					reconciliationJSON(w, 200, row)
				}
			})
			_, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, false)
			if strings.HasPrefix(mode, "prewrite-") {
				var conflict *ConflictError
				if !errors.As(err, &conflict) || writes != 0 {
					t.Fatalf("expected prewrite stop: err=%v writes=%d", err, writes)
				}
			} else if !errors.Is(err, ErrUncertain) || writes != 1 {
				t.Fatalf("expected uncertain stop: err=%v writes=%d", err, writes)
			}
		})
	}
}

func TestLicenseCapacityPreservesInheritedCommercialTerms(t *testing.T) {
	row := capacityFixture(1)
	for _, field := range []string{"price_per_month", "price_per_year", "performance_fee_enabled", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months", "stripe_subscription_id", "max_accounts", "max_business_units"} {
		row[field] = nil
	}
	writes := 0
	client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			writes++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 || body["max_seats"] != float64(2147483647) {
				t.Errorf("wrong quota-only patch: %v", body)
			}
			row["max_seats"] = 2147483647
		}
		if r.URL.Path == "/api/v2/license-and-billing/licenses" {
			capacityList(w, row)
		} else {
			reconciliationJSON(w, 200, row)
		}
	})
	result, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2147483647, false)
	if err != nil || result.Status != StatusUpdated || writes != 1 {
		t.Fatalf("result=%+v err=%v writes=%d", result, err, writes)
	}
}

func TestLicenseCapacityLostOrMalformedWriteResponse(t *testing.T) {
	for _, mode := range []string{"transport", "malformed", "foreign", "wrong-id", "wrong-type"} {
		t.Run(mode, func(t *testing.T) {
			row := capacityFixture(1)
			var writes atomic.Int32
			client := reconciliationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "PATCH" {
					if r.URL.Path == "/api/v2/license-and-billing/licenses" {
						capacityList(w, row)
					} else {
						reconciliationJSON(w, 200, row)
					}
					return
				}
				writes.Add(1)
				if mode == "transport" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				if mode == "malformed" {
					reconciliationJSON(w, 200, map[string]any{"secret": "synthetic-secret"})
					return
				}
				row["max_seats"] = 2
				switch mode {
				case "foreign":
					row["company"] = capacityPlanID
				case "wrong-id":
					row["id"] = capacityPlanID
				case "wrong-type":
					row["max_seats"] = "2"
				}
				reconciliationJSON(w, 200, row)
			})
			_, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, 2, false)
			if !errors.Is(err, ErrUncertain) || writes.Load() != 1 {
				t.Fatalf("err=%v writes=%d", err, writes.Load())
			}
		})
	}
}

func TestLicenseCapacityInputBounds(t *testing.T) {
	client := reconciliationClient(t, func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached API") })
	for _, seats := range []int{-1, 0} {
		if _, err := client.EnsureLicenseCapacity(context.Background(), settingsCompanyID, seats, false); err == nil {
			t.Fatal("accepted invalid seats")
		}
	}
	if _, err := client.EnsureLicenseCapacity(context.Background(), "invalid", 2, false); err == nil {
		t.Fatal("accepted invalid company")
	}
}
