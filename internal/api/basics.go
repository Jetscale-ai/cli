package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

// BasicsManifest is deliberately not general CRUD. Slugs and either a pricing
// plan UUID or the supported named profile must be explicit. BU creation is
// unsupported until provisioning is retry-safe.
// Settings take a scalar JSON value, not the Backend's {"value": ...} envelope.
// Flag writes require currently enabled parents (not merely planned enables).
// UUID-only plans must be free with disabled fees; the named profile must match
// its exact prices and fee terms. Null quotas must be explicit unlimited.
// A selected BU's unknown external agent blocks Ready, not safe local settings.
// Unknown JSON fields (including BU IDs) are rejected rather than ignored.
type BasicsManifest struct {
	Company      generated.CreateCompanyRequest           `json:"company"`
	License      generated.SetupRootCompanyLicensePayload `json:"license"`
	PricingPlan  *BasicsPricingPlan                       `json:"pricing_plan,omitempty"`
	BusinessUnit *BasicsBusinessUnit                      `json:"business_unit,omitempty"`
	Flags        BasicsFlags                              `json:"flags,omitempty"`
	Settings     BasicsSettings                           `json:"settings,omitempty"`
}

type BasicsBusinessUnit struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type BasicsFlags struct {
	Root         map[string]bool `json:"root,omitempty"`
	Company      map[string]bool `json:"company,omitempty"`
	BusinessUnit map[string]bool `json:"business_unit,omitempty"`
}

type BasicsSettings struct {
	Root         map[string]BasicsSetting `json:"root,omitempty"`
	Company      map[string]BasicsSetting `json:"company,omitempty"`
	BusinessUnit map[string]BasicsSetting `json:"business_unit,omitempty"`
}

type BasicsSetting struct {
	ValueType generated.SystemSettingValueType `json:"value_type"`
	Value     json.RawMessage                  `json:"value"`
}

func (m *BasicsManifest) UnmarshalJSON(data []byte) error {
	type plain BasicsManifest
	var decoded plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if len(data) > basicsMaxBody || d.Decode(&decoded) != nil {
		return errors.New("basics: invalid manifest")
	}
	*m = BasicsManifest(decoded)
	return nil
}

// BasicsResult contains only IDs, fixed reason codes and gates, never API bodies,
// setting values, company contact details or external managed-agent assertions.
// Ready remains false for a selected BU: its external agent cannot be inspected.
type BasicsResult struct {
	Apply            bool            `json:"apply"`
	Ready            bool            `json:"ready"`
	OutcomeUncertain bool            `json:"outcome_uncertain"`
	CompanyID        string          `json:"company_id,omitempty"`
	LicenseID        string          `json:"license_id,omitempty"`
	PricingPlanID    string          `json:"pricing_plan_id,omitempty"`
	BusinessUnitID   string          `json:"business_unit_id,omitempty"`
	ManagedAgentID   string          `json:"managed_agent_id,omitempty"`
	Gates            BasicsGates     `json:"gates"`
	Actions          []BasicsAction  `json:"actions"`
	Blockers         []BasicsBlocker `json:"blockers"`
}

type BasicsGates struct {
	Company              bool `json:"company"`
	License              bool `json:"license"`
	PricingPlan          bool `json:"pricing_plan"`
	BusinessUnit         bool `json:"business_unit"`
	ManagedAgentLocal    bool `json:"managed_agent_local"`
	ManagedAgentExternal bool `json:"managed_agent_external"`
	Configuration        bool `json:"configuration"`
}

type BasicsAction struct {
	Resource       string `json:"resource"`
	Scope          string `json:"scope,omitempty"`
	Key            string `json:"key,omitempty"`
	ID             string `json:"id,omitempty"`
	Operation      string `json:"operation"`
	Status         string `json:"status"` // noop, planned, applied, blocked, outcome_uncertain
	Reason         string `json:"reason,omitempty"`
	observed       string
	cascadeChildID uuid.UUID
}

type BasicsBlocker struct {
	Resource string `json:"resource"`
	Scope    string `json:"scope,omitempty"`
	Key      string `json:"key,omitempty"`
	Reason   string `json:"reason"`
}

const (
	basicsMaxBody  = 1 << 20
	basicsPageSize = 100
	basicsMaxPages = 20
)

var basicsSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Conservative subset of Backend FeatureFlagKeys.py. Integration, billing,
// auth, experimental/per-cloud-agent and remediation keys are not supported.
var basicsSafeFlags = map[string]bool{
	"AGENTS.EXPLORER":                    true,
	"AGENTS.RECOMMENDATION_ORCHESTRATOR": true,
	"CLOUD.AWS.FOCUS":                    true,
	"CLOUD.AZURE.FOCUS":                  true,
	"CLOUD.GCP.PLATFORM":                 true,
	"CLOUD.FOCUS":                        true,
	"PRODUCT.MANAGEMENT_DASHBOARD":       true,
}

type basicsCompany struct {
	generated.CreateCompanyRequest
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
}

