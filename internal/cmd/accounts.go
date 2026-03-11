package cmd

import (
	"fmt"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newAccountsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "accounts",
		Aliases: []string{"account"},
		Short:   "Manage cloud account selection",
		Long: `Discover and select the cloud account that commands operate against.

JetScale organises resources under Company → Business Unit → Cloud Account.
The accounts command lets you browse this hierarchy and set the active account.

Examples:
  jetscale accounts list
  jetscale accounts use prod-us-east-1
  jetscale accounts current`,
	}

	cmd.AddCommand(newAccountsListCmd())
	cmd.AddCommand(newAccountsUseCmd())
	cmd.AddCommand(newAccountsCurrentCmd())

	return cmd
}

type accountRow struct {
	Name          string `json:"name"           yaml:"name"`
	Provider      string `json:"provider"       yaml:"provider"`
	BusinessUnit  string `json:"business_unit"  yaml:"business_unit"`
	ID            string `json:"id"             yaml:"id"`
	Active        bool   `json:"active"         yaml:"active"`
}

var accountColumns = []output.Column{
	{Header: " ", Field: func(r interface{}) string {
		if r.(accountRow).Active {
			return "*"
		}
		return " "
	}},
	{Header: "Name", Field: func(r interface{}) string { return r.(accountRow).Name }},
	{Header: "Provider", Field: func(r interface{}) string { return r.(accountRow).Provider }},
	{Header: "Business Unit", Field: func(r interface{}) string { return r.(accountRow).BusinessUnit }},
	{Header: "ID", Field: func(r interface{}) string { return r.(accountRow).ID }},
}

func newAccountsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all accessible cloud accounts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			token, err := auth.EnsureFreshToken(instanceName, apiURL)
			if err != nil {
				return err
			}
			if token == "" {
				return fmt.Errorf("not logged in to %s — run: jetscale auth login", instanceName)
			}

			client := api.NewClient(apiURL, token)
			tree, err := client.FetchAccountTree()
			if err != nil {
				return err
			}

			if len(tree.Accounts) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No cloud accounts found.")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nLink one at https://console.jetscale.ai or via the API.")
				return nil
			}

			cfg, _ := config.Load()
			active := config.ResolveAccount(cfg, instanceName, flagAccount)

			buNameByID := make(map[string]string)
			for _, bu := range tree.BusinessUnits {
				buNameByID[bu.ID] = bu.Name
			}

			rows := make([]interface{}, len(tree.Accounts))
			for i, acct := range tree.Accounts {
				buName := buNameByID[acct.BusinessUnit]
				if buName == "" && len(acct.BusinessUnit) >= 8 {
					buName = acct.BusinessUnit[:8] + "…"
				}
				rows[i] = accountRow{
					Name:         acct.Name,
					Provider:     acct.CloudProviderType,
					BusinessUnit: buName,
					ID:           acct.ID,
					Active:       strings.EqualFold(acct.Name, active) || acct.ID == active,
				}
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			return p.Print(rows, accountColumns)
		},
	}
}

func newAccountsUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <account-name>",
		Short: "Set the active cloud account for this instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			accountName := args[0]

			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			token, err := auth.EnsureFreshToken(instanceName, apiURL)
			if err != nil {
				return err
			}
			if token == "" {
				return fmt.Errorf("not logged in to %s — run: jetscale auth login", instanceName)
			}

			client := api.NewClient(apiURL, token)
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
		Use:   "current",
		Short: "Show the active cloud account",
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
				token, err := auth.EnsureFreshToken(instanceName, apiURL)
				if err != nil || token == "" {
					fmt.Fprintln(out, "No account selected and not logged in.")
					_, _ = fmt.Fprintf(out, "Run: jetscale auth login && jetscale accounts use <name>\n")
					return nil
				}

				client := api.NewClient(apiURL, token)
				tree, err := client.FetchAccountTree()
				if err != nil {
					return err
				}

				if len(tree.Accounts) == 1 {
					acct := tree.Accounts[0]
					_, _ = fmt.Fprintf(out, "%s (%s) on %s (auto-selected, only account)\n", acct.Name, acct.CloudProviderType, instanceName)
					return nil
				}

				fmt.Fprintln(out, "No account selected.")
				fmt.Fprintf(out, "Run: jetscale accounts use <name>\n")
				fmt.Fprintf(out, "See: jetscale accounts list\n")
				return nil
			}

			fmt.Fprintf(out, "%s on %s (%s)\n", accountName, instanceName, apiURL)
			return nil
		},
	}
}
