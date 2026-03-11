package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with JetScale",
	}

	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthWhoamiCmd())
	cmd.AddCommand(newAuthLogoutCmd())
	cmd.AddCommand(newAuthStatusCmd())

	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var flagToken string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to a JetScale instance",
		Long: `Authenticate with a JetScale instance.

You can sign in interactively (email + password prompt), or supply a token
directly for CI/headless use.

Examples:
  jetscale auth login                          # interactive, targets production
  jetscale --local auth login                  # interactive, targets local backend
  jetscale auth login --token eyJ...           # pre-authenticated token
  JETSCALE_TOKEN=eyJ... jetscale auth whoami   # env var (no login needed)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			if flagToken != "" {
				client := auth.NewClient(apiURL)
				who, err := client.Whoami(flagToken)
				if err != nil {
					return fmt.Errorf("token verification failed: %w", err)
				}
				entry := auth.TokenEntry{
					AccessToken: flagToken,
					ExpiresAt:   time.Now().Add(24 * time.Hour),
				}
				if err := auth.SetToken(instanceName, entry); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s (%s)\n", who.Email, instanceName)
				return nil
			}

			reader := bufio.NewReader(os.Stdin)

			fmt.Fprint(cmd.OutOrStderr(), "Email: ")
			email, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read email: %w", err)
			}
			email = strings.TrimSpace(email)

			fmt.Fprint(cmd.OutOrStderr(), "Password: ")
			passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(cmd.OutOrStderr())
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			password := string(passwordBytes)

			client := auth.NewClient(apiURL)
			tokens, err := client.SignIn(email, password)
			if err != nil {
				return err
			}

			entry := auth.TokenEntryFromPayload(tokens)
			if err := auth.SetToken(instanceName, entry); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s (%s)\n", instanceName, apiURL)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagToken, "token", "", "authenticate with a pre-existing bearer token")

	return cmd
}

type whoamiRow struct {
	Email       string `json:"email"        yaml:"email"`
	Username    string `json:"username"     yaml:"username"`
	Instance    string `json:"instance"     yaml:"instance"`
	APIURL      string `json:"api_url"      yaml:"api_url"`
	TokenExpiry string `json:"token_expiry" yaml:"token_expiry"`
	Refreshable bool   `json:"refreshable"  yaml:"refreshable"`
}

var whoamiColumns = []output.Column{
	{Header: "Email", Field: func(r interface{}) string { return r.(whoamiRow).Email }},
	{Header: "Username", Field: func(r interface{}) string { return r.(whoamiRow).Username }},
	{Header: "Instance", Field: func(r interface{}) string { return r.(whoamiRow).Instance }},
	{Header: "API URL", Field: func(r interface{}) string { return r.(whoamiRow).APIURL }},
	{Header: "Token Expiry", Field: func(r interface{}) string { return r.(whoamiRow).TokenExpiry }},
	{Header: "Refreshable", Field: func(r interface{}) string {
		if r.(whoamiRow).Refreshable {
			return "yes"
		}
		return "no"
	}},
}

func newAuthWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the currently authenticated user",
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

			client := auth.NewClient(apiURL)
			who, err := client.Whoami(token)
			if err != nil {
				return err
			}

			expiry := "unknown"
			refreshable := false
			entry, ok, _ := auth.GetToken(instanceName)
			if ok && !entry.ExpiresAt.IsZero() {
				remaining := time.Until(entry.ExpiresAt).Round(time.Second)
				if remaining > 0 {
					expiry = fmt.Sprintf("in %s", remaining)
				} else {
					expiry = "expired"
				}
				refreshable = entry.RefreshToken != ""
			}

			row := whoamiRow{
				Email:       who.Email,
				Username:    who.Username,
				Instance:    instanceName,
				APIURL:      apiURL,
				TokenExpiry: expiry,
				Refreshable: refreshable,
			}

			p, err := printer(cmd)
			if err != nil {
				return err
			}

			if p.Format == output.Table {
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Logged in as %s\n", row.Email)
				if row.Username != "" && row.Username != row.Email {
					fmt.Fprintf(out, "Username:    %s\n", row.Username)
				}
				fmt.Fprintf(out, "Instance:    %s (%s)\n", row.Instance, row.APIURL)
				refreshStr := ""
				if refreshable {
					refreshStr = " (refreshable)"
				}
				fmt.Fprintf(out, "Token:       expires %s%s\n", row.TokenExpiry, refreshStr)
				return nil
			}

			return p.Print(row, whoamiColumns)
		},
	}
}

type statusRow struct {
	Authenticated bool   `json:"authenticated" yaml:"authenticated"`
	Email         string `json:"email"         yaml:"email"`
	Instance      string `json:"instance"      yaml:"instance"`
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check authentication status (non-interactive)",
		Long: `Check if you're authenticated to the current instance.
Exits 0 if authenticated, 1 if not. Useful in scripts.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			token, err := auth.EnsureFreshToken(instanceName, apiURL)
			if err != nil || token == "" {
				p, pErr := printer(cmd)
				if pErr == nil && p.Format != output.Table {
					_ = p.Print(statusRow{Authenticated: false, Instance: instanceName}, nil)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Not authenticated to %s\n", instanceName)
				}
				os.Exit(1)
			}

			client := auth.NewClient(apiURL)
			who, err := client.Whoami(token)
			if err != nil {
				p, pErr := printer(cmd)
				if pErr == nil && p.Format != output.Table {
					_ = p.Print(statusRow{Authenticated: false, Instance: instanceName}, nil)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "Token invalid for %s\n", instanceName)
				}
				os.Exit(1)
			}

			row := statusRow{Authenticated: true, Email: who.Email, Instance: instanceName}

			p, pErr := printer(cmd)
			if pErr == nil && p.Format != output.Table {
				return p.Print(row, nil)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Authenticated as %s on %s\n", who.Email, instanceName)
			return nil
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out and remove stored credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instanceName, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}

			token, _ := auth.ResolveToken(instanceName)
			if token != "" {
				client := auth.NewClient(apiURL)
				_ = client.SignOut(token)
			}

			if err := auth.DeleteToken(instanceName); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Logged out of %s\n", instanceName)
			return nil
		},
	}
}