type basicsPlan struct {
	ID                     uuid.UUID       `json:"id"`
	InternalName           string          `json:"internal_name"`
	DisplayName            string          `json:"display_name"`
	DefaultBillingInterval string          `json:"default_billing_interval"`
	CurrencyCode           string          `json:"currency_code"`
	FeeAllowance           json.RawMessage `json:"performance_fee_allowance_yearly_cents"`
	FeeTerm                json.RawMessage `json:"performance_fee_term_months"`
	Active                 *bool           `json:"is_active"`
	MaxAccounts            json.RawMessage `json:"max_accounts"`
	MaxBusinessUnits       json.RawMessage `json:"max_business_units"`
	MaxSeats               json.RawMessage `json:"max_seats"`
	MaxTokens              json.RawMessage `json:"max_granted_tokens"`
	FeeEnabled             *bool           `json:"performance_fee_enabled"`
	FeeRate                json.RawMessage `json:"performance_fee_rate_bps"`
	Monthly                json.RawMessage `json:"price_per_month"`
	Yearly                 json.RawMessage `json:"price_per_year"`
}

type basicsLicense struct {
	ID               uuid.UUID                 `json:"id"`
	Company          uuid.UUID                 `json:"company"`
	Plan             uuid.UUID                 `json:"pricing_plan"`
	Status           string                    `json:"status"`
	MaxAccounts      json.RawMessage           `json:"max_accounts"`
	MaxBusinessUnits json.RawMessage           `json:"max_business_units"`
	MaxSeats         json.RawMessage           `json:"max_seats"`
	MaxTokens        json.RawMessage           `json:"max_granted_tokens"`
	AvailableTokens  json.RawMessage           `json:"available_tokens"`
	ValidFrom        time.Time                 `json:"valid_from"`
	ValidUntil       *time.Time                `json:"valid_until"`
	BillingInterval  generated.BillingInterval `json:"billing_interval"`
	Name             *string                   `json:"name"`
	Description      *string                   `json:"description"`
	CurrencyCode     string                    `json:"currency_code"`
	FeeAllowance     json.RawMessage           `json:"performance_fee_allowance_yearly_cents"`
	FeeTerm          json.RawMessage           `json:"performance_fee_term_months"`
	FeeEnabled       json.RawMessage           `json:"performance_fee_enabled"`
	FeeRate          json.RawMessage           `json:"performance_fee_rate_bps"`
	Monthly          json.RawMessage           `json:"price_per_month"`
	Yearly           json.RawMessage           `json:"price_per_year"`
}

type basicsBU struct {
	ID           uuid.UUID  `json:"id"`
	Company      uuid.UUID  `json:"company"`
	Name         string     `json:"name"`
	Slug         string     `json:"slug"`
	ManagedAgent *uuid.UUID `json:"managed_agent"`
}

type basicsSnapshot struct {
	company *basicsCompany
	license *basicsLicense
	bu      *basicsBU
	plan    *basicsPlan
	valid   bool
}

func (r *BasicsResult) block(resource, scope, key, reason string) {
	r.Blockers = append(r.Blockers, BasicsBlocker{Resource: resource, Scope: scope, Key: key, Reason: reason})
}

