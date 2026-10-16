package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

var CloudAccountFields = []string{"id", "name", "cloud_provider_type", "business_unit", "provider_account_id", "description", "status", "created_at"}

// ListCompanyCloudAccounts lists every cloud account the Backend authorizes in one company.
func (c *Client) ListCompanyCloudAccounts(ctx context.Context, companyID string) ([]Record, error) {
	id, ok := resourceUUID(companyID)
	if !ok {
		return nil, errors.New("company ID required")
	}
	return c.listAll(ctx, func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		return c.gen.ListCloudAccountsApiV2CloudCloudAccountsGet(ctx, &generated.ListCloudAccountsApiV2CloudCloudAccountsGetParams{CompanyId: &id}, editors...)
	}, nil)
}

// ResolveCloudAccount accepts an ID, provider account ID or exact name within a company.
func (c *Client) ResolveCloudAccount(ctx context.Context, companyID, selector string) (Record, error) {
	accounts, err := c.ListCompanyCloudAccounts(ctx, companyID)
	if err != nil {
		return nil, err
	}
	return SelectAccount(accounts, selector)
}

// SelectAccount picks one account by ID, then provider account ID, then exact name.
func SelectAccount(accounts []Record, selector string) (Record, error) {
	for _, field := range []string{"id", "provider_account_id", "name"} {
		if matches := filterRecords(accounts, func(r Record) bool { return strings.EqualFold(r.String(field), selector) }); len(matches) > 0 {
			return selectOne("cloud account", selector, matches)
		}
	}
	return nil, &NotFoundError{Resource: "cloud account", Selector: selector}
}

type LinkSpec struct {
	CompanyID         string
	BusinessUnitID    string
	Name              string
	Provider          string // AWS, AZURE or GCP
	ProviderAccountID string
	Description       string
	IntegrationData   map[string]any
}

// EnsureCloudAccountLink links a provider account into a business unit unless
// the company already has it. An existing link is matched by provider account
// ID; its credentials are not readable, so they are reported as not compared.
// The account linked in another business unit is a conflict, never a move.
func (c *Client) EnsureCloudAccountLink(ctx context.Context, spec LinkSpec, dryRun bool) (Result, error) {
	r := Result{Resource: "cloud_account"}
	accounts, err := c.ListCompanyCloudAccounts(ctx, spec.CompanyID)
	if err != nil {
		return r, err
	}
	matches := filterRecords(accounts, func(a Record) bool {
		return strings.EqualFold(a.String("cloud_provider_type"), spec.Provider) && a.String("provider_account_id") == spec.ProviderAccountID
	})
	if len(matches) > 1 {
		return r, &AmbiguousError{Resource: "cloud account", Selector: spec.ProviderAccountID, Count: len(matches)}
	}
	if len(matches) == 1 {
		account := matches[0]
		if account.String("business_unit") != spec.BusinessUnitID {
			return r, &ConflictError{Resource: "cloud_account", Reason: "linked_to_another_business_unit", ID: account.ID()}
		}
		r.ID, r.Status = account.ID(), StatusNoop
		r.Details = account.Project(CloudAccountFields...)
		r.Details["credentials_compared"] = false
		if account.String("name") != spec.Name {
			r.Drift, r.Status = []string{"name"}, StatusDrift
		}
		return r, nil
	}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	account := map[string]any{
		"business_unit_id":    spec.BusinessUnitID,
		"name":                spec.Name,
		"cloud_provider_type": spec.Provider,
		"provider_account_id": spec.ProviderAccountID,
	}
	if spec.Description != "" {
		account["description"] = spec.Description
	}
	payload := map[string]any{
		"cloud_account":             account,
		"cloud_account_integration": map[string]any{"cloud_provider_type": spec.Provider, "integration_data": spec.IntegrationData},
	}
	if _, err := c.postJSON(ctx, payload, c.gen.LinkCloudAccountApiV2CloudLinkCloudAccountPostWithBody); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Name == "CLOUD_ACCOUNT_INTEGRATION_ALREADY_EXISTS" {
			return r, &ConflictError{Resource: "cloud_account", Reason: "integration_exists_outside_selection"}
		}
		if !errors.Is(err, ErrUncertain) {
			return r, err
		}
	}
	after, readErr := c.ListCompanyCloudAccounts(ctx, spec.CompanyID)
	linked := filterRecords(after, func(a Record) bool {
		return strings.EqualFold(a.String("cloud_provider_type"), spec.Provider) && a.String("provider_account_id") == spec.ProviderAccountID
	})
	if readErr != nil || len(linked) != 1 || linked[0].String("business_unit") != spec.BusinessUnitID {
		return r, &UncertainError{Resource: "cloud_account"}
	}
	r.ID, r.Status, r.Details = linked[0].ID(), StatusCreated, linked[0].Project(CloudAccountFields...)
	return r, nil
}

