package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Resource commands (companies, business-units, plans, licenses, accounts,
// recommendations, cost-sources, feature-flags) share one contract: noun-verb,
// -o json with curated fields or -q for IDs, idempotent writes that report
// created/noop/drift, --dry-run on every write, prompts only on a terminal
// with -y/--yes to skip them, and the exit codes in exit.go.

const maxResourceTimeout = 30 * time.Minute

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// addResourceFlags adds the shared flags to a resource command group. Groups
// scoped to a company also get --company, defaulting to "config set company".
func addResourceFlags(group *cobra.Command, withCompany bool) {
	group.PersistentFlags().Bool("require-superuser", false, "refuse to run (exit 5) unless the signed-in user is an active superuser")
	group.PersistentFlags().Duration("timeout", 2*time.Minute, "overall deadline for the command (max 30m)")
	group.PersistentFlags().BoolP("quiet", "q", false, "print only IDs, one per line")
	if withCompany {
		group.PersistentFlags().String("company", "", "company ID, slug or exact name (default: config set company)")
	}
}

type resourceRun struct {
	ctx      context.Context
	cancel   context.CancelFunc
	client   *api.Client
	out      output.Printer
	cmd      *cobra.Command
	instance string
	quiet    bool
}

// startResource authenticates once for the command and returns a bounded client.
func startResource(cmd *cobra.Command) (*resourceRun, error) {
	p, err := printer(cmd)
	if err != nil {
		return nil, usageError(err)
	}
	timeout, _ := cmd.Flags().GetDuration("timeout")
	if timeout <= 0 || timeout > maxResourceTimeout {
		return nil, usageError(fmt.Errorf("--timeout must be between 1s and %s", maxResourceTimeout))
	}
	instance, apiURL, err := resolveAPI()
	if err != nil {
		return nil, usageError(err)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	session, err := commandSession(cmd, instance, apiURL)
	if err != nil {
		cancel()
		return nil, &ExitError{Code: ExitForbidden, Reason: "unauthenticated", Err: err}
	}
	client, err := api.NewSessionClient(apiURL, session.HTTPClient).Resources()
	if err != nil {
		cancel()
		return nil, err
	}
	if require, _ := cmd.Flags().GetBool("require-superuser"); require {
		if err := requireSuperuser(ctx, client); err != nil {
			cancel()
			return nil, err
		}
	}
	return &resourceRun{ctx: ctx, cancel: cancel, client: client, out: p, cmd: cmd, instance: instance, quiet: flagBool(cmd, "quiet")}, nil
}

// requireSuperuser needs a nonzero actor ID and an explicit is_superuser: true.
// A role label or a successful status is not proof of complete visibility.
func requireSuperuser(ctx context.Context, client *api.Client) error {
	actor, err := client.Actor(ctx)
	if err != nil {
		return apiExit(err)
	}
	superuser, ok := actor.Bool("is_superuser")
	if _, valid := resourceID(actor.ID()); !valid || !ok || !superuser {
		return &ExitError{Code: ExitForbidden, Reason: "visibility_unproved", Err: errors.New("the signed-in user is not a verified active superuser")}
	}
	return nil
}

func resourceID(s string) (string, bool) {
	return s, len(s) == 36 && strings.Count(s, "-") == 4 && s != "00000000-0000-0000-0000-000000000000"
}

func column(header, key string) output.Column {
	return output.Column{Header: header, Field: func(row interface{}) string {
		value := row.(api.Record)[key]
		switch v := value.(type) {
		case nil:
			return ""
		case []any:
			parts := make([]string, 0, len(v))
			for _, item := range v {
				parts = append(parts, fmt.Sprint(item))
			}
			return strings.Join(parts, ",")
		default:
			return fmt.Sprint(v)
		}
	}}
}

func recordID(r api.Record) string {
	if id := r.ID(); id != "" {
		return id
	}
	return r.String("cloud_account_id")
}

func (r *resourceRun) ids(records []api.Record) error {
	for _, record := range records {
		if id := recordID(record); id != "" {
			if _, err := fmt.Fprintln(r.out.Out, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// list prints curated projections; an empty collection is explicit.
func (r *resourceRun) list(records []api.Record, fields []string, columns []output.Column, noun string) error {
	rows := make([]api.Record, 0, len(records))
	for _, record := range records {
		rows = append(rows, record.Project(fields...))
	}
	if r.quiet {
		return r.ids(rows)
	}
	if r.out.Format == output.Table && len(rows) == 0 {
		_, err := fmt.Fprintf(r.out.Out, "No %s found.\n", noun)
		return err
	}
	items := make([]interface{}, len(rows))
	for i, row := range rows {
		items[i] = row
	}
	return r.out.Print(items, columns)
}

func (r *resourceRun) record(record api.Record) error {
	if r.quiet {
		return r.ids([]api.Record{record})
	}
	if r.out.Format != output.Table {
		return r.out.Print(record, nil)
	}
	keys := make([]string, 0, len(record))
	for key := range record {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := fmt.Fprintf(r.out.Out, "%-28s %v\n", key+":", record[key]); err != nil {
			return err
		}
	}
	return nil
}

// result prints one idempotent write outcome. Drift exits 7 after printing.
func (r *resourceRun) result(res api.Result, hint string) error {
	switch {
	case r.quiet:
		if res.ID != "" {
			_, _ = fmt.Fprintln(r.out.Out, res.ID)
		}
	case r.out.Format != output.Table:
		if err := r.out.Print(res, nil); err != nil {
			return err
		}
	default:
		noun := strings.ReplaceAll(res.Resource, "_", " ")
		line := map[string]string{
			api.StatusCreated: "Created " + noun,
			api.StatusUpdated: "Updated " + noun,
			api.StatusNoop:    noun + " already exists (unchanged)",
			api.StatusPlanned: "Would create " + noun + " (dry run)",
			api.StatusDrift:   noun + " exists but differs",
		}[res.Status]
		if line == "" {
			line = noun + " " + res.Status
		}
		if res.ID != "" {
			line += " " + res.ID
		}
		_, _ = fmt.Fprintln(r.out.Out, line)
		if len(res.Drift) > 0 {
			_, _ = fmt.Fprintf(r.out.Out, "differs from the request: %s (not changed)\n", strings.Join(res.Drift, ", "))
		}
		if hint != "" && res.Status != api.StatusDrift {
			_, _ = fmt.Fprintln(r.cmd.ErrOrStderr(), "Next: "+hint)
		}
	}
	return driftExit(res)
}

func driftExit(results ...api.Result) error {
	for _, res := range results {
		if res.Status == api.StatusDrift {
			return &ExitError{Code: ExitDrift, Reason: "drift", Printed: true, Err: fmt.Errorf("%s %s differs from the request: %s", res.Resource, res.ID, strings.Join(res.Drift, ", "))}
		}
	}
	return nil
}

// printedFailure keeps partial results on stdout and still exits by contract.
func printedFailure(err error) error {
	mapped := apiExit(err)
	var exit *ExitError
	if errors.As(mapped, &exit) {
		exit.Printed = true
	}
	return mapped
}

func requireFlag(cmd *cobra.Command, names ...string) error {
	for _, name := range names {
		value, _ := cmd.Flags().GetString(name)
		if strings.TrimSpace(value) == "" {
			return usageHint(fmt.Errorf("--%s is required", name), cmd.CommandPath()+" --"+name+" <value> ... (see --help)")
		}
	}
	return nil
}

func flagString(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return strings.TrimSpace(value)
}

func flagStrings(cmd *cobra.Command, name string) []string {
	values, _ := cmd.Flags().GetStringSlice(name)
	out := []string{}
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func flagBool(cmd *cobra.Command, name string) bool {
	value, _ := cmd.Flags().GetBool(name)
	return value
}

// companySelector is --company, then the channel COMPANY variable, then the
// instance default from "config set company".
func (r *resourceRun) companySelector() string {
	cfg, err := config.Load()
	if err != nil {
		return flagString(r.cmd, "company")
	}
	return config.ResolveCompany(cfg, r.instance, flagString(r.cmd, "company"))
}

// company resolves the company that scopes most resource commands.
func (r *resourceRun) company() (api.Record, error) {
	selector := r.companySelector()
	if selector == "" {
		return nil, usageHint(errors.New("no company selected"), r.cmd.CommandPath()+" --company <company>   (or set a default: config set company <slug>)")
	}
	company, err := r.client.ResolveCompany(r.ctx, selector)
	if err != nil {
		return nil, apiExit(err)
	}
	return company, nil
}

// explicitAccount is the global --account only when given on this command
// line; a remembered active account never widens or narrows a write.
func explicitAccount(cmd *cobra.Command) string {
	if flag := cmd.Flag("account"); flag != nil && flag.Changed {
		return strings.TrimSpace(flag.Value.String())
	}
	return ""
}

func addYesFlag(cmd *cobra.Command, what string) {
	cmd.Flags().BoolP("yes", "y", false, "skip the confirmation prompt ("+what+")")
}

// stdinIsTerminal is replaceable in tests.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// canPrompt is true only on an interactive terminal outside CI, and never
// when stdin carries the password.
func canPrompt(cmd *cobra.Command) bool {
	return !flagPasswordStdin && os.Getenv("CI") == "" && stdinIsTerminal(cmd.InOrStdin())
}

// confirm returns true with --yes, asks on an interactive terminal, and
// otherwise returns false so the caller refuses instead of hanging.
func confirm(cmd *cobra.Command, question string) (bool, error) {
	if flagBool(cmd, "yes") {
		return true, nil
	}
	if !canPrompt(cmd) {
		return false, nil
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N]: ", question)
	line, err := bufio.NewReader(io.LimitReader(cmd.InOrStdin(), 64)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "y" || answer == "yes" {
		return true, nil
	}
	return false, &ExitError{Code: ExitForbidden, Reason: "confirmation_declined", Err: errors.New("not confirmed; nothing was changed")}
}

func confirmationRequired(err error) error {
	return &ExitError{Code: ExitForbidden, Reason: "confirmation_required", Printed: true, Err: err}
}
