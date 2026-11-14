package cmd

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

const maxRecommendationRows = 1000

func newRecommendationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "recommendations",
		Aliases: []string{"recs", "rec", "recommendation"},
		Short:   "Read recommendations and the evidence they used",
		GroupID: groupCore,
		Long: `Read the latest recommendation versions per cloud account and the evidence
sources (for example FOCUS cost attribution) each one used.

list covers the active account (accounts use) or --account when set, and
otherwise every account of the company.`,
		Example: `  jetscale recommendations list --company unstable-jetscale
  jetscale recs list --account 402289717651 -o json
  jetscale recommendations sources <recommendation-id>`,
	}
	addResourceFlags(cmd, true)
	cmd.AddCommand(newRecsListCmd(), newRecsGetCmd(), newRecsSourcesCmd(), newRecsInspectCmd(), newRecsRegenerateCmd())
	return cmd
}

var recommendationColumns = []output.Column{column("Title", "title"), column("Status", "status"), column("Account", "cloud_account_id"), column("ID", "id")}

func newRecsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the latest recommendation versions per cloud account",
		Args:  exactArgs(0),
		Example: `  jetscale recommendations list --company unstable-jetscale
  jetscale recommendations list --account 402289717651 --limit 20 -o json
  jetscale recommendations list --company acme --all-accounts -q`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			if limit < 1 || limit > maxRecommendationRows {
				return usageError(fmt.Errorf("--limit must be between 1 and %d", maxRecommendationRows))
			}
			filters, err := recommendationListFilters(cmd)
			if err != nil {
				return err
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			var accounts []api.Record
			selector := explicitAccount(cmd)
			if selector == "" && !flagBool(cmd, "all-accounts") {
				cfg, _ := config.Load()
				selector = config.ResolveAccount(cfg, run.instance, "")
			}
			if selector != "" {
				accounts, err = run.pickAccounts([]string{selector})
			} else {
				accounts, err = run.scopeAccounts()
			}
			if err != nil {
				return err
			}
			rows := []api.Record{}
			for _, account := range accounts {
				recs, err := run.client.LatestRecommendations(run.ctx, account.ID(), filters)
				if err != nil {
					return apiExit(err)
				}
				for _, rec := range recs {
					if len(rows) == limit {
						break
					}
					row := rec.Project(api.RecommendationFields...)
					row["cloud_account_id"] = account.ID()
					rows = append(rows, row)
				}
			}
			return run.list(rows, append([]string{"cloud_account_id"}, api.RecommendationFields...), recommendationColumns, "recommendations")
		},
	}
	cmd.Flags().Int("limit", 100, fmt.Sprintf("maximum rows (max %d)", maxRecommendationRows))
	cmd.Flags().Bool("all-accounts", false, "ignore the active account and list every account in scope")
	cmd.Flags().StringSlice("status", nil, "raw Backend status (repeatable; DISCOVERED, IN_PROGRESS, COMPLETED, DISCARDED, POSTPONED, STALE; no filter when omitted)")
	cmd.Flags().StringSlice("resource-type", nil, "cloud resource type (repeatable; no filter when omitted)")
	return cmd
}

// recommendationListFilters is the shared query for recommendations list.
// Repeatable values are one comma-joined param. Omitted flags are absent,
// so the list stays unfiltered. Callers must not redeclare these flags.
// Values with whitespace or commas are rejected before any network call:
// Backend treats the first space-separated word as an operator.
func recommendationListFilters(cmd *cobra.Command) (map[string]string, error) {
	filters := map[string]string{}
	status, err := recommendationFilterValues(cmd, "status")
	if err != nil {
		return nil, err
	}
	if len(status) > 0 {
		filters["status"] = strings.Join(status, ",")
	}
	resourceTypes, err := recommendationFilterValues(cmd, "resource-type")
	if err != nil {
		return nil, err
	}
	if len(resourceTypes) > 0 {
		filters["cloud_resource_type"] = strings.Join(resourceTypes, ",")
	}
	if len(filters) == 0 {
		return nil, nil
	}
	return filters, nil
}

func recommendationFilterValues(cmd *cobra.Command, name string) ([]string, error) {
	values := flagStrings(cmd, name)
	for _, value := range values {
		if strings.Contains(value, ",") || strings.ContainsFunc(value, unicode.IsSpace) {
			return nil, usageError(fmt.Errorf("--%s value %q must not contain whitespace or commas", name, value))
		}
	}
	return values, nil
}

func newRecsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <recommendation-id>",
		Aliases: getAliases,
		Short:   "Get one recommendation",
		Args:    exactArgs(1),
		Example: `  jetscale recommendations get 3f2c0a6e-0000-4000-8000-000000000000 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			rec, err := run.client.GetCustomRecommendation(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(rec.Project(api.RecommendationFields...))
		},
	}
}

func newRecsSourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "sources <recommendation-id>",
		Short:   "List the evidence sources a recommendation used (for example FOCUS cost attribution)",
		Args:    exactArgs(1),
		Example: `  jetscale recommendations sources 3f2c0a6e-0000-4000-8000-000000000000 -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			sources, err := run.client.RecommendationSources(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			if run.quiet {
				for _, s := range sources {
					if key := s.String("source_key"); key != "" {
						_, _ = fmt.Fprintln(run.out.Out, key)
					}
				}
				return nil
			}
			if run.out.Format == output.Table && len(sources) == 0 {
				_, err := fmt.Fprintln(run.out.Out, "No sources found.")
				return err
			}
			rows := make([]interface{}, 0, len(sources))
			for _, s := range sources {
				rows = append(rows, s)
			}
			return run.out.Print(rows, []output.Column{column("Source", "source_key"), column("Status", "status")})
		},
	}
}

func newRecsInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "inspect <recommendation-id> <source-key>",
		Short:   "Inspect one evidence source of a recommendation",
		Args:    exactArgs(2),
		Example: `  jetscale recommendations inspect 3f2c0a6e-0000-4000-8000-000000000000 focus-cost-attribution -o json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			inspection, err := run.client.InspectRecommendationSource(run.ctx, args[0], args[1])
			if err != nil {
				return apiExit(err)
			}
			return run.record(inspection)
		},
	}
}

func newRecsRegenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "regenerate <recommendation-id>",
		Short: "Regenerate in place (same ID/version; starts inference; not idempotent)",
		Args:  exactArgs(1),
		Long: `Regenerate an existing resource recommendation in place (same ID/version).
A successful run overwrites its result rather than creating a new version.
Capture the completed result and its evidence before the next successful run
replaces them.

By default the Backend selects all eligible evidence sources. --exclude-source
omits a source for this run only; it does not disable a billing source or change
stored FOCUS data. Exclusions require a superuser and Backend-supported source
keys for this resource. Every accepted call starts inference; do not retry an
uncertain submission.`,
		Example: `  jetscale recommendations regenerate 3f2c0a6e-0000-4000-8000-000000000000 -y
  jetscale recommendations regenerate 3f2c0a6e-0000-4000-8000-000000000000 --exclude-source focus-cost-attribution -y`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			excludedSources, _ := cmd.Flags().GetStringArray("exclude-source")
			if cmd.Flags().Changed("exclude-source") && len(excludedSources) == 0 {
				return usageError(errors.New("--exclude-source must not be empty or blank"))
			}
			if len(excludedSources) > 32 {
				return usageError(errors.New("--exclude-source accepts at most 32 source keys"))
			}
			seen := make(map[string]bool, len(excludedSources))
			for _, key := range excludedSources {
				if strings.TrimSpace(key) == "" {
					return usageError(errors.New("--exclude-source must not be empty or blank"))
				}
				if seen[key] {
					return usageError(errors.New("--exclude-source must not contain duplicate keys"))
				}
				seen[key] = true
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirm(cmd, "Regenerate starts inference and overwrites this recommendation on success (same ID/version; not idempotent). Continue?")
			if err != nil {
				return err
			}
			if !ok {
				return &ExitError{Code: ExitForbidden, Reason: "confirmation_required", Err: errors.New("regenerate starts inference work on every call; rerun with -y/--yes")}
			}
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			excludedSources, _ := cmd.Flags().GetStringArray("exclude-source")
			rec, err := run.client.RegenerateRecommendation(run.ctx, args[0], excludedSources)
			if err != nil {
				return apiExit(err)
			}
			return run.record(rec)
		},
	}
	cmd.Flags().StringArray("exclude-source", nil, "omit an evidence source for this run only (repeatable; e.g. focus-cost-attribution; requires superuser)")
	addYesFlag(cmd, "inference starts on every call")
	return cmd
}
