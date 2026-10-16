package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jetscale-ai/cli/internal/buildinfo"
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
	flagInstance        string
	flagLocal           bool
	flagAPIURL          string
	flagAccount         string
	flagOutput          string
	flagCAFile          string
	flagCredentialsFile string
	flagLogin           string
	flagPasswordStdin   bool
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   executableName(),
		Short: "JetScale CLI — cloud cost optimization from the terminal",
		Long: `jetscale is the operator CLI for the JetScale cloud cost optimization platform.

This ` + buildinfo.Channel + ` build defaults to ` + buildinfo.DefaultURL() + `.
Use -i to switch instances or --local for local development.

Resource commands are noun-verb (list, get, create, set, link, wait), write
idempotently (created / already exists / drift), take --dry-run on every write,
print -o json or -q IDs, and exit 0 ok, 1 failure, 2 usage, 3 not found,
4 conflict, 5 forbidden or unconfirmed, 6 timeout, 7 drift.`,
		Example: `  jetscale auth login
  jetscale config set company unstable-jetscale
  jetscale accounts list
  jetscale recommendations list
  jetscale feature-flags get CLOUD.FOCUS --root
  jetscale api organization/companies --paginate`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			return buildinfo.Validate()
		},
	}

	root.PersistentFlags().StringVarP(&flagInstance, "instance", "i", "", "named instance to target (e.g. local, staging)")
	root.PersistentFlags().BoolVar(&flagLocal, "local", false, "target local backend (shorthand for -i local)")
	root.PersistentFlags().StringVar(&flagAPIURL, "api-url", "", "override API URL directly")
	root.PersistentFlags().StringVar(&flagCAFile, "ca-file", "", "additional trusted CA PEM for authenticated API requests")
	root.PersistentFlags().StringVar(&flagCredentialsFile, "credentials-file", "", "local build only: read admin credentials from dotenv, sign in for one command, then sign out without saving")
	root.PersistentFlags().StringVar(&flagLogin, "login", "", "email or username for one-command authentication (requires --password-stdin; never saves credentials)")
	root.PersistentFlags().BoolVar(&flagPasswordStdin, "password-stdin", false, "read one non-empty password line from non-terminal stdin (max 4 KiB, 5-second deadline; requires --login)")
	root.PersistentFlags().StringVar(&flagAccount, "account", "", "cloud account: ID, provider account ID or name")
	root.PersistentFlags().StringVarP(&flagOutput, "output", "o", "table", "output format: table, json, yaml")

	root.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core Commands:"},
		&cobra.Group{ID: groupBilling, Title: "Billing Commands:"},
	)
	auth := newAuthCmd()
	auth.GroupID = groupCore
	root.AddCommand(
		auth,
		newCompaniesCmd(),
		newBusinessUnitsCmd(),
		newAccountsCmd(),
		newRecommendationsCmd(),
		newCostSourcesCmd(),
		newFeatureFlagsCmd(),
		newPlansCmd(),
		newLicensesCmd(),
		newAPICmd(),
		newConfigCmd(),
		newSystemCmd(),
		newVersionCmd(),
	)
	// The published basics and flags lanes still call these; hidden from help.
	prepare := newPrepareCmd()
	prepare.Hidden = true
	prepare.Short = "Deprecated: use companies, business-units and feature-flags"
	flags := newFlagsCmd()
	flags.Hidden = true
	flags.Short = "Deprecated: use feature-flags"
	root.AddCommand(prepare, flags)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError(err) })

	wrapSessionLifecycle(root)
	wrapPrepareDeadlineCleanup(prepare)
	wrapPrepareDeadlineCleanup(flags)
	adaptHelp(root, root.Name())
	return root
}

const (
	groupCore    = "core"
	groupBilling = "billing"
)

func executableName() string {
	name := filepath.Base(os.Args[0])
	if strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe") {
		return buildinfo.BinaryName()
	}
	return name
}

// Only rewrite command examples, not hostnames or other occurrences of jetscale.
func adaptHelp(cmd *cobra.Command, name string) {
	cmd.Long = strings.ReplaceAll(cmd.Long, "jetscale ", name+" ")
	cmd.Example = strings.ReplaceAll(cmd.Example, "jetscale ", name+" ")
	for _, child := range cmd.Commands() {
		adaptHelp(child, name)
	}
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
	return ExecuteContext(context.Background())
}

func ExecuteContext(ctx context.Context) error {
	return newRootCmd().ExecuteContext(ctx)
}

// printer returns a Printer configured from the global --output flag.
func printer(cmd *cobra.Command) (output.Printer, error) {
	f, err := output.ParseFormat(flagOutput)
	if err != nil {
		return output.Printer{}, err
	}
	return output.Printer{Format: f, Out: cmd.OutOrStdout()}, nil
}
