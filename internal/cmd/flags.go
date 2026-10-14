package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

const flagsHelp = `Inspect or enable supported flags for one existing company and optional business unit/accounts.

Root gates are application-wide, not tenant-local. Company cascades must be
explicit in the profile as company_cascade: "all_descendants" and affect all
company descendants, not only the selected business unit/accounts.

There is no atomic guarantee across flag writes. No company, business unit,
or account is provisioned. Only enabling AGENTS.EXPLORER, CLOUD.FOCUS,
CLOUD.AZURE.FOCUS, and INTEGRATIONS.AABHRA is supported; missing supported
flag rows may be created.

show and plan read API metadata only; authentication has separate session effects.
apply explicitly requests writes, without prompts or retries. A command or profile
never grants runtime authority or qualifies credential handling. Target and tenant
restrictions required by an execution contract must be enforced by its caller.`

const flagsProfileMaxBytes = 1 << 20

func newFlagsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "flags", Short: "Inspect, plan, or explicitly enable bounded feature flags", Long: flagsHelp}
	for _, mode := range []string{"show", "plan", "apply"} {
		cmd.AddCommand(newFlagsModeCmd(mode))
	}
	return cmd
}

func newFlagsModeCmd(mode string) *cobra.Command {
	var profilePath string
	var validateOnly bool
	var profile api.FlagsProfile
	var instance, target string
	cmd := &cobra.Command{
		Use: mode, Short: mode + " a bounded flags profile", Long: flagsHelp,
		Args: cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if validateOnly {
				if mode == "apply" {
					return errors.New("flags: --validate-only is supported only on show and plan, not apply")
				}
				for _, name := range []string{"login", "password-stdin", "credentials-file", "ca-file"} {
					if flag := cmd.Flag(name); flag != nil && flag.Changed {
						return errors.New("flags: --validate-only cannot be combined with authentication flags")
					}
				}
			}
			if cmd.Flag("account").Changed {
				return errors.New("flags: --account is not supported; select cloud account IDs in --profile")
			}
			if profilePath == "" {
				return errors.New("flags: --profile is required")
			}
			if _, err := printer(cmd); err != nil {
				return errors.New("flags: output must be table, json, or yaml")
			}
			var err error
			profile, err = readFlagsProfile(profilePath)
			if err != nil {
				return err
			}
			instance, target, err = resolveFlagsTarget()
			if err != nil {
				return err
			}
			if !validateOnly {
				// PreRunE precedes eager stdin auth; cleanup wraps the session lifecycle.
				ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
				ctx = context.WithValue(ctx, basicsCancelKey{}, cancel)
				cmd.SetContext(context.WithValue(ctx, sessionTargetKey{}, sessionTarget{instance: instance, origin: target}))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := printer(cmd)
			if err != nil {
				return errors.New("flags: output must be table, json, or yaml")
			}
			if validateOnly {
				if err := printFlagsValidation(p, mode, target, profile); err != nil {
					return errors.New("flags: cannot write validation output")
				}
				return nil
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			session, err := commandSession(cmd, instance, target)
			if err != nil {
				return err // Session acquisition already returns secret-safe errors.
			}
			client := api.NewSessionClient(target, session.HTTPClient)
			var result api.FlagsResult
			switch mode {
			case "show":
				result, err = client.ShowFlags(cmd.Context(), profile)
			case "plan":
				result, err = client.PlanFlags(cmd.Context(), profile)
			case "apply":
				result, err = client.ApplyFlags(cmd.Context(), profile)
			}
			return printFlagsResult(p, mode, result, errors.Join(err, cmd.Context().Err()))
		},
	}
	cmd.Flags().StringVar(&profilePath, "profile", "", "strict JSON profile file (regular, no symlinks, maximum 1 MiB)")
	// Keep this on apply too so the unsafe combination fails explicitly before auth.
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "show/plan only: validate offline without authentication, probing, or writes")
	return cmd
}