// FreshnessDomains are the DR-034 domains of the account freshness contract.
var FreshnessDomains = []string{"resources", "cost", "cost_focus", "cost_attribution"}

func (c *Client) CloudAccountFreshness(ctx context.Context, accountID string) (Record, error) {
	id, ok := resourceUUID(accountID)
	if !ok {
		return nil, &NotFoundError{Resource: "cloud account", Selector: accountID}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetCloudAccountFreshnessStatusApiV2CloudCloudAccountsCloudAccountIdFreshnessStatusGet(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return dataRecord(body)
}

// DomainState extracts one domain's state from a freshness record.
func DomainState(freshness Record, domain string) (state string, summary Record) {
	raw, _ := freshness["domains"].([]any)
	for _, item := range raw {
		d, ok := item.(map[string]any)
		if !ok {
			continue
		}
		record := Record(d)
		if record.String("domain") == domain {
			return record.String("state"), record.Project("domain", "state", "last_observed_at", "last_successful_at", "expires_at", "coverage_status", "safe_error_summary")
		}
	}
	return "", nil
}

var RecommendationFields = []string{"id", "cloud_account", "title", "status", "category", "resource_type", "estimated_monthly_savings", "currency", "created_at", "updated_at", "version", "generation_status"}

func (c *Client) LatestRecommendations(ctx context.Context, accountID string) ([]Record, error) {
	id, ok := resourceUUID(accountID)
	if !ok {
		return nil, &NotFoundError{Resource: "cloud account", Selector: accountID}
	}
	return c.listAll(ctx, func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		return c.gen.ListLatestCustomRecommendationsForAccountApiV2CloudCloudAccountsCloudAccountIdCustomRecommendationsLatestGet(ctx, id, editors...)
	}, nil)
}

func (c *Client) GetCustomRecommendation(ctx context.Context, recommendationID string) (Record, error) {
	id, ok := resourceUUID(recommendationID)
	if !ok {
		return nil, &NotFoundError{Resource: "recommendation", Selector: recommendationID}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetCustomRecommendationByIdApiV2CloudCustomRecommendationsCustomRecommendationIdGet(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	return dataRecord(body)
}

// RecommendationSources lists the evidence sources a recommendation consumed.
func (c *Client) RecommendationSources(ctx context.Context, recommendationID string) ([]Record, error) {
	id, ok := resourceUUID(recommendationID)
	if !ok {
		return nil, &NotFoundError{Resource: "recommendation", Selector: recommendationID}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetCustomRecommendationSourcesApiV2CloudCustomRecommendationsCustomRecommendationIdSourcesGet(ctx, id)
	})
	if err != nil {
		return nil, err
	}
	if records, listErr := dataRecords(body); listErr == nil {
		return records, nil
	}
	record, err := dataRecord(body)
	if err != nil {
		return nil, err
	}
	if nested, ok := record["sources"].([]any); ok {
		out := []Record{}
		for _, item := range nested {
			if m, ok := item.(map[string]any); ok {
				out = append(out, Record(m))
			}
		}
		return out, nil
	}
	return []Record{record}, nil
}

func (c *Client) InspectRecommendationSource(ctx context.Context, recommendationID, sourceKey string) (Record, error) {
	id, ok := resourceUUID(recommendationID)
	if !ok {
		return nil, &NotFoundError{Resource: "recommendation", Selector: recommendationID}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.InspectCustomRecommendationSourceApiV2CloudCustomRecommendationsCustomRecommendationIdSourcesSourceKeyInspectionGet(ctx, id, sourceKey, nil)
	})
	if err != nil {
		return nil, err
	}
	return dataRecord(body)
}

// RegenerateRecommendation requests one new version. It is not idempotent:
// every accepted call starts inference work.
func (c *Client) RegenerateRecommendation(ctx context.Context, recommendationID string) (Record, error) {
	id, ok := resourceUUID(recommendationID)
	if !ok {
		return nil, &NotFoundError{Resource: "recommendation", Selector: recommendationID}
	}
	body, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.RegenerateCustomRecommendationApiV2CloudCustomRecommendationsCustomRecommendationIdActionRegeneratePost(ctx, id, generated.RegenerateCustomRecommendationRequest{})
	})
	if err != nil {
		return nil, err
	}
	record, decodeErr := dataRecord(body)
	if decodeErr != nil {
		return Record{}, nil
	}
	return record.Project(RecommendationFields...), nil
}

