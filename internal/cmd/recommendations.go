package cmd

import (
	"errors"
	"fmt"

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
				recs, err := run.client.LatestRecommendations(run.ctx, account.ID())
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
	return cmd
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
		Use:     "regenerate <recommendation-id>",
		Short:   "Request a new recommendation version (starts inference; not idempotent)",
		Args:    exactArgs(1),
		Example: `  jetscale recommendations regenerate 3f2c0a6e-0000-4000-8000-000000000000 -y`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ok, err := confirm(cmd, "Regenerate starts inference work and is not idempotent. Continue?")
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
			rec, err := run.client.RegenerateRecommendation(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(rec)
		},
	}
	addYesFlag(cmd, "inference starts on every call")
	return cmd
}
