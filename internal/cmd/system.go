package cmd

import (
	"context"
	"fmt"

	"github.com/Jetscale-ai/cli/internal/api/generated"

	"github.com/spf13/cobra"
)

func newSystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "Backend system information and diagnostics",
	}

	cmd.AddCommand(newSystemInfoCmd())
	cmd.AddCommand(newSystemDiagnosticsCmd())

	return cmd
}

func newSystemInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show backend version and environment",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			client, err := generated.NewClientWithResponses(apiURL)
			if err != nil {
				return err
			}

			resp, err := client.InfoApiV2SystemInfoGetWithResponse(context.Background())
			if err != nil {
				return fmt.Errorf("system info request failed")
			}
			if resp.StatusCode() >= 400 {
				return fmt.Errorf("HTTP %d", resp.StatusCode())
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			return p.PrintRaw(resp.Body)
		},
	}
}

func newSystemDiagnosticsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diagnostics",
		Short: "Show backend diagnostics (requires auth)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			session, err := commandSession(cmd, instanceName, apiURL)
			if err != nil {
				return err
			}

			client, err := generated.NewClientWithResponses(apiURL,
				generated.WithHTTPClient(session.HTTPClient),
			)
			if err != nil {
				return err
			}

			resp, err := client.DiagnosticsApiV2SystemDiagnosticsGetWithResponse(context.Background(), nil)
			if err != nil {
				return fmt.Errorf("diagnostics request failed")
			}
			if resp.StatusCode() >= 400 {
				return fmt.Errorf("HTTP %d", resp.StatusCode())
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			return p.PrintRaw(resp.Body)
		},
	}
}