// PrepareBasics plans by default. Apply uses single-attempt generated requests,
// rechecks prerequisites and each target before writing, then reads back. Any
// uncertain mutation stops the run; callers must assess state before rerunning.
// Independent company setup may proceed without a BU; its configuration is
// explicitly deferred until a subsequent assessment of the new tenant.
// No destination/channel assertion belongs here; the command owns that boundary.
func (c *Client) PrepareBasics(ctx context.Context, manifest BasicsManifest, apply bool) (BasicsResult, error) {
	r := BasicsResult{Apply: apply, Actions: []BasicsAction{}, Blockers: []BasicsBlocker{}}
	if !basicsValidateManifest(manifest, &r) {
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	snapshot, err := c.basicsInspect(ctx, manifest, &r)
	if err != nil {
		return r, errors.New("basics: assessment failed")
	}
	if !snapshot.valid {
		return r, nil
	}
	if snapshot.company == nil {
		r.Actions = append(r.Actions, BasicsAction{Resource: "company", Operation: "setup", Status: "planned"})
		if manifest.BusinessUnit != nil {
			r.Actions = append(r.Actions, BasicsAction{Resource: "business_unit", Operation: "create", Status: "blocked", Reason: "unsafe_external_provisioning"})
			r.block("business_unit", "", "", "unsafe_external_provisioning")
		}
		basicsPendingConfiguration(manifest, &r)
		if !apply {
			return r, nil
		}
		// Recheck slug absence and approved plan immediately before the one POST.
		freshResult := BasicsResult{}
		fresh, inspectErr := c.basicsInspect(ctx, manifest, &freshResult)
		if inspectErr != nil || !fresh.valid || fresh.company != nil || !reflect.DeepEqual(snapshot.plan, fresh.plan) {
			r.Actions[0].Status = "blocked"
			r.Actions[0].Reason = "prerequisite_drift"
			r.block("company", "", "", "prerequisite_drift")
			return r, errors.New("basics: prerequisites changed")
		}
		company := manifest.Company
		license := manifest.License
		license.PricingPlanId = fresh.plan.ID
		body := generated.SetupRootCompanyRequest{Company: generated.SetupRootCompanyCompanyPayload{
			Name: company.Name, Slug: company.Slug, DomainEmails: company.DomainEmails,
			Description: company.Description, WebsiteUrl: company.WebsiteUrl, Address: company.Address,
			ContactEmail: company.ContactEmail, PhoneNumber: company.PhoneNumber, Country: company.Country,
		}, License: license}
		writeCtx, writeCancel := context.WithTimeout(ctx, 15*time.Second)
		_, err = basicsReadResponse(c.gen.SetupCompanyApiV2OrganizationActionSetupCompanyPost(writeCtx, body))
		writeCancel()
		if err != nil {
			return basicsUncertain(r, 0)
		}
		afterResult := BasicsResult{}
		after, inspectErr := c.basicsInspect(ctx, manifest, &afterResult)
		if inspectErr != nil || !after.valid || after.company == nil || after.license == nil || !reflect.DeepEqual(snapshot.plan, after.plan) {
			return basicsUncertain(r, 0)
		}
		r.Actions[0].Status = "applied"
		r.Actions[0].ID = after.company.ID.String()
		r.CompanyID, r.LicenseID = afterResult.CompanyID, afterResult.LicenseID
		r.Gates.Company, r.Gates.License = true, true
		r.Gates.Configuration = len(manifest.Flags.Root)+len(manifest.Flags.Company)+len(manifest.Flags.BusinessUnit)+len(manifest.Settings.Root)+len(manifest.Settings.Company)+len(manifest.Settings.BusinessUnit) == 0
		r.Ready = manifest.BusinessUnit == nil && r.Gates.Configuration
		// New tenant configuration is a subsequent assessed run, never a blind
		// continuation across an unknown BU/external provisioning prerequisite.
		return r, nil
	}

	r.Actions = append(r.Actions, BasicsAction{Resource: "company", Operation: "ensure", Status: "noop", ID: r.CompanyID}, BasicsAction{Resource: "license", Operation: "ensure", Status: "noop", ID: r.LicenseID})
	if manifest.BusinessUnit != nil {
		if snapshot.bu == nil {
			r.Actions = append(r.Actions, BasicsAction{Resource: "business_unit", Operation: "create", Status: "blocked", Reason: "unsafe_external_provisioning"})
			r.block("business_unit", "", "", "unsafe_external_provisioning")
			basicsPendingConfiguration(manifest, &r)
			return r, nil
		}
		r.Actions = append(r.Actions, BasicsAction{Resource: "business_unit", Operation: "ensure", Status: "noop", ID: r.BusinessUnitID})
		r.block("managed_agent", "business_unit", "", "external_state_unknown")
		if !r.Gates.ManagedAgentLocal {
			r.block("managed_agent", "business_unit", "", "local_reference_missing")
			basicsPendingConfiguration(manifest, &r)
			return r, nil
		}
	}
	jobs, err := c.basicsConfiguration(ctx, manifest, snapshot, &r)
	if err != nil {
		return r, errors.New("basics: configuration assessment failed")
	}
	for _, action := range r.Actions {
		if action.Status == "blocked" {
			return r, nil
		}
	}
	if apply {
		for _, job := range jobs {
			if r.Actions[job.index].Status != "planned" {
				continue
			}
			freshResult := BasicsResult{}
			fresh, inspectErr := c.basicsInspect(ctx, manifest, &freshResult)
			if inspectErr != nil || !basicsSameSnapshot(snapshot, fresh) {
				r.Actions[job.index].Status = "blocked"
				r.Actions[job.index].Reason = "prerequisite_drift"
				r.block("configuration", "", "", "prerequisite_drift")
				return r, errors.New("basics: prerequisites changed")
			}
			action, readErr := job.inspect(ctx)
			if readErr != nil || action.Status == "blocked" {
				r.Actions[job.index].Status = "blocked"
				r.Actions[job.index].Reason = "target_drift"
				r.block(action.Resource, action.Scope, action.Key, "target_drift")
				return r, errors.New("basics: target changed")
			}
			if action.Status == "noop" {
				r.Actions[job.index] = action
				continue
			}
			if action.ID != r.Actions[job.index].ID || action.Operation != r.Actions[job.index].Operation || action.observed != r.Actions[job.index].observed || action.cascadeChildID != r.Actions[job.index].cascadeChildID {
				r.block(action.Resource, action.Scope, action.Key, "target_drift")
				r.Actions[job.index].Status = "blocked"
				return r, errors.New("basics: target changed")
			}
			if job.write(ctx, action) != nil {
				return basicsUncertain(r, job.index)
			}
			verified, verifyErr := job.inspect(ctx)
			if verifyErr != nil || verified.Status != "noop" || (action.ID != "" && verified.ID != action.ID) || (action.cascadeChildID != uuid.Nil && verified.cascadeChildID != action.cascadeChildID) {
				return basicsUncertain(r, job.index)
			}
			verified.Status = "applied"
			r.Actions[job.index] = verified
		}
	}
	r.Gates.Configuration = true
	for _, action := range r.Actions {
		if action.Status == "planned" {
			r.Gates.Configuration = false
		}
	}
	r.Ready = len(r.Blockers) == 0 && r.Gates.Company && r.Gates.License && r.Gates.Configuration
	return r, nil
}

func basicsUncertain(r BasicsResult, index int) (BasicsResult, error) {
	r.OutcomeUncertain = true
	r.Actions[index].Status = "outcome_uncertain"
	r.Actions[index].Reason = "assess_before_rerun"
	r.block(r.Actions[index].Resource, r.Actions[index].Scope, r.Actions[index].Key, "outcome_uncertain")
	return r, errors.New("basics: mutation outcome uncertain; assess before rerun")
}

// ValidateBasicsManifest performs the same offline preflight as PrepareBasics,
// without inspecting remote state or returning manifest values in errors.
func ValidateBasicsManifest(manifest BasicsManifest) error {
	var result BasicsResult
	if !basicsValidateManifest(manifest, &result) {
		return errors.New("basics: invalid manifest")
	}
	return nil
}

func basicsValidateManifest(m BasicsManifest, r *BasicsResult) bool {
	encoded, encodeErr := json.Marshal(m)
	if encodeErr != nil || len(encoded) > basicsMaxBody {
		r.block("manifest", "", "", "invalid_or_oversized_manifest")
		return false
	}
	if strings.TrimSpace(m.Company.Name) != m.Company.Name {
		r.block("manifest", "", "", "invalid_company_name")
	}
	seenDomains := map[string]bool{}
	for _, domain := range m.Company.DomainEmails {
		if len(domain) == 0 || len(domain) > 253 || strings.TrimSpace(domain) != domain || strings.ContainsAny(domain, " \t\r\n/@") || seenDomains[domain] {
			r.block("manifest", "", "", "invalid_company_domains")
			break
		}
		seenDomains[domain] = true
	}
	if m.Company.Slug == nil || !basicsSlug.MatchString(*m.Company.Slug) || len(*m.Company.Slug) > 100 || len(m.Company.Name) == 0 || len(m.Company.Name) > 255 || len(m.Company.DomainEmails) == 0 || len(m.Company.DomainEmails) > 10 || (m.Company.IsRoot != nil && *m.Company.IsRoot) || (m.PricingPlan == nil && m.License.PricingPlanId == uuid.Nil) {
		r.block("manifest", "", "", "invalid_company_or_plan")
	}
	if m.PricingPlan != nil && !basicsNamedLicenseMetadata(m) {
		r.block("manifest", "", "", "unsupported_named_plan_or_license_metadata")
	}
	if m.BusinessUnit != nil && (!basicsSlug.MatchString(m.BusinessUnit.Slug) || len(m.BusinessUnit.Slug) > 100 || len(m.BusinessUnit.Name) == 0 || len(m.BusinessUnit.Name) > 255) {
		r.block("manifest", "business_unit", "", "invalid_business_unit")
	}
	now := time.Now()
	if (m.License.ValidFrom != nil && m.License.ValidFrom.After(now)) || (m.License.ValidUntil != nil && !m.License.ValidUntil.After(now)) || (m.License.BillingInterval != nil && *m.License.BillingInterval != "MONTHLY" && *m.License.BillingInterval != "YEARLY") {
		r.block("manifest", "", "", "invalid_license")
	}
	for _, scoped := range basicsFlagScopes(m.Flags) {
		if len(scoped.values) > len(basicsSafeFlags) {
			r.block("flag", scoped.scope, "", "too_many_keys")
			continue
		}
		for _, key := range basicsKeys(scoped.values) {
			if !basicsSafeFlags[key] || !scoped.values[key] {
				// Do not echo an unrecognized key supplied by an untrusted manifest.
				r.block("flag", scoped.scope, "", "unsupported_key_or_disable")
			}
		}
		if scoped.scope == "business_unit" && len(scoped.values) > 0 && m.BusinessUnit == nil {
			r.block("flag", scoped.scope, "", "business_unit_required")
		}
	}
	basicsValidateSettings(m, r)
	return len(r.Blockers) == 0
}

func basicsKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func basicsQuota(value json.RawMessage, minimum int) bool {
	if bytes.Equal(value, []byte("null")) {
		return true
	} // explicit unlimited, not missing
	var n int
	return json.Unmarshal(value, &n) == nil && n >= minimum
}

func basicsZero(value json.RawMessage) bool {
	if bytes.Equal(value, []byte("null")) {
		return true
	}
	var n float64
	return json.Unmarshal(value, &n) == nil && n == 0
}

func basicsCompanyMatches(c basicsCompany, m BasicsManifest) bool {
	want, got := m.Company, c.CreateCompanyRequest
	if c.ID == uuid.Nil || c.Status != "ACTIVE" || got.IsRoot == nil || *got.IsRoot || got.Slug == nil || *got.Slug != *want.Slug || got.Name != want.Name {
		return false
	}
	a, b := slices.Clone(want.DomainEmails), slices.Clone(got.DomainEmails)
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		return false
	}
	for _, pair := range [][2]*string{{want.Description, got.Description}, {want.WebsiteUrl, got.WebsiteUrl}, {want.Address, got.Address}, {want.ContactEmail, got.ContactEmail}, {want.PhoneNumber, got.PhoneNumber}, {want.Country, got.Country}} {
		if pair[0] != nil && (pair[1] == nil || *pair[0] != *pair[1]) {
			return false
		}
	}
	return true
}

