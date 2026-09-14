package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestServer() *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v2/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		companyID := "c0000000-0000-0000-0000-000000000001"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"user": map[string]any{
					"id":       "u001",
					"email":    "dev@jetscale.ai",
					"username": "admin",
					"company":  companyID,
				},
			},
		})
	})

	mux.HandleFunc("/api/v2/organization/business-units", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "bu-001", "name": "Engineering", "slug": "engineering"},
				{"id": "bu-002", "name": "Data Platform", "slug": "data-platform"},
			},
		})
	})

	mux.HandleFunc("/api/v2/cloud/cloud-accounts", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                  "a0000001-0000-0000-0000-000000000001",
					"name":                "prod-us-east-1",
					"cloud_provider_type": "AWS",
					"business_unit":       "bu-001",
					"description":         "Production",
				},
				{
					"id":                  "a0000002-0000-0000-0000-000000000002",
					"name":                "staging-eu-west-1",
					"cloud_provider_type": "AWS",
					"business_unit":       "bu-001",
					"description":         "Staging",
				},
				{
					"id":                  "a0000003-0000-0000-0000-000000000003",
					"name":                "analytics-gcp",
					"cloud_provider_type": "GCP",
					"business_unit":       "bu-002",
					"description":         "",
				},
			},
		})
	})

	mux.HandleFunc("/api/v2/cloud/aws/sourced-recommendations/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Extract the ID from the path
		id := r.URL.Path[len("/api/v2/cloud/aws/sourced-recommendations/"):]
		if id == "00000000-0000-0000-0000-000000000099" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"id":                        id,
				"aws_recommendation_id":     "aws-rec-001",
				"resource_name":             "web-api-prod",
				"region":                    "us-east-1",
				"current_resource_type":     "m5.xlarge",
				"recommended_resource_type": "m5.large",
				"action_type":               "Rightsize",
				"reason":                    "CPU utilization averages 12% over 14 days",
				"implementation_effort":     "Low",
				"restart_needed":            true,
				"rollback_possible":         "Yes",
				"processing_status":         "completed",
				"savings_percentage":        50.0,
				"savings":                   map[string]any{"amount": 87.60, "currency": "USD", "cycle": "monthly"},
				"cost":                      map[string]any{"amount": 175.20, "currency": "USD", "cycle": "monthly"},
				"generation_id":             "gen-001",
				"created_at":                "2026-03-10T12:00:00Z",
				"updated_at":                "2026-03-10T12:00:00Z",
			},
		})
	})

	mux.HandleFunc("/api/v2/cloud/aws/sourced-recommendations", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                        "a0000001-0000-0000-0000-100000000001",
					"aws_recommendation_id":     "aws-rec-001",
					"resource_name":             "web-api-prod",
					"region":                    "us-east-1",
					"current_resource_type":     "m5.xlarge",
					"recommended_resource_type": "m5.large",
					"action_type":               "Rightsize",
					"reason":                    "CPU utilization averages 12%",
					"implementation_effort":     "Low",
					"restart_needed":            true,
					"rollback_possible":         "Yes",
					"processing_status":         "completed",
					"savings_percentage":        50.0,
					"savings":                   map[string]any{"amount": 87.60, "currency": "USD", "cycle": "monthly"},
					"cost":                      map[string]any{"amount": 175.20, "currency": "USD", "cycle": "monthly"},
					"generation_id":             "gen-001",
					"created_at":                "2026-03-10T12:00:00Z",
					"updated_at":                "2026-03-10T12:00:00Z",
				},
				{
					"id":                        "a0000002-0000-0000-0000-200000000002",
					"aws_recommendation_id":     "aws-rec-002",
					"resource_name":             "batch-worker",
					"region":                    "eu-west-1",
					"current_resource_type":     "c5.2xlarge",
					"recommended_resource_type": "c5.xlarge",
					"action_type":               "Rightsize",
					"reason":                    "CPU utilization averages 8%",
					"implementation_effort":     "Medium",
					"restart_needed":            false,
					"rollback_possible":         "Yes",
					"processing_status":         "completed",
					"savings_percentage":        50.0,
					"savings":                   map[string]any{"amount": 124.00, "currency": "USD", "cycle": "monthly"},
					"cost":                      map[string]any{"amount": 248.00, "currency": "USD", "cycle": "monthly"},
					"generation_id":             "gen-001",
					"created_at":                "2026-03-10T12:00:00Z",
					"updated_at":                "2026-03-10T12:00:00Z",
				},
			},
			"meta": map[string]any{
				"count":       2,
				"total_count": 2,
				"limit":       50,
				"offset":      0,
			},
		})
	})

	return httptest.NewServer(mux)
}

