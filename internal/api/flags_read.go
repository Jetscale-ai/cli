package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

const flagsMaxDescendants = 100
const flagsPageSize = 50

type flagsFailure string

func (e flagsFailure) Error() string { return "flags: " + string(e) }

func flagsErrorCode(err error) string {
	var failure flagsFailure
	if errors.As(err, &failure) {
		return string(failure)
	}
	return "read_failed"
}

func flagsRead(ctx context.Context, fetch basicsFetch, editors ...generated.RequestEditorFn) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := basicsReadResponse(fetch(ctx, editors...))
	if errors.Is(err, errBasicsNotFound) {
		return nil, errBasicsNotFound
	}
	if err != nil {
		return nil, flagsFailure("read_failed")
	}
	return body, nil
}

func flagsList[T any](ctx context.Context, fetch basicsFetch, filters map[string]string, fields string) ([]T, error) {
	items := []T{}
	seen := map[uuid.UUID]bool{}
	total := -1
	for offset := 0; offset < flagsMaxDescendants; offset += flagsPageSize {
		editor := func(ctx context.Context, req *http.Request) error {
			if err := basicsQuery(filters, fields, offset)(ctx, req); err != nil {
				return err
			}
			q := req.URL.Query()
			q.Set("api_limit", strconv.Itoa(flagsPageSize))
			req.URL.RawQuery = q.Encode()
			return nil
		}
		body, err := flagsRead(ctx, fetch, editor)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Data []json.RawMessage `json:"data"`
			Meta struct {
				Count  *int `json:"count"`
				Total  *int `json:"total_count"`
				Offset *int `json:"offset"`
				Limit  *int `json:"limit"`
			} `json:"meta"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil || envelope.Meta.Count == nil || envelope.Meta.Total == nil || envelope.Meta.Offset == nil || envelope.Meta.Limit == nil || *envelope.Meta.Count != len(envelope.Data) || *envelope.Meta.Offset != offset || *envelope.Meta.Limit != flagsPageSize || len(envelope.Data) > flagsPageSize || *envelope.Meta.Total < 0 || *envelope.Meta.Total > flagsMaxDescendants {
			return nil, flagsFailure("invalid_pagination")
		}
		if total >= 0 && total != *envelope.Meta.Total {
			return nil, flagsFailure("collection_drift")
		}
		total = *envelope.Meta.Total
		for _, raw := range envelope.Data {
			var id struct {
				ID uuid.UUID `json:"id"`
			}
			var item T
			if json.Unmarshal(raw, &id) != nil || id.ID == uuid.Nil || seen[id.ID] || json.Unmarshal(raw, &item) != nil {
				return nil, flagsFailure("invalid_collection")
			}
			seen[id.ID] = true
			items = append(items, item)
		}
		if len(items) == total {
			return items, nil
		}
		if len(items) > total || len(envelope.Data) != flagsPageSize {
			return nil, flagsFailure("incomplete_collection")
		}
	}
	return nil, flagsFailure("descendant_bound")
}

type flagsEntity struct {
	ID      uuid.UUID `json:"id"`
	Company uuid.UUID `json:"company"`
	Name    string    `json:"name"`
	Slug    string    `json:"slug"`
}

type flagsAccount struct {
	ID           uuid.UUID `json:"id"`
	BusinessUnit uuid.UUID `json:"business_unit"`
}

type flagsTopology struct {
	Actor      uuid.UUID
	Company    flagsEntity
	SelectedBU uuid.UUID
	Units      []flagsEntity
	Accounts   []flagsAccount
}

func (c *Client) flagsTopology(ctx context.Context, p FlagsProfile) (flagsTopology, error) {
	var top flagsTopology
	body, err := flagsRead(ctx, c.gen.MeApiV2AuthMeGet)
	if err != nil {
		return top, err
	}
	var me struct {
		Data struct {
			User struct {
				ID        uuid.UUID `json:"id"`
				Superuser *bool     `json:"is_superuser"`
			} `json:"user"`
		} `json:"data"`
	}
	// Secret rows can be hidden as 404s. Full visibility is required to prove
	// absence and the company cascade's complete footprint, not inferred from 200.
	if json.Unmarshal(body, &me) != nil || me.Data.User.ID == uuid.Nil || me.Data.User.Superuser == nil || !*me.Data.User.Superuser {
		return top, flagsFailure("visibility_unproved")
	}
	top.Actor = me.Data.User.ID
	companies, err := flagsList[flagsEntity](ctx, c.gen.ListCompaniesApiV2OrganizationCompaniesGet, map[string]string{"slug": "eq " + p.Company.Slug}, "id,name,slug")
	if err != nil {
		return top, err
	}
	if len(companies) != 1 || companies[0].Name != p.Company.Name || companies[0].Slug != p.Company.Slug {
		return top, flagsFailure("company_selector_mismatch")
	}
	top.Company = companies[0]
	top.Units = []flagsEntity{}
	top.Accounts = []flagsAccount{}
	if p.BusinessUnit != nil || len(p.Flags.Company) > 0 || len(p.Flags.CloudAccounts) > 0 {
		top.Units, err = flagsList[flagsEntity](ctx, c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGet, map[string]string{"company": "eq " + top.Company.ID.String()}, "id,company,name,slug")
		if err != nil {
			return top, err
		}
		seenSlugs := map[string]bool{}
		for _, unit := range top.Units {
			if unit.Company != top.Company.ID || unit.Name == "" || unit.Slug == "" || seenSlugs[unit.Slug] {
				return top, flagsFailure("invalid_topology")
			}
			seenSlugs[unit.Slug] = true
			if p.BusinessUnit != nil && unit.Name == p.BusinessUnit.Name && unit.Slug == p.BusinessUnit.Slug {
				top.SelectedBU = unit.ID
			}
		}
		if p.BusinessUnit != nil && top.SelectedBU == uuid.Nil {
			return top, flagsFailure("business_unit_selector_mismatch")
		}
	}
	if len(p.Flags.Company) > 0 || len(p.Flags.CloudAccounts) > 0 {
		top.Accounts, err = flagsList[flagsAccount](ctx, c.listCloudAccounts, map[string]string{"company_id": top.Company.ID.String()}, "id,business_unit")
		if err != nil {
			return top, err
		}
		units := map[uuid.UUID]bool{}
		for _, u := range top.Units {
			units[u.ID] = true
		}
		for _, a := range top.Accounts {
			if !units[a.BusinessUnit] {
				return top, flagsFailure("account_membership_unproved")
			}
		}
		for _, selected := range p.Flags.CloudAccounts {
			found := false
			for _, a := range top.Accounts {
				if a.ID == selected.ID && (p.BusinessUnit == nil || a.BusinessUnit == top.SelectedBU) {
					found = true
				}
			}
			if !found {
				return top, flagsFailure("account_membership_unproved")
			}
		}
	}
	if len(top.Units)+len(top.Accounts) > flagsMaxDescendants {
		return top, flagsFailure("descendant_bound")
	}
	sort.Slice(top.Units, func(i, j int) bool { return top.Units[i].ID.String() < top.Units[j].ID.String() })
	sort.Slice(top.Accounts, func(i, j int) bool { return top.Accounts[i].ID.String() < top.Accounts[j].ID.String() })
	return top, nil
}

type flagsRef struct {
	scope string
	id    uuid.UUID
	key   string
}

func (n flagsRef) entityID() string {
	if n.id == uuid.Nil {
		return ""
	}
	return n.id.String()
}

func flagsSort(refs map[flagsRef]bool) []flagsRef {
	out := make([]flagsRef, 0, len(refs))
	for n := range refs {
		out = append(out, n)
	}
	rank := map[string]int{"root": 0, "company": 1, "business_unit": 2, "cloud_account": 3}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.scope != b.scope {
			return rank[a.scope] < rank[b.scope]
		}
		if a.id != b.id {
			return a.id.String() < b.id.String()
		}
		return a.key < b.key
	})
	return out
}

type flagsRow struct {
	ID           uuid.UUID       `json:"id"`
	Key          string          `json:"key"`
	FeatureKey   string          `json:"feature_flag_key"`
	Company      uuid.UUID       `json:"company"`
	BusinessUnit uuid.UUID       `json:"business_unit"`
	CloudAccount uuid.UUID       `json:"cloud_account"`
	Root         uuid.UUID       `json:"root_feature_flag"`
	Value        *bool           `json:"value"`
	Secret       *bool           `json:"is_secret"`
	DeprecatedAt json.RawMessage `json:"deprecated_at"`
	Description  json.RawMessage `json:"description"`
	UpdatedAt    json.RawMessage `json:"updated_at"`
}

func (f *flagsRow) unsafe() bool {
	return f != nil && (*f.Secret || (len(f.DeprecatedAt) > 0 && string(f.DeprecatedAt) != "null"))
}

func (c *Client) flagsGet(ctx context.Context, n flagsRef) (*flagsRow, error) {
	fetch := func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		switch n.scope {
		case "root":
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsRootByKeyFlagKeyGet(ctx, n.key, editors...)
		case "company":
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGet(ctx, n.key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGetParams{CompanyId: n.id}, editors...)
		case "business_unit":
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGet(ctx, n.key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGetParams{BusinessUnitId: n.id}, editors...)
		case "cloud_account":
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsCloudAccountByKeyFlagKeyGet(ctx, n.key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsCloudAccountByKeyFlagKeyGetParams{CloudAccountId: n.id}, editors...)
		}
		return nil, flagsFailure("invalid_scope")
	}
	body, err := flagsRead(ctx, fetch)
	if errors.Is(err, errBasicsNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *flagsRow `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		return nil, flagsFailure("invalid_flag_response")
	}
	f := envelope.Data
	if f.ID == uuid.Nil || f.Value == nil || f.Secret == nil || (n.scope == "root" && (f.Key != n.key || len(f.DeprecatedAt) == 0)) || (n.scope != "root" && (f.FeatureKey != n.key || f.Root == uuid.Nil)) || (n.scope == "company" && f.Company != n.id) || (n.scope == "business_unit" && f.BusinessUnit != n.id) || (n.scope == "cloud_account" && f.CloudAccount != n.id) {
		return nil, flagsFailure("invalid_flag_response")
	}
	return f, nil
}

