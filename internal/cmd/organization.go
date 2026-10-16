package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

// getAliases lets az ("show") and gh ("view") habits reach get.
var getAliases = []string{"show", "view"}

func newCompaniesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "companies",
		Aliases: []string{"company"},
		Short:   "List, get and create companies",
		GroupID: groupCore,
		Long: `List, get and create companies.

create is idempotent: an existing company with the slug is "already exists
(unchanged)" when it matches, or drift (exit 7) when it differs; it is never
changed. Every write supports --dry-run.`,
		Example: `  jetscale companies list
  jetscale companies get unstable-jetscale -o json
  jetscale companies create --name "Acme" --slug acme --domain-email acme.com --license-plan ESSENTIAL_ANNUAL --dry-run`,
	}
	addResourceFlags(cmd, false)
	cmd.AddCommand(newCompaniesListCmd(), newCompaniesGetCmd(), newCompaniesCreateCmd())
	return cmd
}

var companyColumns = []output.Column{column("Name", "name"), column("Slug", "slug"), column("Status", "status"), column("ID", "id")}

func newCompaniesListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List companies visible to you",
		Args:  exactArgs(0),
		Example: `  jetscale companies list
  jetscale companies list --slug unstable-jetscale -o json
  jetscale companies list -q`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			filters := map[string]string{}
			if slug := flagString(cmd, "slug"); slug != "" {
				filters["slug"] = "eq " + slug
			}
			if name := flagString(cmd, "name"); name != "" {
				filters["name"] = "eq " + name
			}
			companies, err := run.client.ListCompanies(run.ctx, filters)
			if err != nil {
				return apiExit(err)
			}
			return run.list(companies, api.CompanyFields, companyColumns, "companies")
		},
	}
	cmd.Flags().String("slug", "", "only the company with this slug")
	cmd.Flags().String("name", "", "only companies with this exact name")
	return cmd
}

func newCompaniesGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <company>",
		Aliases: getAliases,
		Short:   "Get one company by ID, slug or exact name (exit 3 if absent)",
		Args:    exactArgs(1),
		Example: `  jetscale companies get unstable-jetscale
  jetscale companies get 8cb59069-340c-419e-8d1f-251404a4ba54 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.client.ResolveCompany(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(company.Project(api.CompanyFields...))
		},
	}
}

func newCompaniesCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a company unless one with the slug exists",
		Long: `Create a company unless one with the slug already exists.

With --license-plan the company and its license are created in one atomic
Backend setup, so a new company is never left without its license. The plan
must be active. An existing company is compared by name, domain emails and
non-root status; its license is not touched (see licenses list).

