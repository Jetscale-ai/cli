package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// FlagScope selects the tier a flag row lives on. Root rows apply
// application-wide; company and business-unit enables cascade to descendants.
type FlagScope struct {
	Kind string `json:"scope"`
	ID   string `json:"entity_id,omitempty"`
}

const (
	FlagRoot         = "root"
	FlagCompany      = "company"
	FlagBusinessUnit = "business_unit"
	FlagCloudAccount = "cloud_account"
)

var FeatureFlagFields = []string{"id", "key", "value", "is_secret", "description", "deprecated_at", "company", "business_unit", "cloud_account", "root_feature_flag", "effective_value"}

// Cascades reports whether enabling at this scope changes descendants.
func (s FlagScope) Cascades() bool {
	return s.Kind == FlagRoot || s.Kind == FlagCompany || s.Kind == FlagBusinessUnit
}

func (s FlagScope) entity() (openapi_types.UUID, error) {
	if s.Kind == FlagRoot {
		return openapi_types.UUID{}, nil
	}
	id, ok := resourceUUID(s.ID)
	if !ok {
		return id, errors.New("flag scope needs a resolved entity ID")
	}
	return id, nil
}

func (c *Client) GetFeatureFlag(ctx context.Context, scope FlagScope, key string) (Record, error) {
	id, err := scope.entity()
	if err != nil {
		return nil, err
	}
	body, err := c.read(ctx, func(ctx context.Context) (*http.Response, error) {
		switch scope.Kind {
		case FlagRoot:
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsRootByKeyFlagKeyGet(ctx, key)
		case FlagCompany:
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGet(ctx, key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsCompanyByKeyFlagKeyGetParams{CompanyId: id})
		case FlagBusinessUnit:
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGet(ctx, key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsBusinessUnitByKeyFlagKeyGetParams{BusinessUnitId: id})
		case FlagCloudAccount:
			return c.gen.GetFlagByKeyApiV2SystemFeatureFlagsCloudAccountByKeyFlagKeyGet(ctx, key, &generated.GetFlagByKeyApiV2SystemFeatureFlagsCloudAccountByKeyFlagKeyGetParams{CloudAccountId: id})
		}
		return nil, errors.New("unknown flag scope")
	})
	if err != nil {
		if IsNotFound(err) {
			return nil, &NotFoundError{Resource: "feature flag", Selector: key}
		}
		return nil, err
	}
	return dataRecord(body)
}

func (c *Client) ListFeatureFlags(ctx context.Context, scope FlagScope) ([]Record, error) {
	id, err := scope.entity()
	if err != nil {
		return nil, err
	}
	return c.listAll(ctx, func(ctx context.Context, editors ...generated.RequestEditorFn) (*http.Response, error) {
		switch scope.Kind {
		case FlagRoot:
			return c.gen.ListFlagsApiV2SystemFeatureFlagsRootGet(ctx, editors...)
		case FlagCompany:
			return c.gen.ListFlagsApiV2SystemFeatureFlagsCompanyGet(ctx, &generated.ListFlagsApiV2SystemFeatureFlagsCompanyGetParams{CompanyId: id}, editors...)
		case FlagBusinessUnit:
			return c.gen.ListFlagsApiV2SystemFeatureFlagsBusinessUnitGet(ctx, &generated.ListFlagsApiV2SystemFeatureFlagsBusinessUnitGetParams{BusinessUnitId: id}, editors...)
		case FlagCloudAccount:
			return c.gen.ListFlagsApiV2SystemFeatureFlagsCloudAccountGet(ctx, &generated.ListFlagsApiV2SystemFeatureFlagsCloudAccountGetParams{CloudAccountId: id}, editors...)
		}
		return nil, errors.New("unknown flag scope")
	}, nil)
}