func readFlagsProfile(path string) (api.FlagsProfile, error) {
	var profile api.FlagsProfile
	file, err := openFlagsProfile(path)
	if err != nil {
		return profile, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, flagsProfileMaxBytes+1))
	invalid := errors.New("flags: invalid JSON profile; unknown, duplicate, null, and trailing fields/data are not allowed")
	if err != nil || len(data) > flagsProfileMaxBytes {
		return profile, errors.New("flags: cannot read bounded regular JSON profile (maximum 1 MiB)")
	}
	// encoding/json permits duplicate keys and null bools; reject both before decoding.
	check := json.NewDecoder(bytes.NewReader(data))
	if checkFlagsJSON(check, 0) != nil {
		return profile, invalid
	}
	if _, err := check.Token(); err != io.EOF {
		return profile, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profile) != nil {
		return profile, invalid
	}
	if err := api.ValidateFlagsProfile(profile); err != nil {
		return profile, errors.New("flags: invalid profile; use version 1, one company, optional business unit/accounts, and supported enable-only flags")
	}
	return profile, nil
}

// Walk anchored directories rather than accepting symlinks in a parent path.
func openFlagsProfile(path string) (*os.File, error) {
	invalid := errors.New("flags: --profile must be a regular JSON file without symlinks, no larger than 1 MiB")
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, invalid
	}
	volume := filepath.VolumeName(absolute)
	root, err := os.OpenRoot(volume + string(filepath.Separator))
	if err != nil {
		return nil, invalid
	}
	defer func() { _ = root.Close() }()
	parts := strings.Split(strings.TrimPrefix(absolute[len(volume):], string(filepath.Separator)), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, invalid
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return nil, invalid
		}
		opened, statErr := next.Stat(".")
		after, afterErr := root.Lstat(part)
		if statErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, opened) || !os.SameFile(info, after) {
			_ = next.Close()
			return nil, invalid
		}
		_ = root.Close()
		root = next
	}
	name := parts[len(parts)-1]
	info, err := root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > flagsProfileMaxBytes {
		return nil, invalid
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, invalid
	}
	opened, statErr := file.Stat()
	after, afterErr := root.Lstat(name)
	if statErr != nil || afterErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() || !os.SameFile(info, opened) || !os.SameFile(info, after) || opened.Size() > flagsProfileMaxBytes {
		_ = file.Close()
		return nil, invalid
	}
	return file, nil
}

func checkFlagsJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("profile nesting limit exceeded")
	}
	token, err := decoder.Token()
	if err != nil || token == nil {
		return errors.New("invalid profile JSON")
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	if delimiter != '{' && delimiter != '[' {
		return errors.New("invalid profile JSON")
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return errors.New("invalid or duplicate profile field")
			}
			// Metadata keys are lowercase ASCII; reject JSON's case-folded aliases.
			if !strings.Contains(name, ".") && strings.ContainsFunc(name, func(r rune) bool { return r != '_' && (r < 'a' || r > 'z') }) {
				return errors.New("invalid profile field casing")
			}
			seen[name] = true
		}
		if err := checkFlagsJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func resolveFlagsTarget() (string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", errors.New("flags: cannot load target configuration")
	}
	// Do not probe even in authenticated preflight; resolve and pin before auth.
	for name, instance := range cfg.Instances {
		if instance.APIURL == "auto" {
			instance.APIURL = ""
			cfg.Instances[name] = instance
		}
	}
	instance, target, err := config.Resolve(cfg, flagInstance, flagLocal, flagAPIURL)
	if err != nil {
		return "", "", errors.New("flags: cannot resolve target; select an existing --instance or explicit --api-url")
	}
	// Validate without constructing a session, reading credentials/CA files, or networking.
	u, err := url.Parse(target)
	invalid := errors.New("flags: target must be an explicit HTTPS origin (loopback HTTP allowed); use --api-url, not auto, without credentials, path, query, or fragment")
	if err != nil || u.User != nil || u.Opaque != "" || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || strings.Contains(target, "#") || strings.ContainsAny(u.Hostname(), "%\\ \t\r\n") || strings.HasSuffix(u.Host, ":") {
		return "", "", invalid
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "http" && host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
		return "", "", invalid
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", invalid
		}
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return instance, u.Scheme + "://" + host, nil
}

