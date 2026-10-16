package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// Result is the outcome of an idempotent write. Drift names the fields that
// differ from the request; a drifted resource is reported, never changed.
type Result struct {
	Resource string   `json:"resource"`
	Status   string   `json:"status"`
	ID       string   `json:"id,omitempty"`
	Drift    []string `json:"drift,omitempty"`
	Details  Record   `json:"details,omitempty"`
}

const (
	StatusCreated = "created"
	StatusUpdated = "updated"
	StatusNoop    = "noop"
	StatusPlanned = "planned"
	StatusDrift   = "drift"
)

// UncertainError reports a write that may have committed. The caller must
// reconcile by reading, not by resending.
type UncertainError struct{ Resource string }

func (e *UncertainError) Error() string {
	return e.Resource + " write outcome uncertain; read before retrying"
}

func (e *UncertainError) Unwrap() error { return ErrUncertain }

// ConflictError reports state that blocks the write and needs a human decision.
type ConflictError struct{ Resource, Reason, ID string }

func (e *ConflictError) Error() string {
	msg := e.Resource + " conflict: " + e.Reason
	if e.ID != "" {
		msg += " (" + e.ID + ")"
	}
	return msg
}

var CompanyFields = []string{"id", "name", "slug", "domain_emails", "is_root", "status", "description", "website_url", "contact_email", "country"}

func (c *Client) ListCompanies(ctx context.Context, filters map[string]string) ([]Record, error) {
	return c.listAll(ctx, c.gen.ListCompaniesApiV2OrganizationCompaniesGet, filters)
}

// ResolveCompany accepts an ID, slug or exact name.
func (c *Client) ResolveCompany(ctx context.Context, selector string) (Record, error) {
	if id, ok := resourceUUID(selector); ok {
		body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
			return c.gen.GetCompanyByIdApiV2OrganizationCompaniesCompanyIdGet(ctx, id)
		})
		if err != nil {
			if IsNotFound(err) {
				return nil, &NotFoundError{Resource: "company", Selector: selector}
			}
			return nil, err
		}
		return dataRecord(body)
	}
	for _, field := range []string{"slug", "name"} {
		matches, err := c.ListCompanies(ctx, map[string]string{field: "eq " + selector})
		if err != nil {
			return nil, err
		}
		if len(matches) > 0 {
			return selectOne("company", selector, matches)
		}
	}
	return nil, &NotFoundError{Resource: "company", Selector: selector}
}

type CompanySpec struct {
	Name         string
	Slug         string
	DomainEmails []string
	Description  string
	License      *LicenseSpec
}

type LicenseSpec struct {
	PricingPlanID   string
	Name            string
	Description     string
	BillingInterval string
}

// EnsureCompany creates the company only when no company has the slug, the
// name or any of the domains; a name or domain under another slug is a
// conflict. With a license it uses the Backend's atomic company-plus-license
// setup, so a company is never left without the license it was created for.
func (c *Client) EnsureCompany(ctx context.Context, spec CompanySpec, dryRun bool) (Result, error) {
	r := Result{Resource: "company"}
	existing, err := c.ListCompanies(ctx, map[string]string{"slug": "eq " + spec.Slug})
	if err != nil {
		return r, err
	}
	if len(existing) > 1 {
		return r, &AmbiguousError{Resource: "company", Selector: spec.Slug, Count: len(existing)}
	}
	if len(existing) == 1 {
		company := existing[0]
		r.ID, r.Status = company.ID(), StatusNoop
		r.Details = company.Project(CompanyFields...)
		if company.String("name") != spec.Name {
			r.Drift = append(r.Drift, "name")
		}
		if !sameSet(company.Strings("domain_emails"), spec.DomainEmails) {
			r.Drift = append(r.Drift, "domain_emails")
		}
		if root, _ := company.Bool("is_root"); root {
			r.Drift = append(r.Drift, "is_root")
		}
		if len(r.Drift) > 0 {
			r.Status = StatusDrift
		}
		return r, nil
	}
	other, reason, err := c.companyClaiming(ctx, spec)
	if err != nil {
		return r, err
	}
	if other != nil {
		return r, &ConflictError{Resource: "company", Reason: reason, ID: other.ID()}
	}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	company := map[string]any{"name": spec.Name, "slug": spec.Slug, "domain_emails": spec.DomainEmails}
	if spec.Description != "" {
		company["description"] = spec.Description
	}
	var writeErr error
	if spec.License != nil {
		license := map[string]any{"pricing_plan_id": spec.License.PricingPlanID}
		for key, value := range map[string]string{"name": spec.License.Name, "description": spec.License.Description, "billing_interval": spec.License.BillingInterval} {
			if value != "" {
				license[key] = value
			}
		}
		_, writeErr = c.postJSON(ctx, map[string]any{"company": company, "license": license}, c.gen.SetupCompanyApiV2OrganizationActionSetupCompanyPostWithBody)
	} else {
		company["is_root"] = false
		_, writeErr = c.postJSON(ctx, company, c.gen.CreateCompanyApiV2OrganizationCompaniesPostWithBody)
	}
	if writeErr != nil && !errors.Is(writeErr, ErrUncertain) {
		return r, writeErr
	}
	after, readErr := c.ListCompanies(ctx, map[string]string{"slug": "eq " + spec.Slug})
	if readErr != nil || len(after) != 1 {
		return r, &UncertainError{Resource: "company"}
	}
	r.ID, r.Status, r.Details = after[0].ID(), StatusCreated, after[0].Project(CompanyFields...)
	return r, nil
}

