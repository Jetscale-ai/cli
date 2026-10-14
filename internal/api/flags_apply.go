package api

import (
	"context"
	"net/http"
	"reflect"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

func flagsActionRef(a FlagsAction) flagsRef {
	id := uuid.Nil
	if a.EntityID != "" {
		id, _ = uuid.Parse(a.EntityID)
	}
	return flagsRef{a.Scope, id, a.Key}
}

func (c *Client) flagsWrite(ctx context.Context, n flagsRef, create bool) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var response *http.Response
	var err error
	switch n.scope {
	case "root":
		if create {
			response, err = c.gen.CreateFlagApiV2SystemFeatureFlagsRootPost(ctx, generated.CreateRootFeatureFlagRequest{Key: n.key, Value: flagsBool(true), IsSecret: flagsBool(false)})
		} else {
			// Omit all optional fields: never clear deprecation or overwrite metadata.
			response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsRootActionSetFlagKeyPatch(ctx, n.key, generated.SetRootFeatureFlagByKeyRequest{Value: true})
		}
	case "company":
		if create {
			response, err = c.gen.CreateFlagApiV2SystemFeatureFlagsCompanyPost(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsCompanyPostParams{CompanyId: n.id}, generated.CreateCompanyFeatureFlagRequest{Key: n.key, Value: flagsBool(true)})
		} else {
			response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatch(ctx, n.key, &generated.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatchParams{CompanyId: n.id}, generated.SetCompanyFeatureFlagRequest{Value: true})
		}
	case "business_unit":
		if create {
			response, err = c.gen.CreateFlagApiV2SystemFeatureFlagsBusinessUnitPost(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsBusinessUnitPostParams{BusinessUnitId: n.id}, generated.CreateBusinessUnitFeatureFlagRequest{Key: n.key, Value: flagsBool(true)})
		} else {
			response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatch(ctx, n.key, &generated.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatchParams{BusinessUnitId: n.id}, generated.SetBusinessUnitFeatureFlagRequest{Value: true})
		}
	case "cloud_account":
		if create {
			response, err = c.gen.CreateFlagApiV2SystemFeatureFlagsCloudAccountPost(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsCloudAccountPostParams{CloudAccountId: n.id}, generated.CreateCloudAccountFeatureFlagRequest{Key: n.key, Value: flagsBool(true)})
		} else {
			response, err = c.gen.ActionSetFlagApiV2SystemFeatureFlagsCloudAccountActionSetFlagKeyPatch(ctx, n.key, &generated.ActionSetFlagApiV2SystemFeatureFlagsCloudAccountActionSetFlagKeyPatchParams{CloudAccountId: n.id}, generated.SetCloudAccountFeatureFlagRequest{Value: true})
		}
	default:
		return flagsFailure("invalid_scope")
	}
	if _, err = basicsReadResponse(response, err); err != nil {
		return flagsFailure("write_outcome_uncertain")
	}
	return nil
}

func flagsStop(r FlagsResult, index int, uncertain bool, reason string, unsafeKeys map[string]bool) FlagsResult {
	r.Satisfied = false
	r.OutcomeUncertain = uncertain
	attempted := r.Actions[index]
	for i := index; i < len(r.Actions); i++ {
		if r.Actions[i].Status != "planned" {
			continue
		}
		r.Actions[i].Status, r.Actions[i].Reason = "blocked", "execution_stopped"
		if uncertain && attempted.Scope == "company" && attempted.Cascade && r.Actions[i].Operation == "cascade_enable" && r.Actions[i].Key == attempted.Key {
			r.Actions[i].Status, r.Actions[i].Reason = "outcome_uncertain", reason
		}
	}
	if uncertain {
		r.Actions[index].Status = "outcome_uncertain"
	} else {
		r.Actions[index].Status = "blocked"
	}
	r.Actions[index].Reason = reason
	a := r.Actions[index]
	r.flagsBlock(a.Scope, a.EntityID, a.Key, reason)
	r.flagsRedact(unsafeKeys)
	return r
}

