package api

import (
	"bytes"
	"encoding/json"
)

// BasicsPricingPlan selects the current catalog UUID; a license UUID is an optional pin.
// Only the explicitly supported profile is accepted, never arbitrary paid plans.
type BasicsPricingPlan struct {
	InternalName string `json:"internal_name"`
}

const (
	basicsEssentialAnnual         = "ESSENTIAL_ANNUAL"
	basicsNamedLicenseName        = "Unstable Jetscale Test License"
	basicsNamedLicenseDescription = "Internal simulated customer license for three registered cloud accounts under the Essential Annual plan. No payment method, Stripe subscription or commercial commitment is being configured."
)

func basicsNamedLicenseMetadata(m BasicsManifest) bool {
	return m.PricingPlan.InternalName == basicsEssentialAnnual &&
		m.License.Name != nil && *m.License.Name == basicsNamedLicenseName &&
		m.License.Description != nil && *m.License.Description == basicsNamedLicenseDescription &&
		m.License.BillingInterval != nil && *m.License.BillingInterval == "YEARLY"
}

func basicsProfileNumber(value json.RawMessage, want int, inherit bool) bool {
	if inherit && bytes.Equal(value, []byte("null")) {
		return true
	}
	var n float64
	return !bytes.Equal(value, []byte("null")) && json.Unmarshal(value, &n) == nil && n == float64(want)
}

func basicsNamedPlanMatches(p *basicsPlan) bool {
	return p.InternalName == basicsEssentialAnnual && p.DisplayName == "Essential (Annual Plan)" &&
		p.DefaultBillingInterval == "YEARLY" && p.CurrencyCode == "CAD" &&
		p.FeeEnabled != nil && *p.FeeEnabled &&
		basicsProfileNumber(p.Monthly, 199, false) && basicsProfileNumber(p.Yearly, 2388, false) &&
		basicsProfileNumber(p.FeeRate, 2500, false) && basicsProfileNumber(p.FeeAllowance, 500000, false) && basicsProfileNumber(p.FeeTerm, 12, false)
}

func basicsLicenseTermsMatch(l basicsLicense, p *basicsPlan, named bool) bool {
	if named {
		return l.CurrencyCode == "CAD" &&
			(bytes.Equal(l.FeeEnabled, []byte("null")) || bytes.Equal(l.FeeEnabled, []byte("true"))) &&
			basicsProfileNumber(l.Monthly, 199, true) && basicsProfileNumber(l.Yearly, 2388, true) &&
			basicsProfileNumber(l.FeeRate, 2500, true) && basicsProfileNumber(l.FeeAllowance, 500000, true) && basicsProfileNumber(l.FeeTerm, 12, true)
	}
	// Preserve the UUID-only contract: null overrides inherit the verified free,
	// disabled-fee plan. Missing fee-enabled historically inherits too.
	feeSafe := bytes.Equal(l.FeeEnabled, []byte("false"))
	if len(l.FeeEnabled) == 0 || bytes.Equal(l.FeeEnabled, []byte("null")) {
		feeSafe = p.FeeEnabled != nil && !*p.FeeEnabled
	}
	return feeSafe && basicsZero(l.FeeRate) && basicsZero(l.Monthly) && basicsZero(l.Yearly)
}