// SetFeatureFlag writes one stored value only when it differs, then reads it
// back. It sends value alone, so descriptions, secrecy and deprecation stay
// untouched. Descendant cascades are the Backend's; this verifies the row.
func (c *Client) SetFeatureFlag(ctx context.Context, scope FlagScope, key string, value bool, dryRun bool) (Result, error) {
	r := Result{Resource: "feature_flag", Details: Record{"scope": scope.Kind, "entity_id": scope.ID, "key": key, "value": value}}
	current, err := c.GetFeatureFlag(ctx, scope, key)
	create := false
	switch {
	case IsNotFound(err):
		create = true
	case err != nil:
		return r, err
	default:
		r.ID = current.ID()
		if stored, ok := current.Bool("value"); ok && stored == value {
			r.Status = StatusNoop
			return r, nil
		}
	}
	r.Details["operation"] = map[bool]string{true: "create", false: "set"}[create]
	if dryRun {
		r.Status = StatusPlanned
		return r, nil
	}
	if writeErr := c.writeFlag(ctx, scope, key, value, create); writeErr != nil && !errors.Is(writeErr, ErrUncertain) {
		return r, writeErr
	}
	after, readErr := c.GetFeatureFlag(ctx, scope, key)
	if readErr != nil {
		return r, &UncertainError{Resource: "feature_flag"}
	}
	if stored, ok := after.Bool("value"); !ok || stored != value {
		return r, &UncertainError{Resource: "feature_flag"}
	}
	r.ID = after.ID()
	r.Status = map[bool]string{true: StatusCreated, false: StatusUpdated}[create]
	return r, nil
}

func (c *Client) writeFlag(ctx context.Context, scope FlagScope, key string, value, create bool) error {
	id, err := scope.entity()
	if err != nil {
		return err
	}
	payload := map[string]any{"value": value}
	if create {
		payload["key"] = key
	}
	reader, err := jsonBody(payload)
	if err != nil {
		return err
	}
	const contentType = "application/json"
	_, err = c.write(ctx, func(ctx context.Context) (*http.Response, error) {
		switch {
		case scope.Kind == FlagRoot && create:
			return c.gen.CreateFlagApiV2SystemFeatureFlagsRootPostWithBody(ctx, contentType, reader)
		case scope.Kind == FlagRoot:
			return c.gen.ActionSetFlagApiV2SystemFeatureFlagsRootActionSetFlagKeyPatchWithBody(ctx, key, contentType, reader)
		case scope.Kind == FlagCompany && create:
			return c.gen.CreateFlagApiV2SystemFeatureFlagsCompanyPostWithBody(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsCompanyPostParams{CompanyId: id}, contentType, reader)
		case scope.Kind == FlagCompany:
			return c.gen.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatchWithBody(ctx, key, &generated.ActionSetFlagApiV2SystemFeatureFlagsCompanyActionSetFlagKeyPatchParams{CompanyId: id}, contentType, reader)
		case scope.Kind == FlagBusinessUnit && create:
			return c.gen.CreateFlagApiV2SystemFeatureFlagsBusinessUnitPostWithBody(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsBusinessUnitPostParams{BusinessUnitId: id}, contentType, reader)
		case scope.Kind == FlagBusinessUnit:
			return c.gen.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatchWithBody(ctx, key, &generated.ActionSetFlagApiV2SystemFeatureFlagsBusinessUnitActionSetFlagKeyPatchParams{BusinessUnitId: id}, contentType, reader)
		case scope.Kind == FlagCloudAccount && create:
			return c.gen.CreateFlagApiV2SystemFeatureFlagsCloudAccountPostWithBody(ctx, &generated.CreateFlagApiV2SystemFeatureFlagsCloudAccountPostParams{CloudAccountId: id}, contentType, reader)
		case scope.Kind == FlagCloudAccount:
			return c.gen.ActionSetFlagApiV2SystemFeatureFlagsCloudAccountActionSetFlagKeyPatchWithBody(ctx, key, &generated.ActionSetFlagApiV2SystemFeatureFlagsCloudAccountActionSetFlagKeyPatchParams{CloudAccountId: id}, contentType, reader)
		}
		return nil, errors.New("unknown flag scope")
	})
	return err
}
