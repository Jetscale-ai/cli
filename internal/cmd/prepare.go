package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Jetscale-ai/cli/internal/api"
	"github.com/Jetscale-ai/cli/internal/buildinfo"
	"github.com/Jetscale-ai/cli/internal/config"
	"github.com/Jetscale-ai/cli/internal/output"
	"github.com/spf13/cobra"
)

const (
	prepareManifestMaxBytes = 1 << 20
	prepareBasicsTimeout    = 3 * time.Minute
)

type basicsCancelKey struct{}

// Wrap outside session cleanup so eager-auth failures also release the timer.
func wrapPrepareDeadlineCleanup(prepare *cobra.Command) {
	for _, cmd := range prepare.Commands() {
		run := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if cancel, ok := cmd.Context().Value(basicsCancelKey{}).(context.CancelFunc); ok {
				defer cancel()
			}
			return run(cmd, args)
		}
	}
}

func newPrepareCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "prepare", Short: "Assess bounded unstable application preparation"}
	cmd.AddCommand(newPrepareBasicsCmd())
	return cmd
}

func newPrepareBasicsCmd() *cobra.Command {
	var manifestPath string
	var apply, validateOnly bool
	var manifest api.BasicsManifest
	var instance, target string

	cmd := &cobra.Command{
		Use:   "basics",
		Short: "Assess Unstable Jetscale basics; write only with --apply",
		Long: `Assess only Unstable Jetscale and Unstable Jetscale Clouds on the unstable origin.

A strict JSON manifest is required. --validate-only checks it offline without
opening authentication or inspecting remote readiness. Assessment is read-only;
--apply explicitly enables the API's bounded, single-attempt preparation writes.
BU provisioning and external managed-agent validation are not supported.`,
		Args: cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if validateOnly && apply {
				return errors.New("--validate-only cannot be combined with --apply")
			}
			if validateOnly {
				for _, name := range []string{"login", "password-stdin", "credentials-file", "ca-file"} {
					if flag := cmd.Flag(name); flag != nil && flag.Changed {
						return errors.New("--validate-only cannot be combined with authentication flags")
					}
				}
			} else if flag := cmd.Flag("credentials-file"); flag != nil && flag.Changed {
				return errors.New("--credentials-file is not supported for unstable preparation")
			}
			if manifestPath == "" {
				return errors.New("--manifest is required")
			}
			if _, err := printer(cmd); err != nil {
				return errors.New("prepare basics: output must be table, json, or yaml")
			}
			var err error
			manifest, err = readBasicsManifest(manifestPath)
			if err != nil {
				return err
			}
			if err := validateUnstableBasicsManifest(manifest); err != nil {
				return err
			}
			if err := api.ValidateBasicsManifest(manifest); err != nil {
				return err
			}
			instance, target, err = resolveBasicsTarget()
			if err != nil {
				return err
			}
			if !validateOnly {
				// PreRunE precedes the session wrapper's eager stdin authentication.
				ctx, stop := context.WithTimeout(cmd.Context(), prepareBasicsTimeout)
				ctx = context.WithValue(ctx, basicsCancelKey{}, stop)
				cmd.SetContext(context.WithValue(ctx, sessionTargetKey{}, sessionTarget{instance: instance, origin: target}))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {

			p, err := printer(cmd)
			if err != nil {
				return err
			}
			if validateOnly {
				return printBasicsJSON(p, struct {
					Accepted     bool   `json:"accepted"`
					ValidateOnly bool   `json:"validate_only"`
					Target       string `json:"target"`
				}{true, true, target})
			}
			if err := cmd.Context().Err(); err != nil {
				return err
			}
			session, err := commandSession(cmd, instance, target)
			if err != nil {
				return err
			}
			client := api.NewSessionClient(target, session.HTTPClient)
			result, requestErr := client.PrepareBasics(cmd.Context(), manifest, apply)
			return printBasicsResult(p, result, errors.Join(requestErr, cmd.Context().Err()))
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "regular JSON manifest file (maximum 1 MiB)")
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "validate the manifest offline; no authentication or writes")
	cmd.Flags().BoolVar(&apply, "apply", false, "explicitly apply the assessed, supported preparation actions")

	return cmd
}

func readBasicsManifest(path string) (api.BasicsManifest, error) {
	var manifest api.BasicsManifest
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > prepareManifestMaxBytes {
		return manifest, errors.New("--manifest must be a regular JSON file no larger than 1 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return manifest, errors.New("cannot open basics manifest")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) || opened.Size() > prepareManifestMaxBytes {
		return manifest, errors.New("basics manifest changed or is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, prepareManifestMaxBytes+1))
	if err != nil || len(data) > prepareManifestMaxBytes || json.Unmarshal(data, &manifest) != nil {
		return manifest, errors.New("basics: invalid JSON manifest (unknown fields and trailing data are not allowed)")
	}
	return manifest, nil
}

func validateUnstableBasicsManifest(m api.BasicsManifest) error {
	if m.Company.Name != "Unstable Jetscale" || m.Company.Slug == nil || *m.Company.Slug != "unstable-jetscale" ||
		(m.Company.IsRoot != nil && *m.Company.IsRoot) || len(m.Company.DomainEmails) != 1 || m.Company.DomainEmails[0] != "jetscale.ai" ||
		m.BusinessUnit == nil || m.BusinessUnit.Name != "Unstable Jetscale Clouds" || m.BusinessUnit.Slug != "unstable-jetscale-clouds" {
		return errors.New("basics manifest must target non-root Unstable Jetscale (unstable-jetscale), domain jetscale.ai, and Unstable Jetscale Clouds (unstable-jetscale-clouds)")
	}
	return nil
}

func resolveBasicsTarget() (string, string, error) {
	if buildinfo.Channel != "unstable" {
		return "", "", errors.New("prepare basics requires a compiled unstable-channel build")
	}
	cfg, err := config.Load()
	if err != nil {
		return "", "", errors.New("prepare basics: cannot load target configuration")
	}
	// Resolve must not probe an auto target, even during offline preflight.
	for name, instance := range cfg.Instances {
		if instance.APIURL == "auto" {
			instance.APIURL = ""
			cfg.Instances[name] = instance
		}
	}
	instance, target, err := config.Resolve(cfg, flagInstance, flagLocal, flagAPIURL)
	if err != nil || target != buildinfo.UnstableURL {
		return "", "", errors.New("prepare basics requires the exact https://unstable.jetscale.ai origin")
	}
	return instance, target, nil
}

func printBasicsJSON(p output.Printer, value any) error {
	// PrintRaw preserves JSON field names in YAML and never marshals hidden fields.
	body, err := json.Marshal(value)
	if err != nil {
		return errors.New("prepare basics: cannot encode sanitized result")
	}
	return p.PrintRaw(body)
}

func printBasicsResult(p output.Printer, result api.BasicsResult, requestErr error) error {
	if err := printBasicsJSON(p, result); err != nil {
		return err
	}
	if result.OutcomeUncertain {
		return errors.New("prepare basics: outcome uncertain; assess state before rerunning")
	}
	if requestErr != nil {
		return errors.New("prepare basics: request failed; inspect the sanitized result")
	}
	for _, action := range result.Actions {
		if action.Status == "blocked" || action.Status == "outcome_uncertain" || action.Status == "error" {
			return errors.New("prepare basics: blocked, uncertain, or failed; inspect the sanitized result")
		}
	}
	if len(result.Blockers) != 0 {
		return fmt.Errorf("prepare basics: blocked (%d blockers); inspect the sanitized result", len(result.Blockers))
	}
	return nil
}
