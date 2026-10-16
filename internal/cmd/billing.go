package cmd

import (
	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newPlansCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "plans",
		Aliases: []string{"plan", "pricing-plans"},
		Short:   "List and get pricing plans",
		GroupID: groupBilling,
		Example: `  jetscale plans list --active
  jetscale plans get ESSENTIAL_ANNUAL -o json`,
	}
	addResourceFlags(cmd, false)
	cmd.AddCommand(newPlansListCmd(), newPlansGetCmd())
	return cmd
}

var planColumns = []output.Column{column("Internal name", "internal_name"), column("Display name", "display_name"), column("Active", "is_active"), column("Interval", "default_billing_interval"), column("ID", "id")}

func newPlansListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pricing plans",
		Args:  exactArgs(0),
		Example: `  jetscale plans list
  jetscale plans list --active -o json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			filters := map[string]string{}
			if name := flagString(cmd, "internal-name"); name != "" {
				filters["internal_name"] = "eq " + name
			}
			if flagBool(cmd, "active") {
				filters["is_active"] = "eq true"
			}
			plans, err := run.client.ListPricingPlans(run.ctx, filters)
			if err != nil {
				return apiExit(err)
			}
			return run.list(plans, api.PricingPlanFields, planColumns, "pricing plans")
		},
	}
	cmd.Flags().String("internal-name", "", "only the plan with this internal name")
	cmd.Flags().Bool("active", false, "only active plans")
	return cmd
}

func newPlansGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <plan>",
		Aliases: getAliases,
		Short:   "Get one pricing plan by ID or internal name (exit 3 if absent)",
		Args:    exactArgs(1),
		Example: `  jetscale plans get ESSENTIAL_ANNUAL -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			plan, err := run.client.ResolvePricingPlan(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(plan.Project(api.PricingPlanFields...))
		},
	}
}

func newLicensesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "licenses",
		Aliases: []string{"license"},
		Short:   "List and get licenses (read-only)",
		GroupID: groupBilling,
		Long: `List and get licenses.

Licenses are created with their company ("companies create --license-plan"),
because creating one for an existing company can activate pending
performance-fee charges.`,
		Example: `  jetscale licenses list --company unstable-jetscale -o json`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newLicensesListCmd(), newLicensesGetCmd())
	return cmd
}

var licenseColumns = []output.Column{column("Name", "name"), column("Status", "status"), column("Interval", "billing_interval"), column("Valid until", "valid_until"), column("ID", "id")}

func newLicensesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List licenses of a company",
		Args:    exactArgs(0),
		Example: `  jetscale licenses list --company unstable-jetscale`,
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
			licenses, err := run.client.ListLicenses(run.ctx, company.ID())
			if err != nil {
				return apiExit(err)
			}
			return run.list(licenses, api.LicenseFields, licenseColumns, "licenses")
		},
	}
}

func newLicensesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <license-id>",
		Aliases: getAliases,
		Short:   "Get one license (exit 3 if absent)",
		Args:    exactArgs(1),
		Example: `  jetscale licenses get b4ff82d3-1521-4f09-9595-ea2cb812bbaa -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			license, err := run.client.GetLicense(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(license.Project(api.LicenseFields...))
		},
	}
}
