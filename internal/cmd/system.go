package cmd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Jetscale-ai/cli/internal/api/generated"
	"github.com/Jetscale-ai/cli/internal/auth"
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
				return fmt.Errorf("connect to %s: %w", apiURL, err)
			}
			if resp.StatusCode() >= 400 {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode(), string(resp.Body))
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

			token, err := auth.EnsureFreshToken(instanceName, apiURL)
			if err != nil {
				return err
			}
			if token == "" {
				return fmt.Errorf("not logged in to %s — run: jetscale auth login", instanceName)
			}

			client, err := generated.NewClientWithResponses(apiURL,
				generated.WithRequestEditorFn(bearerAuthEditor(token)),
			)
			if err != nil {
				return err
			}

			resp, err := client.DiagnosticsApiV2SystemDiagnosticsGetWithResponse(context.Background())
			if err != nil {
				return fmt.Errorf("connect to %s: %w", apiURL, err)
			}
			if resp.StatusCode() >= 400 {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode(), string(resp.Body))
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			return p.PrintRaw(resp.Body)
		},
	}
}

func bearerAuthEditor(token string) generated.RequestEditorFn {
	return func(_ context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}
