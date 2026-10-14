package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

const (
	flagsTestCompany      = "21000000-0000-0000-0000-000000000001"
	flagsTestBU           = "41000000-0000-0000-0000-000000000001"
	flagsTestBU2          = "41000000-0000-0000-0000-000000000002"
	flagsTestAccount      = "51000000-0000-0000-0000-000000000001"
	flagsTestAccount2     = "51000000-0000-0000-0000-000000000002"
	flagsTestOtherCompany = "21000000-0000-0000-0000-000000000002"
	flagsTestOtherBU      = "41000000-0000-0000-0000-000000000003"
	flagsTestOtherAccount = "51000000-0000-0000-0000-000000000003"
	flagsTestKey          = "AGENTS.EXPLORER"
)

func flagsTestProfile() FlagsProfile {
	return FlagsProfile{Version: 1, Company: FlagsEntitySelector{Name: "Test Company", Slug: "test-company"}, BusinessUnit: &FlagsEntitySelector{Name: "Test Clouds", Slug: "test-clouds"}, Flags: FlagsLayers{Root: map[string]bool{flagsTestKey: true}, Company: map[string]bool{flagsTestKey: true}, BusinessUnit: map[string]bool{flagsTestKey: true}}, CompanyCascade: "all_descendants"}
}

type flagsTestWrite struct {
	Scope, ID, Key, Method string
	Body                   map[string]any
}

type flagsFixture struct {
	mu        sync.Mutex
	companies []map[string]any
	units     []map[string]any
	accounts  []map[string]any
	rows      map[string]map[string]any
	writes    []flagsTestWrite
	reads     []string
	superuser any
	hook      func(http.ResponseWriter, *http.Request) bool
	partial   bool
}

func newFlagsFixture() *flagsFixture {
	return &flagsFixture{
		companies: []map[string]any{{"id": flagsTestCompany, "name": "Test Company", "slug": "test-company"}, {"id": flagsTestOtherCompany, "name": "Other Company", "slug": "other-company"}},
		units:     []map[string]any{{"id": flagsTestBU, "company": flagsTestCompany, "name": "Test Clouds", "slug": "test-clouds"}, {"id": flagsTestBU2, "company": flagsTestCompany, "name": "Second Clouds", "slug": "second-clouds"}, {"id": flagsTestOtherBU, "company": flagsTestOtherCompany, "name": "Other Clouds", "slug": "other-clouds"}},
		accounts:  []map[string]any{{"id": flagsTestAccount, "business_unit": flagsTestBU}, {"id": flagsTestAccount2, "business_unit": flagsTestBU2}, {"id": flagsTestOtherAccount, "business_unit": flagsTestOtherBU}},
		rows:      map[string]map[string]any{}, superuser: true,
	}
}

func flagsFixtureRowKey(scope, id, key string) string { return scope + "/" + id + "/" + key }

func (f *flagsFixture) flag(scope, id, key string, enabled bool) {
	rootID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("root/"+key)).String()
	rowID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(flagsFixtureRowKey(scope, id, key))).String()
	row := map[string]any{"id": rowID, "value": enabled, "is_secret": false, "deprecated_at": nil, "description": "Synthetic metadata", "updated_at": "2026-01-01T00:00:00Z"}
	if scope == "root" {
		row["key"], row["id"] = key, rootID
	} else {
		row["feature_flag_key"], row["root_feature_flag"] = key, rootID
		row[scope] = id
	}
	f.rows[flagsFixtureRowKey(scope, id, key)] = row
}

func flagsFixtureJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func flagsFixtureList(w http.ResponseWriter, r *http.Request, data []map[string]any) {
	sort.Slice(data, func(i, j int) bool { return fmt.Sprint(data[i]["id"]) < fmt.Sprint(data[j]["id"]) })
	offset, _ := strconv.Atoi(r.URL.Query().Get("api_offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("api_limit"))
	total := len(data)
	if offset > len(data) {
		offset = len(data)
	}
	end := offset + limit
	if end > len(data) {
		end = len(data)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data[offset:end], "meta": map[string]any{"count": end - offset, "total_count": total, "offset": offset, "limit": limit}})
}

func (f *flagsFixture) server(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == "GET" {
			f.reads = append(f.reads, r.URL.RequestURI())
		}
		if f.hook != nil && f.hook(w, r) {
			return
		}
		switch r.URL.Path {
		case "/api/v2/auth/me":
			flagsFixtureJSON(w, map[string]any{"user": map[string]any{"id": "71000000-0000-0000-0000-000000000001", "is_superuser": f.superuser}})
			return
		case "/api/v2/organization/companies":
			if r.Method != "GET" {
				t.Error("entity provisioning forbidden")
				w.WriteHeader(500)
				return
			}
			data := []map[string]any{}
			if r.URL.Query().Get("slug") != "eq test-company" {
				t.Error("unscoped company read")
			}
			for _, row := range f.companies {
				if "eq "+fmt.Sprint(row["slug"]) == r.URL.Query().Get("slug") {
					data = append(data, row)
				}
			}
			flagsFixtureList(w, r, data)
			return
		case "/api/v2/organization/business-units":
			if r.Method != "GET" {
				t.Error("entity provisioning forbidden")
				w.WriteHeader(500)
				return
			}
			if r.URL.Query().Get("company") != "eq "+flagsTestCompany {
				t.Error("unscoped BU read")
			}
			data := []map[string]any{}
			for _, row := range f.units {
				if "eq "+fmt.Sprint(row["company"]) == r.URL.Query().Get("company") {
					data = append(data, row)
				}
			}
			flagsFixtureList(w, r, data)
			return
		case "/api/v2/cloud/cloud-accounts":
			if r.Method != "GET" {
				t.Error("cloud provisioning forbidden")
				w.WriteHeader(500)
				return
			}
			if r.URL.Query().Get("company_id") != flagsTestCompany {
				t.Error("unscoped account read")
			}
			data := []map[string]any{}
			for _, a := range f.accounts {
				for _, u := range f.units {
					if a["business_unit"] == u["id"] && u["company"] == r.URL.Query().Get("company_id") {
						data = append(data, a)
					}
				}
			}
			flagsFixtureList(w, r, data)
			return
		}
		prefix := "/api/v2/system/feature-flags/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
		scope := strings.ReplaceAll(parts[0], "-", "_")
		id := ""
		if scope != "root" {
			id = r.URL.Query().Get(scope + "_id")
		}
		key := ""
		if len(parts) > 1 {
			key = parts[len(parts)-1]
		}
		if r.Method == "GET" {
			if len(parts) != 3 || parts[1] != "by-key" {
				t.Errorf("unexpected flag read %s", r.URL.Path)
			}
			row := f.rows[flagsFixtureRowKey(scope, id, key)]
			if row == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			flagsFixtureJSON(w, row)
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid write body")
		}
		if r.Method == "POST" {
			key, _ = body["key"].(string)
		} else if r.Method != "PATCH" {
			t.Errorf("forbidden method %s", r.Method)
		}
		if body["value"] != true {
			t.Error("not enable-only")
		}
		for field := range body {
			allowed := field == "value" || (r.Method == "POST" && field == "key") || (scope == "root" && r.Method == "POST" && field == "is_secret" && body[field] == false)
			if !allowed {
				t.Errorf("unexpected metadata write %s", field)
			}
		}
		if !flagsKnownKey(key) {
			t.Error("unknown key write")
		}
		if scope == "company" && id != flagsTestCompany {
			t.Error("other company write")
		}
		if scope == "business_unit" && id == flagsTestOtherBU {
			t.Error("other BU write")
		}
		if scope == "cloud_account" && id == flagsTestOtherAccount {
			t.Error("other account write")
		}
		f.writes = append(f.writes, flagsTestWrite{scope, id, key, r.Method, body})
		row := f.rows[flagsFixtureRowKey(scope, id, key)]
		if r.Method == "POST" && row != nil {
			w.WriteHeader(409)
			return
		}
		if scope != "root" {
			parentScope, parentID := "root", ""
			if scope == "business_unit" {
				parentScope, parentID = "company", flagsTestCompany
			}
			if scope == "cloud_account" {
				for _, a := range f.accounts {
					if a["id"] == id {
						parentScope, parentID = "business_unit", fmt.Sprint(a["business_unit"])
					}
				}
			}
			parent := f.rows[flagsFixtureRowKey(parentScope, parentID, key)]
			if parent == nil || parent["value"] != true {
				t.Error("write before parent")
				w.WriteHeader(422)
				return
			}
		}
		if row == nil {
			f.flag(scope, id, key, true)
			row = f.rows[flagsFixtureRowKey(scope, id, key)]
		} else {
			row["value"] = true
		}
		if scope == "company" && !f.partial {
			for _, u := range f.units {
				if u["company"] == id {
					uid := fmt.Sprint(u["id"])
					child := f.rows[flagsFixtureRowKey("business_unit", uid, key)]
					if child == nil {
						f.flag("business_unit", uid, key, true)
					} else {
						child["value"] = true
					}
					for _, a := range f.accounts {
						if a["business_unit"] == uid {
							aid := fmt.Sprint(a["id"])
							child := f.rows[flagsFixtureRowKey("cloud_account", aid, key)]
							if child == nil {
								f.flag("cloud_account", aid, key, true)
							} else {
								child["value"] = true
							}
						}
					}
				}
			}
		}
		flagsFixtureJSON(w, row)
	}))
	t.Cleanup(server.Close)
	return NewSessionClient(server.URL, server.Client())
}

func (f *flagsFixture) writeLog() []flagsTestWrite {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]flagsTestWrite{}, f.writes...)
}