func (c *Client) basicsInspect(ctx context.Context, m BasicsManifest, r *BasicsResult) (basicsSnapshot, error) {
	s := basicsSnapshot{valid: true}
	actorCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	body, err := basicsReadResponse(c.gen.MeApiV2AuthMeGet(actorCtx))
	cancel()
	if err != nil {
		return s, err
	}
	var me struct {
		Data struct {
			User struct {
				ID        uuid.UUID `json:"id"`
				Superuser *bool     `json:"is_superuser"`
			} `json:"user"`
		} `json:"data"`
	}
	// Filtered collections and hidden not-found rows cannot prove absence or
	// contain a company cascade without full actor visibility.
	if json.Unmarshal(body, &me) != nil || me.Data.User.ID == uuid.Nil || me.Data.User.Superuser == nil || !*me.Data.User.Superuser {
		r.block("actor", "", "", "visibility_unproved")
		s.valid = false
		return s, nil
	}
	companies, err := basicsList[basicsCompany](ctx, c.gen.ListCompaniesApiV2OrganizationCompaniesGet, map[string]string{"slug": "eq " + *m.Company.Slug}, "id,name,slug,domain_emails,is_root,status,description,website_url,address,contact_email,phone_number,country")
	if err != nil {
		return s, err
	}
	if len(companies) > 1 {
		r.block("company", "", "", "multiple_matches")
		s.valid = false
	}
	for _, company := range companies {
		if !basicsCompanyMatches(company, m) {
			r.block("company", "", "", "identity_drift")
			s.valid = false
		}
	}
	if !s.valid {
		return s, nil
	}
	if len(companies) == 1 {
		s.company = &companies[0]
		r.CompanyID = s.company.ID.String()
		r.Gates.Company = true
	}
	planFilter := map[string]string{"id": "eq " + m.License.PricingPlanId.String()}
	planFields := "id,is_active,max_accounts,max_business_units,max_seats,max_granted_tokens,performance_fee_enabled,performance_fee_rate_bps,price_per_month,price_per_year"
	if m.PricingPlan != nil {
		planFilter = map[string]string{"internal_name": "eq " + basicsEssentialAnnual}
		planFields += ",internal_name,display_name,default_billing_interval,currency_code,performance_fee_allowance_yearly_cents,performance_fee_term_months"
	}
	plans, err := basicsList[basicsPlan](ctx, c.gen.ListPricingPlansApiV2LicenseAndBillingPricingPlansGet, planFilter, planFields)
	if err != nil {
		return s, err
	}
	if len(plans) != 1 || (m.License.PricingPlanId != uuid.Nil && plans[0].ID != m.License.PricingPlanId) {
		r.block("pricing_plan", "", "", "missing_or_multiple_plan")
		s.valid = false
		return s, nil
	}
	s.plan = &plans[0]
	p := s.plan
	planTermsMatch := p.FeeEnabled != nil && !*p.FeeEnabled && basicsZero(p.FeeRate) && basicsZero(p.Monthly) && basicsZero(p.Yearly)
	if m.PricingPlan != nil {
		planTermsMatch = basicsNamedPlanMatches(p)
	}
	if p.Active == nil || !*p.Active || !planTermsMatch || !basicsQuota(p.MaxAccounts, 3) || !basicsQuota(p.MaxBusinessUnits, 1) || !basicsQuota(p.MaxSeats, 1) || !basicsQuota(p.MaxTokens, 1) {
		r.block("pricing_plan", "", "", "unsafe_plan_or_quotas")
		s.valid = false
		return s, nil
	}
	r.PricingPlanID = p.ID.String()
	r.Gates.PricingPlan = true
	if s.company == nil {
		return s, nil
	}
	licenseFields := "id,company,pricing_plan,status,max_accounts,max_business_units,max_seats,max_granted_tokens,available_tokens,valid_from,valid_until,billing_interval,name,description,performance_fee_enabled,performance_fee_rate_bps,price_per_month,price_per_year"
	if m.PricingPlan != nil {
		licenseFields += ",currency_code,performance_fee_allowance_yearly_cents,performance_fee_term_months"
	}
	licenses, err := basicsList[basicsLicense](ctx, c.gen.ListLicensesApiV2LicenseAndBillingLicensesGet, map[string]string{"company": "eq " + r.CompanyID}, licenseFields)
	if err != nil {
		return s, err
	}
	// Never repair an existing tenant by creating a license: creation activates
	// pending performance-fee charges after commit, beyond this setup scope.
	if len(licenses) != 1 {
		r.block("license", "", "", "missing_or_multiple_license")
		s.valid = false
		return s, nil
	}
	l := licenses[0]
	now := time.Now()

	if l.ID == uuid.Nil || l.Company != s.company.ID || l.Plan != p.ID || l.Status != "ACTIVE" || l.ValidFrom.IsZero() || l.ValidFrom.After(now) || (l.ValidUntil != nil && !l.ValidUntil.After(now)) || !basicsQuota(l.MaxAccounts, 3) || !basicsQuota(l.MaxBusinessUnits, 1) || !basicsQuota(l.MaxSeats, 1) || !basicsQuota(l.MaxTokens, 1) || !basicsQuota(l.AvailableTokens, 1) || !basicsLicenseTermsMatch(l, p, m.PricingPlan != nil) || (m.License.BillingInterval != nil && l.BillingInterval != *m.License.BillingInterval) || (m.License.ValidFrom != nil && !l.ValidFrom.Equal(*m.License.ValidFrom)) || (m.License.ValidUntil != nil && (l.ValidUntil == nil || !l.ValidUntil.Equal(*m.License.ValidUntil))) || (m.License.Name != nil && (l.Name == nil || *l.Name != *m.License.Name)) || (m.License.Description != nil && (l.Description == nil || *l.Description != *m.License.Description)) {
		r.block("license", "", "", "invalid_license")
		s.valid = false
		return s, nil
	}
	s.license = &l
	r.LicenseID = l.ID.String()
	r.Gates.License = true
	if m.BusinessUnit == nil {
		return s, nil
	}
	units, err := basicsList[basicsBU](ctx, c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGet, map[string]string{"company": "eq " + r.CompanyID, "slug": "eq " + m.BusinessUnit.Slug}, "id,company,name,slug,managed_agent")
	if err != nil {
		return s, err
	}
	if len(units) > 1 {
		r.block("business_unit", "", "", "multiple_matches")
		s.valid = false
		return s, nil
	}
	if len(units) == 1 {
		bu := units[0]
		if bu.ID == uuid.Nil || bu.Company != s.company.ID || bu.Slug != m.BusinessUnit.Slug || bu.Name != m.BusinessUnit.Name || (bu.ManagedAgent != nil && *bu.ManagedAgent == uuid.Nil) {
			r.block("business_unit", "", "", "identity_drift")
			s.valid = false
			return s, nil
		}
		s.bu = &bu
		r.BusinessUnitID = bu.ID.String()
		r.Gates.BusinessUnit = true
		if bu.ManagedAgent != nil {
			r.ManagedAgentID = bu.ManagedAgent.String()
			r.Gates.ManagedAgentLocal = true
		}
	}
	return s, nil
}

