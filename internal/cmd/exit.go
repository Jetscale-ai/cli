package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/spf13/cobra"
)

// Exit codes are part of the CLI contract: scripts branch on them, not on text.
const (
	ExitOK        = 0
	ExitFailure   = 1 // backend or transport failure, or an uncertain write
	ExitUsage     = 2 // invalid arguments or flags, including unknown flags
	ExitNotFound  = 3
	ExitConflict  = 4 // conflicting or ambiguous state that needs a decision
	ExitForbidden = 5 // unauthenticated, forbidden, refused or confirmation required
	ExitTimeout   = 6
	ExitDrift     = 7 // the resource exists but differs from the request
)

// ExitError carries a stable reason token and the exit code for a failure.
// Hint is a corrected invocation for the "Try:" line. Transient is true only
// when repeating the same command is safe. Printed records that a result
// document already went to stdout, so the JSON error object is not added.
type ExitError struct {
	Code      int
	Reason    string
	Err       error
	Hint      string
	Transient bool
	Printed   bool
	Fields    map[string]any
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Reason
}

func (e *ExitError) Unwrap() error { return e.Err }

func usageError(err error) error {
	return &ExitError{Code: ExitUsage, Reason: "usage", Err: err}
}

func usageHint(err error, hint string) error {
	return &ExitError{Code: ExitUsage, Reason: "usage", Err: err, Hint: hint}
}

// Main runs the CLI and returns the process exit code.
func Main() int {
	root := newRootCmd()
	cmd, err := root.ExecuteC()
	return finish(root, cmd, err, os.Stdout, os.Stderr)
}

func finish(root, cmd *cobra.Command, err error, stdout, stderr io.Writer) int {
	if err == nil {
		return ExitOK
	}
	var exit *ExitError
	if !errors.As(err, &exit) {
		if isCobraUsage(err) {
			exit = &ExitError{Code: ExitUsage, Reason: "usage", Err: err}
		} else {
			_, _ = fmt.Fprintln(stderr, "Error: "+err.Error())
			return ExitFailure
		}
	}
	hint := exit.Hint
	if hint == "" {
		hint = defaultHint(root, cmd, exit)
	}
	if strings.EqualFold(flagOutput, "json") && !exit.Printed {
		body := map[string]any{"code": exit.Code, "reason": exit.Reason, "message": exit.Error(), "transient": exit.Transient}
		if hint != "" {
			body["try"] = hint
		}
		for key, value := range exit.Fields {
			body[key] = value
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"error": body})
	}
	_, _ = fmt.Fprintln(stderr, "Error: "+exit.Error())
	if hint != "" {
		_, _ = fmt.Fprintln(stderr, "Try: "+hint)
	}
	return exit.Code
}

// listCommands names the command that lists each resource kind.
var listCommands = map[string]string{
	"company":        "companies list",
	"business unit":  "business-units list --company <company>",
	"cloud account":  "accounts list --company <company>",
	"cost source":    "cost-sources list --company <company>",
	"pricing plan":   "plans list",
	"license":        "licenses list --company <company>",
	"recommendation": "recommendations list --company <company>",
}

func defaultHint(root, cmd *cobra.Command, exit *ExitError) string {
	name := "jetscale"
	if root != nil {
		name = root.Name()
	}
	path := name
	if cmd != nil {
		path = cmd.CommandPath()
	}
	var notFound *api.NotFoundError
	switch {
	case exit.Reason == "usage":
		return path + " --help"
	case exit.Reason == "unauthenticated":
		return name + " auth login"
	case errors.As(exit, &notFound):
		if list, ok := listCommands[notFound.Resource]; ok {
			return name + " " + list
		}
	case exit.Reason == "multiple_matches":
		return "select by ID instead of name"
	case exit.Reason == "confirmation_required":
		return "rerun with --yes, or with --dry-run to preview"
	case exit.Reason == "outcome_uncertain":
		return "read the current state first (get, or the same command with --dry-run); do not resend"
	case exit.Reason == "drift":
		return "inspect with get; nothing was changed"
	}
	return ""
}

func isCobraUsage(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "accepts ", "requires at least", "requires at most", "invalid argument", "flag needs an argument", "required flag", "if any flags in the group"} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

// exactArgs reports argument-count errors as usage errors.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}

func minimumArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.MinimumNArgs(n)(cmd, args); err != nil {
			return usageError(err)
		}
		return nil
	}
}

// apiExit maps API-layer failures onto the exit contract. Backend free text is
// never echoed; only its stable error name and required permission.
func apiExit(err error) error {
	if err == nil {
		return nil
	}
	var exit *ExitError
	if errors.As(err, &exit) {
		return err
	}
	var notFound *api.NotFoundError
	var ambiguous *api.AmbiguousError
	var conflict *api.ConflictError
	var uncertain *api.UncertainError
	var backend *api.APIError
	switch {
	case errors.As(err, &notFound):
		return &ExitError{Code: ExitNotFound, Reason: "not_found", Err: err}
	case errors.As(err, &ambiguous):
		return &ExitError{Code: ExitConflict, Reason: "multiple_matches", Err: err}
	case errors.As(err, &conflict):
		fields := map[string]any{}
		if conflict.ID != "" {
			fields["id"] = conflict.ID
		}
		return &ExitError{Code: ExitConflict, Reason: conflict.Reason, Err: err, Fields: fields}
	case errors.As(err, &uncertain), errors.Is(err, api.ErrUncertain):
		return &ExitError{Code: ExitFailure, Reason: "outcome_uncertain", Err: err}
	case errors.As(err, &backend):
		fields := map[string]any{"status": backend.Status}
		if backend.Name != "" {
			fields["api_error_name"] = backend.Name
		}
		if backend.RequiredPermission != "" {
			fields["required_permission"] = backend.RequiredPermission
		}
		code, reason := ExitFailure, "backend_rejected"
		switch {
		case backend.Status == 401:
			code, reason = ExitForbidden, "unauthenticated"
		case backend.Status == 403:
			code, reason = ExitForbidden, "forbidden"
		case backend.Status == 404:
			code, reason = ExitNotFound, "not_found"
		case backend.Status == 409:
			code, reason = ExitConflict, "conflict"
		case backend.Status >= 500:
			reason = "backend_error"
		}
		// Writes turn 502/504 into outcome_uncertain, so these are safe to repeat.
		transient := backend.Status == 429 || backend.Status == 502 || backend.Status == 503 || backend.Status == 504
		return &ExitError{Code: code, Reason: reason, Err: err, Fields: fields, Transient: transient}
	case errors.Is(err, context.DeadlineExceeded):
		return &ExitError{Code: ExitTimeout, Reason: "timeout", Err: err, Transient: true}
	case errors.Is(err, api.ErrTransport):
		return &ExitError{Code: ExitFailure, Reason: "transport", Err: err, Transient: true}
	case errors.Is(err, api.ErrInvalidResponse):
		return &ExitError{Code: ExitFailure, Reason: "invalid_response", Err: err}
	}
	return &ExitError{Code: ExitFailure, Reason: "error", Err: err}
}