// companyClaiming finds a company under another slug that already has the
// name or one of the domains. Creating beside it would leave two tenants that
// people confuse and that both claim the domain's sign-ins.
func (c *Client) companyClaiming(ctx context.Context, spec CompanySpec) (Record, string, error) {
	companies, err := c.ListCompanies(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	for _, company := range companies {
		if strings.EqualFold(strings.TrimSpace(company.String("name")), strings.TrimSpace(spec.Name)) {
			return company, "name_in_use", nil
		}
		for _, domain := range company.Strings("domain_emails") {
			for _, want := range spec.DomainEmails {
				if emailDomain(domain) == emailDomain(want) {
					return company, "domain_in_use", nil
				}
			}
		}
	}
	return nil, "", nil
}

// emailDomain reduces "billing@acme.com" and "Acme.com" alike to "acme.com".
func emailDomain(value string) string {
	_, domain, found := strings.Cut(value, "@")
	if !found {
		domain = value
	}
	return strings.ToLower(strings.TrimSpace(domain))
}

type bodyCall func(context.Context, string, io.Reader, ...generated.RequestEditorFn) (*http.Response, error)

func (c *Client) postJSON(ctx context.Context, payload any, do bodyCall) (Record, error) {
	reader, err := jsonBody(payload)
	if err != nil {
		return nil, err
	}
	body, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return do(ctx, "application/json", reader)
	})
	if err != nil {
		return nil, err
	}
	record, decodeErr := dataRecord(body)
	if decodeErr != nil {
		return Record{}, nil
	}
	return record, nil
}

var BusinessUnitFields = []string{"id", "name", "slug", "company", "description", "managed_agent", "status"}

func (c *Client) ListBusinessUnitsForCompany(ctx context.Context, companyID string) ([]Record, error) {
	return c.listAll(ctx, c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGet, map[string]string{"company": "eq " + companyID})
}

// ResolveBusinessUnit accepts an ID, slug or exact name within one company.
func (c *Client) ResolveBusinessUnit(ctx context.Context, companyID, selector string) (Record, error) {
	units, err := c.ListBusinessUnitsForCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if _, ok := resourceUUID(selector); ok {
		return selectOne("business unit", selector, filterRecords(units, func(r Record) bool { return r.ID() == selector }))
	}
	for _, field := range []string{"slug", "name"} {
		if matches := filterRecords(units, func(r Record) bool { return r.String(field) == selector }); len(matches) > 0 {
			return selectOne("business unit", selector, matches)
		}
	}
	return nil, &NotFoundError{Resource: "business unit", Selector: selector}
}

type BusinessUnitSpec struct {
	CompanyID   string
	Name        string
	Slug        string
	Description string
}