func basicsSameSnapshot(a, b basicsSnapshot) bool {
	return b.valid && b.company != nil && b.license != nil && reflect.DeepEqual(a, b)
}

func basicsReadResponse(response *http.Response, requestErr error) ([]byte, error) {
	if response != nil && response.Body != nil {
		defer func() { _ = response.Body.Close() }()
	}
	if requestErr != nil || response == nil || response.Body == nil {
		return nil, errors.New("basics: request failed")
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, errBasicsNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("basics: request rejected")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, basicsMaxBody+1))
	if err != nil || len(body) > basicsMaxBody || !json.Valid(body) {
		return nil, errors.New("basics: invalid bounded response")
	}
	return body, nil
}

var errBasicsNotFound = errors.New("basics: not found")

type basicsFetch func(context.Context, ...generated.RequestEditorFn) (*http.Response, error)

// listCloudAccounts adapts the typed endpoint to basicsFetch; basicsQuery sets company_id.
func (c *Client) listCloudAccounts(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
	return c.gen.ListCloudAccountsApiV2CloudCloudAccountsGet(ctx, nil, editors...)
}

func basicsList[T any](ctx context.Context, fetch basicsFetch, filters map[string]string, fields string) ([]T, error) {
	items := []T{}
	seen := map[uuid.UUID]bool{}
	total := -1
	for page := 0; page < basicsMaxPages; page++ {
		pageCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		body, err := basicsReadResponse(fetch(pageCtx, basicsQuery(filters, fields, page*basicsPageSize)))
		cancel()
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
		if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil || envelope.Meta.Count == nil || envelope.Meta.Total == nil || envelope.Meta.Offset == nil || envelope.Meta.Limit == nil || *envelope.Meta.Count != len(envelope.Data) || *envelope.Meta.Offset != page*basicsPageSize || *envelope.Meta.Limit != basicsPageSize || len(envelope.Data) > basicsPageSize || *envelope.Meta.Total < 0 || *envelope.Meta.Total > basicsMaxPages*basicsPageSize {
			return nil, errors.New("basics: invalid list response")
		}
		if total >= 0 && total != *envelope.Meta.Total {
			return nil, errors.New("basics: collection changed")
		}
		total = *envelope.Meta.Total
		for _, raw := range envelope.Data {
			var id struct {
				ID uuid.UUID `json:"id"`
			}
			var item T
			if json.Unmarshal(raw, &id) != nil || id.ID == uuid.Nil || seen[id.ID] || json.Unmarshal(raw, &item) != nil {
				return nil, errors.New("basics: invalid collection item")
			}
			seen[id.ID] = true
			items = append(items, item)
		}
		if len(items) == total {
			return items, nil
		}
		if len(items) > total || len(envelope.Data) != basicsPageSize {
			return nil, errors.New("basics: incomplete collection")
		}
	}
	return nil, errors.New("basics: collection bound exceeded")
}

