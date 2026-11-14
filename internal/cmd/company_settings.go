package cmd

import (
	"errors"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/spf13/cobra"
)

func newCompanySettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "company-settings", Short: "Get and ensure known non-secret company settings", GroupID: groupCore,
		Long: `Get and ensure known non-secret WorkOS company settings.

Only organization ID and provider type can be ensured, as strings. Required-SSO
policy is read-only. Unknown keys and malformed values fail closed; raw setting
bodies are never printed. Creating a mapping may activate existing SSO gates;
this command does not configure shared WorkOS providers or authorize that effect.`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newCompanySettingsGetCmd(), newCompanySettingsEnsureCmd())
	return cmd
}

func newCompanySettingsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get KEY", Short: "Get a safe scalar setting (exit 3 if absent)", Args: exactArgs(1),
		Example: `  jetscale company-settings get WORKOS_SSO_REQUIRED_COMPANY_SETTING_KEY --company acme -o json`,
		PreRunE: func(_ *cobra.Command, args []string) error {
			if err := api.ValidateCompanySettingKey(args[0], false); err != nil {
				return usageError(err)
			}
			return nil
		},
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
			row, err := run.client.GetCompanySetting(run.ctx, company.ID(), args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(row)
		},
	}
}

func newCompanySettingsEnsureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "ensure KEY", Short: "Create a missing string setting, or report noop/drift", Args: exactArgs(1),
		Long: `Create a missing non-secret string setting. An exact match is a noop;
conflicting values or types exit 7 without overwriting. Values are not trimmed.
Writes require --yes or interactive confirmation. Dry runs never write.`,
		Example: `  jetscale company-settings ensure WORKOS_SSO_ORG_ID_COMPANY_SETTING_KEY --company acme --value org_example --dry-run`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := api.ValidateCompanySettingKey(args[0], true); err != nil {
				return usageError(err)
			}
			if !cmd.Flags().Changed("value") {
				return usageError(errors.New("--value is required"))
			}
			value, _ := cmd.Flags().GetString("value")
			if err := api.ValidateCompanySettingValue(value); err != nil {
				return usageError(err)
			}
			return nil
		},
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
			value, _ := cmd.Flags().GetString("value")
			result, err := run.client.EnsureCompanySetting(run.ctx, company.ID(), args[0], value, true)
			if err != nil {
				return apiExit(err)
			}
			if result.Status != api.StatusPlanned || flagBool(cmd, "dry-run") {
				return run.result(result, "")
			}
			confirmed, err := confirm(cmd, "Create this company setting? A mapping may activate existing SSO gates.")
			if err != nil {
				return err
			}
			if !confirmed {
				if err := run.result(result, ""); err != nil {
					return err
				}
				return confirmationRequired(errors.New("creating this setting requires --yes or terminal confirmation; nothing was changed"))
			}
			result, err = run.client.EnsureCompanySetting(run.ctx, company.ID(), args[0], value, false)
			if err != nil {
				return apiExit(err)
			}
			return run.result(result, "")
		},
	}
	cmd.Flags().String("value", "", "non-secret string value (required, max 1024 bytes)")
	cmd.Flags().Bool("dry-run", false, "report the outcome without writing")
	addYesFlag(cmd, "create missing setting only")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "yes")
	return cmd
}
