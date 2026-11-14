package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "users", Short: "Get users and explicitly reconcile platform-wide superuser access", GroupID: groupCore,
		Long: `Read curated user metadata or ensure one exact email is a platform-wide
superuser associated with an explicitly selected company. Requires an ACTIVE
superuser actor. Never changes passwords, activates accounts or resends invitations.`,
	}
	addResourceFlags(cmd, false)
	cmd.AddCommand(newUsersGetCmd(), newUsersEnsureSuperuserCmd())
	return cmd
}

func usersArgs(cmd *cobra.Command, args []string) error {
	if err := exactArgs(1)(cmd, args); err != nil {
		return err
	}
	if err := api.ValidateUserEmail(args[0]); err != nil {
		return usageError(err)
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout < time.Second || timeout > 2*time.Minute {
		return usageError(errors.New("--timeout must be between 1s and 2m for users commands"))
	}
	_, err := printer(cmd)
	if err != nil {
		return usageError(err)
	}
	return nil
}

func newUsersGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get EMAIL", Aliases: getAliases, Short: "Get one exact email with credential-free fields", Args: usersArgs,
		Example: "  jetscale users get agent@jetscale.ai -o json",
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			user, err := run.client.GetUser(run.ctx, args[0])
			if err != nil {
				return apiExit(err)
			}
			return run.record(user.Project())
		},
	}
}

func newUsersEnsureSuperuserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "ensure-superuser EMAIL", Short: "Explicitly grant platform-wide superuser privilege for one company-linked user",
		Long: `Ensure one exact email is a platform-wide superuser with the selected company.
The verb is explicit elevation intent, not a company-scoped administrator role.

Requires --company on this invocation; environment/config defaults are ignored.
A signup-complete ACTIVE superuser is noop. A matching ordinary ACTIVE user
is promoted with only is_superuser:true. Unproved signup completion blocks both.
Missing users receive one company-linked SIGNUP_SUPERUSER invitation; the result
is pending, not completed access. Existing invitations are never resent.

--dry-run reads without writing; it does not prove invitation license, domain
or seat eligibility. Promotion is quota-free; company invitations are not.
Use --yes for noninteractive writes. Serialize all writers for this identity.`,
		Example: `  jetscale users ensure-superuser agent@jetscale.ai --company unstable-jetscale --dry-run -o json
  jetscale users ensure-superuser agent@jetscale.ai --company "Unstable Jetscale" --yes -o json`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := usersArgs(cmd, args); err != nil {
				return err
			}
			selector, _ := cmd.Flags().GetString("company")
			if !cmd.Flags().Changed("company") || selector == "" || strings.TrimSpace(selector) != selector {
				return usageError(errors.New("--company requires an explicit exact ID, slug or name without surrounding whitespace"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			run, err := startResource(cmd)
			if err != nil {
				return err
			}
			defer run.cancel()
			selector, _ := cmd.Flags().GetString("company")
			company, err := run.client.ResolveCompany(run.ctx, selector)
			if err != nil {
				return apiExit(err)
			}
			id, err := uuid.Parse(company.ID())
			if err != nil || id == uuid.Nil {
				return apiExit(api.ErrInvalidResponse)
			}
			if selector != id.String() && selector != company.String("slug") && selector != company.String("name") {
				return apiExit(&api.ConflictError{Resource: "company", Reason: "selector_mismatch"})
			}
			res, err := run.client.EnsureSuperuser(run.ctx, args[0], id.String(), true)
			if err != nil {
				return apiExit(err)
			}
			if flagBool(cmd, "dry-run") || res.Status != api.StatusPlanned {
				return usersResult(run, res)
			}
			approved, err := confirm(cmd, fmt.Sprintf("Grant platform-wide superuser privilege to %s, associated with company %s? A missing user receives an invitation email", args[0], id.String()))
			if err != nil {
				return err
			}
			if !approved {
				return &ExitError{Code: ExitForbidden, Reason: "confirmation_required", Err: errors.New("platform-wide superuser grant requires --yes or interactive confirmation; nothing changed")}
			}
			// Re-read after confirmation rather than treating the earlier plan as
			// a lock. Backend still requires coordinated, serialized writers.
			res, err = run.client.EnsureSuperuser(run.ctx, args[0], id.String(), false)
			if err != nil {
				return apiExit(err)
			}
			return usersResult(run, res)
		},
	}
	cmd.Flags().String("company", "", "explicit company UUID, slug or exact name (no inherited default)")
	cmd.Flags().Bool("dry-run", false, "read and project the intended state without writing or sending email")
	addYesFlag(cmd, "grant platform-wide superuser privilege or send one invitation")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "yes")
	return cmd
}

func usersResult(run *resourceRun, res api.Result) error {
	if res.Status == api.StatusPending {
		_, _ = fmt.Fprintln(run.cmd.ErrOrStderr(), "Pending: signup is required; email delivery and completed access are not proved. No invitation was resent.")
	}
	if run.quiet || run.out.Format != output.Table {
		return run.result(res, "")
	}
	if _, err := fmt.Fprintf(run.out.Out, "%s user (platform-wide superuser intent)\n", res.Status); err != nil {
		return err
	}
	return run.record(res.Details)
}
