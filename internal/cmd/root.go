package cmd

import (
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "jetscale",
		Short: "JetScale CLI — cloud cost optimization from the terminal",
		Long: `jetscale is the operator CLI for the JetScale cloud cost optimization platform.

It provides authenticated access to recommendations, analysis, and remediation
plans through a scriptable terminal interface.

Get started:
  jetscale auth login
  jetscale recommendations list
  jetscale analyze "Rightsize our EC2 fleet"`,
		SilenceUsage: true,
	}

	root.AddCommand(newVersionCmd())

	return root
}

func Execute() error {
	return newRootCmd().Execute()
}
