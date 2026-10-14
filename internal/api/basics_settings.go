package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/google/uuid"
)

// Only scalar, non-secret SETTINGS_DEFINITIONS entries with bounded values.
// Model routing, credentials, demo/debug, auth controls and generic JSON are
// intentionally unsupported; they require separate effect/validation contracts.
var basicsSettingTypes = map[string]generated.SystemSettingValueType{
	"enable_langfuse":            "boolean",
	"enable_caching":             "boolean",
	"recommendation_temperature": "number",
}

type basicsSettingScope struct {
	scope  string
	values map[string]BasicsSetting
}

func basicsSettingScopes(settings BasicsSettings) []basicsSettingScope {
	return []basicsSettingScope{{"root", settings.Root}, {"company", settings.Company}, {"business_unit", settings.BusinessUnit}}
}

func basicsValidateSettings(m BasicsManifest, r *BasicsResult) {
	for _, scoped := range basicsSettingScopes(m.Settings) {
		if len(scoped.values) > len(basicsSettingTypes) {
			r.block("setting", scoped.scope, "", "too_many_keys")
			continue
		}
		for _, key := range basicsKeys(scoped.values) {
			setting := scoped.values[key]
			expected, known := basicsSettingTypes[key]
			valid := known && expected == setting.ValueType && len(setting.Value) <= 32
			switch expected {
			case "boolean":
				valid = valid && bytes.Equal(bytes.TrimSpace(setting.Value), []byte("true"))
			case "number":
				var n float64
				valid = valid && !bytes.Equal(bytes.TrimSpace(setting.Value), []byte("null")) && json.Unmarshal(setting.Value, &n) == nil && n >= 0 && n <= 1
			}
			if !valid {
				r.block("setting", scoped.scope, "", "unsupported_key_type_or_value")
			}
		}
		if scoped.scope == "business_unit" && len(scoped.values) > 0 && m.BusinessUnit == nil {
			r.block("setting", scoped.scope, "", "business_unit_required")
		}
	}
}

type basicsSettingRow struct {
	ID           uuid.UUID                        `json:"id"`
	Company      uuid.UUID                        `json:"company"`
	BusinessUnit uuid.UUID                        `json:"business_unit"`
	Key          string                           `json:"key"`
	ValueType    generated.SystemSettingValueType `json:"value_type"`
	Value        map[string]json.RawMessage       `json:"value"`
}

func basicsValueEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	switch value := left.(type) {
	case bool:
		other, ok := right.(bool)
		return ok && value == other
	case float64:
		other, ok := right.(float64)
		return ok && value == other
	default:
		return false
	}
}

