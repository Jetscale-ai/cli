package cmd

import (
	"fmt"

	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View and modify CLI configuration",
	}

	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigSetCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigInstancesCmd())

	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show resolved API target for current flags/env",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, url, err := resolveAPI()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s → %s\n", name, url)
			return nil
		},
	}
}

type instanceRow struct {
	Name    string `json:"name"     yaml:"name"`
	APIURL  string `json:"api_url"  yaml:"api_url"`
	Default bool   `json:"default"  yaml:"default"`
}

var instanceColumns = []output.Column{
	{Header: " ", Field: func(r interface{}) string {
		if r.(instanceRow).Default {
			return "*"
		}
		return " "
	}},
	{Header: "Name", Field: func(r interface{}) string { return r.(instanceRow).Name }},
	{Header: "API URL", Field: func(r interface{}) string { return r.(instanceRow).APIURL }},
}

func newConfigInstancesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "instances",
		Short: "List all configured instances",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			def := cfg.DefaultInstance
			if def == "" {
				def = "production"
			}

			rows := make([]interface{}, 0, len(cfg.Instances))
			for name, inst := range cfg.Instances {
				url := inst.APIURL
				if url == "auto" {
					url = "auto-detect (localhost:8000 / :8010)"
				}
				rows = append(rows, instanceRow{
					Name:    name,
					APIURL:  url,
					Default: name == def,
				})
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			return p.Print(rows, instanceColumns)
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Long: `Set a configuration value. Supported keys:

  default-instance    Change the default instance (e.g. for enterprise deploy)
  instance.<name>     Set or add a named instance URL

Examples:
  jetscale config set default-instance acme
  jetscale config set instance.acme https://jetscale.acme-corp.com`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			switch {
			case key == "default-instance":
				if _, ok := cfg.Instances[value]; !ok {
					return fmt.Errorf("instance %q not found (add it first: jetscale config set instance.%s <url>)", value, value)
				}
				cfg.DefaultInstance = value

			case len(key) > 9 && key[:9] == "instance.":
				name := key[9:]
				if cfg.Instances == nil {
					cfg.Instances = make(map[string]config.Instance)
				}
				cfg.Instances[name] = config.Instance{APIURL: value}
				fmt.Fprintf(cmd.OutOrStdout(), "Added instance %q → %s\n", name, value)

			default:
				return fmt.Errorf("unknown config key %q (supported: default-instance, instance.<name>)", key)
			}

			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved.\n")
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a config value",
		Long: `Get a configuration value. Supported keys:

  default-instance    Show the default instance name
  api-url             Show the resolved API URL (after all overrides)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			switch key {
			case "default-instance":
				def := cfg.DefaultInstance
				if def == "" {
					def = "production"
				}
				fmt.Fprintln(cmd.OutOrStdout(), def)

			case "api-url":
				_, url, err := config.Resolve(cfg, flagInstance, flagLocal, flagAPIURL)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), url)

			default:
				return fmt.Errorf("unknown config key %q (supported: default-instance, api-url)", key)
			}
			return nil
		},
	}
}