func basicsQuery(filters map[string]string, fields string, offset int) generated.RequestEditorFn {
	return func(_ context.Context, request *http.Request) error {
		q := request.URL.Query()
		for key, value := range filters {
			q.Set(key, value)
		}
		q.Set("api_fields", fields)
		q.Set("api_limit", strconv.Itoa(basicsPageSize))
		q.Set("api_offset", strconv.Itoa(offset))
		q.Set("api_sort", "id")
		request.URL.RawQuery = q.Encode()
		return nil
	}
}

type basicsJob struct {
	index   int
	inspect func(context.Context) (BasicsAction, error)
	write   func(context.Context, BasicsAction) error
}

type basicsFlagScope struct {
	scope  string
	values map[string]bool
}

func basicsFlagScopes(flags BasicsFlags) []basicsFlagScope {
	return []basicsFlagScope{{"root", flags.Root}, {"company", flags.Company}, {"business_unit", flags.BusinessUnit}}
}

func basicsPendingConfiguration(m BasicsManifest, r *BasicsResult) {
	for _, scoped := range basicsFlagScopes(m.Flags) {
		for _, key := range basicsKeys(scoped.values) {
			r.Actions = append(r.Actions, BasicsAction{Resource: "flag", Scope: scoped.scope, Key: key, Operation: "ensure", Status: "blocked", Reason: "missing_prerequisites"})
			r.block("flag", scoped.scope, key, "missing_prerequisites")
		}
	}
	for _, scoped := range basicsSettingScopes(m.Settings) {
		for _, key := range basicsKeys(scoped.values) {
			r.Actions = append(r.Actions, BasicsAction{Resource: "setting", Scope: scoped.scope, Key: key, Operation: "ensure", Status: "blocked", Reason: "missing_prerequisites"})
			r.block("setting", scoped.scope, key, "missing_prerequisites")
		}
	}
}