func printFlagsValidation(p output.Printer, mode, target string, profile api.FlagsProfile) error {
	if p.Format != output.Table {
		body, err := json.Marshal(struct {
			Accepted            bool   `json:"accepted"`
			ValidateOnly        bool   `json:"validate_only"`
			Mode                string `json:"mode"`
			Target              string `json:"target"`
			RootApplicationWide bool   `json:"root_application_wide"`
			CompanyCascade      string `json:"company_cascade"`
		}{true, true, mode, target, len(profile.Flags.Root) > 0, profile.CompanyCascade})
		if err != nil {
			return errors.New("flags: cannot encode validation result")
		}
		return p.PrintRaw(body)
	}
	_, err := fmt.Fprintf(p.Out, "Profile accepted (validate-only, %s)\nTarget: %s\nRoot application-wide: %t\nCompany cascade: %s\nOffline validation does not inspect remote readiness or grant runtime authority.\n", mode, target, len(profile.Flags.Root) > 0, flagsCell(profile.CompanyCascade))
	return err
}

func printFlagsResult(p output.Printer, mode string, result api.FlagsResult, requestErr error) error {
	if err := renderFlagsResult(p, result); err != nil {
		return errors.New("flags: cannot write result output")
	}
	if result.OutcomeUncertain {
		return errors.New("flags: outcome uncertain; inspect state with flags show before any further apply; do not retry blindly")
	}
	if requestErr != nil {
		return errors.New("flags: request failed; inspect the sanitized result; apply is not atomic and must not be retried blindly")
	}
	if len(result.Blockers) > 0 {
		return errors.New("flags: blocked; inspect result blockers")
	}
	for _, action := range result.Actions {
		switch action.Status {
		case "noop", "planned", "applied":
			if mode == "apply" && action.Status == "planned" {
				return errors.New("flags: apply incomplete; inspect state before any further apply")
			}
		case "outcome_uncertain":
			return errors.New("flags: outcome uncertain; inspect state before any further apply; do not retry blindly")
		case "blocked":
			return errors.New("flags: blocked; inspect result actions")
		default:
			return errors.New("flags: invalid action status; inspect state before any further apply")
		}
	}
	if mode == "apply" && !result.Satisfied {
		return errors.New("flags: apply unsatisfied; inspect state before any further apply")
	}
	return nil
}

func renderFlagsResult(p output.Printer, result api.FlagsResult) error {
	if p.Format != output.Table {
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		// Preserve the API's JSON names in YAML too, not Go field-name lowercasing.
		return p.PrintRaw(body)
	}
	w := tabwriter.NewWriter(p.Out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintf(w, "Mode: %s\nCompany: %s\nBusiness unit: %s\nSatisfied: %t\nOutcome uncertain: %t\nRoot application-wide: %t\n\n", flagsCell(result.Mode), flagsCell(result.CompanyID), flagsCell(result.BusinessUnitID), result.Satisfied, result.OutcomeUncertain, result.RootApplicationWide); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "STATES\nSCOPE\tENTITY ID\tKEY\tSTORED\tEFFECTIVE\tREASON")
	for _, state := range result.States {
		absent := "unknown"
		if state.Reason == "stored_missing" {
			absent = "missing"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", flagsCell(state.Scope), flagsCell(state.EntityID), flagsCell(state.Key), flagsBool(state.Stored, absent), flagsBool(state.Effective, "unknown"), flagsCell(state.Reason))
	}
	_, _ = fmt.Fprintln(w, "\nACTIONS\nSCOPE\tENTITY ID\tKEY\tOPERATION\tSTATUS\tCASCADE (ALL DESCENDANTS)\tREASON")
	for _, action := range result.Actions {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%t\t%s\n", flagsCell(action.Scope), flagsCell(action.EntityID), flagsCell(action.Key), flagsCell(action.Operation), flagsCell(action.Status), action.Cascade, flagsCell(action.Reason))
	}
	_, _ = fmt.Fprintln(w, "\nBLOCKERS\nSCOPE\tENTITY ID\tKEY\tREASON")
	for _, blocker := range result.Blockers {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", flagsCell(blocker.Scope), flagsCell(blocker.EntityID), flagsCell(blocker.Key), flagsCell(blocker.Reason))
	}
	return w.Flush()
}

func flagsBool(value *bool, absent string) string {
	if value == nil {
		return absent
	}
	return strconv.FormatBool(*value)
}

func flagsCell(value string) string {
	if value == "" {
		return "-"
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r < 32 || r == 127 }) {
		return strconv.QuoteToASCII(value)
	}
	return value
}