var CostSourceFields = []string{"id", "name", "company", "business_unit", "cloud_provider_type", "source_kind", "provider_scope_id", "contract_version", "is_enabled", "status", "validation_status", "last_validated_at", "import_status", "last_imported_at", "coverage", "provider_config"}

// costSourcePublicConfig is the only provider_config output; external IDs and
// any other credential material never leave the process.
var costSourcePublicConfig = []string{"bucket", "prefix", "region", "role_arn", "source_kind", "export_project_id", "dataset", "table", "location", "query_project_id", "storage_account", "container", "credential_cloud_account_id"}

func PublicCostSource(r Record) Record {
	out := r.Project(CostSourceFields...)
	if config, ok := r["provider_config"].(map[string]any); ok {
		out["provider_config"] = Record(config).Project(costSourcePublicConfig...)
	}
	return out
}

func (c *Client) ListCostSources(ctx context.Context, companyID string) ([]Record, error) {
	id, ok := resourceUUID(companyID)
	if !ok {
		return nil, errors.New("company ID required")
	}
	return c.listAll(ctx, func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		return c.gen.ListCostDataSourcesApiV2CloudCostDataSourcesGet(ctx, &generated.ListCostDataSourcesApiV2CloudCostDataSourcesGetParams{CompanyId: id}, editors...)
	}, nil)
}

func (c *Client) GetCostSource(ctx context.Context, companyID, sourceID string) (Record, error) {
	company, okCompany := resourceUUID(companyID)
	source, okSource := resourceUUID(sourceID)
	if !okCompany || !okSource {
		return nil, &NotFoundError{Resource: "cost source", Selector: sourceID}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetCostDataSourceApiV2CloudCostDataSourcesSourceIdGet(ctx, source, &generated.GetCostDataSourceApiV2CloudCostDataSourcesSourceIdGetParams{CompanyId: company})
	})
	if err != nil {
		return nil, err
	}
	return dataRecord(body)
}

// ResolveCostSource accepts an ID or exact name within a company.
func (c *Client) ResolveCostSource(ctx context.Context, companyID, selector string) (Record, error) {
	sources, err := c.ListCostSources(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"id", "name"} {
		if matches := filterRecords(sources, func(r Record) bool { return r.String(field) == selector }); len(matches) > 0 {
			return selectOne("cost source", selector, matches)
		}
	}
	return nil, &NotFoundError{Resource: "cost source", Selector: selector}
}

type CoverageItem struct {
	CloudAccountID    string `json:"cloud_account_id"`
	ProviderAccountID string `json:"provider_account_id"`
}

type CostSourceSpec struct {
	CompanyID       string
	BusinessUnitID  string
	Provider        string
	SourceKind      string
	Name            string
	ProviderScopeID string
	ContractVersion string
	Coverage        []CoverageItem
	ProviderConfig  map[string]any
}

// EnsureCostSource creates a disabled FOCUS source unless the company already
// has one with the name. Enabling is a separate, explicit step because it
// starts imports.
func (c *Client) EnsureCostSource(ctx context.Context, spec CostSourceSpec, dryRun bool) (Result, error) {
	r := Result{Resource: "cost_source"}
	sources, err := c.ListCostSources(ctx, spec.CompanyID)
	if err != nil {
		return r, err
	}
	matches := filterRecords(sources, func(s Record) bool { return s.String("name") == spec.Name })
	if len(matches) > 1 {
		return r, &AmbiguousError{Resource: "cost source", Selector: spec.Name, Count: len(matches)}
	}
	if len(matches) == 1 {
		source := matches[0]
		r.ID, r.Status, r.Details = source.ID(), StatusNoop, PublicCostSource(source)
		r.Drift = costSourceDrift(source, spec)
		if len(r.Drift) > 0 {
			r.Status = StatusDrift
		}
		return r, nil
	}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	payload := map[string]any{
		"company_id":          spec.CompanyID,
		"cloud_provider_type": spec.Provider,
		"source_kind":         spec.SourceKind,
		"name":                spec.Name,
		"provider_scope_id":   spec.ProviderScopeID,
		"coverage":            spec.Coverage,
		"provider_config":     spec.ProviderConfig,
		"is_enabled":          false,
	}
	if spec.BusinessUnitID != "" {
		payload["business_unit_id"] = spec.BusinessUnitID
	}
	if spec.ContractVersion != "" {
		payload["contract_version"] = spec.ContractVersion
	}
	if _, err := c.postJSON(ctx, payload, c.gen.CreateCostDataSourceApiV2CloudCostDataSourcesPostWithBody); err != nil && !errors.Is(err, ErrUncertain) {
		return r, err
	}
	after, readErr := c.ListCostSources(ctx, spec.CompanyID)
	created := filterRecords(after, func(s Record) bool { return s.String("name") == spec.Name })
	if readErr != nil || len(created) != 1 {
		return r, &UncertainError{Resource: "cost_source"}
	}
	r.ID, r.Status, r.Details = created[0].ID(), StatusCreated, PublicCostSource(created[0])
	return r, nil
}

