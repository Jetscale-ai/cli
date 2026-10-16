package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newCostSourcesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cost-sources",
		Aliases: []string{"cost-source"},
		Short:   "Create, validate and enable FOCUS cost data sources",
		GroupID: groupCore,
		Long: `Create, validate and enable FOCUS cost data sources.

create makes a disabled source unless the company already has one with the
name; validate checks it; enable starts imports and cost matching.`,
		Example: `  jetscale cost-sources list --company unstable-jetscale
  jetscale cost-sources create --company acme --provider aws --name "Acme AWS FOCUS" \
    --provider-scope-id 123456789012 --cover 123456789012 --bucket acme-focus --prefix focus/ --dry-run
  jetscale cost-sources enable "Acme AWS FOCUS" --company acme -y`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newCostSourcesListCmd(), newCostSourcesGetCmd(), newCostSourcesCreateCmd(), newCostSourcesValidateCmd(), newCostSourcesEnableCmd())
	return cmd
}

var costSourceColumns = []output.Column{column("Name", "name"), column("Kind", "source_kind"), column("Enabled", "is_enabled"), column("ID", "id")}

func newCostSourcesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List FOCUS cost sources of a company",
		Example: `  jetscale cost-sources list --company unstable-jetscale -o json`,
		Args:    exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			sources, err := run.client.ListCostSources(run.ctx, company.ID())
			if err != nil {
				return apiExit(err)
			}
			public := make([]api.Record, 0, len(sources))
			for _, s := range sources {
				public = append(public, api.PublicCostSource(s))
			}
			return run.list(public, api.CostSourceFields, costSourceColumns, "cost sources")
		},
	}
	return cmd
}

func newCostSourcesGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <source>",
		Aliases: getAliases,
		Short:   "Get one cost source by ID or exact name",
		Example: `  jetscale cost-sources get "Unstable AWS FOCUS" --company unstable-jetscale -o json`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			source, err := run.client.ResolveCostSource(run.ctx, company.ID(), args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(api.PublicCostSource(source))
		},
	}
	return cmd
}

var focusKinds = map[string]string{"AWS": "focus_s3", "GCP": "focus_bigquery", "AZURE": "focus_azure_blob"}

func newCostSourcesCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a disabled FOCUS cost source unless the company has one with the name",
		Long: `Create a disabled FOCUS cost source unless the company already has one with
the name; then validate and enable it explicitly. Coverage lists the provider
account IDs whose cost rows the source carries; each must be a linked account
of the company.

Provider config:
  aws    --bucket B --prefix P [--region R] [--role-arn A]    secret: external_id
  gcp    --export-project-id P --dataset D --table T [--location L] [--query-project-id Q]
         --credential-account A
  azure  --storage-account S --container C --prefix P --credential-account A`,
		Args: exactArgs(0),
		Example: `  jetscale cost-sources create --company acme --provider aws --name "Acme AWS FOCUS" \
    --provider-scope-id 123456789012 --cover 123456789012 --bucket acme-focus --prefix focus/ --dry-run
  jetscale cost-sources create --company acme --provider gcp --name "Acme GCP FOCUS" \
    --provider-scope-id 01ABCD-234567-89EFGH --cover acme-prod --export-project-id acme-billing \
    --dataset focus --table export --credential-account acme-prod`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlag(cmd, "name", "provider", "provider-scope-id"); err != nil {
				return err
			}
			provider := strings.ToUpper(flagString(cmd, "provider"))
			kind, ok := focusKinds[provider]
			if !ok {
				return usageError(errors.New("--provider must be aws, azure or gcp"))
			}
			cover := flagStrings(cmd, "cover")
			if len(cover) == 0 {
				return usageError(errors.New("--cover is required at least once"))
			}
			config := map[string]any{}
			required := map[string][]string{"AWS": {"bucket", "prefix"}, "GCP": {"export-project-id", "dataset", "table"}, "AZURE": {"storage-account", "container", "prefix"}}[provider]
			optional := map[string][]string{"AWS": {"region", "role-arn"}, "GCP": {"location", "query-project-id"}, "AZURE": {}}[provider]
			for _, name := range required {
				if flagString(cmd, name) == "" {
					return usageError(fmt.Errorf("--%s is required for %s", name, strings.ToLower(provider)))
				}
				config[strings.ReplaceAll(name, "-", "_")] = flagString(cmd, name)
			}
			for _, name := range optional {
				if value := flagString(cmd, name); value != "" {
					config[strings.ReplaceAll(name, "-", "_")] = value
				}
			}
			credentialAccount := flagString(cmd, "credential-account")
			if provider != "AWS" && credentialAccount == "" {
				return usageError(errors.New("--credential-account is required for gcp and azure"))
			}
			var secrets map[string]string
			var err error
			if provider == "AWS" {
				secrets, err = readCredentialFile(flagString(cmd, "credential-file"), []string{"external_id"})
				if err != nil {
					return err
				}
				if secrets["external_id"] != "" {
					config["external_id"] = secrets["external_id"]
				}
			} else if flagString(cmd, "credential-file") != "" {
				return usageError(errors.New("--credential-file applies only to aws sources"))
			}
			defer clearSecrets(config)
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			spec := api.CostSourceSpec{CompanyID: company.ID(), Provider: provider, SourceKind: kind, Name: flagString(cmd, "name"), ProviderScopeID: flagString(cmd, "provider-scope-id"), ContractVersion: flagString(cmd, "contract-version"), ProviderConfig: config}
			if selector := flagString(cmd, "business-unit"); selector != "" {
				unit, err := run.client.ResolveBusinessUnit(run.ctx, company.ID(), selector)
				if err != nil {
					return apiExit(err)
				}
				spec.BusinessUnitID = unit.ID()
			}
			accounts, err := run.resolveAccounts(company.ID(), cover)
			if err != nil {
				return err
			}
			for _, account := range accounts {
				spec.Coverage = append(spec.Coverage, api.CoverageItem{CloudAccountID: account.ID(), ProviderAccountID: account.String("provider_account_id")})
			}
			if credentialAccount != "" {
				account, err := run.client.ResolveCloudAccount(run.ctx, company.ID(), credentialAccount)
				if err != nil {
					return apiExit(err)
				}
				config["credential_cloud_account_id"] = account.ID()
			}
			res, err := run.client.EnsureCostSource(run.ctx, spec, flagBool(cmd, "dry-run"))
			if err != nil {
				return apiExit(err)
			}
			return run.result(res, run.cmd.Root().Name()+" cost-sources validate \""+spec.Name+"\" --company "+company.String("slug"))
		},
	}
	cmd.Flags().String("business-unit", "", "owning business unit (optional)")
	cmd.Flags().String("name", "", "source name, unique within the company (required)")
	cmd.Flags().String("provider", "", "aws, azure or gcp (required)")
	cmd.Flags().String("provider-scope-id", "", "billing scope: payer account, billing account or billing profile (required)")
	cmd.Flags().StringSlice("cover", nil, "provider account ID the source covers (repeatable, required)")
	cmd.Flags().String("contract-version", "1.2", "FOCUS contract version")
	cmd.Flags().String("credential-account", "", "gcp/azure: linked account whose credentials read the export")
	cmd.Flags().String("credential-file", "", "aws: JSON object with external_id, from a private file or descriptor")
	for _, name := range []string{"bucket", "prefix", "region", "role-arn", "export-project-id", "dataset", "table", "location", "query-project-id", "storage-account", "container"} {
		cmd.Flags().String(name, "", "provider config: "+strings.ReplaceAll(name, "-", " "))
	}
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	return cmd
}

func newCostSourcesValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "validate <source>",
		Short:   "Validate a cost source's configuration (--live also reads the export)",
		Example: `  jetscale cost-sources validate "Unstable AWS FOCUS" --company unstable-jetscale --live`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			source, err := run.client.ResolveCostSource(run.ctx, company.ID(), args[0])
			if err != nil {
				return apiExit(err)
			}
			validated, err := run.client.ValidateCostSource(run.ctx, company.ID(), source.ID(), flagBool(cmd, "live"))
			if err != nil {
				return apiExit(err)
			}
			return run.record(validated)
		},
	}
	cmd.Flags().Bool("live", false, "also read the provider export")
	return cmd
}

func newCostSourcesEnableCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enable <source>",
		Short: "Enable a cost source (starts imports and cost matching)",
		Example: `  jetscale cost-sources enable "Unstable AWS FOCUS" --company unstable-jetscale --dry-run
  jetscale cost-sources enable "Unstable AWS FOCUS" --company unstable-jetscale -y`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dryRun := flagBool(cmd, "dry-run")
			if !dryRun {
				ok, err := confirm(cmd, "Enabling starts imports and cost matching. Continue?")
				if err != nil {
					return err
				}
				if !ok {
					return &ExitError{Code: ExitForbidden, Reason: "confirmation_required", Err: errors.New("enabling starts imports; rerun with -y/--yes or --dry-run")}
				}
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			source, err := run.client.ResolveCostSource(run.ctx, company.ID(), args[0])
			if err != nil {
				return apiExit(err)
			}
			res, err := run.client.SetCostSourceEnabled(run.ctx, company.ID(), source.ID(), true, dryRun)
			if err != nil {
				return apiExit(err)
			}
			return run.result(res, run.cmd.Root().Name()+" accounts wait <account> --company "+company.String("slug")+" --for cost_focus --timeout 30m")
		},
	}
	addYesFlag(cmd, "imports start on enable")
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	return cmd
}