Creating a license can activate pending performance-fee charges.`,
		Args: exactArgs(0),
		Example: `  jetscale companies create --name "Acme" --slug acme --domain-email acme.com --dry-run
  jetscale companies create --name "Acme" --slug acme --domain-email acme.com \
    --license-plan ESSENTIAL_ANNUAL --license-name "Acme License" --billing-interval YEARLY`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlag(cmd, "name", "slug"); err != nil {
				return err
			}
			spec := api.CompanySpec{
				Name:         flagString(cmd, "name"),
				Slug:         flagString(cmd, "slug"),
				DomainEmails: flagStrings(cmd, "domain-email"),
				Description:  flagString(cmd, "description"),
			}
			if !slugPattern.MatchString(spec.Slug) {
				return usageError(errors.New("--slug must be lowercase letters, digits and single hyphens"))
			}
			if len(spec.DomainEmails) == 0 {
				return usageHint(errors.New("--domain-email is required at least once"), cmd.CommandPath()+" --name <name> --slug <slug> --domain-email <domain>")
			}
			plan := flagString(cmd, "license-plan")
			interval := strings.ToUpper(flagString(cmd, "billing-interval"))
			if interval != "" && interval != "MONTHLY" && interval != "YEARLY" {
				return usageError(errors.New("--billing-interval must be MONTHLY or YEARLY"))
			}
			if plan == "" && (flagString(cmd, "license-name") != "" || flagString(cmd, "license-description") != "" || interval != "") {
				return usageError(errors.New("license flags need --license-plan"))
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			if plan != "" {
				record, err := run.client.ResolvePricingPlan(run.ctx, plan)
				if err != nil {
					return apiExit(err)
				}
				if active, ok := record.Bool("is_active"); !ok || !active {
					return &ExitError{Code: ExitForbidden, Reason: "plan_inactive", Err: fmt.Errorf("pricing plan %s is not active", plan), Hint: run.cmd.Root().Name() + " plans list --active"}
				}
				spec.License = &api.LicenseSpec{PricingPlanID: record.ID(), Name: flagString(cmd, "license-name"), Description: flagString(cmd, "license-description"), BillingInterval: interval}
			}
			res, err := run.client.EnsureCompany(run.ctx, spec, flagBool(cmd, "dry-run"))
			if err != nil {
				return apiExit(err)
			}
			if spec.License != nil {
				if res.Details == nil {
					res.Details = api.Record{}
				}
				res.Details["pricing_plan_id"] = spec.License.PricingPlanID
			}
			return run.result(res, run.cmd.Root().Name()+" licenses list --company "+spec.Slug)
		},
	}
	cmd.Flags().String("name", "", "company name (required)")
	cmd.Flags().String("slug", "", "unique company slug (required)")
	cmd.Flags().StringSlice("domain-email", nil, "email domain allowed for the company (repeatable, required)")
	cmd.Flags().String("description", "", "company description")
	cmd.Flags().String("license-plan", "", "create the license atomically from this active plan (ID or internal name)")
	cmd.Flags().String("license-name", "", "license name (with --license-plan)")
	cmd.Flags().String("license-description", "", "license description (with --license-plan)")
	cmd.Flags().String("billing-interval", "", "MONTHLY or YEARLY (with --license-plan; default is the plan's)")
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	return cmd
}

func newBusinessUnitsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "business-units",
		Aliases: []string{"business-unit", "bu"},
		Short:   "List, get and create business units",
		GroupID: groupCore,
		Example: `  jetscale business-units list --company unstable-jetscale
  jetscale bu create --company acme --name "Acme Clouds" --slug acme-clouds --dry-run`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newUnitsListCmd(), newUnitsGetCmd(), newUnitsCreateCmd())
	return cmd
}

var unitColumns = []output.Column{column("Name", "name"), column("Slug", "slug"), column("ID", "id")}

func newUnitsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List business units of a company",
		Args:  exactArgs(0),
		Example: `  jetscale business-units list --company unstable-jetscale
  jetscale business-units list -q`,
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
			units, err := run.client.ListBusinessUnitsForCompany(run.ctx, company.ID())
			if err != nil {
				return apiExit(err)
			}
			return run.list(units, api.BusinessUnitFields, unitColumns, "business units")
		},
	}
}

func newUnitsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <business-unit>",
		Aliases: getAliases,
		Short:   "Get one business unit by ID, slug or exact name (exit 3 if absent)",
		Args:    exactArgs(1),
		Example: `  jetscale business-units get unstable-jetscale-clouds --company unstable-jetscale -o json`,
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
			unit, err := run.client.ResolveBusinessUnit(run.ctx, company.ID(), args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(unit.Project(api.BusinessUnitFields...))
		},
	}
}

func newUnitsCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a business unit unless the company has one with the slug",
		Long: `Create a business unit unless the company already has one with the slug.

The Backend does not enforce slug uniqueness and provisions the unit's managed
agent after the unit commits. Run one create at a time per company, and after
an uncertain outcome (exit 1, reason outcome_uncertain) read with
"business-units get" instead of creating again.`,
		Args: exactArgs(0),
		Example: `  jetscale business-units create --company acme --name "Acme Clouds" --slug acme-clouds --dry-run
  jetscale business-units create --company acme --name "Acme Clouds" --slug acme-clouds -q`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlag(cmd, "name", "slug"); err != nil {
				return err
			}
			slug := flagString(cmd, "slug")
			if !slugPattern.MatchString(slug) {
				return usageError(errors.New("--slug must be lowercase letters, digits and single hyphens"))
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
			res, err := run.client.EnsureBusinessUnit(run.ctx, api.BusinessUnitSpec{CompanyID: company.ID(), Name: flagString(cmd, "name"), Slug: slug, Description: flagString(cmd, "description")}, flagBool(cmd, "dry-run"))
			if err != nil {
				return apiExit(err)
			}
			return run.result(res, run.cmd.Root().Name()+" accounts link --company "+company.String("slug")+" --business-unit "+slug+" ...")
		},
	}
	cmd.Flags().String("name", "", "business unit name (required)")
	cmd.Flags().String("slug", "", "slug, unique within the company (required)")
	cmd.Flags().String("description", "", "business unit description")
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	return cmd
}