func costSourceDrift(source Record, spec CostSourceSpec) []string {
	drift := []string{}
	if !strings.EqualFold(source.String("source_kind"), spec.SourceKind) {
		drift = append(drift, "source_kind")
	}
	if source.String("provider_scope_id") != spec.ProviderScopeID {
		drift = append(drift, "provider_scope_id")
	}
	want := []string{}
	for _, item := range spec.Coverage {
		want = append(want, item.ProviderAccountID)
	}
	have := []string{}
	if raw, ok := source["coverage"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				have = append(have, Record(m).String("provider_account_id"))
			}
		}
	}
	if !sameSet(want, have) {
		drift = append(drift, "coverage")
	}
	config, _ := source["provider_config"].(map[string]any)
	keys := make([]string, 0, len(spec.ProviderConfig))
	for key := range spec.ProviderConfig {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !containsString(costSourcePublicConfig, key) {
			continue // credential fields are write-only and cannot be compared
		}
		if fmt.Sprint(spec.ProviderConfig[key]) != fmt.Sprint(config[key]) {
			drift = append(drift, "provider_config."+key)
		}
	}
	return drift
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func (c *Client) ValidateCostSource(ctx context.Context, companyID, sourceID string, live bool) (Record, error) {
	source, ok := resourceUUID(sourceID)
	if !ok {
		return nil, &NotFoundError{Resource: "cost source", Selector: sourceID}
	}
	reader, err := jsonBody(map[string]any{"company_id": companyID, "live_check": live})
	if err != nil {
		return nil, err
	}
	body, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.ValidateCostDataSourceApiV2CloudCostDataSourcesSourceIdActionValidatePostWithBody(ctx, source, "application/json", reader)
	})
	if err != nil {
		return nil, err
	}
	record, decodeErr := dataRecord(body)
	if decodeErr != nil {
		return Record{}, nil
	}
	return PublicCostSource(record), nil
}

// SetCostSourceEnabled changes is_enabled only when it differs.
func (c *Client) SetCostSourceEnabled(ctx context.Context, companyID, sourceID string, enabled, dryRun bool) (Result, error) {
	r := Result{Resource: "cost_source", ID: sourceID}
	current, err := c.GetCostSource(ctx, companyID, sourceID)
	if err != nil {
		return r, err
	}
	if state, ok := current.Bool("is_enabled"); ok && state == enabled {
		r.Status, r.Details = StatusNoop, PublicCostSource(current)
		return r, nil
	}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	source, _ := resourceUUID(sourceID)
	reader, err := jsonBody(map[string]any{"company_id": companyID, "is_enabled": enabled})
	if err != nil {
		return r, err
	}
	if _, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.SetCostDataSourceEnabledApiV2CloudCostDataSourcesSourceIdActionSetEnabledPostWithBody(ctx, source, "application/json", reader)
	}); err != nil && !errors.Is(err, ErrUncertain) {
		return r, err
	}
	after, readErr := c.GetCostSource(ctx, companyID, sourceID)
	if state, ok := after.Bool("is_enabled"); readErr != nil || !ok || state != enabled {
		return r, &UncertainError{Resource: "cost_source"}
	}
	r.Status, r.Details = StatusUpdated, PublicCostSource(after)
	return r, nil
}

// ListAccessibleCloudAccounts lists every cloud account the Backend authorizes
// for the caller, across companies.
func (c *Client) ListAccessibleCloudAccounts(ctx context.Context) ([]Record, error) {
	return c.listAll(ctx, func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		return c.gen.ListCloudAccountsApiV2CloudCloudAccountsGet(ctx, nil, editors...)
	}, nil)
}
