package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

var flagKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(\.[A-Z0-9_]+)*$`)

func newFeatureFlagsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "feature-flags",
		Aliases: []string{"feature-flag"},
		Short:   "List, get and set feature flags at one scope",
		GroupID: groupCore,
		Long: `List, get and set feature flags on exactly one scope:

  --root                                application-wide
  --company C                           company C and, on enable, its descendants
  --company C --business-unit B         business unit B and its cloud accounts
  --company C --account A               one cloud account

set writes a stored value only when it differs and reads it back; it sends
the value alone, so descriptions, secrecy and deprecation stay untouched.
Writes at root, company or business-unit scope fan out: set shows the plan and
asks on a terminal; elsewhere it needs -y/--yes, or prints the plan and exits 5.`,
		Example: `  jetscale feature-flags get CLOUD.FOCUS --company unstable-jetscale -o json
  jetscale feature-flags set CLOUD.FOCUS=true AGENTS.EXPLORER=true --root --dry-run
  jetscale feature-flags set AGENTS.EXPLORER=true --company acme --business-unit acme-clouds -y`,
	}
	addResourceFlags(cmd, true)
	cmd.PersistentFlags().Bool("root", false, "root (application-wide) scope")
	cmd.PersistentFlags().String("business-unit", "", "business unit ID, slug or exact name (with --company)")
	cmd.AddCommand(newFeatureFlagsListCmd(), newFeatureFlagsGetCmd(), newFeatureFlagsSetCmd())
	return cmd
}

func validateFlagScope(cmd *cobra.Command) error {
	root := flagBool(cmd, "root")
	company, unit, account := flagString(cmd, "company"), flagString(cmd, "business-unit"), explicitAccount(cmd)
	switch {
	case root && (company != "" || unit != "" || account != ""):
		return usageError(errors.New("--root cannot be combined with --company, --business-unit or --account"))
	case unit != "" && account != "":
		return usageError(errors.New("--business-unit and --account are mutually exclusive"))
	}
	return nil
}

// flagScope resolves the single target scope. Without --root the company is
// --company or the configured default; the account is never a remembered one.
func (r *resourceRun) flagScope() (api.FlagScope, error) {
	if flagBool(r.cmd, "root") {
		return api.FlagScope{Kind: api.FlagRoot}, nil
	}
	company, err := r.company()
	if err != nil {
		return api.FlagScope{}, err
	}
	if selector := flagString(r.cmd, "business-unit"); selector != "" {
		unit, err := r.client.ResolveBusinessUnit(r.ctx, company.ID(), selector)
		if err != nil {
			return api.FlagScope{}, apiExit(err)
		}
		return api.FlagScope{Kind: api.FlagBusinessUnit, ID: unit.ID()}, nil
	}
	if selector := explicitAccount(r.cmd); selector != "" {
		account, err := r.client.ResolveCloudAccount(r.ctx, company.ID(), selector)
		if err != nil {
			return api.FlagScope{}, apiExit(err)
		}
		return api.FlagScope{Kind: api.FlagCloudAccount, ID: account.ID()}, nil
	}
	return api.FlagScope{Kind: api.FlagCompany, ID: company.ID()}, nil
}

var featureFlagColumns = []output.Column{column("Key", "key"), column("Value", "value"), column("Secret", "is_secret"), column("ID", "id")}

func newFeatureFlagsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the flag rows stored at one scope",
		Args:  exactArgs(0),
		Example: `  jetscale feature-flags list --root
  jetscale feature-flags list --company unstable-jetscale --business-unit unstable-jetscale-clouds -o json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := validateFlagScope(cmd); err != nil {
				return err
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			scope, err := run.flagScope()
			if err != nil {
				return err
			}
			flags, err := run.client.ListFeatureFlags(run.ctx, scope)
			if err != nil {
				return apiExit(err)
			}
			return run.list(flags, api.FeatureFlagFields, featureFlagColumns, "feature flags")
		},
	}
}

func newFeatureFlagsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <KEY>...",
		Aliases: getAliases,
		Short:   "Get stored values for keys at one scope (absent keys are reported, not errors)",
		Args:    minimumArgs(1),
		Example: `  jetscale feature-flags get CLOUD.FOCUS --root
  jetscale feature-flags get CLOUD.FOCUS AGENTS.EXPLORER --company unstable-jetscale -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, key := range args {
				if !flagKeyPattern.MatchString(key) {
					return usageError(fmt.Errorf("invalid flag key %q", key))
				}
			}
			if err := validateFlagScope(cmd); err != nil {
				return err
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			scope, err := run.flagScope()
			if err != nil {
				return err
			}
			rows := []api.Record{}
			for _, key := range args {
				row := api.Record{"key": key, "scope": scope.Kind, "entity_id": scope.ID, "present": false}
				flag, err := run.client.GetFeatureFlag(run.ctx, scope, key)
				switch {
				case api.IsNotFound(err):
				case err != nil:
					return apiExit(err)
				default:
					for k, v := range flag.Project(api.FeatureFlagFields...) {
						row[k] = v
					}
					row["present"] = true
				}
				rows = append(rows, row)
			}
			fields := append([]string{"key", "scope", "entity_id", "present"}, api.FeatureFlagFields...)
			return run.list(rows, fields, []output.Column{column("Key", "key"), column("Present", "present"), column("Value", "value"), column("Scope", "scope")}, "feature flags")
		},
	}
}

type flagAssignment struct {
	key   string
	value bool
}

func parseFlagAssignments(args []string) ([]flagAssignment, error) {
	seen := map[string]bool{}
	out := []flagAssignment{}
	for _, arg := range args {
		key, raw, ok := strings.Cut(arg, "=")
		if !ok || !flagKeyPattern.MatchString(key) || seen[key] {
			return nil, usageHint(fmt.Errorf("expected unique KEY=true|false, got %q", arg), "feature-flags set CLOUD.FOCUS=true --company <company>")
		}
		var value bool
		switch strings.ToLower(raw) {
		case "true":
			value = true
		case "false":
			value = false
		default:
			return nil, usageError(fmt.Errorf("value for %s must be true or false", key))
		}
		seen[key] = true
		out = append(out, flagAssignment{key: key, value: value})
	}
	return out, nil
}

func newFeatureFlagsSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <KEY=true|false>...",
		Short: "Set stored values at one scope; only differing rows are written",
		Long: `Set stored values at one scope, in argument order. Unchanged rows are noop.

Stops at the first failure without writing later keys and prints the results
so far. An uncertain write (reason outcome_uncertain) must be reconciled with
"feature-flags get" before any retry.`,
		Args: minimumArgs(1),
		Example: `  jetscale feature-flags set CLOUD.FOCUS=true --company acme --dry-run
  jetscale feature-flags set CLOUD.FOCUS=true CLOUD.AZURE.FOCUS=true --company acme -y
  jetscale feature-flags set AGENTS.EXPLORER=true --root`,
		RunE: func(cmd *cobra.Command, args []string) error {
			assignments, err := parseFlagAssignments(args)
			if err != nil {
				return err
			}
			if err := validateFlagScope(cmd); err != nil {
				return err
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			scope, err := run.flagScope()
			if err != nil {
				return err
			}
			dryRun := flagBool(cmd, "dry-run")
			if !dryRun && scope.Cascades() && !flagBool(cmd, "yes") {
				// Preview first; write only after an explicit yes.
				planned, err := run.setFlags(scope, assignments, true)
				if err != nil {
					return err
				}
				if !anyPlanned(planned) {
					return run.printResults(planned)
				}
				if !canPrompt(cmd) {
					if err := run.printResults(planned); err != nil {
						return err
					}
					return confirmationRequired(fmt.Errorf("%s writes fan out to descendants; rerun with -y/--yes to apply", scope.Kind))
				}
				for _, res := range planned {
					key, _ := res.Details["key"].(string)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "  ~ %s = %v (%s)\n", key, res.Details["value"], res.Status)
				}
				if _, err := confirm(cmd, fmt.Sprintf("Write these %s-scope flags (they fan out to descendants)?", scope.Kind)); err != nil {
					return err
				}
			}
			results, err := run.setFlags(scope, assignments, dryRun)
			if err != nil {
				return err
			}
			return run.printResults(results)
		},
	}
	cmd.Flags().Bool("dry-run", false, "report what would change without writing")
	addYesFlag(cmd, "root, company or business-unit writes")
	return cmd
}

func (r *resourceRun) setFlags(scope api.FlagScope, assignments []flagAssignment, dryRun bool) ([]api.Result, error) {
	results := []api.Result{}
	for _, a := range assignments {
		res, err := r.client.SetFeatureFlag(r.ctx, scope, a.key, a.value, dryRun)
		if err != nil {
			_ = r.printResults(results)
			return nil, printedFailure(err)
		}
		results = append(results, res)
	}
	return results, nil
}

func anyPlanned(results []api.Result) bool {
	for _, res := range results {
		if res.Status == api.StatusPlanned {
			return true
		}
	}
	return false
}

func (r *resourceRun) printResults(results []api.Result) error {
	if r.quiet {
		for _, res := range results {
			if res.ID != "" {
				if _, err := fmt.Fprintln(r.out.Out, res.ID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if r.out.Format != output.Table {
		return r.out.Print(results, nil)
	}
	for _, res := range results {
		key, _ := res.Details["key"].(string)
		if _, err := fmt.Fprintf(r.out.Out, "%s %s %s\n", key, res.Status, res.ID); err != nil {
			return err
		}
	}
	return nil
}