// No CAS/transaction spans inspection and Backend writes. Rechecks detect
// observable drift; a writer can still race between the final read and mutation.
func (c *Client) flagsApply(ctx context.Context, p FlagsProfile, baseline flagsSnapshot, r FlagsResult) (FlagsResult, error) {
	monitored := map[flagsRef]bool{}
	for n := range baseline.rows {
		monitored[n] = true
	}
	refs := flagsSort(monitored)
	for i := range r.Actions {
		a := r.Actions[i]
		if a.Status != "planned" || a.Operation == "cascade_enable" {
			continue
		}
		current, err := c.flagsInspect(ctx, p, refs...)
		if err != nil {
			// Incomplete inspection cannot re-establish visibility of prior labels.
			for _, ref := range refs {
				current.unsafeKeys[ref.key] = true
			}
		}
		if err != nil {
			return flagsStop(r, i, false, flagsErrorCode(err), current.unsafeKeys), err
		}
		if !flagsSame(baseline, current) {
			return flagsStop(r, i, false, "prewrite_drift", current.unsafeKeys), flagsFailure("prewrite_drift")
		}
		if ctx.Err() != nil {
			return flagsStop(r, i, false, "execution_stopped", current.unsafeKeys), flagsFailure("execution_stopped")
		}
		n := flagsActionRef(a)
		writeErr := c.flagsWrite(ctx, n, a.Operation == "create")
		// Reconcile after a rejected/ambiguous response too, but never retry the
		// mutation. An enabled parent can have committed before children failed.
		after, readErr := c.flagsInspect(ctx, p, refs...)
		if readErr != nil {
			for _, ref := range refs {
				after.unsafeKeys[ref.key] = true
			}
		}
		if readErr == nil {
			r.States = after.result("apply", p).States
		}
		if writeErr != nil || readErr != nil {
			if readErr != nil {
				for j := range r.States {
					state := &r.States[j]
					if state.Key != r.Actions[i].Key {
						continue
					}
					state.Effective, state.Reason = nil, "outcome_uncertain"
					if state.Scope == a.Scope && state.EntityID == a.EntityID || a.Scope == "company" && (state.Scope == "business_unit" || state.Scope == "cloud_account") {
						state.Stored = nil
					}
				}
			}
			return flagsStop(r, i, true, "write_outcome_uncertain", after.unsafeKeys), flagsFailure("write_outcome_uncertain")
		}
		affected := map[flagsRef]bool{n: true}
		for child, parent := range baseline.cascade {
			if parent == n {
				affected[child] = true
			}
		}
		verified := reflect.DeepEqual(baseline.top, after.top) && len(after.unsafeKeys) == 0
		for row, previous := range baseline.rows {
			if affected[row] {
				f := after.rows[row]
				if f == nil || !*f.Value || !after.effective(row) || (previous != nil && (f.ID != previous.ID || !reflect.DeepEqual(f.Description, previous.Description) || !reflect.DeepEqual(f.DeprecatedAt, previous.DeprecatedAt) || *f.Secret != *previous.Secret)) {
					verified = false
				}
			} else if !reflect.DeepEqual(previous, after.rows[row]) {
				verified = false
			}
		}
		if !verified {
			return flagsStop(r, i, true, "readback_unverified", after.unsafeKeys), flagsFailure("readback_unverified")
		}
		r.Actions[i].Status, r.Actions[i].Reason = "applied", "verified_enabled"
		for j := range r.Actions {
			if r.Actions[j].Operation == "cascade_enable" && affected[flagsActionRef(r.Actions[j])] {
				r.Actions[j].Status, r.Actions[j].Reason = "applied", "verified_cascade"
			}
		}
		baseline = after
	}
	final := baseline.result("apply", p)
	r.States, r.Satisfied = final.States, final.Satisfied
	return r, nil
}