// EnsureBusinessUnit creates a business unit only when the company has no unit
// with the slug. The Backend does not enforce slug uniqueness and provisions a
// managed agent after commit, so callers must serialize runs and never resend
// after an uncertain outcome.
func (c *Client) EnsureBusinessUnit(ctx context.Context, spec BusinessUnitSpec, dryRun bool) (Result, error) {
	r := Result{Resource: "business_unit"}
	units, err := c.ListBusinessUnitsForCompany(ctx, spec.CompanyID)
	if err != nil {
		return r, err
	}
	matches := filterRecords(units, func(u Record) bool { return u.String("slug") == spec.Slug })
	if len(matches) > 1 {
		return r, &AmbiguousError{Resource: "business unit", Selector: spec.Slug, Count: len(matches)}
	}
	if len(matches) == 1 {
		unit := matches[0]
		r.ID, r.Status, r.Details = unit.ID(), StatusNoop, unit.Project(BusinessUnitFields...)
		if unit.String("name") != spec.Name {
			r.Drift, r.Status = []string{"name"}, StatusDrift
		}
		return r, nil
	}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	payload := map[string]any{"company_id": spec.CompanyID, "name": spec.Name, "slug": spec.Slug}
	if spec.Description != "" {
		payload["description"] = spec.Description
	}
	if _, err := c.postJSON(ctx, payload, c.gen.CreateBusinessUnitApiV2OrganizationBusinessUnitsPostWithBody); err != nil && !errors.Is(err, ErrUncertain) {
		return r, err
	}
	after, readErr := c.ListBusinessUnitsForCompany(ctx, spec.CompanyID)
	created := filterRecords(after, func(u Record) bool { return u.String("slug") == spec.Slug })
	if readErr != nil || len(created) != 1 {
		return r, &UncertainError{Resource: "business_unit"}
	}
	r.ID, r.Status, r.Details = created[0].ID(), StatusCreated, created[0].Project(BusinessUnitFields...)
	return r, nil
}

var PricingPlanFields = []string{
	"id", "internal_name", "display_name", "is_active", "default_billing_interval", "currency_code",
	"price_per_month", "price_per_year", "performance_fee_enabled", "performance_fee_rate_bps",
	"performance_fee_allowance_yearly_cents", "performance_fee_term_months",
	"max_accounts", "max_business_units", "max_seats", "max_granted_tokens",
}

func (c *Client) ListPricingPlans(ctx context.Context, filters map[string]string) ([]Record, error) {
	return c.listAll(ctx, c.gen.ListPricingPlansApiV2LicenseAndBillingPricingPlansGet, filters)
}

// ResolvePricingPlan accepts an ID or internal name.
func (c *Client) ResolvePricingPlan(ctx context.Context, selector string) (Record, error) {
	filter := map[string]string{"internal_name": "eq " + selector}
	if _, ok := resourceUUID(selector); ok {
		filter = map[string]string{"id": "eq " + selector}
	}
	plans, err := c.ListPricingPlans(ctx, filter)
	if err != nil {
		return nil, err
	}
	return selectOne("pricing plan", selector, plans)
}

var LicenseFields = []string{
	"id", "company", "pricing_plan", "status", "name", "description", "billing_interval", "currency_code",
	"valid_from", "valid_until", "price_per_month", "price_per_year", "performance_fee_enabled",
	"performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months",
	"max_accounts", "max_business_units", "max_seats", "max_granted_tokens", "available_tokens",
}

func (c *Client) ListLicenses(ctx context.Context, companyID string) ([]Record, error) {
	return c.listAll(ctx, c.gen.ListLicensesApiV2LicenseAndBillingLicensesGet, map[string]string{"company": "eq " + companyID})
}

func (c *Client) GetLicense(ctx context.Context, id string) (Record, error) {
	licenseID, ok := resourceUUID(id)
	if !ok {
		return nil, &NotFoundError{Resource: "license", Selector: id}
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetLicenseByIdApiV2LicenseAndBillingLicensesLicenseIdGet(ctx, openapi_types.UUID(licenseID))
	})
	if err != nil {
		return nil, err
	}
	return dataRecord(body)
}

func sameSet(a, b []string) bool {
	x, y := normalized(a), normalized(b)
	return slices.Equal(x, y)
}

func normalized(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, strings.ToLower(strings.TrimSpace(v)))
	}
	sort.Strings(out)
	return slices.Compact(out)
}
