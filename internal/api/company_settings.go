package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

const (
	CompanySettingWorkOSOrg      = "WORKOS_SSO_ORG_ID_COMPANY_SETTING_KEY"
	CompanySettingWorkOSProvider = "WORKOS_SSO_PROVIDER_TYPE_COMPANY_SETTING_KEY"
	CompanySettingWorkOSRequired = "WORKOS_SSO_REQUIRED_COMPANY_SETTING_KEY"
)

// ValidateCompanySettingKey limits projection to known non-secret settings;
// the Backend does not provide secret classification metadata.
func ValidateCompanySettingKey(key string, writable bool) error {
	switch key {
	case CompanySettingWorkOSOrg, CompanySettingWorkOSProvider:
		return nil
	case CompanySettingWorkOSRequired:
		if !writable {
			return nil
		}
		return errors.New("required-SSO policy is read-only")
	default:
		return errors.New("unsupported company setting key; only known non-secret WorkOS settings are supported")
	}
}

func ValidateCompanySettingValue(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return errors.New("setting value must be a non-empty string of at most 1024 bytes")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("setting value must not contain control characters")
		}
	}
	return nil
}

// GetCompanySetting returns a scalar projection, never a raw setting body.
// Missing is not equivalent to false, especially for required-SSO policy.
func (c *Client) GetCompanySetting(ctx context.Context, companyID, key string) (Record, error) {
	row, err := c.readCompanySetting(ctx, companyID, key)
	if err != nil {
		return nil, err
	}
	if key != CompanySettingWorkOSRequired && row["value_type"] != string(generated.String) {
		return nil, ErrInvalidResponse
	}
	return row, nil
}

func (c *Client) readCompanySetting(ctx context.Context, companyID, key string) (Record, error) {
	if err := ValidateCompanySettingKey(key, false); err != nil {
		return nil, err
	}
	id, ok := resourceUUID(companyID)
	if !ok {
		return nil, errors.New("company ID must be a nonzero UUID")
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.GetSettingByKeyApiV2SystemCompanySettingsByKeySettingKeyGet(ctx, key,
			&generated.GetSettingByKeyApiV2SystemCompanySettingsByKeySettingKeyGetParams{CompanyId: id})
	})
	if err != nil {
		return nil, err
	}
	row, err := dataRecord(body)
	if err != nil {
		return nil, err
	}
	return projectCompanySetting(row, companyID, key)
}

func projectCompanySetting(row Record, companyID, key string) (Record, error) {
	id, valid := row["id"].(string)
	if _, ok := resourceUUID(id); !valid || !ok || row["company"] != companyID || row["key"] != key {
		return nil, ErrInvalidResponse
	}
	payload, ok := row["value"].(map[string]any)
	if !ok || len(payload) != 1 {
		return nil, ErrInvalidResponse
	}
	value, exists := payload["value"]
	if !exists {
		return nil, ErrInvalidResponse
	}
	switch row["value_type"] {
	case string(generated.String):
		v, ok := value.(string)
		if !ok || ValidateCompanySettingValue(v) != nil {
			return nil, ErrInvalidResponse
		}
	case string(generated.Boolean):
		if _, ok := value.(bool); !ok {
			return nil, ErrInvalidResponse
		}
	default:
		return nil, ErrInvalidResponse
	}
	out := Record{"id": id, "company": companyID, "key": key, "value_type": row["value_type"], "value": value}
	if key == CompanySettingWorkOSRequired {
		switch v := value.(type) {
		case bool:
			out["required"] = v
		case string:
			// Unknown policy strings must block, not silently imply password access.
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true":
				out["required"] = true
			case "false":
				out["required"] = false
			default:
				return nil, ErrInvalidResponse
			}
		}
	}
	return out, nil
}

// EnsureCompanySetting creates only missing rows. Existing conflicting values
// or types report drift without PATCH, even with explicit write confirmation.
func (c *Client) EnsureCompanySetting(ctx context.Context, companyID, key, value string, dryRun bool) (Result, error) {
	r := Result{Resource: "company_setting"}
	if err := ValidateCompanySettingKey(key, true); err != nil {
		return r, err
	}
	if err := ValidateCompanySettingValue(value); err != nil {
		return r, err
	}
	existing, err := c.readCompanySetting(ctx, companyID, key)
	if err == nil {
		r.ID, r.Status, r.Details = existing.ID(), StatusNoop, existing
		if existing["value_type"] != string(generated.String) {
			r.Drift = append(r.Drift, "value_type")
		} else if existing["value"] != value {
			r.Drift = append(r.Drift, "value")
		}
		if len(r.Drift) > 0 {
			r.Status = StatusDrift
		}
		return r, nil
	}
	if !IsNotFound(err) {
		return r, err
	}
	r.Details = Record{"company": companyID, "key": key, "value_type": string(generated.String), "value": value, "operation": "create"}
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	id, _ := resourceUUID(companyID) // Validated by the scoped read above.
	body, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.CreateSettingApiV2SystemCompanySettingsPost(ctx, generated.CreateCompanySystemSettingRequest{
			Company: id, Key: key, ValueType: generated.String, Value: map[string]any{"value": value},
		})
	})
	if err != nil {
		if errors.Is(err, ErrUncertain) {
			return r, &UncertainError{Resource: r.Resource}
		}
		return r, err
	}
	created, err := dataRecord(body)
	if err == nil {
		created, err = projectCompanySetting(created, companyID, key)
	}
	if err != nil || created["value_type"] != string(generated.String) || created["value"] != value {
		return r, &UncertainError{Resource: r.Resource}
	}
	after, err := c.GetCompanySetting(ctx, companyID, key)
	if err != nil || after.ID() != created.ID() || after["value"] != value {
		return r, &UncertainError{Resource: r.Resource}
	}
	r.ID, r.Status, r.Details = after.ID(), StatusCreated, after
	return r, nil
}