func TestMe(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	me, err := client.Me()
	if err != nil {
		t.Fatal(err)
	}
	if me.Email != "dev@jetscale.ai" {
		t.Errorf("email = %q", me.Email)
	}
	if me.Company == nil {
		t.Fatal("expected company to be set")
	}
}

func TestNewSessionClient(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("session client injected Authorization")
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "test-session" {
			t.Error("session cookie missing")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"user":{"id":"u001","username":"admin"}}}`))
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	serverURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{{Name: "session", Value: "test-session", Path: "/", Secure: true}})
	httpClient := srv.Client()
	httpClient.Jar = jar
	client := NewSessionClient(srv.URL, httpClient)
	me, err := client.Me()
	if err != nil {
		t.Fatal(err)
	}
	if me.ID != "u001" || me.Username != "admin" {
		t.Fatalf("unexpected user: %+v", me)
	}
}

func TestFetchAccountTreeWithoutBusinessUnits(t *testing.T) {
	for _, company := range []string{"null", `"company-001"`} {
		t.Run(company, func(t *testing.T) {
			businessUnitCalls, accountCalls := 0, 0
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/auth/me", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(w, `{"data":{"user":{"id":"u001","company":%s,"is_superuser":true}}}`, company)
			})
			mux.HandleFunc("/api/v2/organization/business-units", func(w http.ResponseWriter, _ *http.Request) {
				businessUnitCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[]}`))
			})
			mux.HandleFunc("/api/v2/cloud/cloud-accounts", func(w http.ResponseWriter, _ *http.Request) {
				accountCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"account-001","name":"accessible-account","business_unit":"bu-001"}]}`))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			tree, err := NewSessionClient(srv.URL, srv.Client()).FetchAccountTree()
			if err != nil {
				t.Fatal(err)
			}
			if tree.User.ID != "u001" || len(tree.BusinessUnits) != 0 || len(tree.Accounts) != 1 || tree.Accounts[0].ID != "account-001" {
				t.Fatalf("unexpected tree: %+v", tree)
			}
			wantBusinessUnitCalls := 1
			if company == "null" {
				wantBusinessUnitCalls = 0
				if tree.User.Company != nil {
					t.Error("expected company-less user")
				}
			}
			if businessUnitCalls != wantBusinessUnitCalls || accountCalls != 1 {
				t.Fatalf("business-unit calls = %d, account calls = %d", businessUnitCalls, accountCalls)
			}
		})
	}
}

func TestClientErrorsDoNotExposeResponse(t *testing.T) {
	methods := map[string]func(*Client) error{
		"me":              func(c *Client) error { _, err := c.Me(); return err },
		"business units":  func(c *Client) error { _, err := c.ListBusinessUnits("company-001"); return err },
		"cloud accounts":  func(c *Client) error { _, err := c.ListCloudAccounts(nil); return err },
		"recommendations": func(c *Client) error { _, err := c.ListRecommendations(0, 0); return err },
		"recommendation": func(c *Client) error {
			_, err := c.GetRecommendation("a0000001-0000-0000-0000-000000000001")
			return err
		},
		"account tree": func(c *Client) error { _, err := c.FetchAccountTree(); return err },
	}
	for name, call := range methods {
		t.Run(name, func(t *testing.T) {
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError, http.StatusUnprocessableEntity, http.StatusOK} {
				t.Run(fmt.Sprint(status), func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						// Invalid shapes also exercise errors from generated response parsing.
						_, _ = w.Write([]byte(`{"detail":"sensitive-server-value","data":"sensitive-server-value"}`))
					}))
					defer srv.Close()
					err := call(NewSessionClient(srv.URL, srv.Client()))
					if err == nil {
						t.Fatal("expected API error")
					}
					if strings.Contains(err.Error(), "sensitive-server-value") || strings.Contains(err.Error(), srv.URL) {
						t.Fatalf("error exposed server content: %v", err)
					}
					if status == http.StatusForbidden || status == http.StatusInternalServerError {
						if want := fmt.Sprintf("HTTP %d", status); err.Error() != want {
							t.Fatalf("error = %q, want %q", err, want)
						}
					}
				})
			}
			t.Run("transport", func(t *testing.T) {
				client := NewSessionClient("https://example.invalid", &http.Client{
					Transport: clientTestTransport(func(*http.Request) (*http.Response, error) {
						return nil, errors.New("sensitive-transport-value")
					}),
				})
				if err := call(client); err == nil || err.Error() != "API request failed" {
					t.Fatalf("expected sanitized transport error, got %v", err)
				}
			})
		})
	}
}

type clientTestTransport func(*http.Request) (*http.Response, error)

func (f clientTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestMeUnauthorized(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "bad-token")
	_, err := client.Me()
	if err == nil {
		t.Error("expected error for bad token")
	}
}

func TestListBusinessUnits(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	bus, err := client.ListBusinessUnits("c0000000-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if len(bus) != 2 {
		t.Fatalf("expected 2 BUs, got %d", len(bus))
	}
	if bus[0].Name != "Engineering" {
		t.Errorf("bu[0].name = %q", bus[0].Name)
	}
}

func TestListCloudAccounts(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	accounts, err := client.ListCloudAccounts([]string{"bu-001", "bu-002"})
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(accounts))
	}
	if accounts[0].Name != "prod-us-east-1" {
		t.Errorf("accounts[0].name = %q", accounts[0].Name)
	}
	if accounts[2].CloudProviderType != "GCP" {
		t.Errorf("accounts[2].type = %q", accounts[2].CloudProviderType)
	}
}

func TestListCloudAccountsWithoutUnitIDs(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	accounts, err := client.ListCloudAccounts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Errorf("expected 3 accessible accounts for nil BUs, got %d", len(accounts))
	}
}

func TestFetchAccountTree(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	tree, err := client.FetchAccountTree()
	if err != nil {
		t.Fatal(err)
	}
	if tree.User.Email != "dev@jetscale.ai" {
		t.Errorf("user.email = %q", tree.User.Email)
	}
	if len(tree.BusinessUnits) != 2 {
		t.Errorf("expected 2 BUs, got %d", len(tree.BusinessUnits))
	}
	if len(tree.Accounts) != 3 {
		t.Errorf("expected 3 accounts, got %d", len(tree.Accounts))
	}
}

func TestFindAccountByName(t *testing.T) {
	accounts := []CloudAccount{
		{ID: "a001-xxxx", Name: "prod-us-east-1", CloudProviderType: "AWS"},
		{ID: "a002-xxxx", Name: "staging-eu-west-1", CloudProviderType: "AWS"},
	}

	acct, ok := FindAccountByName(accounts, "prod-us-east-1")
	if !ok || acct.ID != "a001-xxxx" {
		t.Errorf("expected prod-us-east-1, got %+v", acct)
	}

	acct, ok = FindAccountByName(accounts, "Prod-US-East-1")
	if !ok || acct.ID != "a001-xxxx" {
		t.Errorf("case-insensitive match failed")
	}

	acct, ok = FindAccountByName(accounts, "a002")
	if !ok || acct.Name != "staging-eu-west-1" {
		t.Errorf("UUID prefix match failed")
	}

	_, ok = FindAccountByName(accounts, "nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestListRecommendations(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	result, err := client.ListRecommendations(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 recommendations, got %d", len(result.Items))
	}
	if result.Items[0].ResourceName != "web-api-prod" {
		t.Errorf("items[0].resource_name = %q", result.Items[0].ResourceName)
	}
	if result.Items[1].Region != "eu-west-1" {
		t.Errorf("items[1].region = %q", result.Items[1].Region)
	}
	if result.Meta.TotalCount != 2 {
		t.Errorf("meta.total_count = %d", result.Meta.TotalCount)
	}
}

func TestListRecommendationsUnauthorized(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "bad-token")
	_, err := client.ListRecommendations(0, 0)
	if err == nil {
		t.Error("expected error for bad token")
	}
}

func TestGetRecommendation(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	rec, err := client.GetRecommendation("a0000001-0000-0000-0000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ResourceName != "web-api-prod" {
		t.Errorf("resource_name = %q", rec.ResourceName)
	}
	if rec.SavingsPercentage != 50.0 {
		t.Errorf("savings_percentage = %f", rec.SavingsPercentage)
	}
	if rec.Savings.Amount != 87.60 {
		t.Errorf("savings.amount = %f", rec.Savings.Amount)
	}
}

func TestGetRecommendationNotFound(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	_, err := client.GetRecommendation("00000000-0000-0000-0000-000000000099")
	if err == nil {
		t.Error("expected not-found error")
	}
}

func TestGetRecommendationInvalidID(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	_, err := client.GetRecommendation("not-a-uuid")
	if err == nil {
		t.Error("expected invalid UUID error")
	}
}