type flagsSnapshot struct {
	top        flagsTopology
	rows       map[flagsRef]*flagsRow
	wanted     map[flagsRef]bool
	cascade    map[flagsRef]flagsRef
	unsafeKeys map[string]bool
}

func (s flagsSnapshot) parent(n flagsRef) (flagsRef, bool) {
	switch n.scope {
	case "company":
		return flagsRef{"root", uuid.Nil, n.key}, true
	case "business_unit":
		return flagsRef{"company", s.top.Company.ID, n.key}, true
	case "cloud_account":
		for _, a := range s.top.Accounts {
			if a.ID == n.id {
				return flagsRef{"business_unit", a.BusinessUnit, n.key}, true
			}
		}
	}
	return flagsRef{}, false
}

func (s flagsSnapshot) effective(n flagsRef) bool {
	row := s.rows[n]
	if row == nil || s.unsafeKeys[n.key] || !*row.Value {
		return false
	}
	if parent, ok := s.parent(n); ok {
		return s.effective(parent)
	}
	return n.scope == "root"
}

func (c *Client) flagsInspect(ctx context.Context, p FlagsProfile, monitored ...flagsRef) (flagsSnapshot, error) {
	s := flagsSnapshot{rows: map[flagsRef]*flagsRow{}, wanted: map[flagsRef]bool{}, cascade: map[flagsRef]flagsRef{}, unsafeKeys: map[string]bool{}}
	var err error
	s.top, err = c.flagsTopology(ctx, p)
	if err != nil {
		return s, err
	}
	refs := map[flagsRef]bool{}
	var add func(flagsRef)
	add = func(n flagsRef) {
		if refs[n] {
			return
		}
		refs[n] = true
		if parent, ok := s.parent(n); ok {
			add(parent)
		}
	}
	for _, scope := range []struct {
		scope  string
		id     uuid.UUID
		values map[string]bool
	}{
		{"root", uuid.Nil, p.Flags.Root}, {"company", s.top.Company.ID, p.Flags.Company}, {"business_unit", s.top.SelectedBU, p.Flags.BusinessUnit},
	} {
		for key := range scope.values {
			n := flagsRef{scope.scope, scope.id, key}
			s.wanted[n] = true
			add(n)
		}
	}
	for _, a := range p.Flags.CloudAccounts {
		for key := range a.Flags {
			n := flagsRef{"cloud_account", a.ID, key}
			s.wanted[n] = true
			add(n)
		}
	}
	for _, n := range monitored {
		add(n)
	}
	// Read root/company prerequisites first to discover the actual cascade.
	for _, n := range flagsSort(refs) {
		if n.scope != "root" && n.scope != "company" {
			continue
		}
		s.rows[n], err = c.flagsGet(ctx, n)
		if err != nil {
			return s, err
		}
		if s.rows[n].unsafe() {
			s.unsafeKeys[n.key] = true
		}
	}
	for key := range p.Flags.Company {
		n := flagsRef{"company", s.top.Company.ID, key}
		if row := s.rows[n]; row != nil && *row.Value {
			continue
		}
		for _, u := range s.top.Units {
			child := flagsRef{"business_unit", u.ID, key}
			s.cascade[child] = n
			add(child)
		}
		for _, a := range s.top.Accounts {
			child := flagsRef{"cloud_account", a.ID, key}
			s.cascade[child] = n
			add(child)
		}
	}
	seenIDs := map[uuid.UUID]flagsRef{}
	for _, n := range flagsSort(refs) {
		if _, read := s.rows[n]; !read {
			if s.unsafeKeys[n.key] {
				s.rows[n] = nil
				continue
			}
			s.rows[n], err = c.flagsGet(ctx, n)
			if err != nil {
				return s, err
			}
		}
		f := s.rows[n]
		if f == nil {
			continue
		}
		if f.unsafe() {
			s.unsafeKeys[n.key] = true
		}
		if previous, exists := seenIDs[f.ID]; exists && previous != n {
			return s, flagsFailure("duplicate_flag_id")
		}
		seenIDs[f.ID] = n
		if n.scope != "root" {
			root := s.rows[flagsRef{"root", uuid.Nil, n.key}]
			if root == nil || f.Root != root.ID {
				return s, flagsFailure("root_reference_drift")
			}
		}
	}
	return s, nil
}

func flagsSame(a, b flagsSnapshot) bool {
	return reflect.DeepEqual(a.top, b.top) && reflect.DeepEqual(a.rows, b.rows) && reflect.DeepEqual(a.unsafeKeys, b.unsafeKeys)
}
