package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"time"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

func ValidateLicenseCapacity(minSeats int) error {
	if minSeats < 1 || int64(minSeats) > math.MaxInt32 {
		return errors.New("--min-seats must be an integer between 1 and 2147483647")
	}
	return nil
}

// These fields must be visible before changing a quota. Null commercial
// overrides are preserved as null (inheritance), never replaced with defaults.
var capacityPreservedFields = []string{
	"pricing_plan", "status", "valid_from", "valid_until", "billing_interval", "currency_code",
	"price_per_month", "price_per_year", "performance_fee_enabled", "performance_fee_rate_bps",
	"performance_fee_allowance_yearly_cents", "performance_fee_term_months", "stripe_subscription_id",
	"max_accounts", "max_business_units", "max_granted_tokens",
}

func capacitySeats(row Record) (*int, error) {
	value, present := row["max_seats"]
	if !present {
		return nil, ErrInvalidResponse
	}
	if value == nil {
		return nil, nil
	}
	n, ok := value.(float64)
	if !ok || n < 0 || n > math.MaxInt32 || math.Trunc(n) != n {
		return nil, ErrInvalidResponse
	}
	seats := int(n)
	return &seats, nil
}

func capacityIdentity(row Record, companyID string) bool {
	id, ok := row["id"].(string)
	_, valid := resourceUUID(id)
	_, companyValid := resourceUUID(row.String("company"))
	status, statusOK := row["status"].(string)
	return ok && valid && companyValid && row.String("company") == companyID && statusOK && generated.LicenseStatus(status).Valid()
}

func capacitySnapshot(row Record, companyID, licenseID string) (Record, error) {
	if !capacityIdentity(row, companyID) || row.ID() != licenseID || row["status"] != string(generated.LicenseStatusACTIVE) {
		return nil, ErrInvalidResponse
	}
	from, ok := row["valid_from"].(string)
	start, err := time.Parse(time.RFC3339Nano, from)
	now := time.Now()
	if !ok || err != nil || start.IsZero() || start.After(now) {
		return nil, ErrInvalidResponse
	}
	if until, present := row["valid_until"]; !present {
		return nil, ErrInvalidResponse
	} else if until != nil {
		value, ok := until.(string)
		end, err := time.Parse(time.RFC3339Nano, value)
		if !ok || err != nil || !end.After(now) {
			return nil, ErrInvalidResponse
		}
	}
	if _, ok := resourceUUID(row.String("pricing_plan")); !ok {
		return nil, ErrInvalidResponse
	}
	if interval := row["billing_interval"]; interval != "MONTHLY" && interval != "YEARLY" {
		return nil, ErrInvalidResponse
	}
	if value := row["currency_code"]; value != nil {
		currency, ok := value.(string)
		if !ok || len(currency) != 3 {
			return nil, ErrInvalidResponse
		}
		for _, r := range currency {
			if r < 'A' || r > 'Z' {
				return nil, ErrInvalidResponse
			}
		}
	}
	snapshot := Record{"id": licenseID, "company": companyID}
	for _, field := range capacityPreservedFields {
		value, present := row[field]
		if !present {
			return nil, ErrInvalidResponse
		}
		switch field {
		case "pricing_plan":
			value = row.String(field)
		case "price_per_month", "price_per_year", "performance_fee_rate_bps", "performance_fee_allowance_yearly_cents", "performance_fee_term_months", "max_accounts", "max_business_units", "max_granted_tokens":
			if value != nil {
				n, ok := value.(float64)
				if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
					return nil, ErrInvalidResponse
				}
				if field != "price_per_month" && field != "price_per_year" && (math.Trunc(n) != n || n > math.MaxInt32) {
					return nil, ErrInvalidResponse
				}
			}
		case "performance_fee_enabled":
			if value != nil {
				if _, ok := value.(bool); !ok {
					return nil, ErrInvalidResponse
				}
			}
		case "stripe_subscription_id":
			if value != nil {
				if _, ok := value.(string); !ok {
					return nil, ErrInvalidResponse
				}
			}
		}
		snapshot[field] = value
	}
	for _, field := range []string{"name", "description"} {
		if value, present := row[field]; present {
			if value != nil {
				if _, ok := value.(string); !ok {
					return nil, ErrInvalidResponse
				}
			}
			snapshot[field] = value
		}
	}
	return snapshot, nil
}

