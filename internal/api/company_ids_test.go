package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestListCompanyIDsRejectsInvalidCollections(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	row := `{"id":"` + id + `"}`
	for _, test := range []struct {
		name string
		body string
	}{
		{"missing_total", `{"data":[` + row + `],"meta":{}}`},
		{"missing_empty_total", `{"data":[],"meta":{}}`},
		{"null_total", `{"data":[` + row + `],"meta":{"total_count":null}}`},
		{"negative_total", `{"data":[],"meta":{"total_count":-1}}`},
		{"fractional_total", `{"data":[],"meta":{"total_count":0.5}}`},
		{"short_incomplete", `{"data":[` + row + `],"meta":{"total_count":2}}`},
		{"more_than_total", `{"data":[` + row + `],"meta":{"total_count":0}}`},
		{"duplicate_id", `{"data":[` + row + `,` + row + `],"meta":{"total_count":2}}`},
		{"normalized_duplicate", `{"data":[{"id":"AAAAAAAA-1111-4111-8111-111111111111"},{"id":"aaaaaaaa-1111-4111-8111-111111111111"}],"meta":{"total_count":2}}`},
		{"invalid_uuid", `{"data":[{"id":"not-a-uuid"}],"meta":{"total_count":1}}`},
		{"zero_uuid", `{"data":[{"id":"00000000-0000-0000-0000-000000000000"}],"meta":{"total_count":1}}`},
		{"missing_id", `{"data":[{}],"meta":{"total_count":1}}`},
		{"nested_id", `{"data":[{"id":` + row + `}],"meta":{"total_count":1}}`},
		{"numeric_id", `{"data":[{"id":1}],"meta":{"total_count":1}}`},
		{"null_data", `{"data":null,"meta":{"total_count":0}}`},
		{"truncated_json", `{"data":[`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := NewSessionClient(server.URL, server.Client()).Resources()
			if err != nil {
				t.Fatal(err)
			}
			rows, err := client.ListCompanyIDs(context.Background())
			if err == nil || rows != nil || calls != 1 {
				t.Fatalf("invalid collection returned data or retried: rows=%v err=%v calls=%d", rows, err, calls)
			}
		})
	}
}

func TestListCompanyIDsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[],"meta":{"total_count":0}}`)
	}))
	defer server.Close()
	client, err := NewSessionClient(server.URL, server.Client()).Resources()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListCompanyIDs(context.Background())
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty count-backed collection: rows=%v err=%v", rows, err)
	}
}

func TestListCompanyIDsPagination(t *testing.T) {
	for _, test := range []struct {
		name     string
		maxPages int
	}{
		{"complete", 2},
		{"page_bound", 1},
		{"changed_total", 2},
		{"missing_second_total", 2},
		{"duplicate_across_pages", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			want := make([]Record, 101)
			for i := range want {
				want[i] = Record{"id": fmt.Sprintf("11111111-1111-4111-8111-%012d", i+1)}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Path != "/api/v2/organization/companies" || len(q) != 4 ||
					q.Get("api_fields") != "id" || q.Get("api_sort") != "id" || q.Get("api_limit") != "100" || q.Get("api_offset") != strconv.Itoa((calls-1)*100) {
					t.Errorf("unsafe paginated ID request: %s %s", r.Method, r.URL)
				}
				rows := want[:100]
				meta := map[string]any{"total_count": 101}
				if calls == 2 {
					rows = want[100:]
					switch test.name {
					case "changed_total":
						meta["total_count"] = 102
					case "missing_second_total":
						delete(meta, "total_count")
					case "duplicate_across_pages":
						rows = want[:1]
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "meta": meta})
			}))
			defer server.Close()
			client, err := NewSessionClient(server.URL, server.Client()).Resources()
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := WithMaxPages(context.Background(), test.maxPages)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := client.ListCompanyIDs(ctx)
			if test.name == "complete" {
				if err != nil || !reflect.DeepEqual(rows, want) || calls != 2 {
					t.Fatalf("count-backed pagination: rows=%v err=%v calls=%d", rows, err, calls)
				}
			} else if err == nil || rows != nil || calls != test.maxPages {
				t.Fatalf("partial collection returned: rows=%v err=%v calls=%d", rows, err, calls)
			}
			if test.name == "page_bound" && !errors.Is(err, ErrPageBound) {
				t.Fatalf("page limit not honored: %v", err)
			}
		})
	}
}

func TestListCompanyIDsProjectsAndSorts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"AAAAAAAA-1111-4111-8111-111111111111","name":"foreign-metadata"},{"id":"11111111-1111-4111-8111-111111111111","contact_email":"foreign-metadata@example.invalid"}],"meta":{"total_count":2}}`)
	}))
	defer server.Close()
	client, err := NewSessionClient(server.URL, server.Client()).Resources()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListCompanyIDs(context.Background())
	want := []Record{{"id": "11111111-1111-4111-8111-111111111111"}, {"id": "aaaaaaaa-1111-4111-8111-111111111111"}}
	if err != nil || !reflect.DeepEqual(rows, want) || strings.Contains(fmt.Sprint(rows), "foreign-metadata") {
		t.Fatalf("ID-only sorted projection: rows=%v err=%v", rows, err)
	}
}
