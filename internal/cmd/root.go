package cmd

import (
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Global flags — available on every command.
var (
	flagInstance string
	flagLocal    bool
	flagAPIURL   string
	flagAccount  string
	flagOutput   string
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "jetscale",
		Short: "JetScale CLI — cloud cost optimization from the terminal",
		Long: `jetscale is the operator CLI for the JetScale cloud cost optimization platform.

It provides authenticated access to recommendations, analysis, and remediation
plans through a scriptable terminal interface.

By default, commands target the JetScale SaaS (` + config.ProductionURL + `).
Use -i to switch instances or --local for local development.

Get started:
  jetscale auth login
  jetscale accounts list
  jetscale accounts use prod-us-east-1
  jetscale recommendations list

Developer shortcuts:
  jetscale --local auth login                 # authenticate to local backend
  jetscale --local accounts list              # discover local accounts
  jetscale --account staging recommendations list  # one-off account override`,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVarP(&flagInstance, "instance", "i", "", "named instance to target (e.g. local, staging)")
	root.PersistentFlags().BoolVar(&flagLocal, "local", false, "target local backend (shorthand for -i local)")
	root.PersistentFlags().StringVar(&flagAPIURL, "api-url", "", "override API URL directly")
	root.PersistentFlags().StringVar(&flagAccount, "account", "", "cloud account name or ID override")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "output format: table, json, yaml")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newConfigCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newAccountsCmd())
	root.AddCommand(newSystemCmd())

	return root
}

// resolveAPI loads config and resolves the API URL using the global flags.
func resolveAPI() (name string, url string, err error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", err
	}
	return config.Resolve(cfg, flagInstance, flagLocal, flagAPIURL)
}

func Execute() error {
	return newRootCmd().Execute()
}

// printer returns a Printer configured from the global --output flag.
func printer(cmd *cobra.Command) (output.Printer, error) {
	f, err := output.ParseFormat(flagOutput)
	if err != nil {
		return output.Printer{}, err
	}
	return output.Printer{Format: f, Out: cmd.OutOrStdout()}, nil
}
