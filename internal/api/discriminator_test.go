package api

import (
	"encoding/json"
	"testing"

	"github.com/Jetscale-ai/cli/internal/api/generated"
)

func TestOptionalDiscriminatorSerialization(t *testing.T) {
	var config generated.CloudCostDataSourceUpsertRequest_ProviderConfig
	var scope generated.CreateExplorerSessionRequest_Scope
	testOptionalDiscriminator(t, "AwsS3CostDataSourceWriteConfig", "source_kind", "focus_s3", &config, config.FromAwsS3CostDataSourceWriteConfig, config.MergeAwsS3CostDataSourceWriteConfig)
	testOptionalDiscriminator(t, "GcpBigQueryCostDataSourceWriteConfig", "source_kind", "focus_bigquery", &config, config.FromGcpBigQueryCostDataSourceWriteConfig, config.MergeGcpBigQueryCostDataSourceWriteConfig)
	testOptionalDiscriminator(t, "AzureBlobCostDataSourceWriteConfig", "source_kind", "focus_azure_blob", &config, config.FromAzureBlobCostDataSourceWriteConfig, config.MergeAzureBlobCostDataSourceWriteConfig)
	testOptionalDiscriminator(t, "CloudAccountScope", "kind", "cloud_account", &scope, scope.FromCloudAccountScope, scope.MergeCloudAccountScope)
	testOptionalDiscriminator(t, "BusinessUnitScope", "kind", "business_unit", &scope, scope.FromBusinessUnitScope, scope.MergeBusinessUnitScope)
	testOptionalDiscriminator(t, "CompanyScope", "kind", "company", &scope, scope.FromCompanyScope, scope.MergeCompanyScope)
}

func testOptionalDiscriminator[T any](t *testing.T, name, property, want string, union interface {
	json.Marshaler
	json.Unmarshaler
}, from, merge func(T) error) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		var value T
		// Neither constructing nor decoding an omitted property should insert a default.
		for _, decode := range []bool{false, true} {
			if decode {
				if err := json.Unmarshal([]byte(`{}`), &value); err != nil {
					t.Fatal(err)
				}
			}
			if _, present := discriminatorObject(t, value)[property]; present {
				t.Fatal("unset optional discriminator must remain omitted")
			}
		}
		explicit, err := json.Marshal(map[string]string{property: want})
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(explicit, &value); err != nil {
			t.Fatal(err)
		}
		if got := discriminatorObject(t, value)[property]; got != want {
			t.Fatalf("explicit discriminator = %v, want %s", got, want)
		}

		for _, helper := range []struct {
			name string
			call func(T) error
		}{{"From", from}, {"Merge", merge}} {
			t.Run(helper.name, func(t *testing.T) {
				if err := union.UnmarshalJSON([]byte(`{}`)); err != nil {
					t.Fatal(err)
				}
				var zero T
				if err := helper.call(zero); err != nil {
					t.Fatal(err)
				}
				if got := discriminatorObject(t, union)[property]; got != want {
					t.Fatalf("union discriminator = %v, want %s", got, want)
				}
			})
		}
	})
}

func discriminatorObject(t *testing.T, value any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	return object
}
