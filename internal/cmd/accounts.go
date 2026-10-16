package cmd

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounts",
		Aliases: []string{"account", "cloud-accounts"},
		Short:   "List, link and watch cloud accounts",
		GroupID: groupCore,
		Long: `List, link and watch cloud accounts, and choose the active one.

JetScale organises resources under Company → Business Unit → Cloud Account.
Reads use --company (or the configured default) and otherwise cover every
account you can access. Linking starts discovery and account-wide
recommendations, with inference and cloud-read costs. Secrets are read only
from --credential-file, never from flags or the environment.`,
		Example: `  jetscale accounts list --company unstable-jetscale
  jetscale accounts link --company acme --business-unit acme-clouds --name "Acme AWS" \
    --provider aws --role-arn arn:aws:iam::123456789012:role/Jetscale --dry-run
  jetscale accounts wait 123456789012 --company acme --for resources --timeout 20m
  jetscale accounts use prod-us-east-1`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newAccountsListCmd(), newAccountsGetCmd(), newAccountsLinkCmd(), newAccountsStatusCmd(), newAccountsWaitCmd(), newAccountsUseCmd(), newAccountsCurrentCmd())
	return cmd
}

// scopeAccounts lists the selected company's accounts, or every accessible
// account when no company is selected. Only reads use the wider scope.
func (r *resourceRun) scopeAccounts() ([]api.Record, error) {
	if r.companySelector() == "" {
		accounts, err := r.client.ListAccessibleCloudAccounts(r.ctx)
		return accounts, apiExit(err)
	}
	company, err := r.company()
	if err != nil {
		return nil, err
	}
	accounts, err := r.client.ListCompanyCloudAccounts(r.ctx, company.ID())
	return accounts, apiExit(err)
}

func (r *resourceRun) pickAccounts(selectors []string) ([]api.Record, error) {
	accounts, err := r.scopeAccounts()
	if err != nil {
		return nil, err
	}
	picked := []api.Record{}
	for _, selector := range selectors {
		account, err := api.SelectAccount(accounts, selector)
		if err != nil {
			return nil, apiExit(err)
		}
		picked = append(picked, account)
	}
	return picked, nil
}

func keepRecords(records []api.Record, keep func(api.Record) bool) []api.Record {
	out := []api.Record{}
	for _, r := range records {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

var cloudAccountColumns = []output.Column{
	{Header: " ", Field: func(row interface{}) string {
		if active, _ := row.(api.Record).Bool("active"); active {
			return "*"
		}
		return " "
	}},
	column("Name", "name"), column("Provider", "cloud_provider_type"), column("Provider account", "provider_account_id"), column("ID", "id"),
}

func newAccountsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cloud accounts (* marks the active account)",
		Args:  exactArgs(0),
		Example: `  jetscale accounts list
  jetscale accounts list --company unstable-jetscale --provider aws -o json
  jetscale accounts list --company acme --business-unit acme-clouds -q`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			accounts, err := run.scopeAccounts()
			if err != nil {
				return err
			}
			if selector := flagString(cmd, "business-unit"); selector != "" {
				company, err := run.company()
				if err != nil {
					return err
				}
				unit, err := run.client.ResolveBusinessUnit(run.ctx, company.ID(), selector)
				if err != nil {
					return apiExit(err)
				}
				accounts = keepRecords(accounts, func(r api.Record) bool { return r.String("business_unit") == unit.ID() })
			}
			if provider := strings.ToUpper(flagString(cmd, "provider")); provider != "" {
				accounts = keepRecords(accounts, func(r api.Record) bool { return strings.EqualFold(r.String("cloud_provider_type"), provider) })
			}
			cfg, _ := config.Load()
			active := config.ResolveAccount(cfg, run.instance, flagAccount)
			for _, account := range accounts {
				account["active"] = active != "" && (strings.EqualFold(account.String("name"), active) || account.ID() == active)
			}
			return run.list(accounts, append(api.CloudAccountFields, "active"), cloudAccountColumns, "cloud accounts")
		},
	}
	cmd.Flags().String("business-unit", "", "only accounts of this business unit (needs a company)")
	cmd.Flags().String("provider", "", "only aws, azure or gcp accounts")
	return cmd
}

func newAccountsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <account>",
		Aliases: getAliases,
		Short:   "Get one cloud account by ID, provider account ID or exact name",
		Args:    exactArgs(1),
		Example: `  jetscale accounts get 402289717651 --company unstable-jetscale -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			accounts, err := run.pickAccounts(args)
			if err != nil {
				return err
			}
			return run.record(accounts[0].Project(api.CloudAccountFields...))
		},
	}
}

func newAccountsLinkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Link a provider account into a business unit unless the company already has it",
		Long: `Link a provider account into a business unit unless the company already has it.

An existing link is matched by provider and provider account ID and reported
as already existing; its credentials cannot be read back, so details show
credentials_compared: false. An account linked to another business unit is a
conflict (exit 4) and is never moved. Linking starts discovery and
account-wide recommendations.

Provider flags:
  aws    --role-arn ARN [--region R]...           secret: external_id
  gcp    --project-id P (--workload-identity-provider W --service-account-email E
         [--subject-token-source S] [--quota-project-id Q])   secret: service_account_key_json
  azure  --tenant-id T --client-id C --subscription-id S      secret: client_secret | certificate [, certificate_password]`,
		Args: exactArgs(0),
		Example: `  jetscale accounts link --company acme --business-unit acme-clouds --name "Acme AWS" \
    --provider aws --role-arn arn:aws:iam::123456789012:role/Jetscale --dry-run
  jetscale accounts link --company acme --business-unit acme-clouds --name "Acme GCP" --provider gcp \
    --project-id acme-prod --workload-identity-provider projects/1/locations/global/workloadIdentityPools/p/providers/q \
    --service-account-email jetscale@acme-prod.iam.gserviceaccount.com
  jetscale accounts link --company acme --business-unit acme-clouds --name "Acme Azure" --provider azure \
    --tenant-id <tenant> --client-id <client> --subscription-id <subscription> --credential-file ./azure.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlag(cmd, "business-unit", "name", "provider"); err != nil {
				return err
			}
			spec, err := linkSpec(cmd)
			if err != nil {
				return err
			}
			defer clearSecrets(spec.IntegrationData)
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			company, err := run.company()
			if err != nil {
				return err
			}
			unit, err := run.client.ResolveBusinessUnit(run.ctx, company.ID(), flagString(cmd, "business-unit"))
			if err != nil {
				return apiExit(err)
			}
			spec.CompanyID, spec.BusinessUnitID = company.ID(), unit.ID()
			res, err := run.client.EnsureCloudAccountLink(run.ctx, spec, flagBool(cmd, "dry-run"))
			if err != nil {
				return apiExit(err)
			}
			return run.result(res, run.cmd.Root().Name()+" accounts wait "+spec.ProviderAccountID+" --company "+company.String("slug")+" --for resources --timeout 20m")
		},
	}
	cmd.Flags().String("business-unit", "", "business unit ID, slug or exact name (required)")
	cmd.Flags().String("name", "", "cloud account name (required)")
	cmd.Flags().String("provider", "", "aws, azure or gcp (required)")
	cmd.Flags().String("provider-account-id", "", "provider account ID; derived from the role ARN, project or subscription when omitted")
	cmd.Flags().String("description", "", "cloud account description")
	cmd.Flags().String("credential-file", "", "JSON object of secret fields, from a private file or descriptor such as /dev/fd/3")
	cmd.Flags().String("role-arn", "", "aws: IAM role Jetscale assumes")
	cmd.Flags().StringSlice("region", nil, "aws: region to discover (repeatable; the Backend selects regions when omitted)")
	cmd.Flags().String("project-id", "", "gcp: project ID")
	cmd.Flags().String("workload-identity-provider", "", "gcp: workload identity provider resource name")
	cmd.Flags().String("service-account-email", "", "gcp: service account to impersonate")
	cmd.Flags().String("subject-token-source", "kubernetes_oidc", "gcp: subject token source for workload identity federation")
	cmd.Flags().String("quota-project-id", "", "gcp: quota project")
	cmd.Flags().String("tenant-id", "", "azure: tenant ID")
	cmd.Flags().String("client-id", "", "azure: application (client) ID")
	cmd.Flags().String("subscription-id", "", "azure: subscription ID")
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	return cmd
}

func newAccountsStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <account>...",
		Short: "Show discovery, cost and FOCUS freshness for accounts (one read, no refresh)",
		Args:  minimumArgs(1),
		Example: `  jetscale accounts status 402289717651 jetscale-ai --company unstable-jetscale
  jetscale accounts status 402289717651 --company unstable-jetscale -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			accounts, err := run.pickAccounts(args)
			if err != nil {
				return err
			}
			rows := []api.Record{}
			for _, account := range accounts {
				freshness, err := run.client.CloudAccountFreshness(run.ctx, account.ID())
				if err != nil {
					return apiExit(err)
				}
				row := api.Record{"cloud_account_id": account.ID(), "name": account.String("name"), "provider_account_id": account.String("provider_account_id")}
				for _, domain := range api.FreshnessDomains {
					state, summary := api.DomainState(freshness, domain)
					row[domain] = state
					if summary != nil {
						row[domain+"_detail"] = summary
					}
				}
				rows = append(rows, row)
			}
			fields := []string{"cloud_account_id", "name", "provider_account_id"}
			for _, domain := range api.FreshnessDomains {
				fields = append(fields, domain, domain+"_detail")
			}
			columns := []output.Column{column("Name", "name"), column("Resources", "resources"), column("Cost", "cost"), column("FOCUS", "cost_focus"), column("Attribution", "cost_attribution")}
			return run.list(rows, fields, columns, "cloud accounts")
		},
	}
}

func newAccountsWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait <account>...",
		Short: "Wait until a freshness domain reaches an accepted state on every account",
		Long: `Poll account freshness until the domain reaches an accepted state on every
account. Exits 1 (domain_failed) when a domain fails, 5 (needs_attention)
when it needs a human, and 6 (timeout) at --timeout. Waiting only reads; it
never starts refresh work.

