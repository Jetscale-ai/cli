package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Jetscale-ai/cli/internal/auth"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Authenticate with JetScale"}
	cmd.AddCommand(newAuthLoginCmd(), newAuthWhoamiCmd(), newAuthLogoutCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Sign in to a JetScale instance using session cookies",
		Long: `Sign in with an email and hidden password prompt. Verify the session before
saving it in the existing protected credential store. Cookies are bound to the
compiled channel and API origin. A new login may close existing browser sessions.

For local automation, --credentials-file reads JETSCALE_DEFAULT_ADMIN_EMAIL and
JETSCALE_DEFAULT_ADMIN_PASSWORD without shell expansion. This mode verifies login
then signs out without saving; use the same flag on accounts list to run it in a
temporary session. No browser storage is imported.

For any channel, --login supplies an email or username and --password-stdin reads
one password line from a pipe or file. This also verifies a temporary session
and signs out without saving.

Examples:
  jetscale auth login
  jetscale auth whoami
  jetscale-local --credentials-file ../stack/.env auth login`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			instance, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}
			if flagCredentialsFile != "" || flagPasswordStdin {
				if _, err := commandSession(cmd, instance, apiURL); err != nil {
					return err
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Login verified (temporary session).")
				return nil
			}
			if _, set := os.LookupEnv(buildinfo.EnvVar("TOKEN")); set {
				return errors.New("unset the channel TOKEN variable before logging in with cookies")
			}
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return errors.New("interactive login requires a terminal; automation can use --login with --password-stdin")
			}
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Signing in may close existing sessions for this user.")
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Email: ")
			email, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil {
				return errors.New("cannot read login email")
			}
			_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Password: ")
			password, err := term.ReadPassword(int(os.Stdin.Fd()))
			_, _ = fmt.Fprintln(cmd.ErrOrStderr())
			if err != nil {
				return errors.New("cannot read login password")
			}
			session, err := auth.LoginSessionContext(cmd.Context(), apiURL, strings.TrimSpace(email), string(password), flagCAFile)
			clear(password)
			if err != nil {
				return err
			}
			rememberSession(cmd, session, instance, false)
			cmd.Context().Value(sessionContextKey{}).(*commandSessionState).fresh = true
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Login verified for %s.\n", instance)
			return nil
		},
	}
}

type whoamiRow struct {
	Email    string `json:"email,omitempty" yaml:"email,omitempty"`
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Instance string `json:"instance" yaml:"instance"`
	APIURL   string `json:"api_url" yaml:"api_url"`
}

func newAuthWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use: "whoami", Short: "Show the currently authenticated user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instance, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}
			session, err := commandSession(cmd, instance, apiURL)
			if err != nil {
				return err
			}
			who, err := commandIdentity(cmd, session)
			if err != nil {
				return err
			}
			p, err := printer(cmd)
			if err != nil {
				return err
			}
			row := whoamiRow{Email: who.Email, Username: who.Username, Instance: instance, APIURL: apiURL}
			if p.Format == output.Table {
				identity := who.Email
				if identity == "" {
					identity = who.Username
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Logged in as %s\nInstance: %s (%s)\n", identity, instance, apiURL)
				return nil
			}
			return p.Print(row, nil)
		},
	}
}

type statusRow struct {
	Authenticated bool   `json:"authenticated" yaml:"authenticated"`
	Email         string `json:"email,omitempty" yaml:"email,omitempty"`
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	Instance      string `json:"instance" yaml:"instance"`
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Check authentication status (non-interactive)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			instance, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}
			session, authErr := commandSession(cmd, instance, apiURL)
			var who auth.WhoamiResult
			if authErr == nil {
				who, authErr = commandIdentity(cmd, session)
			}
			p, err := printer(cmd)
			if err != nil {
				return err
			}
			row := statusRow{Authenticated: authErr == nil, Email: who.Email, Instance: instance}
			if flagPasswordStdin {
				row.Username = who.Username
			}
			if p.Format != output.Table {
				if err := p.Print(row, nil); err != nil {
					return err
				}
			} else if authErr == nil {
				identity := who.Email
				if identity == "" {
					identity = who.Username
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Authenticated as %s on %s\n", identity, instance)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Not authenticated to %s\n", instance)
			}
			return authErr
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Sign out the current session and clear stored credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flagCredentialsFile != "" {
				return errors.New("--credentials-file already signs out automatically; do not use it with auth logout")
			}
			instance, apiURL, err := resolveAPI()
			if err != nil {
				return err
			}
			session, err := commandSession(cmd, instance, apiURL)
			if err != nil {
				return err
			}
			if err := session.SignOutContext(cmd.Context()); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Signed out of %s.\n", instance)
			return nil
		},
	}
}
