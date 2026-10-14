package api

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// FlagsProfile is an enable-only request, never an entity provisioning request.
// A company enable overwrites descendant false values; consent covers every
// descendant of the company, not just the optional selected BU/accounts.
type FlagsProfile struct {
	Version        int                  `json:"version"`
	Company        FlagsEntitySelector  `json:"company"`
	BusinessUnit   *FlagsEntitySelector `json:"business_unit,omitempty"`
	Flags          FlagsLayers          `json:"flags"`
	CompanyCascade string               `json:"company_cascade,omitempty"`
}

type FlagsEntitySelector struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type FlagsLayers struct {
	Root          map[string]bool     `json:"root"`
	Company       map[string]bool     `json:"company"`
	BusinessUnit  map[string]bool     `json:"business_unit"`
	CloudAccounts []FlagsAccountLayer `json:"cloud_accounts,omitempty"`
}

type FlagsAccountLayer struct {
	ID    uuid.UUID       `json:"id"`
	Flags map[string]bool `json:"flags"`
}

type FlagsResult struct {
	Mode                string         `json:"mode"`
	CompanyID           string         `json:"company_id"`
	BusinessUnitID      string         `json:"business_unit_id"`
	Satisfied           bool           `json:"satisfied"`
	OutcomeUncertain    bool           `json:"outcome_uncertain"`
	States              []FlagsState   `json:"states"`
	Actions             []FlagsAction  `json:"actions"`
	Blockers            []FlagsBlocker `json:"blockers"`
	RootApplicationWide bool           `json:"root_application_wide"`
}

type FlagsState struct {
	Scope     string `json:"scope"`
	EntityID  string `json:"entity_id"`
	Key       string `json:"key"`
	Stored    *bool  `json:"stored"`
	Effective *bool  `json:"effective"`
	Reason    string `json:"reason"`
}

type FlagsAction struct {
	Scope     string `json:"scope"`
	EntityID  string `json:"entity_id"`
	Key       string `json:"key"`
	Operation string `json:"operation"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	Cascade   bool   `json:"cascade"`
}

type FlagsBlocker struct {
	Scope    string `json:"scope"`
	EntityID string `json:"entity_id"`
	Key      string `json:"key"`
	Reason   string `json:"reason"`
}

func flagsKnownKey(key string) bool {
	switch key {
	case "AGENTS.EXPLORER", "CLOUD.FOCUS", "CLOUD.AZURE.FOCUS", "INTEGRATIONS.AABHRA":
		return true
	}
	return false
}

// ValidateFlagsProfile returns only fixed codes, including for unknown/secret keys.
func ValidateFlagsProfile(p FlagsProfile) error {
	validSelector := func(s FlagsEntitySelector) bool {
		return strings.TrimSpace(s.Name) == s.Name && s.Name != "" && len(s.Name) <= 200 && len(s.Slug) <= 100 && basicsSlug.MatchString(s.Slug)
	}
	if p.Version != 1 || !validSelector(p.Company) || (p.BusinessUnit != nil && !validSelector(*p.BusinessUnit)) || (len(p.Flags.BusinessUnit) > 0 && p.BusinessUnit == nil) || (p.CompanyCascade != "" && p.CompanyCascade != "all_descendants") || len(p.Flags.CloudAccounts) > 100 {
		return errors.New("flags: invalid_profile")
	}
	maps := []map[string]bool{p.Flags.Root, p.Flags.Company, p.Flags.BusinessUnit}
	seen := map[uuid.UUID]bool{}
	for _, a := range p.Flags.CloudAccounts {
		if a.ID == uuid.Nil || seen[a.ID] || len(a.Flags) == 0 {
			return errors.New("flags: invalid_profile")
		}
		seen[a.ID] = true
		maps = append(maps, a.Flags)
	}
	count := 0
	for _, values := range maps {
		for key, value := range values {
			if !flagsKnownKey(key) || !value {
				return errors.New("flags: unsupported_flag")
			}
			count++
		}
	}
	if count == 0 {
		return errors.New("flags: empty_profile")
	}
	return nil
}

func (c *Client) ShowFlags(ctx context.Context, p FlagsProfile) (FlagsResult, error) {
	return c.flagsRun(ctx, p, "show")
}

func (c *Client) PlanFlags(ctx context.Context, p FlagsProfile) (FlagsResult, error) {
	return c.flagsRun(ctx, p, "plan")
}

func (c *Client) ApplyFlags(ctx context.Context, p FlagsProfile) (FlagsResult, error) {
	return c.flagsRun(ctx, p, "apply")
}

func flagsResult(mode string) FlagsResult {
	return FlagsResult{Mode: mode, States: []FlagsState{}, Actions: []FlagsAction{}, Blockers: []FlagsBlocker{}}
}

func (r *FlagsResult) flagsBlock(scope, id, key, reason string) {
	for _, b := range r.Blockers {
		if b.Scope == scope && b.EntityID == id && b.Key == key && b.Reason == reason {
			return
		}
	}
	r.Blockers = append(r.Blockers, FlagsBlocker{Scope: scope, EntityID: id, Key: key, Reason: reason})
	r.Satisfied = false
}

func (c *Client) flagsRun(ctx context.Context, p FlagsProfile, mode string) (FlagsResult, error) {
	r := flagsResult(mode)
	if err := ValidateFlagsProfile(p); err != nil {
		r.flagsBlock("profile", "", "", "invalid_profile")
		return r, err
	}
	r.RootApplicationWide = len(p.Flags.Root) > 0
	bounded, err := c.flagsBoundedClient()
	if err != nil {
		r.flagsBlock("profile", "", "", flagsErrorCode(err))
		return r, err
	}
	c = bounded
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s, err := c.flagsInspect(ctx, p)
	if err != nil {
		r.flagsBlock("profile", "", "", flagsErrorCode(err))
		return r, err
	}
	r = s.result(mode, p)
	if mode != "apply" || len(r.Blockers) > 0 {
		return r, nil
	}
	return c.flagsApply(ctx, p, s, r)
}

func (r *FlagsResult) flagsRedact(unsafeKeys map[string]bool) {
	for i := range r.States {
		if unsafeKeys[r.States[i].Key] {
			r.States[i].Key, r.States[i].Stored, r.States[i].Effective, r.States[i].Reason = "", nil, nil, "unsafe_flag"
		}
	}
	for i := range r.Actions {
		if unsafeKeys[r.Actions[i].Key] {
			r.Actions[i].Key = ""
		}
	}
	for i := range r.Blockers {
		if unsafeKeys[r.Blockers[i].Key] {
			r.Blockers[i].Key = ""
		}
	}
}