func (c *Client) basicsSettingAction(ctx context.Context, scope, key string, want BasicsSetting, s basicsSnapshot) (BasicsAction, error) {
	if expected, known := basicsSettingTypes[key]; !known || expected != want.ValueType {
		return BasicsAction{}, errors.New("basics: unsupported setting")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	a := BasicsAction{Resource: "setting", Scope: scope, Key: key, Operation: "create", Status: "planned"}
	var response *http.Response
	var err error
	switch scope {
	case "root":
		response, err = c.gen.GetSettingByKeyApiV2SystemRootSettingsByKeySettingKeyGet(ctx, key)
	case "company":
		response, err = c.gen.GetSettingByKeyApiV2SystemCompanySettingsByKeySettingKeyGet(ctx, key, &generated.GetSettingByKeyApiV2SystemCompanySettingsByKeySettingKeyGetParams{CompanyId: s.company.ID})
	case "business_unit":
		response, err = c.gen.GetSettingByKeyApiV2SystemBusinessUnitSettingsByKeySettingKeyGet(ctx, key, &generated.GetSettingByKeyApiV2SystemBusinessUnitSettingsByKeySettingKeyGetParams{BusinessUnitId: s.bu.ID})
	}
	body, err := basicsReadResponse(response, err)
	if err != nil && !errors.Is(err, errBasicsNotFound) {
		return a, err
	}
	var row *basicsSettingRow
	if err == nil {
		var envelope struct {
			Data *basicsSettingRow `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil {
			return a, errors.New("basics: invalid setting")
		}
		row = envelope.Data
		if row.ID == uuid.Nil || row.Key != key || (scope == "company" && row.Company != s.company.ID) || (scope == "business_unit" && row.BusinessUnit != s.bu.ID) {
			return a, errors.New("basics: invalid setting scope")
		}
		a.ID = row.ID.String()
		a.Operation = "patch"
		if row.ValueType != want.ValueType || len(row.Value) != 1 || len(row.Value["value"]) == 0 {
			a.Status = "blocked"
			a.Reason = "setting_shape_or_type_drift"
			return a, nil
		}
		// Refuse non-scalar response payloads instead of comparing/returning them.
		var scalar any
		if json.Unmarshal(row.Value["value"], &scalar) != nil {
			return a, errors.New("basics: invalid setting value")
		}
		_, isBool := scalar.(bool)
		_, isNumber := scalar.(float64)
		if (want.ValueType == "boolean" && !isBool) || (want.ValueType == "number" && !isNumber) {
			a.Status = "blocked"
			a.Reason = "setting_shape_or_type_drift"
			return a, nil
		}
		canonical, _ := json.Marshal(scalar)
		a.observed = string(canonical)
	}
	params := &generated.GetEffectiveSettingApiV2SettingsEffectiveKeyGetParams{}
	if scope != "root" {
		params.CompanyId = &s.company.ID
	}
	if scope == "business_unit" {
		params.BusinessUnitId = &s.bu.ID
	}
	body, err = basicsReadResponse(c.gen.GetEffectiveSettingApiV2SettingsEffectiveKeyGet(ctx, key, params))
	if errors.Is(err, errBasicsNotFound) {
		a.Status = "blocked"
		a.Reason = "effective_setting_missing"
		return a, nil
	}
	if err != nil {
		return a, err
	}
	var effective struct {
		Data *struct {
			Key        string          `json:"key"`
			ValueType  string          `json:"value_type"`
			Value      json.RawMessage `json:"value"`
			Scope      string          `json:"scope"`
			ScopeChain []struct {
				Scope     string          `json:"scope"`
				Value     json.RawMessage `json:"value"`
				Source    string          `json:"source"`
				SettingID *uuid.UUID      `json:"setting_id"`
			} `json:"scope_chain"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &effective) != nil || effective.Data == nil || effective.Data.Key != key || effective.Data.ValueType != string(want.ValueType) {
		return a, errors.New("basics: invalid effective setting")
	}
	if effective.Data.Scope != "root" && effective.Data.Scope != "company" && effective.Data.Scope != "business_unit" && effective.Data.Scope != "system" {
		return a, errors.New("basics: invalid effective scope")
	}
	if scope == "root" {
		// The selected value may belong to the caller's company; verify only the root tier.
		roots, matches := 0, false
		for _, tier := range effective.Data.ScopeChain {
			if tier.Scope != "root" {
				continue
			}
			roots++
			if row == nil {
				matches = tier.SettingID == nil && tier.Source == "unset" && bytes.Equal(bytes.TrimSpace(tier.Value), []byte("null"))
			} else {
				matches = tier.SettingID != nil && *tier.SettingID == row.ID && tier.Source == "root" && basicsValueEqual(tier.Value, row.Value["value"])
			}
		}
		if roots != 1 || !matches {
			a.Status = "blocked"
			a.Reason = "effective_setting_mismatch"
			return a, nil
		}
	}
	if row != nil && basicsValueEqual(row.Value["value"], want.Value) {
		if scope != "root" && (effective.Data.Scope != scope || !basicsValueEqual(effective.Data.Value, want.Value)) {
			a.Status = "blocked"
			a.Reason = "effective_setting_mismatch"
			return a, nil
		}
		a.Status = "noop"
	}
	return a, nil
}

func (c *Client) basicsWriteSetting(ctx context.Context, scope, key string, want BasicsSetting, s basicsSnapshot, action BasicsAction) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var value any
	if json.Unmarshal(want.Value, &value) != nil {
		return errors.New("basics: invalid setting")
	}
	payload := map[string]interface{}{"value": value}
	var response *http.Response
	var err error
	if action.Operation == "create" {
		switch scope {
		case "root":
			response, err = c.gen.CreateSettingApiV2SystemRootSettingsPost(ctx, generated.CreateRootSystemSettingRequest{Key: key, ValueType: want.ValueType, Value: payload})
		case "company":
			response, err = c.gen.CreateSettingApiV2SystemCompanySettingsPost(ctx, generated.CreateCompanySystemSettingRequest{Company: s.company.ID, Key: key, ValueType: want.ValueType, Value: payload})
		case "business_unit":
			response, err = c.gen.CreateSettingApiV2SystemBusinessUnitSettingsPost(ctx, generated.CreateBusinessUnitSystemSettingRequest{BusinessUnit: s.bu.ID, Key: key, ValueType: want.ValueType, Value: payload})
		}
	} else {
		id, parseErr := uuid.Parse(action.ID)
		if parseErr != nil {
			return errors.New("basics: invalid setting ID")
		}
		switch scope {
		case "root":
			response, err = c.gen.EditSettingApiV2SystemRootSettingsSettingIdPatch(ctx, id, generated.UpdateRootSystemSettingRequest{ValueType: want.ValueType, Value: payload})
		case "company":
			response, err = c.gen.EditSettingApiV2SystemCompanySettingsSettingIdPatch(ctx, id, generated.UpdateCompanySystemSettingRequest{ValueType: want.ValueType, Value: payload})
		case "business_unit":
			response, err = c.gen.EditSettingApiV2SystemBusinessUnitSettingsSettingIdPatch(ctx, id, generated.UpdateBusinessUnitSystemSettingRequest{ValueType: want.ValueType, Value: payload})
		}
	}
	_, err = basicsReadResponse(response, err)
	return err
}

func (c *Client) basicsSettingJobs(ctx context.Context, m BasicsManifest, s basicsSnapshot, r *BasicsResult) ([]basicsJob, error) {
	jobs := []basicsJob{}
	for _, scoped := range basicsSettingScopes(m.Settings) {
		for _, key := range basicsKeys(scoped.values) {
			scope, want := scoped.scope, scoped.values[key]
			inspect := func(ctx context.Context) (BasicsAction, error) {
				return c.basicsSettingAction(ctx, scope, key, want, s)
			}
			action, err := inspect(ctx)
			if err != nil {
				return nil, err
			}
			jobs = append(jobs, basicsJob{index: len(r.Actions), inspect: inspect, write: func(ctx context.Context, action BasicsAction) error {
				return c.basicsWriteSetting(ctx, scope, key, want, s, action)
			}})
			r.Actions = append(r.Actions, action)
			if action.Status == "blocked" {
				r.block("setting", scope, key, action.Reason)
			}
		}
	}
	return jobs, nil
}
