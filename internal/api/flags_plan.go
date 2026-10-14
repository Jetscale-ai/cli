package api

import "github.com/google/uuid"

const flagsMaxRequests = 1000

// Estimate the stable footprint, reserving worst-case collection pagination and
// both full inspections per direct write. Drift still aborts; this is no latency guarantee.
func (s flagsSnapshot) requestEstimate(p FlagsProfile, r FlagsResult) int {
	collections := 1 // company selector
	if p.BusinessUnit != nil || len(p.Flags.Company) > 0 || len(p.Flags.CloudAccounts) > 0 {
		collections++
	}
	if len(p.Flags.Company) > 0 || len(p.Flags.CloudAccounts) > 0 {
		collections++
	}
	pages := (flagsMaxDescendants + flagsPageSize - 1) / flagsPageSize
	inspection := 1 + collections*pages + len(s.rows) // actor, topology, monitored rows
	directWrites := 0
	for _, action := range r.Actions {
		if action.Status == "planned" && action.Operation != "cascade_enable" {
			directWrites++
		}
	}
	return inspection*(1+2*directWrites) + directWrites
}

func (s flagsSnapshot) result(mode string, p FlagsProfile) FlagsResult {
	r := flagsResult(mode)
	r.CompanyID = s.top.Company.ID.String()
	if s.top.SelectedBU != uuid.Nil {
		r.BusinessUnitID = s.top.SelectedBU.String()
	}
	r.RootApplicationWide = len(p.Flags.Root) > 0
	r.Satisfied = true
	refs := map[flagsRef]bool{}
	for n := range s.rows {
		refs[n] = true
	}
	ready := map[flagsRef]bool{}
	for _, n := range flagsSort(refs) {
		f := s.rows[n]
		key := n.key
		state := FlagsState{Scope: n.scope, EntityID: n.entityID(), Key: key, Reason: "stored_missing"}
		if s.unsafeKeys[key] {
			state.Key, state.Reason = "", "unsafe_flag"
		} else {
			state.Effective = flagsBool(s.effective(n))
			if f != nil {
				state.Stored = flagsBool(*f.Value)
				if *f.Value {
					state.Reason = "enabled"
				} else {
					state.Reason = "stored_false"
				}
				if *f.Value && !*state.Effective {
					state.Reason = "ancestor_disabled"
				}
			}
		}
		r.States = append(r.States, state)
		parent, parentExists := s.parent(n)
		parentReady := !parentExists || ready[parent]
		ready[n] = f != nil && *f.Value && parentReady && !s.unsafeKeys[key]
		_, cascade := s.cascade[n]
		if !s.wanted[n] && !cascade {
			continue
		}
		if !s.effective(n) {
			r.Satisfied = false
		}
		a := FlagsAction{Scope: n.scope, EntityID: n.entityID(), Key: key, Operation: "set", Status: "planned", Reason: "enable", Cascade: cascade || (n.scope == "company" && (f == nil || !*f.Value))}
		if f == nil {
			a.Operation = "create"
		}
		if cascade {
			a.Operation, a.Reason = "cascade_enable", "company_cascade"
		}
		reason := ""
		switch {
		case s.unsafeKeys[key]:
			a.Key, reason = "", "unsafe_flag"
		case !parentReady:
			reason = "missing_prerequisite"
		case n.scope == "company" && (f == nil || !*f.Value) && p.CompanyCascade != "all_descendants":
			reason = "cascade_consent_required"
		case cascade && !ready[s.cascade[n]]:
			reason = "company_cascade_blocked"
		case f != nil && *f.Value && !cascade:
			a.Status, a.Reason = "noop", "already_enabled"
		}
		if reason != "" {
			a.Status, a.Reason = "blocked", reason
			r.flagsBlock(a.Scope, a.EntityID, a.Key, reason)
		} else {
			ready[n] = true
		}
		r.Actions = append(r.Actions, a)
	}
	if mode != "show" && len(r.Blockers) == 0 && s.requestEstimate(p, r) > flagsMaxRequests {
		for i := range r.Actions {
			if r.Actions[i].Status == "planned" {
				r.Actions[i].Status, r.Actions[i].Reason = "blocked", "request_budget_exceeded"
			}
		}
		r.flagsBlock("profile", "", "", "request_budget_exceeded")
	}
	return r
}

func flagsBool(value bool) *bool { return &value }
