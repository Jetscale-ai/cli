package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestListCloudAccountsEmpty(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()

	client := NewClient(srv.URL, "valid")
	accounts, err := client.ListCloudAccounts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Errorf("expected 0 accounts for nil BUs, got %d", len(accounts))
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