func (c *Client) inspectLicenseCapacity(ctx context.Context, companyID string) (Record, Record, *int, error) {
	rows, err := c.ListLicenses(ctx, companyID)
	if err != nil {
		return nil, nil, nil, err
	}
	var active []Record
	for _, row := range rows {
		if !capacityIdentity(row, companyID) {
			return nil, nil, nil, ErrInvalidResponse
		}
		if row["status"] == string(generated.LicenseStatusACTIVE) {
			active = append(active, row)
		}
	}
	if len(active) != 1 {
		return nil, nil, nil, &ConflictError{Resource: "license", Reason: "missing_or_multiple_active_licenses"}
	}
	row, err := c.GetLicense(ctx, active[0].ID())
	if err != nil {
		return nil, nil, nil, err
	}
	snapshot, err := capacitySnapshot(row, companyID, active[0].ID())
	if err != nil {
		return nil, nil, nil, err
	}
	seats, err := capacitySeats(row)
	return row, snapshot, seats, err
}

func capacityResult(row Record, companyID string, minSeats int, seats *int) Result {
	var maxSeats any
	if seats != nil {
		maxSeats = *seats
	}
	return Result{Resource: "license_capacity", ID: row.ID(), Status: StatusNoop,
		Details: Record{"company": companyID, "min_seats": minSeats, "max_seats": maxSeats, "unlimited": seats == nil}}
}

// EnsureLicenseCapacity grows the selected active license's finite seat quota
// only. It neither creates licenses nor changes catalog or commercial terms.
func (c *Client) EnsureLicenseCapacity(ctx context.Context, companyID string, minSeats int, dryRun bool) (Result, error) {
	r := Result{Resource: "license_capacity"}
	if err := ValidateLicenseCapacity(minSeats); err != nil {
		return r, err
	}
	if _, ok := resourceUUID(companyID); !ok {
		return r, errors.New("company ID must be a nonzero UUID")
	}
	row, before, seats, err := c.inspectLicenseCapacity(ctx, companyID)
	if err != nil {
		return r, err
	}
	terms, err := json.Marshal(before)
	if err != nil {
		return r, ErrInvalidResponse
	}
	digest := sha256.Sum256(terms)
	r = capacityResult(row, companyID, minSeats, seats)
	r.Details["terms_sha256"] = hex.EncodeToString(digest[:])
	if seats == nil || *seats >= minSeats {
		return r, nil
	}
	r.Status = StatusPlanned
	r.Details["operation"], r.Details["previous_max_seats"], r.Details["max_seats"] = "grow", *seats, minSeats
	if dryRun {
		return r, nil
	}
	// Reconcile selection and terms just before writing. The API has no CAS;
	// callers must serialize all writers of this license, not just this command.
	current, snapshot, currentSeats, err := c.inspectLicenseCapacity(ctx, companyID)
	if err != nil {
		return r, err
	}
	if current.ID() != row.ID() || !reflect.DeepEqual(before, snapshot) || !reflect.DeepEqual(seats, currentSeats) {
		return r, &ConflictError{Resource: "license", Reason: "concurrent_change", ID: row.ID()}
	}
	id, _ := resourceUUID(row.ID())
	body, err := c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		return c.gen.UpdateLicenseApiV2LicenseAndBillingLicensesLicenseIdPatch(ctx, id, generated.UpdateLicenseRequest{MaxSeats: &minSeats})
	})
	if err != nil {
		if errors.Is(err, ErrUncertain) {
			return r, &UncertainError{Resource: r.Resource}
		}
		return r, err
	}
	updated, err := dataRecord(body)
	if err != nil || !capacityReadbackMatches(updated, before, companyID, row.ID(), minSeats) {
		return r, &UncertainError{Resource: r.Resource}
	}
	after, afterSnapshot, afterSeats, err := c.inspectLicenseCapacity(ctx, companyID)
	if err != nil || after.ID() != row.ID() || afterSeats == nil || *afterSeats != minSeats || !reflect.DeepEqual(before, afterSnapshot) {
		return r, &UncertainError{Resource: r.Resource}
	}
	r.Status = StatusUpdated
	return r, nil
}

func capacityReadbackMatches(row, before Record, companyID, licenseID string, minSeats int) bool {
	snapshot, err := capacitySnapshot(row, companyID, licenseID)
	if err != nil || !reflect.DeepEqual(before, snapshot) {
		return false
	}
	seats, err := capacitySeats(row)
	return err == nil && seats != nil && *seats == minSeats
}
