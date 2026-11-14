package cmd

import (
	"errors"
	"fmt"

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
		Short:   "List, get and ensure license seat capacity",
		GroupID: groupBilling,
		Long: `List and get licenses, or grow an existing active license's finite seat quota.
Ensure-capacity patches only max_seats; it never creates a license or changes
pricing plans, commercial terms, or other quotas.

Licenses are created with their company ("companies create --license-plan"),
because creating one for an existing company can activate pending
performance-fee charges.`,
		Example: `  jetscale licenses list --company unstable-jetscale -o json`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newLicensesListCmd(), newLicensesGetCmd(), newLicensesEnsureCapacityCmd())
	return cmd
}

func newLicensesEnsureCapacityCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "ensure-capacity", Short: "Grow only an active license's finite seat quota", Args: exactArgs(0),
		Long: `Resolve one active company license and ensure its finite seat quota meets
--min-seats. Unlimited (null) and sufficient quotas are unchanged. Missing or
invalid metadata blocks the write. Only max_seats is patched, and commercial
terms are checked before and after. This is not a pricing-plan sandbox override.

Writes require --yes or terminal confirmation. Serialize all writers of the
license: the Backend has no atomic conditional update. An uncertain outcome
stops without retry or rollback.`,
		Example: `  jetscale licenses ensure-capacity --company acme --min-seats 2 --dry-run`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			seats, _ := cmd.Flags().GetInt("min-seats")
			if !cmd.Flags().Changed("min-seats") {
				return usageError(errors.New("--min-seats is required"))
			}
			if err := api.ValidateLicenseCapacity(seats); err != nil {
				return usageError(err)
			}
			return nil
		},
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
			seats, _ := cmd.Flags().GetInt("min-seats")
			result, err := run.client.EnsureLicenseCapacity(run.ctx, company.ID(), seats, true)
			if err != nil {
				return apiExit(err)
			}
			if result.Status != api.StatusPlanned || flagBool(cmd, "dry-run") {
				return printLicenseCapacity(run, result)
			}
			confirmed, err := confirm(cmd, "Grow this license's seat quota only, preserving commercial terms?")
			if err != nil {
				return err
			}
			if !confirmed {
				if err := printLicenseCapacity(run, result); err != nil {
					return err
				}
				return confirmationRequired(errors.New("growing this seat quota requires --yes or terminal confirmation; nothing was changed"))
			}
			result, err = run.client.EnsureLicenseCapacity(run.ctx, company.ID(), seats, false)
			if err != nil {
				return apiExit(err)
			}
			return printLicenseCapacity(run, result)
		},
	}
	cmd.Flags().Int("min-seats", 0, "minimum seat capacity (required, 1..2147483647)")
	cmd.Flags().Bool("dry-run", false, "report the outcome without writing")
	addYesFlag(cmd, "grow finite seat quota only")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "yes")
	return cmd
}

func printLicenseCapacity(run *resourceRun, result api.Result) error {
	if run.quiet || run.out.Format != output.Table {
		return run.result(result, "")
	}
	var line string
	switch result.Status {
	case api.StatusPlanned:
		line = fmt.Sprintf("Would grow license %s max_seats from %v to %v (not applied)", result.ID, result.Details["previous_max_seats"], result.Details["max_seats"])
	case api.StatusUpdated:
		line = fmt.Sprintf("Grew license %s max_seats from %v to %v", result.ID, result.Details["previous_max_seats"], result.Details["max_seats"])
	default:
		line = fmt.Sprintf("License %s already has sufficient seat capacity (unchanged)", result.ID)
	}
	_, err := fmt.Fprintln(run.out.Out, line)
	return err
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
