package cmd

import (
	"fmt"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"

	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

func newRecommendationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "recommendations",
		Aliases: []string{"recs", "rec"},
		Short:   "View cost optimization recommendations",
		Long: `Browse and inspect JetScale cost optimization recommendations.

Recommendations are sourced from AWS cost analysis and show actionable
changes to reduce spend: rightsizing, scheduling, reserved capacity, etc.

Examples:
  jetscale recommendations list
  jetscale recommendations list --limit 10
  jetscale recommendations show 550e8400-e29b-41d4-a716-446655440000
  jetscale recs list -o json | jq '.[] | select(.savings_percentage > 20)'`,
	}

	cmd.AddCommand(newRecommendationsListCmd())
	cmd.AddCommand(newRecommendationsShowCmd())

	return cmd
}

type recRow struct {
	ID             string  `json:"id"                yaml:"id"`
	Resource       string  `json:"resource"          yaml:"resource"`
	Region         string  `json:"region"            yaml:"region"`
	Action         string  `json:"action"            yaml:"action"`
	Current        string  `json:"current_type"      yaml:"current_type"`
	Recommended    string  `json:"recommended_type"  yaml:"recommended_type"`
	SavingsPercent float64 `json:"savings_percent"   yaml:"savings_percent"`
	SavingsAmount  string  `json:"savings_amount"    yaml:"savings_amount"`
	Effort         string  `json:"effort"            yaml:"effort"`
	Status         string  `json:"status"            yaml:"status"`
}

var recColumns = []output.Column{
	{Header: "Resource", Field: func(r interface{}) string { return truncateStr(r.(recRow).Resource, 30) }},
	{Header: "Region", Field: func(r interface{}) string { return r.(recRow).Region }},
	{Header: "Action", Field: func(r interface{}) string { return r.(recRow).Action }},
	{Header: "Current", Field: func(r interface{}) string { return r.(recRow).Current }},
	{Header: "Recommended", Field: func(r interface{}) string { return r.(recRow).Recommended }},
	{Header: "Savings %", Field: func(r interface{}) string {
		return fmt.Sprintf("%.1f%%", r.(recRow).SavingsPercent)
	}},
	{Header: "Savings", Field: func(r interface{}) string { return r.(recRow).SavingsAmount }},
	{Header: "Effort", Field: func(r interface{}) string { return r.(recRow).Effort }},
	{Header: "Status", Field: func(r interface{}) string { return r.(recRow).Status }},
}

func newRecommendationsListCmd() *cobra.Command {
	var limit, offset int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cost optimization recommendations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			session, err := commandSession(cmd, instanceName, apiURL)
			if err != nil {
				return err
			}
			client := api.NewSessionClient(apiURL, session.HTTPClient)
			result, err := client.ListRecommendations(limit, offset)
			if err != nil {
				return err
			}

			if len(result.Items) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No recommendations found.")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nGenerate recommendations from the JetScale dashboard or API.")
				return nil
			}

			rows := make([]interface{}, len(result.Items))
			for i, rec := range result.Items {
				rows[i] = recRow{
					ID:             rec.ID,
					Resource:       rec.ResourceName,
					Region:         rec.Region,
					Action:         rec.ActionType,
					Current:        rec.CurrentResourceType,
					Recommended:    rec.RecommendedResourceType,
					SavingsPercent: rec.SavingsPercentage,
					SavingsAmount:  formatSavings(rec.Savings),
					Effort:         rec.ImplementationEffort,
					Status:         rec.ProcessingStatus,
				}
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}

			if err := p.Print(rows, recColumns); err != nil {
				return err
			}

			if p.Format == output.Table && result.Meta.TotalCount > 0 {
				shown := result.Meta.Offset + len(result.Items)
				if shown < result.Meta.TotalCount {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nShowing %d of %d recommendations. Use --offset %d to see more.\n",
						len(result.Items), result.Meta.TotalCount, shown)
				}
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of results (0 = server default)")
	cmd.Flags().IntVar(&offset, "offset", 0, "number of results to skip")

	return cmd
}

func newRecommendationsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show details of a specific recommendation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			session, err := commandSession(cmd, instanceName, apiURL)
			if err != nil {
				return err
			}
			client := api.NewSessionClient(apiURL, session.HTTPClient)
			rec, err := client.GetRecommendation(args[0])
			if err != nil {
				return err
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}

			if p.Format != output.Table {
				return p.Print(rec, nil)
			}

			return printRecDetail(cmd, rec)
		},
	}
}

func printRecDetail(cmd *cobra.Command, rec api.Recommendation) error {
	w := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(w, "Recommendation: %s\n", rec.ID)
	_, _ = fmt.Fprintf(w, "Status:         %s\n", rec.ProcessingStatus)
	_, _ = fmt.Fprintf(w, "Resource:       %s\n", rec.ResourceName)
	_, _ = fmt.Fprintf(w, "Region:         %s\n", rec.Region)
	_, _ = fmt.Fprintf(w, "Action:         %s\n", rec.ActionType)
	_, _ = fmt.Fprintf(w, "Current Type:   %s\n", rec.CurrentResourceType)
	_, _ = fmt.Fprintf(w, "Recommended:    %s\n", rec.RecommendedResourceType)
	_, _ = fmt.Fprintf(w, "Effort:         %s\n", rec.ImplementationEffort)
	_, _ = fmt.Fprintf(w, "Restart:        %v\n", rec.RestartNeeded)
	_, _ = fmt.Fprintf(w, "Rollback:       %s\n", rec.RollbackPossible)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Reason:\n  %s\n", rec.Reason)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Savings:        %s (%.1f%%)\n", formatSavings(rec.Savings), rec.SavingsPercentage)
	_, _ = fmt.Fprintf(w, "Current Cost:   %s\n", formatSavings(rec.Cost))
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "Created:        %s\n", rec.CreatedAt)
	_, _ = fmt.Fprintf(w, "Updated:        %s\n", rec.UpdatedAt)

	if len(rec.CurrentResourceSummary) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Current Resources:")
		for _, r := range rec.CurrentResourceSummary {
			_, _ = fmt.Fprintf(w, "  - %s (%s)", r.ResourceType, r.Type)
			if r.ResourceName != "" {
				_, _ = fmt.Fprintf(w, " %s", r.ResourceName)
			}
			_, _ = fmt.Fprintln(w)
		}
	}

	if len(rec.RecommendedResourceSummary) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, "Recommended Resources:")
		for _, r := range rec.RecommendedResourceSummary {
			_, _ = fmt.Fprintf(w, "  - %s (%s)", r.ResourceType, r.Type)
			if r.ResourceName != "" {
				_, _ = fmt.Fprintf(w, " %s", r.ResourceName)
			}
			_, _ = fmt.Fprintln(w)
		}
	}

	return nil
}

func formatSavings(s api.SavingsInfo) string {
	if s.Amount == 0 && s.Currency == "" {
		return "—"
	}
	currency := strings.ToUpper(s.Currency)
	if currency == "" {
		currency = "USD"
	}
	cycle := s.Cycle
	if cycle == "" {
		cycle = "monthly"
	}
	return fmt.Sprintf("%.2f %s/%s", s.Amount, currency, cycle)
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