type basicsFlag struct {
	ID           uuid.UUID       `json:"id"`
	Key          string          `json:"key"`
	FeatureKey   string          `json:"feature_flag_key"`
	Company      uuid.UUID       `json:"company"`
	BusinessUnit uuid.UUID       `json:"business_unit"`
	Root         uuid.UUID       `json:"root_feature_flag"`
	Value        *bool           `json:"value"`
	Secret       *bool           `json:"is_secret"`
	DeprecatedAt json.RawMessage `json:"deprecated_at"`
}

func (c *Client) basicsGetFlag(ctx context.Context, scope, key string, s basicsSnapshot) (*basicsFlag, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var response *http.Response
	var err error
	switch scope {
	case "root":
		response, err = c.gen.GetFlagByKeyApiV2SystemFeatureFlagsRootByKeyFlagKeyGet(ctx, key)
	case "company":
		response, err = c.gen.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGet(ctx, key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGetParams{CompanyId: s.company.ID})
	case "business_unit":
		response, err = c.gen.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGet(ctx, key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGetParams{BusinessUnitId: s.bu.ID})
	}
	body, err := basicsReadResponse(response, err)
	if errors.Is(err, errBasicsNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Data *basicsFlag `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
		return nil, errors.New("basics: invalid flag")
	}
	f := envelope.Data
	if f.ID == uuid.Nil || f.Value == nil || f.Secret == nil || (scope == "root" && f.Key != key) || (scope != "root" && (f.FeatureKey != key || f.Root == uuid.Nil)) || (scope == "company" && f.Company != s.company.ID) || (scope == "business_unit" && f.BusinessUnit != s.bu.ID) {
		return nil, errors.New("basics: invalid flag scope")
	}
	return f, nil
}

func (c *Client) basicsFlagAction(ctx context.Context, scope, key string, s basicsSnapshot) (BasicsAction, error) {
	a := BasicsAction{Resource: "flag", Scope: scope, Key: key, Operation: "enable", Status: "planned"}
	root, err := c.basicsGetFlag(ctx, "root", key, s)
	if err != nil {
		return a, err
	}
	if root == nil || *root.Secret || !bytes.Equal(root.DeprecatedAt, []byte("null")) {
		a.Status = "blocked"
		a.Reason = "missing_secret_or_deprecated_root"
		return a, nil
	}
	if scope != "root" && !*root.Value {
		a.Status = "blocked"
		a.Reason = "root_disabled"
		return a, nil
	}
	if scope == "business_unit" {
		company, getErr := c.basicsGetFlag(ctx, "company", key, s)
		if getErr != nil {
			return a, getErr
		}
		if company == nil || *company.Secret || !*company.Value || company.Root != root.ID {
			a.Status = "blocked"
			a.Reason = "company_disabled_or_secret"
			return a, nil
		}
	}
	flag := root
	if scope != "root" {
		flag, err = c.basicsGetFlag(ctx, scope, key, s)
		if err != nil {
			return a, err
		}
	}
	if scope == "company" {
		if s.bu == nil {
			a.Status = "blocked"
			a.Reason = "cascade_scope_unproved"
			return a, nil
		}
		child, childErr := c.basicsGetFlag(ctx, "business_unit", key, s)
		if childErr != nil {
			return a, childErr
		}
		a.observed = "missing"
		if child != nil {
			if *child.Secret || child.Root != root.ID {
				a.Status = "blocked"
				a.Reason = "cascade_child_drift"
				return a, nil
			}
			a.cascadeChildID = child.ID
			a.observed = strconv.FormatBool(*child.Value)
		}
		// A committed parent with an incomplete cascade is not a safe no-op or retry.
		if flag != nil && *flag.Value && (child == nil || !*child.Value) {
			a.Status = "blocked"
			a.Reason = "cascade_child_incomplete"
			return a, nil
		}
	}
	if flag != nil {
		a.ID = flag.ID.String()
		if *flag.Secret || (scope != "root" && flag.Root != root.ID) {
			a.Status = "blocked"
			a.Reason = "secret_or_root_drift"
			return a, nil
		}
		if *flag.Value {
			a.Status = "noop"
			return a, nil
		}
	}
	if scope == "company" {
		units, listErr := basicsList[basicsBU](ctx, c.gen.ListBusinessUnitsApiV2OrganizationBusinessUnitsGet, map[string]string{"company": "eq " + s.company.ID.String()}, "id,company,name,slug,managed_agent")
		if listErr != nil {
			return a, listErr
		}
		if len(units) != 1 || units[0].ID != s.bu.ID || units[0].Company != s.company.ID {
			a.Status = "blocked"
			a.Reason = "cascade_scope_unproved"
			return a, nil
		}
		// This is company-filtered, not a scan across unrelated accounts.
		accounts, listErr := basicsList[struct {
			ID           uuid.UUID `json:"id"`
			BusinessUnit uuid.UUID `json:"business_unit"`
		}](ctx, c.listCloudAccounts, map[string]string{"company_id": s.company.ID.String()}, "id,business_unit")
		if listErr != nil {
			return a, listErr
		}
		if len(accounts) != 0 {
			a.Status = "blocked"
			a.Reason = "cascade_accounts_present"
		}
	}
	return a, nil
}

func (c *Client) basicsWriteFlag(ctx context.Context, scope, key string, s basicsSnapshot) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var response *http.Response
	var err error
	switch scope {
	case "root":
		response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsRootActionSetFlagKeyPatch(ctx, key, generated.SetRootFeatureFlagByKeyRequest{Value: true})
	case "company":
		response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatch(ctx, key, &generated.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatchParams{CompanyId: s.company.ID}, generated.SetCompanyFeatureFlagRequest{Value: true})
	case "business_unit":
		response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatch(ctx, key, &generated.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatchParams{BusinessUnitId: s.bu.ID}, generated.SetBusinessUnitFeatureFlagRequest{Value: true})
	}
	_, err = basicsReadResponse(response, err)
	return err
}

func (c *Client) basicsConfiguration(ctx context.Context, m BasicsManifest, s basicsSnapshot, r *BasicsResult) ([]basicsJob, error) {
	jobs := []basicsJob{}
	for _, scoped := range basicsFlagScopes(m.Flags) {
		for _, key := range basicsKeys(scoped.values) {
			scope := scoped.scope
			inspect := func(ctx context.Context) (BasicsAction, error) { return c.basicsFlagAction(ctx, scope, key, s) }
			action, err := inspect(ctx)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, basicsJob{index: len(r.Actions), inspect: inspect, write: func(ctx context.Context, _ BasicsAction) error { return c.basicsWriteFlag(ctx, scope, key, s) }})
			r.Actions = append(r.Actions, action)
			if action.Status == "blocked" {
				r.block(action.Resource, scope, key, action.Reason)
			}
		}
	}
	settingJobs, err := c.basicsSettingJobs(ctx, m, s, r)
	return append(jobs, settingJobs...), err
}