Domains: resources (discovery), cost, cost_focus, cost_attribution.`,
		Args: minimumArgs(1),
		Example: `  jetscale accounts wait 402289717651 --company unstable-jetscale --for resources --timeout 20m
  jetscale accounts wait 402289717651 jetscale-ai --company unstable-jetscale --for cost_focus --timeout 30m`,
		RunE: func(cmd *cobra.Command, args []string) error {
			domain := flagString(cmd, "for")
			if !slices.Contains(api.FreshnessDomains, domain) {
				return usageError(fmt.Errorf("--for must be one of %s", strings.Join(api.FreshnessDomains, ", ")))
			}
			accepted := flagStrings(cmd, "state")
			interval, _ := cmd.Flags().GetDuration("interval")
			if interval < 5*time.Second {
				return usageError(errors.New("--interval must be at least 5s"))
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			accounts, err := run.pickAccounts(args)
			if err != nil {
				return err
			}
			rows := make([]api.Record, len(accounts))
			show := func() { _ = run.list(compactRows(rows), waitFields, waitColumns, "cloud accounts") }
			for {
				done := true
				for i, account := range accounts {
					if rows[i] != nil && slices.Contains(accepted, rows[i].String("state")) {
						continue
					}
					freshness, err := run.client.CloudAccountFreshness(run.ctx, account.ID())
					if err != nil {
						show()
						return printedFailure(err)
					}
					state, summary := api.DomainState(freshness, domain)
					rows[i] = api.Record{"cloud_account_id": account.ID(), "name": account.String("name"), "domain": domain, "state": state, "detail": summary}
					switch {
					case slices.Contains(accepted, state):
					case state == "failed":
						show()
						return &ExitError{Code: ExitFailure, Reason: "domain_failed", Printed: true, Err: fmt.Errorf("%s failed on %s", domain, account.String("name"))}
					case state == "needs_attention":
						show()
						return &ExitError{Code: ExitForbidden, Reason: "needs_attention", Printed: true, Err: fmt.Errorf("%s needs attention on %s", domain, account.String("name"))}
					default:
						done = false
					}
				}
				if done {
					show()
					return nil
				}
				select {
				case <-run.ctx.Done():
					show()
					return &ExitError{Code: ExitTimeout, Reason: "timeout", Printed: true, Transient: true, Err: fmt.Errorf("%s did not reach %s before the deadline", domain, strings.Join(accepted, " or ")), Hint: "rerun with a longer --timeout"}
				case <-time.After(interval):
				}
			}
		},
	}
	cmd.Flags().String("for", "resources", "freshness domain to wait for")
	cmd.Flags().StringSlice("state", []string{"ready", "stale"}, "accepted states (stale means a completed run that has since expired)")
	cmd.Flags().Duration("interval", 30*time.Second, "poll interval (min 5s)")
	return cmd
}

var waitFields = []string{"cloud_account_id", "name", "domain", "state", "detail"}
var waitColumns = []output.Column{column("Name", "name"), column("Domain", "domain"), column("State", "state")}

func compactRows(rows []api.Record) []api.Record {
	out := []api.Record{}
	for _, row := range rows {
		if row != nil {
			out = append(out, row)
		}
	}
	return out
}

func newAccountsUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "use <account-name>",
		Short:   "Set the active cloud account for this instance",
		Args:    cobra.ExactArgs(1),
		Example: `  jetscale accounts use prod-us-east-1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			accountName := args[0]

			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			session, err := commandSession(cmd, instanceName, apiURL)
			if err != nil {
				return err
			}
			client := api.NewSessionClient(apiURL, session.HTTPClient)
			tree, err := client.FetchAccountTree()
			if err != nil {
				return err
			}

			acct, found := api.FindAccountByName(tree.Accounts, accountName)
			if !found {
				names := make([]string, len(tree.Accounts))
				for i, a := range tree.Accounts {
					names[i] = a.Name
				}
				return fmt.Errorf("account %q not found (available: %s)", accountName, strings.Join(names, ", "))
			}

			if err := config.SetActiveAccount(instanceName, acct.Name); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Active account: %s (%s) on %s\n", acct.Name, acct.CloudProviderType, instanceName)
			return nil
		},
	}
}

func newAccountsCurrentCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "current",
		Short:   "Show the active cloud account",
		Example: `  jetscale accounts current`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			accountName := config.ResolveAccount(cfg, instanceName, flagAccount)
			out := cmd.OutOrStdout()

			if accountName == "" {
				session, err := commandSession(cmd, instanceName, apiURL)
				if err != nil {
					if flagCredentialsFile != "" {
						return err
					}
					_, _ = fmt.Fprintln(out, "No account selected and not logged in.")
					_, _ = fmt.Fprintf(out, "Run: jetscale auth login && jetscale accounts use <name>\n")
					return nil
				}

				client := api.NewSessionClient(apiURL, session.HTTPClient)
				tree, err := client.FetchAccountTree()
				if err != nil {
					return err
				}

				if len(tree.Accounts) == 1 {
					acct := tree.Accounts[0]
					_, _ = fmt.Fprintf(out, "%s (%s) on %s (auto-selected, only account)\n", acct.Name, acct.CloudProviderType, instanceName)
					return nil
				}

				_, _ = fmt.Fprintln(out, "No account selected.")
				_, _ = fmt.Fprintf(out, "Run: jetscale accounts use <name>\n")
				_, _ = fmt.Fprintf(out, "See: jetscale accounts list\n")
				return nil
			}

			_, _ = fmt.Fprintf(out, "%s on %s (%s)\n", accountName, instanceName, apiURL)
			return nil
		},
	}
}
